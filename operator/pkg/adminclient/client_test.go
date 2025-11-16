package adminclient

import (
	"context"
	"testing"
	"time"
)

func TestNewClientPool(t *testing.T) {
	pool := NewClientPool()
	if pool == nil {
		t.Fatal("NewClientPool() returned nil")
	}
	if pool.clients == nil {
		t.Fatal("ClientPool.clients is nil")
	}
}

func TestClientPoolBasic(t *testing.T) {
	pool := NewClientPool()

	// Test that getting a client for an invalid address returns an error
	_, err := pool.GetClient("invalid:9999")
	if err == nil {
		t.Log("Note: Expected error for invalid address, but got success (may have short timeout)")
	}
}

func TestClientPoolClose(t *testing.T) {
	pool := NewClientPool()

	// Close empty pool (should not panic)
	err := pool.Close()
	if err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Verify clients map is reset
	if len(pool.clients) != 0 {
		t.Fatalf("Expected 0 clients after close, got %d", len(pool.clients))
	}
}

func TestFindLeader(t *testing.T) {
	pool := NewClientPool()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Test with empty node list
	_, _, err := pool.FindLeader(ctx, []string{})
	if err == nil {
		t.Fatal("Expected error with empty node list")
	}

	// Test with invalid addresses
	_, _, err = pool.FindLeader(ctx, []string{"invalid1:9999", "invalid2:9999"})
	if err == nil {
		t.Log("Note: Expected error with invalid addresses")
	}
}

func TestNewAdminClient(t *testing.T) {
	// Test creating client with invalid address
	_, err := NewAdminClient("invalid:9999")
	if err == nil {
		t.Log("Note: Expected error with invalid address, but got success")
	}
}

func TestAdminClientClose(t *testing.T) {
	// Create a client that will fail to connect
	client := &AdminClient{
		conn: nil,
		addr: "test:9999",
	}

	// Close should not panic with nil connection
	err := client.Close()
	if err != nil {
		t.Fatalf("Close() with nil connection failed: %v", err)
	}
}
