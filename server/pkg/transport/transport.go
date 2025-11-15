package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"go.etcd.io/etcd/raft/v3/raftpb"
)

// Transport handles peer-to-peer Raft message communication
type Transport struct {
	mu       sync.RWMutex
	id       uint64 // This node's Raft ID
	addr     string // This node's listen address
	peers    map[uint64]*Peer
	handler  MessageHandler
	server   *http.Server
	client   *http.Client
	stopped  bool
	stopCh   chan struct{}
}

// MessageHandler is called when a message is received
type MessageHandler interface {
	Process(ctx context.Context, msg raftpb.Message) error
}

// NewTransport creates a new transport
func NewTransport(id uint64, addr string, handler MessageHandler) *Transport {
	return &Transport{
		id:      id,
		addr:    addr,
		peers:   make(map[uint64]*Peer),
		handler: handler,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		stopCh: make(chan struct{}),
	}
}

// Start starts the transport HTTP server
func (t *Transport) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/raft", t.handleRaftMessage)
	mux.HandleFunc("/raft/snapshot", t.handleSnapshot)

	t.server = &http.Server{
		Addr:    t.addr,
		Handler: mux,
	}

	log.Printf("Transport starting on %s", t.addr)

	// Start HTTP server in background
	go func() {
		if err := t.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Transport server error: %v", err)
		}
	}()

	return nil
}

// Stop stops the transport
func (t *Transport) Stop() error {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return nil
	}
	t.stopped = true
	close(t.stopCh)
	t.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if t.server != nil {
		return t.server.Shutdown(ctx)
	}

	return nil
}

// AddPeer adds a peer to the transport
func (t *Transport) AddPeer(id uint64, addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.peers[id]; exists {
		return
	}

	peer := NewPeer(id, addr, t.client)
	t.peers[id] = peer
	log.Printf("Added peer: id=%d addr=%s", id, addr)
}

// RemovePeer removes a peer from the transport
func (t *Transport) RemovePeer(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if peer, exists := t.peers[id]; exists {
		peer.Stop()
		delete(t.peers, id)
		log.Printf("Removed peer: id=%d", id)
	}
}

// Send sends a message to a peer
func (t *Transport) Send(msg raftpb.Message) error {
	t.mu.RLock()
	peer, exists := t.peers[msg.To]
	t.mu.RUnlock()

	if !exists {
		return fmt.Errorf("peer %d not found", msg.To)
	}

	return peer.Send(msg)
}

// SendMessages sends multiple messages
func (t *Transport) SendMessages(msgs []raftpb.Message) {
	for _, msg := range msgs {
		if err := t.Send(msg); err != nil {
			// Log error but don't fail - Raft will retry
			log.Printf("Failed to send message to %d: %v", msg.To, err)
		}
	}
}

// handleRaftMessage handles incoming Raft messages
func (t *Transport) handleRaftMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read message
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var msg raftpb.Message
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "failed to unmarshal message", http.StatusBadRequest)
		return
	}

	// Process message through handler
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := t.handler.Process(ctx, msg); err != nil {
		log.Printf("Failed to process message: %v", err)
		http.Error(w, "failed to process message", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleSnapshot handles snapshot transfer
func (t *Transport) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read snapshot data
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var msg raftpb.Message
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "failed to unmarshal snapshot", http.StatusBadRequest)
		return
	}

	// Process snapshot through handler
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := t.handler.Process(ctx, msg); err != nil {
		log.Printf("Failed to process snapshot: %v", err)
		http.Error(w, "failed to process snapshot", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// Peer represents a connection to a peer node
type Peer struct {
	id     uint64
	addr   string
	client *http.Client
	mu     sync.Mutex
	active bool
	stopCh chan struct{}
}

// NewPeer creates a new peer connection
func NewPeer(id uint64, addr string, client *http.Client) *Peer {
	return &Peer{
		id:     id,
		addr:   addr,
		client: client,
		active: true,
		stopCh: make(chan struct{}),
	}
}

// Send sends a message to this peer
func (p *Peer) Send(msg raftpb.Message) error {
	p.mu.Lock()
	if !p.active {
		p.mu.Unlock()
		return fmt.Errorf("peer %d is not active", p.id)
	}
	p.mu.Unlock()

	// Marshal message
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Choose endpoint based on message type
	endpoint := "/raft"
	if msg.Type == raftpb.MsgSnap {
		endpoint = "/raft/snapshot"
	}

	url := fmt.Sprintf("http://%s%s", p.addr, endpoint)

	// Send HTTP POST request
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("peer returned error: %s - %s", resp.Status, string(body))
	}

	return nil
}

// Stop stops the peer connection
func (p *Peer) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.active {
		return
	}

	p.active = false
	close(p.stopCh)
}
