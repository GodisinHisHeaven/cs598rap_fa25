package raftnode

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cs598rap/raft-kubernetes/server/pkg/config"
	"github.com/cs598rap/raft-kubernetes/server/pkg/kvstore"
	"github.com/cs598rap/raft-kubernetes/server/pkg/storage"
	"github.com/cs598rap/raft-kubernetes/server/pkg/transport"
	"go.etcd.io/etcd/raft/v3"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

// EntryType defines the type of Raft log entry
type EntryType uint8

const (
	EntryNormal     EntryType = 0 // Normal KV operation
	EntryConfChange EntryType = 1 // Configuration change
)

// Entry represents a log entry
type Entry struct {
	Type  EntryType `json:"type"`
	Key   string    `json:"key,omitempty"`
	Value string    `json:"value,omitempty"`
}

// RaftNode represents a Raft consensus node
type RaftNode struct {
	cfg       *config.Config
	node      raft.Node
	store     *kvstore.KVStore
	wal       *storage.WAL
	snap      *storage.Snapshotter
	transport *transport.Transport
	mu        sync.RWMutex
	stopped   bool

	// Raft state
	appliedIndex  uint64
	snapshotIndex uint64

	// Proposal channels
	proposeC    chan string
	confChangeC chan raftpb.ConfChange

	// Proposal tracking
	proposalsMu sync.RWMutex
	proposals   map[uint64]chan error // Index -> result channel

	// Peer management
	peers map[uint64]string // Raft ID -> Node address

	// Leader state
	isLeader bool
}

// NewRaftNode creates a new Raft node
func NewRaftNode(cfg *config.Config, store *kvstore.KVStore) (*RaftNode, error) {
	// Create WAL directory
	walDir := filepath.Join(cfg.DataDir, "wal")
	if err := os.MkdirAll(walDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	// Create snapshot directory
	snapDir := filepath.Join(cfg.DataDir, "snapshots")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	// Create WAL
	wal, err := storage.NewWAL(walDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create WAL: %w", err)
	}

	// Create snapshotter
	snap := storage.NewSnapshotter(snapDir)

	rn := &RaftNode{
		cfg:         cfg,
		store:       store,
		wal:         wal,
		snap:        snap,
		proposeC:    make(chan string, 100),
		confChangeC: make(chan raftpb.ConfChange, 10),
		proposals:   make(map[uint64]chan error),
		peers:       make(map[uint64]string),
	}

	return rn, nil
}

// Start starts the Raft node
func (rn *RaftNode) Start() error {
	// Load existing snapshot if any
	snapshot, err := rn.snap.Load()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to load snapshot: %w", err)
	}

	// Load WAL
	hardState, entries, err := rn.wal.ReadAll()
	if err != nil {
		return fmt.Errorf("failed to read WAL: %w", err)
	}

	// Create Raft configuration
	c := &raft.Config{
		ID:              rn.nodeIDToRaftID(rn.cfg.NodeID),
		ElectionTick:    10,
		HeartbeatTick:   1,
		Storage:         raft.NewMemoryStorage(),
		MaxSizePerMsg:   1024 * 1024,
		MaxInflightMsgs: 256,
		Logger:          &raftLogger{},
	}

	// Get initial peers
	var peers []raft.Peer
	if len(entries) == 0 && snapshot == nil {
		// New cluster
		peerMap := rn.cfg.GetPeers()
		for nodeID := range peerMap {
			raftID := rn.nodeIDToRaftID(nodeID)
			peers = append(peers, raft.Peer{ID: raftID})
		}
		rn.node = raft.StartNode(c, peers)
	} else {
		// Restart existing node
		rn.node = raft.RestartNode(c)
	}

	// Restore snapshot if exists
	if snapshot != nil {
		if err := rn.store.Restore(snapshot.Data); err != nil {
			return fmt.Errorf("failed to restore snapshot: %w", err)
		}
		rn.snapshotIndex = snapshot.Metadata.Index
		rn.appliedIndex = snapshot.Metadata.Index
	}

	// Apply WAL entries
	if err := rn.replayWAL(hardState, entries); err != nil {
		return fmt.Errorf("failed to replay WAL: %w", err)
	}

	// Initialize transport
	raftID := rn.nodeIDToRaftID(rn.cfg.NodeID)
	rn.transport = transport.NewTransport(raftID, rn.cfg.RaftAddr, rn)
	if err := rn.transport.Start(); err != nil {
		return fmt.Errorf("failed to start transport: %w", err)
	}

	// Add initial peers to transport
	peerMap := rn.cfg.GetPeers()
	for nodeID, addr := range peerMap {
		if nodeID == rn.cfg.NodeID {
			// Don't add self as peer
			continue
		}
		peerRaftID := rn.nodeIDToRaftID(nodeID)
		rn.transport.AddPeer(peerRaftID, addr)
		rn.peers[peerRaftID] = addr
	}

	// Start background goroutines
	go rn.run()

	log.Printf("Raft node started: ID=%s, RaftID=%d", rn.cfg.NodeID, c.ID)
	return nil
}

