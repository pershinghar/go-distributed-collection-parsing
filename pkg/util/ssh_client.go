// Package util provides SSH client functionality with PTY support.
//
// Example usage in goroutines:
//
//	config := &models.SSHConfig{
//		Host:     "example.com",
//		Port:     22,
//		Username: "user",
//		Password: "password",
//	}
//
//	client := util.NewSSHClient(config)
//	defer client.Close()
//
//	ctx := context.Background()
//	if err := client.Connect(ctx); err != nil {
//		log.Fatal(err)
//	}
//
//	if err := client.CreatePTY(); err != nil {
//		log.Fatal(err)
//	}
//
//	err := client.RunCommand(ctx, "ls -la", func(line string) error {
//		fmt.Println("Output:", line)
//		return nil
//	})
package util

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pershinghar/go-distributed-collection-parsing/pkg/models"
	"golang.org/x/crypto/ssh"
)

// SSHClient represents an SSH client connection with PTY support
type SSHClient struct {
	config        *models.SSHConfig
	client        *ssh.Client
	session       *ssh.Session
	stdin         io.WriteCloser
	stdout        io.Reader
	stderr        io.Reader
	isClosed      bool
	shellRunning  bool
	promptRegex   *regexp.Regexp
	promptChan    chan bool
	errChan       chan error
	outputHandler func(line string) error
	mu            sync.Mutex
	readerStarted bool
}

// NewSSHClient creates a new SSH client instance
func NewSSHClient(config *models.SSHConfig) *SSHClient {
	// Set default port if not specified
	if config.Port == 0 {
		config.Port = 22
	}
	
	// Set default timeout if not specified
	if config.Timeout == 0 {
		config.Timeout = 30
	}
	
	// Set default PTY config if not specified
	if config.PTYConfig == nil {
		config.PTYConfig = models.DefaultPTYConfig()
	}
	
	return &SSHClient{
		config:        config,
		isClosed:      false,
		promptRegex:   regexp.MustCompile(`[\w-]+(?:[@\-w]+)?(?:#|\$)\s*$`),
		promptChan:    make(chan bool, 1),
		errChan:       make(chan error, 1),
		readerStarted: false,
	}
}

// Connect establishes an SSH connection to the remote host
func (c *SSHClient) Connect(ctx context.Context) error {
	if c.isClosed {
		return fmt.Errorf("client is closed")
	}

	// Prepare SSH client config
	sshConfig, err := c.prepareSSHConfig()
	if err != nil {
		return fmt.Errorf("failed to prepare SSH config: %w", err)
	}

	// Create connection with timeout
	address := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)
	dialer := net.Dialer{
		Timeout: time.Duration(c.config.Timeout) * time.Second,
	}
	
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to dial %s: %w", address, err)
	}

	// Perform SSH handshake
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, address, sshConfig)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to establish SSH connection: %w", err)
	}

	c.client = ssh.NewClient(sshConn, chans, reqs)
	return nil
}

// prepareSSHConfig prepares the SSH client configuration
func (c *SSHClient) prepareSSHConfig() (*ssh.ClientConfig, error) {
	config := &ssh.ClientConfig{
		User:            c.config.Username,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // In production, use proper host key verification
		Timeout:         time.Duration(c.config.Timeout) * time.Second,
	}

	// Try key-based authentication first
	if c.config.PrivateKeyPath != "" || len(c.config.PrivateKey) > 0 {
		var signer ssh.Signer

		if c.config.PrivateKeyPath != "" {
			// Load key from file
			key, err := loadPrivateKeyFromFile(c.config.PrivateKeyPath, c.config.KeyPassphrase)
			if err != nil {
				return nil, fmt.Errorf("failed to load private key from file: %w", err)
			}
			signer = key
		} else if len(c.config.PrivateKey) > 0 {
			// Use provided key content
			key, err := loadPrivateKeyFromBytes(c.config.PrivateKey, c.config.KeyPassphrase)
			if err != nil {
				return nil, fmt.Errorf("failed to load private key from bytes: %w", err)
			}
			signer = key
		}

		if signer != nil {
			config.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
		}
	}

	// Fall back to password authentication if no key is provided
	if len(config.Auth) == 0 && c.config.Password != "" {
		config.Auth = []ssh.AuthMethod{ssh.Password(c.config.Password)}
	}

	if len(config.Auth) == 0 {
		return nil, fmt.Errorf("no authentication method provided (need password or private key)")
	}

	return config, nil
}

