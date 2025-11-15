package kvstore

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Operation types
const (
	OpPut = "PUT"
	OpGet = "GET"
)

// Operation represents a KV operation for linearizability testing
type Operation struct {
	Type      string    `json:"type"`
	Key       string    `json:"key"`
	Value     string    `json:"value,omitempty"`
	ClientID  string    `json:"client_id"`
	OpID      int64     `json:"op_id"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Result    string    `json:"result,omitempty"`
	Success   bool      `json:"success"`
}

// KVStore is a simple in-memory key-value store
type KVStore struct {
	mu      sync.RWMutex
	data    map[string]string
	history []Operation // For Porcupine linearizability checking
}

// NewKVStore creates a new KV store
func NewKVStore() *KVStore {
	return &KVStore{
		data:    make(map[string]string),
		history: make([]Operation, 0),
	}
}

// Get retrieves a value by key
func (kv *KVStore) Get(key string) (string, bool) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	value, exists := kv.data[key]
	return value, exists
}

// Put sets a key-value pair
func (kv *KVStore) Put(key, value string) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	kv.data[key] = value
}

// Delete removes a key
func (kv *KVStore) Delete(key string) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	delete(kv.data, key)
}

// RecordOperation records an operation for linearizability checking
func (kv *KVStore) RecordOperation(op Operation) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	kv.history = append(kv.history, op)
}

// GetHistory returns the operation history
func (kv *KVStore) GetHistory() []Operation {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	// Return a copy
	history := make([]Operation, len(kv.history))
	copy(history, kv.history)
	return history
}

// ClearHistory clears the operation history
func (kv *KVStore) ClearHistory() {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	kv.history = make([]Operation, 0)
}

// Snapshot creates a snapshot of the current state
func (kv *KVStore) Snapshot() ([]byte, error) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	snapshot := make(map[string]string)
	for k, v := range kv.data {
		snapshot[k] = v
	}

	return json.Marshal(snapshot)
}

// Restore restores state from a snapshot
func (kv *KVStore) Restore(data []byte) error {
	var snapshot map[string]string
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("failed to unmarshal snapshot: %w", err)
	}

	kv.mu.Lock()
	defer kv.mu.Unlock()

	kv.data = snapshot
	return nil
}

// Size returns the number of keys in the store
func (kv *KVStore) Size() int {
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	return len(kv.data)
}

// Clear removes all keys
func (kv *KVStore) Clear() {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	kv.data = make(map[string]string)
}