// Stop stops the Raft node
func (rn *RaftNode) Stop() error {
	rn.mu.Lock()
	if rn.stopped {
		rn.mu.Unlock()
		return nil
	}
	rn.stopped = true
	rn.mu.Unlock()

	// Stop transport
	if rn.transport != nil {
		if err := rn.transport.Stop(); err != nil {
			log.Printf("Failed to stop transport: %v", err)
		}
	}

	rn.node.Stop()
	if err := rn.wal.Close(); err != nil {
		return fmt.Errorf("failed to close WAL: %w", err)
	}

	return nil
}

// Propose proposes a new entry to the Raft cluster
func (rn *RaftNode) Propose(ctx context.Context, data []byte) error {
	return rn.node.Propose(ctx, data)
}

// ProposeConfChange proposes a configuration change
func (rn *RaftNode) ProposeConfChange(ctx context.Context, cc raftpb.ConfChange) error {
	return rn.node.ProposeConfChange(ctx, cc)
}

// IsLeader returns true if this node is the current leader
func (rn *RaftNode) IsLeader() bool {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	return rn.isLeader
}

// GetLeader returns the current leader's Raft ID
func (rn *RaftNode) GetLeader() uint64 {
	return rn.node.Status().Lead
}

// Status returns the Raft node status
func (rn *RaftNode) Status() raft.Status {
	return rn.node.Status()
}

// GetAppliedIndex returns the current applied index
func (rn *RaftNode) GetAppliedIndex() uint64 {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	return rn.appliedIndex
}

// IsNodeCaughtUp checks if a node has caught up with the leader
func (rn *RaftNode) IsNodeCaughtUp(nodeID uint64) bool {
	status := rn.node.Status()

	// Get progress for the node
	if progress, ok := status.Progress[nodeID]; ok {
		// Check if the node's Match index is close to the leader's committed index
		// Allow a small lag (e.g., 10 entries)
		maxLag := uint64(10)
		return status.Commit <= progress.Match+maxLag
	}

	return false
}

// TransferLeadership transfers leadership to the target node
func (rn *RaftNode) TransferLeadership(targetID uint64) {
	rn.node.TransferLeadership(context.Background(), rn.nodeIDToRaftID(rn.cfg.NodeID), targetID)
}

// run is the main Raft event loop
func (rn *RaftNode) run() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rn.node.Tick()

		case rd := <-rn.node.Ready():
			// Save to WAL
			if err := rn.wal.Save(rd.HardState, rd.Entries); err != nil {
				log.Printf("Failed to save WAL: %v", err)
			}

			// Send messages to peers
			if rn.transport != nil && len(rd.Messages) > 0 {
				rn.transport.SendMessages(rd.Messages)
			}

			// Apply snapshot
			if !raft.IsEmptySnap(rd.Snapshot) {
				if err := rn.store.Restore(rd.Snapshot.Data); err != nil {
					log.Printf("Failed to restore snapshot: %v", err)
				}
				rn.snapshotIndex = rd.Snapshot.Metadata.Index
				rn.appliedIndex = rd.Snapshot.Metadata.Index
			}

			// Apply committed entries
			for _, entry := range rd.CommittedEntries {
				if err := rn.applyEntry(entry); err != nil {
					log.Printf("Failed to apply entry: %v", err)
				}
				rn.appliedIndex = entry.Index

				// Notify proposal waiters
				rn.notifyProposal(entry.Index, nil)
			}

			// Check if we need to create a snapshot
			if rn.appliedIndex-rn.snapshotIndex >= uint64(rn.cfg.SnapshotInterval) {
				if err := rn.createSnapshot(); err != nil {
					log.Printf("Failed to create snapshot: %v", err)
				}
			}

			// Update leader status
			if rd.SoftState != nil {
				rn.mu.Lock()
				rn.isLeader = rd.SoftState.RaftState == raft.StateLeader
				rn.mu.Unlock()
			}

			rn.node.Advance()
		}

		rn.mu.RLock()
		stopped := rn.stopped
		rn.mu.RUnlock()
		if stopped {
			return
		}
	}
}

// applyEntry applies a committed entry to the state machine
func (rn *RaftNode) applyEntry(entry raftpb.Entry) error {
	switch entry.Type {
	case raftpb.EntryNormal:
		if len(entry.Data) == 0 {
			// Ignore empty entries
			return nil
		}

		var e Entry
		if err := json.Unmarshal(entry.Data, &e); err != nil {
			return fmt.Errorf("failed to unmarshal entry: %w", err)
		}

		// Apply to state machine
		if e.Type == EntryNormal {
			rn.store.Put(e.Key, e.Value)
		}

	case raftpb.EntryConfChange:
		var cc raftpb.ConfChange
		if err := cc.Unmarshal(entry.Data); err != nil {
			return fmt.Errorf("failed to unmarshal conf change: %w", err)
		}

		// Apply configuration change
		rn.node.ApplyConfChange(cc)

		// Update peer list and transport
		switch cc.Type {
		case raftpb.ConfChangeAddNode, raftpb.ConfChangeAddLearnerNode:
			// Extract node address from Context
			if len(cc.Context) > 0 {
				addr := string(cc.Context)
				rn.peers[cc.NodeID] = addr
				if rn.transport != nil {
					rn.transport.AddPeer(cc.NodeID, addr)
				}
				log.Printf("Added node: ID=%d addr=%s", cc.NodeID, addr)
			} else {
				log.Printf("Added node: ID=%d (no address provided)", cc.NodeID)
			}
		case raftpb.ConfChangeRemoveNode:
			delete(rn.peers, cc.NodeID)
			if rn.transport != nil {
				rn.transport.RemovePeer(cc.NodeID)
			}
			log.Printf("Removed node: ID=%d", cc.NodeID)
		}
	}

	return nil
}