// CreatePTY creates a pseudo-terminal session
func (c *SSHClient) CreatePTY() error {
	if c.client == nil {
		return fmt.Errorf("not connected: call Connect() first")
	}

	if c.session != nil {
		return fmt.Errorf("PTY session already exists")
	}

	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Request PTY
	modes := ssh.TerminalModes{
		ssh.ECHO:          0,     // disable echoing
	}

	err = session.RequestPty(
		c.config.PTYConfig.Term,
		c.config.PTYConfig.Rows,
		c.config.PTYConfig.Columns,
		modes,
	)
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to request PTY: %w", err)
	}

	// Set up stdin, stdout, stderr
	c.stdin, err = session.StdinPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	c.stdout, err = session.StdoutPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	c.stderr, err = session.StderrPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	c.session = session
	return nil
}

// startReader starts a goroutine that continuously reads from stdout and stderr,
// detects prompts, and calls the output handler. This should be called once when
// the shell starts.
func (c *SSHClient) startReader(outputHandler func(line string) error) {
	c.mu.Lock()
	if c.readerStarted {
		c.mu.Unlock()
		return
	}
	c.readerStarted = true
	c.outputHandler = outputHandler
	c.mu.Unlock()

	// Monitor stdout for prompt and stream output
	go func() {
		reader := bufio.NewReader(c.stdout)
		var lineBuffer []byte
		
		for {
			// Read one byte at a time to handle prompts without trailing newlines
			b, err := reader.ReadByte()
			if err != nil {
				if err == io.EOF {
					// Check if we have a pending buffer with a prompt
					if len(lineBuffer) > 0 {
						line := string(lineBuffer)
						log.Printf("[%s] Received stdout (EOF): %s", c.config.Host, line)
						
						c.mu.Lock()
						handler := c.outputHandler
						c.mu.Unlock()
						
						if handler != nil {
							handler(line)
						}
						
						if c.promptRegex.MatchString(line) {
							log.Printf("[%s] Prompt detected at EOF: %s", c.config.Host, line)
							select {
							case c.promptChan <- true:
							default:
							}
						}
					}
					break
				}
				select {
				case c.errChan <- fmt.Errorf("stdout read error: %w", err):
				default:
				}
				return
			}
			
			// Accumulate bytes
			lineBuffer = append(lineBuffer, b)

			// Check for prompt in current buffer (handles prompts without newlines)
			line := string(lineBuffer)
			if c.promptRegex.MatchString(line) {
				log.Printf("[%s] Received stdout [Prompt]: %s", c.config.Host, line)
				c.promptChan <- true
				lineBuffer = lineBuffer[:0]
			}
			
			// When we hit a newline, process the complete line
			// Don't check for the prompt here, for this implementation it's ok, but maybe FIXME in the future
			// because of multi lined prompts...
			if b == '\n' {
				lineWithoutNewline := strings.TrimSuffix(line, "\n")
				log.Printf("[%s] Received stdout: %s", c.config.Host, lineWithoutNewline)
				
				// Call output handler; we need to lock to avoid possible race conditions
				c.mu.Lock()
				handler := c.outputHandler
				c.mu.Unlock()
				
				if handler != nil {
					if err := handler(lineWithoutNewline); err != nil {
						select {
						case c.errChan <- fmt.Errorf("output handler error: %w", err):
						default:
						}
						return
					}
				}
				
				// Reset buffer for next line
				lineBuffer = lineBuffer[:0]
			}
		}
		
		log.Printf("[%s] Reader finished", c.config.Host)
	}()

	// Monitor stderr (non-blocking, errors are non-fatal for prompt detection)
	go func() {
		scanner := bufio.NewScanner(c.stderr)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("[%s] Received stderr: %s", c.config.Host, line)
			
			c.mu.Lock()
			handler := c.outputHandler
			c.mu.Unlock()
			
			if handler != nil {
				// Don't propagate stderr handler errors, just log them
				if err := handler(line); err != nil {
					log.Printf("[%s] Stderr handler error: %v", c.config.Host, err)
				}
			}
		}
		if err := scanner.Err(); err != nil && err != io.EOF {
			log.Printf("[%s] Stderr scan error: %v", c.config.Host, err)
		}
	}()
}

