package kvstore

import (
	"testing"
	"time"
)

func TestNewKVStore(t *testing.T) {
	kv := NewKVStore()
	if kv == nil {
		t.Fatal("NewKVStore() returned nil")
	}
	if kv.data == nil {
		t.Fatal("KVStore.data is nil")
	}
	if kv.history == nil {
		t.Fatal("KVStore.history is nil")
	}
}

func TestPutAndGet(t *testing.T) {
	kv := NewKVStore()

	// Test Put and Get
	kv.Put("key1", "value1")
	val, exists := kv.Get("key1")
	if !exists {
		t.Fatal("Key 'key1' should exist")
	}
	if val != "value1" {
		t.Fatalf("Expected 'value1', got '%s'", val)
	}

	// Test Get on non-existent key
	_, exists = kv.Get("nonexistent")
	if exists {
		t.Fatal("Key 'nonexistent' should not exist")
	}
}

func TestPutOverwrite(t *testing.T) {
	kv := NewKVStore()

	// Put initial value
	kv.Put("key1", "value1")

	// Overwrite with new value
	kv.Put("key1", "value2")

	val, exists := kv.Get("key1")
	if !exists {
		t.Fatal("Key 'key1' should exist")
	}
	if val != "value2" {
		t.Fatalf("Expected 'value2', got '%s'", val)
	}
}

func TestDelete(t *testing.T) {
	kv := NewKVStore()

	// Put a key
	kv.Put("key1", "value1")

	// Delete the key
	kv.Delete("key1")

	// Verify it's gone
	_, exists := kv.Get("key1")
	if exists {
		t.Fatal("Key 'key1' should not exist after deletion")
	}

	// Delete non-existent key (should not panic)
	kv.Delete("nonexistent")
}

func TestSize(t *testing.T) {
	kv := NewKVStore()

	if kv.Size() != 0 {
		t.Fatalf("Expected size 0, got %d", kv.Size())
	}

	kv.Put("key1", "value1")
	if kv.Size() != 1 {
		t.Fatalf("Expected size 1, got %d", kv.Size())
	}

	kv.Put("key2", "value2")
	if kv.Size() != 2 {
		t.Fatalf("Expected size 2, got %d", kv.Size())
	}

	kv.Delete("key1")
	if kv.Size() != 1 {
		t.Fatalf("Expected size 1 after deletion, got %d", kv.Size())
	}
}

func TestClear(t *testing.T) {
	kv := NewKVStore()

	kv.Put("key1", "value1")
	kv.Put("key2", "value2")
	kv.Put("key3", "value3")

	if kv.Size() != 3 {
		t.Fatalf("Expected size 3, got %d", kv.Size())
	}

	kv.Clear()

	if kv.Size() != 0 {
		t.Fatalf("Expected size 0 after clear, got %d", kv.Size())
	}
}

func TestRecordOperation(t *testing.T) {
	kv := NewKVStore()

	op := Operation{
		Type:      OpPut,
		Key:       "key1",
		Value:     "value1",
		ClientID:  "client1",
		OpID:      1,
		StartTime: time.Now(),
		EndTime:   time.Now().Add(10 * time.Millisecond),
		Result:    "success",
		Success:   true,
	}

	kv.RecordOperation(op)

	history := kv.GetHistory()
	if len(history) != 1 {
		t.Fatalf("Expected 1 operation in history, got %d", len(history))
	}

	if history[0].Type != OpPut {
		t.Fatalf("Expected OpPut, got %s", history[0].Type)
	}
	if history[0].Key != "key1" {
		t.Fatalf("Expected key1, got %s", history[0].Key)
	}
}

func TestGetHistory(t *testing.T) {
	kv := NewKVStore()

	// Record multiple operations
	for i := 0; i < 5; i++ {
		op := Operation{
			Type:      OpPut,
			Key:       "key",
			Value:     "value",
			ClientID:  "client1",
			OpID:      int64(i),
			StartTime: time.Now(),
			EndTime:   time.Now(),
			Success:   true,
		}
		kv.RecordOperation(op)
	}

	history := kv.GetHistory()
	if len(history) != 5 {
		t.Fatalf("Expected 5 operations in history, got %d", len(history))
	}

	// Verify history is a copy (modifying it shouldn't affect the store)
	history[0].Type = OpGet
	history2 := kv.GetHistory()
	if history2[0].Type != OpPut {
		t.Fatal("History should be a copy, but original was modified")
	}
}

func TestClearHistory(t *testing.T) {
	kv := NewKVStore()

	// Record some operations
	for i := 0; i < 3; i++ {
		op := Operation{
			Type:     OpPut,
			Key:      "key",
			Value:    "value",
			ClientID: "client1",
			OpID:     int64(i),
			Success:  true,
		}
		kv.RecordOperation(op)
	}

	if len(kv.GetHistory()) != 3 {
		t.Fatalf("Expected 3 operations, got %d", len(kv.GetHistory()))
	}

	kv.ClearHistory()

	if len(kv.GetHistory()) != 0 {
		t.Fatalf("Expected 0 operations after clear, got %d", len(kv.GetHistory()))
	}
}

func TestSnapshot(t *testing.T) {
	kv := NewKVStore()

	// Put some data
	kv.Put("key1", "value1")
	kv.Put("key2", "value2")
	kv.Put("key3", "value3")

	// Take snapshot
	snapshot, err := kv.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	if snapshot == nil {
		t.Fatal("Snapshot should not be nil")
	}

	// Clear the store
	kv.Clear()
	if kv.Size() != 0 {
		t.Fatal("Store should be empty after clear")
	}

	// Restore from snapshot
	err = kv.Restore(snapshot)
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify data is restored
	if kv.Size() != 3 {
		t.Fatalf("Expected size 3 after restore, got %d", kv.Size())
	}

	val, exists := kv.Get("key1")
	if !exists || val != "value1" {
		t.Fatal("key1 not restored correctly")
	}

	val, exists = kv.Get("key2")
	if !exists || val != "value2" {
		t.Fatal("key2 not restored correctly")
	}

	val, exists = kv.Get("key3")
	if !exists || val != "value3" {
		t.Fatal("key3 not restored correctly")
	}
}

func TestRestoreInvalidData(t *testing.T) {
	kv := NewKVStore()

	// Try to restore invalid JSON
	err := kv.Restore([]byte("invalid json"))
	if err == nil {
		t.Fatal("Restore should fail with invalid JSON")
	}
}

func TestConcurrentAccess(t *testing.T) {
	kv := NewKVStore()

	// Test concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				key := "key"
				value := "value"
				kv.Put(key, value)
				kv.Get(key)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Store should still be functional
	kv.Put("test", "value")
	val, exists := kv.Get("test")
	if !exists || val != "value" {
		t.Fatal("Store corrupted after concurrent access")
	}
}

func TestHistoryConcurrency(t *testing.T) {
	kv := NewKVStore()

	// Test concurrent history operations
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				op := Operation{
					Type:     OpPut,
					Key:      "key",
					Value:    "value",
					ClientID: "client",
					OpID:     int64(id*100 + j),
					Success:  true,
				}
				kv.RecordOperation(op)
				kv.GetHistory()
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// History should have all operations
	history := kv.GetHistory()
	if len(history) != 1000 {
		t.Fatalf("Expected 1000 operations in history, got %d", len(history))
	}
}
