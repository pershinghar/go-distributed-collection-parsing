package models

// RabbitMQConfig holds configuration for RabbitMQ connections
type RabbitMQConfig struct {
	// RabbitMQ server URL (e.g., "amqp://guest:guest@localhost:5672/")
	URL string
	
	// Exchange name for publishing messages
	Exchange string
	
	// Exchange type (e.g., "fanout", "topic", "direct")
	ExchangeType string
	
	// Routing key (optional, depends on exchange type)
	RoutingKey string
	
	// Whether the exchange is durable
	Durable bool
	
	// Whether the exchange is auto-deleted when not in use
	AutoDelete bool
	
	// Queue name for consuming messages (used by parser service)
	QueueName string
	
	// Whether the queue is durable
	QueueDurable bool
	
	// Whether the queue is auto-deleted when not in use
	QueueAutoDelete bool
	
	// Whether the queue is exclusive (only accessible by the connection that created it)
	QueueExclusive bool
}

// DefaultRabbitMQConfig returns a default RabbitMQ configuration
func DefaultRabbitMQConfig() *RabbitMQConfig {
	return &RabbitMQConfig{
		URL:              "amqp://guest:guest@localhost:5672/",
		Exchange:         "ssh-output",
		ExchangeType:     "fanout",
		RoutingKey:       "",
		Durable:          true,
		AutoDelete:       false,
		QueueName:        "raw-data-queue",
		QueueDurable:     true,
		QueueAutoDelete:  false,
		QueueExclusive:   false,
	}
}