// RunCommand executes a command interactively, waiting for prompts before and after command execution.
// The outputHandler function is called for each line of output.
// This method can be called multiple times sequentially - each call will wait for the prompt from
// the previous command (or initial prompt for the first call), send the command, and wait for completion.
func (c *SSHClient) RunCommand(ctx context.Context, command string, outputHandler func(line string) error) error {
	if c.session == nil {
		return fmt.Errorf("PTY not created: call CreatePTY() first")
	}
	// Start interactive shell if not already running
	if !c.shellRunning {
		log.Printf("[%s] Starting interactive shell", c.config.Host)
		if err := c.session.Shell(); err != nil {
			return fmt.Errorf("failed to start shell: %w", err)
		}
		c.shellRunning = true
		
		// Start the reader goroutine when shell starts
		log.Printf("[%s] Starting reader goroutine", c.config.Host)
		c.startReader(outputHandler)
	} else {
		// Update output handler for subsequent commands - now it's redudant, but allows to change handler dynamically
		// so basically when the shell is already running, we can change the handler (it's set by startReader function few lines above)
		c.mu.Lock()
		c.outputHandler = outputHandler
		c.mu.Unlock()
	}

	// Step 1: Wait for prompt (initial prompt for first command, or prompt from previous command)
	log.Printf("[%s] RunCommand - Waiting for prompt", c.config.Host)
	select {
	case <-c.promptChan:
		log.Printf("[%s] RunCommand - Prompt detected, sending command", c.config.Host)
	case err := <-c.errChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}

	// Step 2: Send command via stdin
	if _, err := fmt.Fprintf(c.stdin, "%s\r", command); err != nil {
		return fmt.Errorf("failed to send command: %w", err)
	}
	log.Printf("[%s] RunCommand - Command sent: %s", c.config.Host, command)

	// Step 3: Wait for prompt after command completion
	select {
	case <-c.promptChan:
		// I know that command was completed because we receive promptChan signal
		log.Printf("[%s] RunCommand - Command completed", c.config.Host)
		// and when the prompt is detected, we are telling that it's still detected, so we can run more commands
		// this is kinda hack, but for now it's ok, maybe FIXME in the future
		c.promptChan <- true
		return nil
	case err := <-c.errChan:
		log.Printf("[%s] RunCommand - Error: %v", c.config.Host, err)
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close closes the SSH connection and cleans up resources
func (c *SSHClient) Close() error {
	if c.isClosed {
		return nil
	}

	var errs []error

	if c.stdin != nil {
		if err := c.stdin.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if c.session != nil {
		if err := c.session.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if c.client != nil {
		if err := c.client.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	c.isClosed = true
	
	c.mu.Lock()
	c.shellRunning = false
	c.readerStarted = false
	c.mu.Unlock()

	if len(errs) > 0 {
		return fmt.Errorf("errors during close: %v", errs)
	}

	return nil
}

// IsConnected returns true if the client is connected
func (c *SSHClient) IsConnected() bool {
	return c.client != nil && !c.isClosed
}

// Helper functions for loading private keys

func loadPrivateKeyFromFile(path, passphrase string) (ssh.Signer, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return loadPrivateKeyFromBytes(key, passphrase)
}

func loadPrivateKeyFromBytes(key []byte, passphrase string) (ssh.Signer, error) {
	var signer ssh.Signer
	var err error

	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(key)
	}

	if err != nil {
		return nil, err
	}

	return signer, nil
}

