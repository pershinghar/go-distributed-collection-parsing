package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pershinghar/go-distributed-collection-parsing/pkg/models"
	"github.com/pershinghar/go-distributed-collection-parsing/pkg/util"
)

func loadSSHConfig(configPath string) ([]models.SSHConfig, error) {
	// Check if config file exists
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}
		return nil, fmt.Errorf("error checking config file: %w", err)
	}

	log.Printf("[main] Found config file: %s", configPath)

	// Read the file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// Parse JSON
	var sshConfigs []models.SSHConfig
	if err := json.Unmarshal(data, &sshConfigs); err != nil {
		return nil, fmt.Errorf("error parsing JSON: %w", err)
	}

	return sshConfigs, nil
}

func loadRabbitMQConfig(configPath string) (*models.RabbitMQConfig, error) {
	// Check if config file exists
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}
		return nil, fmt.Errorf("error checking config file: %w", err)
	}

	log.Printf("[main] Found RabbitMQ config file: %s", configPath)

	// Read the file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// Parse JSON
	var rabbitMQConfig models.RabbitMQConfig
	if err := json.Unmarshal(data, &rabbitMQConfig); err != nil {
		return nil, fmt.Errorf("error parsing JSON: %w", err)
	}

	return &rabbitMQConfig, nil
}


func sshWorker(ctx context.Context, wg *sync.WaitGroup, config *models.SSHConfig, rabbitMQClient *util.RabbitMQClient, collectionID string) {
	// decrement when routine is done
    defer wg.Done()

    sshClient := util.NewSSHClient(config)
    defer sshClient.Close()

    // connect
    if err := sshClient.Connect(ctx); err != nil {
        log.Printf("[%s] sshWorker - FAIL - %v", config.Host, err)
        return
    }

    // pty stuff
    if err := sshClient.CreatePTY(); err != nil {
        log.Printf("[%s] sshWorker - CreatePTY failed: %v", config.Host, err)
        return
    }

    var outputBuffer string
    var outputBufferMutex sync.Mutex

    outputHandler := func(line string) error {
		outputBufferMutex.Lock()
		outputBuffer += line + "\n"
		
		// Check if outputBuffer exceeds 1MB - if so, we should use MinIO for large outputs
		// will be added later
		if len(outputBuffer) > 1024*1024 { // 1MB
			outputBufferMutex.Unlock()
			return fmt.Errorf("output buffer exceeded 1MB limit (%d bytes)", len(outputBuffer))
		}
		outputBufferMutex.Unlock()

        // now we won't do any more processing here, will send it to rabbit in the end of whole worker

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

    // send command and output handling
	commands := []string{"show version", "show priviledge"}
	for _, command := range commands {
		log.Printf("[%s] sshWorker - Running command: %s", config.Host, command)
		
		// Reset output buffer for each command
		outputBufferMutex.Lock()
		outputBuffer = ""
		outputBufferMutex.Unlock()
		
		err := sshClient.RunCommand(ctx, command, outputHandler)
		if err != nil {
			log.Printf("[%s] sshWorker - RunCommand failed: %v", config.Host, err)
			continue
		}
		log.Printf("[%s] sshWorker - Command completed: %s", config.Host, command)
		
		// Send output to RabbitMQ
		if rabbitMQClient != nil && rabbitMQClient.IsConnected() {
			outputBufferMutex.Lock()
			bufferCopy := outputBuffer
			outputBufferMutex.Unlock()
			
			if bufferCopy != "" {
				chunkID := uuid.New().String()
				rawData := &models.RawData{
					CollectionID:  collectionID,
					SourceID:      config.Host,
					Timestamp:     time.Now(),
					ChunkID:       chunkID,
					Payload:       &bufferCopy,
					ObjectStoreKey: nil, // Will be set when using MinIO for large payloads
				}
				
				if err := rabbitMQClient.Publish(ctx, rawData); err != nil {
					log.Printf("[%s] sshWorker - Failed to publish to RabbitMQ: %v", config.Host, err)
				} else {
					log.Printf("[%s] sshWorker - Published command output to RabbitMQ (chunk: %s)", config.Host, chunkID)
				}
			}
		}
	}

    log.Printf("[%s] sshWorker - Completed", config.Host)
}

func main() {
	log.Println("[main] Starting Collector Service...")

	// Load SSH host configurations
	sshConfigPath := filepath.Join("config", "sshhosts.json")
	sshConfigs, err := loadSSHConfig(sshConfigPath)
	if err != nil {
		log.Printf("[main] Error loading SSH config: %v", err)
		return
	}

	// Load RabbitMQ configuration
	rabbitMQConfigPath := filepath.Join("config", "rabbitmq.json")
	rabbitMQConfig, err := loadRabbitMQConfig(rabbitMQConfigPath)
	if err != nil {
		log.Printf("[main] Error loading RabbitMQ config: %v", err)
		return
	}

	// Create and connect RabbitMQ client
	rabbitMQClient := util.NewRabbitMQClient(rabbitMQConfig)
	defer rabbitMQClient.Close()

	ctx := context.Background()
	if err := rabbitMQClient.Connect(ctx); err != nil {
		log.Printf("[main] Failed to connect to RabbitMQ: %v", err)
		return
	}
	log.Println("[main] Connected to RabbitMQ")

	// Generate collection ID for this collection cycle
	collectionID := uuid.New().String()
	log.Printf("[main] Collection ID: %s", collectionID)

    // Use a context with a timeout for the entire collection cycle (e.g., 1 minute)
    sshCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
    defer cancel()

    var wgSsh sync.WaitGroup

	for _, config := range sshConfigs {
        wgSsh.Add(1)
        go sshWorker(sshCtx, &wgSsh, &config, rabbitMQClient, collectionID)
    }
    log.Println("[main] Waiting for all ssh workers to complete...")
    wgSsh.Wait()

    log.Println("[main] All ssh workers completed")
}