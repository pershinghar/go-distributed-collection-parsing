package util

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/pershinghar/go-distributed-collection-parsing/pkg/models"
	amqp "github.com/rabbitmq/amqp091-go"
)

// RabbitMQClient represents a RabbitMQ client connection
type RabbitMQClient struct {
	config  *models.RabbitMQConfig
	conn    *amqp.Connection
	channel *amqp.Channel
	mu      sync.Mutex
	isClosed bool
}

// NewRabbitMQClient creates a new RabbitMQ client instance
func NewRabbitMQClient(config *models.RabbitMQConfig) *RabbitMQClient {
	// Use default config if not provided
	if config == nil {
		config = models.DefaultRabbitMQConfig()
	}
	
	// Set defaults if not specified
	if config.URL == "" {
		config.URL = "amqp://guest:guest@localhost:5672/"
	}
	if config.Exchange == "" {
		config.Exchange = "ssh-output"
	}
	if config.ExchangeType == "" {
		config.ExchangeType = "fanout"
	}
	
	return &RabbitMQClient{
		config:   config,
		isClosed: false,
	}
}

// Connect establishes a connection to RabbitMQ and sets up the exchange
func (c *RabbitMQClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if c.isClosed {
		return fmt.Errorf("client is closed")
	}
	
	if c.conn != nil {
		return nil // Already connected
	}
	
	// Connect to RabbitMQ
	conn, err := amqp.Dial(c.config.URL)
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}
	c.conn = conn
	
	// Open channel
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to open channel: %w", err)
	}
	c.channel = ch
	
	// Declare exchange
	err = ch.ExchangeDeclare(
		c.config.Exchange,  // name
		c.config.ExchangeType, // type
		c.config.Durable,    // durable
		c.config.AutoDelete, // auto-deleted
		false,               // internal
		false,               // no-wait
		nil,                 // arguments
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return fmt.Errorf("failed to declare exchange: %w", err)
	}
	
	log.Printf("[RabbitMQ] Connected and exchange '%s' declared", c.config.Exchange)
	return nil
}

// Publish sends a RawData message to RabbitMQ
func (c *RabbitMQClient) Publish(ctx context.Context, data *models.RawData) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if c.isClosed {
		return fmt.Errorf("client is closed")
	}
	
	if c.channel == nil {
		return fmt.Errorf("not connected: call Connect() first")
	}
	
	// Marshal the data to JSON
	messageJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	
	// Publish message
	err = c.channel.PublishWithContext(
		ctx,
		c.config.Exchange,  // exchange
		c.config.RoutingKey, // routing key
		false,              // mandatory
		false,              // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        messageJSON,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}
	
	return nil
}

// Close closes the RabbitMQ connection and cleans up resources
func (c *RabbitMQClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if c.isClosed {
		return nil
	}
	
	var errs []error
	
	if c.channel != nil {
		if err := c.channel.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	
	c.isClosed = true
	
	if len(errs) > 0 {
		return fmt.Errorf("errors during close: %v", errs)
	}
	
	return nil
}

// IsConnected returns true if the client is connected
func (c *RabbitMQClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && !c.isClosed
}

// CreateQueue creates a queue and binds it to the exchange
func (c *RabbitMQClient) CreateQueue(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if c.isClosed {
		return "", fmt.Errorf("client is closed")
	}
	
	if c.channel == nil {
		return "", fmt.Errorf("not connected: call Connect() first")
	}
	
	queueName := c.config.QueueName
	if queueName == "" {
		queueName = "raw-data-queue"
	}
	
	// Declare queue
	queue, err := c.channel.QueueDeclare(
		queueName,              // name
		c.config.QueueDurable,  // durable
		c.config.QueueAutoDelete, // delete when unused
		c.config.QueueExclusive, // exclusive
		false,                  // no-wait
		nil,                    // arguments
	)
	if err != nil {
		return "", fmt.Errorf("failed to declare queue: %w", err)
	}
	
	// Bind queue to exchange
	err = c.channel.QueueBind(
		queue.Name,        // queue name
		c.config.RoutingKey, // routing key (empty for fanout)
		c.config.Exchange, // exchange
		false,             // no-wait
		nil,               // arguments
	)
	if err != nil {
		return "", fmt.Errorf("failed to bind queue to exchange: %w", err)
	}
	
	log.Printf("[RabbitMQ] Queue '%s' created and bound to exchange '%s'", queue.Name, c.config.Exchange)
	return queue.Name, nil
}

// Consume starts consuming messages from the queue
// The handler function will be called for each message received
func (c *RabbitMQClient) Consume(ctx context.Context, queueName string, handler func(data *models.RawData) error) error {
	c.mu.Lock()
	if c.isClosed || c.channel == nil {
		c.mu.Unlock()
		return fmt.Errorf("client is closed or not connected")
	}
	channel := c.channel
	c.mu.Unlock()
	
	// Start consuming messages
	msgs, err := channel.Consume(
		queueName, // queue
		"",        // consumer tag (empty = auto-generated)
		false,     // auto-ack (false = manual ack)
		false,     // exclusive
		false,     // no-local
		false,     // no-wait
		nil,       // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to register consumer: %w", err)
	}
	
	log.Printf("[RabbitMQ] Started consuming from queue '%s'", queueName)
	
	// Process messages
	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("[RabbitMQ] Consumer stopped due to context cancellation")
				return
			case msg, ok := <-msgs:
				if !ok {
					log.Printf("[RabbitMQ] Consumer channel closed")
					return
				}
				
				// Unmarshal message
				var rawData models.RawData
				if err := json.Unmarshal(msg.Body, &rawData); err != nil {
					log.Printf("[RabbitMQ] Failed to unmarshal message: %v", err)
					msg.Nack(false, false) // Reject message without requeue
					continue
				}
				
				// Call handler
				if err := handler(&rawData); err != nil {
					log.Printf("[RabbitMQ] Handler error: %v", err)
					msg.Nack(false, true) // Reject message with requeue
					continue
				}
				
				// Acknowledge message
				msg.Ack(false)
			}
		}
	}()
	
	return nil
}

