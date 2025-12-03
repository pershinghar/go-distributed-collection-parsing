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

	"github.com/pershinghar/go-distributed-collection-parsing/pkg/models"
	"github.com/pershinghar/go-distributed-collection-parsing/pkg/util"
)

func loadConfig(configPath string) ([]models.SSHConfig, error) {
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


func sshWorker(ctx context.Context, wg *sync.WaitGroup, config *models.SSHConfig) {
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
		outputBufferMutex.Unlock()

        // todo: send to rabbit / minio

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
		err := sshClient.RunCommand(ctx, command, outputHandler)
		if err != nil {
			log.Printf("[%s] sshWorker - RunCommand failed: %v", config.Host, err)
			continue
		}
		log.Printf("[%s] sshWorker - Command completed: %s", config.Host, command)
	}

    log.Printf("[%s] sshWorker - Output: %s", config.Host, outputBuffer)

}

func main() {
	log.Println("[main] Starting Collector Service...")

	// Load SSH host configurations
	configPath := filepath.Join("config", "sshhosts.json")
	sshConfigs, err := loadConfig(configPath)
	if err != nil {
		log.Printf("[main] Error loading config: %v", err)
		return
	}

    // Use a context with a timeout for the entire collection cycle (e.g., 1 minute)
    sshCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
    defer cancel()

    var wgSsh sync.WaitGroup

	for _, config := range sshConfigs {
        wgSsh.Add(1)
        go sshWorker(sshCtx, &wgSsh, &config)
    }
    log.Println("[main] Waiting for all ssh workers to complete...")
    wgSsh.Wait()

    log.Println("[main] All ssh workers completed")
}