// createSnapshot creates a snapshot of the current state
func (rn *RaftNode) createSnapshot() error {
	data, err := rn.store.Snapshot()
	if err != nil {
		return fmt.Errorf("failed to create store snapshot: %w", err)
	}

	snapshot := raftpb.Snapshot{
		Data: data,
		Metadata: raftpb.SnapshotMetadata{
			Index: rn.appliedIndex,
			Term:  0, // Will be filled by Raft
		},
	}

	if err := rn.snap.Save(snapshot); err != nil {
		return fmt.Errorf("failed to save snapshot: %w", err)
	}

	// Compact WAL
	if err := rn.wal.Compact(rn.appliedIndex); err != nil {
		return fmt.Errorf("failed to compact WAL: %w", err)
	}

	rn.snapshotIndex = rn.appliedIndex
	log.Printf("Created snapshot at index %d", rn.appliedIndex)
	return nil
}

// replayWAL replays WAL entries
func (rn *RaftNode) replayWAL(hardState raftpb.HardState, entries []raftpb.Entry) error {
	// TODO: Replay entries through Raft
	return nil
}

// nodeIDToRaftID converts a node ID string to a Raft ID
func (rn *RaftNode) nodeIDToRaftID(nodeID string) uint64 {
	// Simple hash function for demo
	// In production, use a proper ID assignment mechanism
	hash := uint64(0)
	for _, c := range nodeID {
		hash = hash*31 + uint64(c)
	}
	return hash
}

// Process implements the transport.MessageHandler interface
// This is called by the transport when a message is received from a peer
func (rn *RaftNode) Process(ctx context.Context, msg raftpb.Message) error {
	return rn.node.Step(ctx, msg)
}

// ProposeAndWait proposes a new entry and waits for it to be committed
func (rn *RaftNode) ProposeAndWait(ctx context.Context, data []byte) error {
	// Check if we're the leader
	if !rn.IsLeader() {
		return fmt.Errorf("not the leader")
	}

	// Propose the entry
	if err := rn.node.Propose(ctx, data); err != nil {
		return fmt.Errorf("failed to propose: %w", err)
	}

	// Get the expected commit index
	// This is a simplified approach - in production, we'd track the exact proposal
	expectedIndex := rn.appliedIndex + 1

	// Create wait channel
	waitCh := make(chan error, 1)

	rn.proposalsMu.Lock()
	rn.proposals[expectedIndex] = waitCh
	rn.proposalsMu.Unlock()

	// Wait for commit or timeout
	select {
	case err := <-waitCh:
		return err
	case <-ctx.Done():
		// Clean up on timeout
		rn.proposalsMu.Lock()
		delete(rn.proposals, expectedIndex)
		rn.proposalsMu.Unlock()
		return ctx.Err()
	}
}

// notifyProposal notifies waiters that a proposal has been committed
func (rn *RaftNode) notifyProposal(index uint64, err error) {
	rn.proposalsMu.Lock()
	defer rn.proposalsMu.Unlock()

	if ch, exists := rn.proposals[index]; exists {
		select {
		case ch <- err:
		default:
		}
		delete(rn.proposals, index)
	}
}

// raftLogger implements raft.Logger
type raftLogger struct{}

func (l *raftLogger) Debug(v ...interface{})                 { log.Print(v...) }
func (l *raftLogger) Debugf(format string, v ...interface{}) { log.Printf(format, v...) }
func (l *raftLogger) Info(v ...interface{})                  { log.Print(v...) }
func (l *raftLogger) Infof(format string, v ...interface{})  { log.Printf(format, v...) }
func (l *raftLogger) Warning(v ...interface{})               { log.Print(v...) }
func (l *raftLogger) Warningf(format string, v ...interface{}) {
	log.Printf(format, v...)
}
func (l *raftLogger) Error(v ...interface{})                 { log.Print(v...) }
func (l *raftLogger) Errorf(format string, v ...interface{}) { log.Printf(format, v...) }
func (l *raftLogger) Fatal(v ...interface{})                 { log.Fatal(v...) }
func (l *raftLogger) Fatalf(format string, v ...interface{}) { log.Fatalf(format, v...) }
func (l *raftLogger) Panic(v ...interface{})                 { log.Panic(v...) }
func (l *raftLogger) Panicf(format string, v ...interface{}) { log.Panicf(format, v...) }
