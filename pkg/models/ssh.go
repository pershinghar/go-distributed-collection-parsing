package models

// SSHConfig holds configuration for SSH connections
type SSHConfig struct {
	// Host address (IP or hostname)
	Host string
	
	// Port number (default: 22)
	Port int
	
	// Username for authentication
	Username string
	
	// Authentication methods
	// Password-based authentication
	Password string
	
	// Key-based authentication (path to private key file)
	PrivateKeyPath string
	
	// Private key content (alternative to PrivateKeyPath)
	PrivateKey []byte
	
	// Passphrase for encrypted private key (if applicable)
	KeyPassphrase string
	
	// Timeout in seconds for connection establishment
	Timeout int
	
	// PTY terminal configuration
	PTYConfig *PTYConfig
}

// PTYConfig holds configuration for pseudo-terminal
type PTYConfig struct {
	// Terminal type (e.g., "xterm", "xterm-256color")
	Term string
	
	// Number of columns (width)
	Columns int
	
	// Number of rows (height)
	Rows int
}

// DefaultPTYConfig returns a default PTY configuration
func DefaultPTYConfig() *PTYConfig {
	return &PTYConfig{
		Term:    "dumb",
		Columns: 80,
		Rows:    24,
	}
}

