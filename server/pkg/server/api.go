package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/cs598rap/raft-kubernetes/server/pkg/kvstore"
	"github.com/cs598rap/raft-kubernetes/server/pkg/raftnode"
)

// APIServer handles client KV operations
type APIServer struct {
	addr   string
	node   *raftnode.RaftNode
	store  *kvstore.KVStore
	server *http.Server
}

// NewAPIServer creates a new API server
func NewAPIServer(addr string, node *raftnode.RaftNode, store *kvstore.KVStore) *APIServer {
	api := &APIServer{
		addr:  addr,
		node:  node,
		store: store,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/kv/", api.handleKV)
	mux.HandleFunc("/history", api.handleHistory)
	mux.HandleFunc("/health", api.handleHealth)

	api.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return api
}

// Start starts the API server
func (a *APIServer) Start() error {
	return a.server.ListenAndServe()
}

// Stop stops the API server
func (a *APIServer) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.server.Shutdown(ctx)
}

// handleKV handles KV GET and PUT operations
func (a *APIServer) handleKV(w http.ResponseWriter, r *http.Request) {
	// Extract key from path (/kv/{key})
	key := r.URL.Path[len("/kv/"):]
	if key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.handleGet(w, r, key)
	case http.MethodPost, http.MethodPut:
		a.handlePut(w, r, key)
	case http.MethodDelete:
		a.handleDelete(w, r, key)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGet handles GET requests
func (a *APIServer) handleGet(w http.ResponseWriter, r *http.Request, key string) {
	startTime := time.Now()

	value, exists := a.store.Get(key)

	endTime := time.Now()

	// Record operation for linearizability checking
	op := kvstore.Operation{
		Type:      kvstore.OpGet,
		Key:       key,
		Value:     value,
		ClientID:  r.RemoteAddr,
		OpID:      time.Now().UnixNano(),
		StartTime: startTime,
		EndTime:   endTime,
		Success:   exists,
	}
	a.store.RecordOperation(op)

	if !exists {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"key":   key,
		"value": value,
	})
}

// handlePut handles PUT requests
func (a *APIServer) handlePut(w http.ResponseWriter, r *http.Request, key string) {
	startTime := time.Now()

	// Read value from request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Create entry
	entry := raftnode.Entry{
		Type:  raftnode.EntryNormal,
		Key:   key,
		Value: req.Value,
	}

	entryData, err := json.Marshal(entry)
	if err != nil {
		http.Error(w, "failed to marshal entry", http.StatusInternalServerError)
		return
	}

	// Propose to Raft
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.node.Propose(ctx, entryData); err != nil {
		http.Error(w, fmt.Sprintf("proposal failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Wait for proposal to be committed (simplified - in production use proposal tracking)
	time.Sleep(100 * time.Millisecond)

	endTime := time.Now()

	// Record operation
	op := kvstore.Operation{
		Type:      kvstore.OpPut,
		Key:       key,
		Value:     req.Value,
		ClientID:  r.RemoteAddr,
		OpID:      time.Now().UnixNano(),
		StartTime: startTime,
		EndTime:   endTime,
		Success:   true,
	}
	a.store.RecordOperation(op)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"key":    key,
	})
}

// handleDelete handles DELETE requests
func (a *APIServer) handleDelete(w http.ResponseWriter, r *http.Request, key string) {
	// For simplicity, DELETE is implemented as PUT with empty value
	entry := raftnode.Entry{
		Type:  raftnode.EntryNormal,
		Key:   key,
		Value: "",
	}

	entryData, err := json.Marshal(entry)
	if err != nil {
		http.Error(w, "failed to marshal entry", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.node.Propose(ctx, entryData); err != nil {
		http.Error(w, fmt.Sprintf("proposal failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

// handleHistory returns the operation history for linearizability checking
func (a *APIServer) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	history := a.store.GetHistory()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// handleHealth returns health status
func (a *APIServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"status":    "ok",
		"is_leader": a.node.IsLeader(),
		"leader_id": a.node.GetLeader(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
