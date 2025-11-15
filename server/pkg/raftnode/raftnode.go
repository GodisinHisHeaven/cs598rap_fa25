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
	cfg    *config.Config
	node   raft.Node
	store  *kvstore.KVStore
	wal    *storage.WAL
	snap   *storage.Snapshotter
	mu     sync.RWMutex
	stopped bool

	// Raft state
	appliedIndex uint64
	snapshotIndex uint64

	// Proposal channels
	proposeC    chan string
	confChangeC chan raftpb.ConfChange

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
			for _, msg := range rd.Messages {
				// TODO: Implement message transport
				_ = msg
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

		// Update peer list
		switch cc.Type {
		case raftpb.ConfChangeAddNode, raftpb.ConfChangeAddLearnerNode:
			// TODO: Add peer to transport
			log.Printf("Added node: ID=%d", cc.NodeID)
		case raftpb.ConfChangeRemoveNode:
			// TODO: Remove peer from transport
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
