package models

import "time"

// RawData represents the data collected from a remote device,
// before any parsing or structuring.
type RawData struct {
    // Unique identifier for tracking this collection instance
    CollectionID string `json:"collection_id"` 
    
    // Identifier for the source device (e.g., hostname or IP)
    SourceID string `json:"source_id"`
    
    // Timestamp when the data was collected
    Timestamp time.Time `json:"timestamp"`

	// Identifier for data chunk downloaded from device (e.g. command called on ssh)
	ChunkID string `json:"chunk_id"`
    
    // The actual raw text/log data collected
    Payload *string `json:"payload,omitempty"`

	// 2. For LARGE payloads (> 1MB). Value will be nil if using string.
    // Key to the data in MinIO/S3.
    ObjectStoreKey *string `json:"object_store_key,omitempty"`
}