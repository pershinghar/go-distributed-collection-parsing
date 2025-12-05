package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pershinghar/go-distributed-collection-parsing/pkg/models"
	"github.com/pershinghar/go-distributed-collection-parsing/pkg/util"
)

func loadRabbitMQConfig(configPath string) (*models.RabbitMQConfig, error) {
	// Check if config file exists
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}
		return nil, fmt.Errorf("error checking config file: %w", err)
	}

	log.Printf("[parser] Found RabbitMQ config file: %s", configPath)

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

	// Set default queue name if not specified
	if rabbitMQConfig.QueueName == "" {
		rabbitMQConfig.QueueName = "raw-data-queue"
	}

	return &rabbitMQConfig, nil
}

func processRawData(data *models.RawData) error {
	log.Printf("[parser] ========================================")
	log.Printf("[parser] Received RawData message:")
	log.Printf("[parser]   CollectionID: %s", data.CollectionID)
	log.Printf("[parser]   SourceID: %s", data.SourceID)
	log.Printf("[parser]   ChunkID: %s", data.ChunkID)
	log.Printf("[parser]   Timestamp: %s", data.Timestamp.Format(time.RFC3339))
	
	if data.Payload != nil {
		log.Printf("[parser]   Payload length: %d bytes", len(*data.Payload))
		log.Printf("[parser]   Payload content:")
		log.Printf("[parser]   ---")
		
		// Split payload into lines and print all
		payloadLines := strings.Split(*data.Payload, "\n")
		for i, line := range payloadLines {
			log.Printf("[parser]   [%d] %s", i+1, line)
		}
		log.Printf("[parser]   ---")
	}
	
	log.Printf("[parser] ========================================")
	
	// TODO: Implement parsing logic here
	// e.g., parse command output, extract structured data, etc.
	
	return nil
}

func main() {
	log.Println("[parser] Starting Parser Service...")

	// Load RabbitMQ configuration
	configPath := filepath.Join("config", "rabbitmq.json")
	rabbitMQConfig, err := loadRabbitMQConfig(configPath)
	if err != nil {
		log.Fatalf("[parser] Error loading RabbitMQ config: %v", err)
	}

	// Create and connect RabbitMQ client
	rabbitMQClient := util.NewRabbitMQClient(rabbitMQConfig)
	defer rabbitMQClient.Close()

	ctx := context.Background()
	if err := rabbitMQClient.Connect(ctx); err != nil {
		log.Fatalf("[parser] Failed to connect to RabbitMQ: %v", err)
	}
	log.Println("[parser] Connected to RabbitMQ")

	// Create queue and bind to exchange
	queueName, err := rabbitMQClient.CreateQueue(ctx)
	if err != nil {
		log.Fatalf("[parser] Failed to create queue: %v", err)
	}
	log.Printf("[parser] Queue '%s' created and bound to exchange", queueName)

	// Set up context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start consuming messages
	if err := rabbitMQClient.Consume(ctx, queueName, processRawData); err != nil {
		log.Fatalf("[parser] Failed to start consuming: %v", err)
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	log.Println("[parser] Parser service running. Press Ctrl+C to stop...")
	
	// Wait for signal or timeout
	select {
	case <-sigChan:
		log.Println("[parser] Received interrupt signal, shutting down...")
		cancel()
	case <-ctx.Done():
		log.Println("[parser] Context cancelled")
	}

	// Give some time for graceful shutdown
	time.Sleep(1 * time.Second)
	log.Println("[parser] Parser service stopped")
}