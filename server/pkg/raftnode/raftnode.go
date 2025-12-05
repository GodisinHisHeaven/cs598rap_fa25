package raftnode

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	storage   *raft.MemoryStorage // Raft storage backend
	transport *transport.Transport
	mu        sync.RWMutex
	stopped   bool

	// Raft state
	appliedIndex     uint64
	snapshotIndex    uint64
	snapshotInterval uint64

	// Proposal channels
	proposeC    chan string
	confChangeC chan raftpb.ConfChange

	// Proposal tracking
	proposalsMu sync.RWMutex
	proposals   map[uint64]chan error // Index -> result channel

	// Peer management
	peers        map[uint64]string // Raft ID -> Node address
	pendingPeers map[uint64]struct{}

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
		cfg:          cfg,
		store:        store,
		wal:          wal,
		snap:         snap,
		proposeC:     make(chan string, 100),
		confChangeC:  make(chan raftpb.ConfChange, 10),
		proposals:    make(map[uint64]chan error),
		peers:        make(map[uint64]string),
		pendingPeers: make(map[uint64]struct{}),
	}

	return rn, nil
}

func (rn *RaftNode) effectiveNamespace() string {
	if rn.cfg.Namespace != "" {
		return rn.cfg.Namespace
	}
	return "default"
}

func (rn *RaftNode) effectiveClusterName() string {
	if rn.cfg.ClusterName != "" {
		return rn.cfg.ClusterName
	}
	return config.DeriveClusterName(rn.cfg.NodeID)
}

func (rn *RaftNode) peerServiceDomain() string {
	cluster := rn.effectiveClusterName()
	ns := rn.effectiveNamespace()
	if rn.cfg.PeerService != "" {
		return fmt.Sprintf("%s.%s.svc.cluster.local", rn.cfg.PeerService, ns)
	}
	return fmt.Sprintf("%s-raft.%s.svc.cluster.local", cluster, ns)
}

// peerAddress builds the routable Raft address for a node ID using the
// cluster's headless service.
func (rn *RaftNode) peerAddress(nodeID string) string {
	host := fmt.Sprintf("%s.%s", nodeID, rn.peerServiceDomain())
	port := rn.cfg.PeerPort
	if port == 0 {
		port = 9000
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// PeerAddressForNode exposes the routable Raft address for a given node ID.
func (rn *RaftNode) PeerAddressForNode(nodeID string) string {
	return rn.peerAddress(nodeID)
}

func (rn *RaftNode) initialPeerMap() map[string]string {
	peerMap := rn.cfg.GetPeers()

	if len(peerMap) == 0 && rn.cfg.EnableDiscovery {
		discovered := rn.discoverPeers()
		if len(discovered) > 0 {
			peerMap = discovered
		}
	}

	if len(peerMap) == 0 {
		// Always include self so the node can bootstrap a cluster even if
		// discovery fails (e.g., DNS not ready yet).
		peerMap[rn.cfg.NodeID] = rn.peerAddress(rn.cfg.NodeID)
	}

	return peerMap
}

// discoverPeers performs a best-effort DNS discovery against the headless
// service. Only nodes that resolve are returned.
func (rn *RaftNode) discoverPeers() map[string]string {
	peers := make(map[string]string)

	maxPeers := rn.cfg.DiscoveryMaxPeers
	if maxPeers <= 0 {
		maxPeers = 9
	}
	cluster := rn.effectiveClusterName()

	for i := 0; i < maxPeers; i++ {
		nodeID := fmt.Sprintf("%s-%d", cluster, i)
		host := fmt.Sprintf("%s.%s", nodeID, rn.peerServiceDomain())

		if _, err := net.LookupHost(host); err != nil {
			continue
		}

		port := rn.cfg.PeerPort
		if port == 0 {
			port = 9000
		}
		addr := fmt.Sprintf("%s:%d", host, port)
		peers[nodeID] = addr
	}

	if len(peers) == 0 {
		log.Printf("peer discovery found no peers via DNS; falling back to self")
	} else {
		log.Printf("peer discovery found %d peer(s): %v", len(peers), peers)
	}

	return peers
}

// discoveryLoop periodically attempts to discover and join peers without
// requiring a rolling restart. This lets StatefulSet scale operations add
// members dynamically.
func (rn *RaftNode) discoveryLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		rn.mu.RLock()
		stopped := rn.stopped
		rn.mu.RUnlock()
		if stopped {
			return
		}
		rn.reconcileDiscoveredPeers()
	}
}

func (rn *RaftNode) reconcileDiscoveredPeers() {
	if !rn.IsLeader() {
		return
	}

	discovered := rn.discoverPeers()
	selfID := rn.nodeIDToRaftID(rn.cfg.NodeID)

	for nodeID, addr := range discovered {
		raftID := rn.nodeIDToRaftID(nodeID)
		if raftID == selfID {
			continue
		}

		rn.mu.RLock()
		_, known := rn.peers[raftID]
		_, pending := rn.pendingPeers[raftID]
		rn.mu.RUnlock()
		if known || pending {
			continue
		}

		// Ensure transport knows how to reach the peer before proposing the add
		if rn.transport != nil {
			rn.transport.AddPeer(raftID, addr)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := rn.node.ProposeConfChange(ctx, raftpb.ConfChange{
			Type:    raftpb.ConfChangeAddNode,
			NodeID:  raftID,
			Context: []byte(addr),
		})
		cancel()
		if err != nil {
			log.Printf("Failed to propose discovered peer %s (%d): %v", nodeID, raftID, err)
			continue
		}

		rn.mu.Lock()
		rn.pendingPeers[raftID] = struct{}{}
		rn.mu.Unlock()
		log.Printf("Proposed adding discovered peer %s at %s", nodeID, addr)
	}
}

// Start starts the Raft node
func (rn *RaftNode) Start() error {
	// Load existing snapshot if any
	// ---------------------------------------
	intervalStr := rn.cfg.SnapshotInterval // e.g. "30s" / "10000" / "5m"

	var snapshotInterval uint64

	// Try parse as duration ("30s", "1m", "500ms")
	if d, err := time.ParseDuration(intervalStr); err == nil {
		snapshotInterval = uint64(d / time.Millisecond) // convert to ms
		log.Printf("Parsed snapshotInterval='%s' as duration %d ms", intervalStr, snapshotInterval)
	} else {
		// Try parse as raw integer ("10000")
		if n, err2 := strconv.Atoi(intervalStr); err2 == nil {
			snapshotInterval = uint64(n)
			log.Printf("Parsed snapshotInterval='%s' as integer %d", intervalStr, snapshotInterval)
		} else {
			return fmt.Errorf("invalid snapshotInterval value '%s'", intervalStr)
		}
	}

	rn.snapshotInterval = snapshotInterval

	snapshot, err := rn.snap.Load()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to load snapshot: %w", err)
	}

	// Load WAL
	hardState, entries, err := rn.wal.ReadAll()
	if err != nil {
		return fmt.Errorf("failed to read WAL: %w", err)
	}

	// Create and initialize Storage BEFORE starting Raft node
	rn.storage = raft.NewMemoryStorage()

	// Restore snapshot to Storage if exists
	if snapshot != nil {
		if err := rn.storage.ApplySnapshot(*snapshot); err != nil {
			return fmt.Errorf("failed to apply snapshot to storage: %w", err)
		}
		// Also restore to KV store
		if err := rn.store.Restore(snapshot.Data); err != nil {
			return fmt.Errorf("failed to restore snapshot to KV store: %w", err)
		}
		rn.snapshotIndex = snapshot.Metadata.Index
		rn.appliedIndex = snapshot.Metadata.Index
	}

	// Restore HardState to Storage if exists
	if !raft.IsEmptyHardState(hardState) {
		if err := rn.storage.SetHardState(hardState); err != nil {
			return fmt.Errorf("failed to set hard state: %w", err)
		}
	}

	// Restore WAL entries to Storage if exists
	if len(entries) > 0 {
		if err := rn.storage.Append(entries); err != nil {
			return fmt.Errorf("failed to append entries to storage: %w", err)
		}
	}

	// Create Raft configuration with initialized Storage
	c := &raft.Config{
		ID:              rn.nodeIDToRaftID(rn.cfg.NodeID),
		ElectionTick:    10,
		HeartbeatTick:   1,
		Storage:         rn.storage,
		MaxSizePerMsg:   1024 * 1024,
		MaxInflightMsgs: 256,
		Logger:          &raftLogger{},
	}

	// Get initial peers (static list or discovered via DNS)
	initialPeerMap := rn.initialPeerMap()

	var peers []raft.Peer
	if len(entries) == 0 && snapshot == nil {
		// New cluster - start fresh
		for nodeID := range initialPeerMap {
			raftID := rn.nodeIDToRaftID(nodeID)
			peers = append(peers, raft.Peer{ID: raftID})
		}
		rn.node = raft.StartNode(c, peers)
	} else {
		// Restart existing node with restored state
		rn.node = raft.RestartNode(c)
	}

	// Initialize transport
	raftID := rn.nodeIDToRaftID(rn.cfg.NodeID)
	rn.transport = transport.NewTransport(raftID, rn.cfg.RaftAddr, rn)
	if err := rn.transport.Start(); err != nil {
		return fmt.Errorf("failed to start transport: %w", err)
	}

	// Add initial peers to transport
	for nodeID, addr := range initialPeerMap {
		if nodeID == rn.cfg.NodeID {
			// Don't add self as peer
			continue
		}
		peerRaftID := rn.nodeIDToRaftID(nodeID)
		rn.transport.AddPeer(peerRaftID, addr)
		rn.mu.Lock()
		rn.peers[peerRaftID] = addr
		rn.mu.Unlock()
	}

	// Start background goroutines
	go rn.run()
	go rn.discoveryLoop()

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

	log.Printf("[DEBUG] run() loop started for node %s", rn.cfg.NodeID)
	tickCount := 0

	for {
		select {
		case <-ticker.C:
			tickCount++
			if tickCount%10 == 0 {
				log.Printf("[DEBUG] Tick %d for node %s", tickCount, rn.cfg.NodeID)
			}
			rn.node.Tick()

		case rd := <-rn.node.Ready():
			log.Printf("[DEBUG] Ready message received: HardState=%+v, Entries=%d, CommittedEntries=%d, Messages=%d, SoftState=%+v",
				rd.HardState, len(rd.Entries), len(rd.CommittedEntries), len(rd.Messages), rd.SoftState)
			// Save to WAL
			if err := rn.wal.Save(rd.HardState, rd.Entries); err != nil {
				log.Printf("Failed to save WAL: %v", err)
			}

			// CRITICAL: Append entries to Storage (needed for Raft to access them later)
			if len(rd.Entries) > 0 {
				if err := rn.storage.Append(rd.Entries); err != nil {
					log.Printf("Failed to append entries to storage: %v", err)
				}
				log.Printf("[DEBUG] Appended %d entries to storage", len(rd.Entries))
			}

			// Send messages to peers
			if rn.transport != nil && len(rd.Messages) > 0 {
				rn.transport.SendMessages(rd.Messages)
				log.Printf("[DEBUG] Sent %d messages to peers", len(rd.Messages))
			}

			// Apply snapshot
			if !raft.IsEmptySnap(rd.Snapshot) {
				if err := rn.storage.ApplySnapshot(rd.Snapshot); err != nil {
					log.Printf("Failed to apply snapshot to storage: %v", err)
				}
				if err := rn.store.Restore(rd.Snapshot.Data); err != nil {
					log.Printf("Failed to restore snapshot to KV store: %v", err)
				}
				rn.snapshotIndex = rd.Snapshot.Metadata.Index
				rn.appliedIndex = rd.Snapshot.Metadata.Index
				log.Printf("[DEBUG] Applied snapshot at index %d", rn.snapshotIndex)
			}

			// Apply committed entries
			log.Printf("[DEBUG] Applying %d committed entries", len(rd.CommittedEntries))
			for i, entry := range rd.CommittedEntries {
				log.Printf("[DEBUG]   Entry %d: Index=%d, Term=%d, Type=%v", i, entry.Index, entry.Term, entry.Type)
				if err := rn.applyEntry(entry); err != nil {
					log.Printf("Failed to apply entry: %v", err)
				}
				rn.appliedIndex = entry.Index

				// Notify proposal waiters
				rn.notifyProposal(entry.Index, nil)
			}
			log.Printf("[DEBUG] Applied index now: %d", rn.appliedIndex)

			// Check if we need to create a snapshot
			if rn.appliedIndex-rn.snapshotIndex >= rn.snapshotInterval {
				if err := rn.createSnapshot(); err != nil {
					log.Printf("Failed to create snapshot: %v", err)
				}
			}

			// Update leader status
			if rd.SoftState != nil {
				rn.mu.Lock()
				oldIsLeader := rn.isLeader
				rn.isLeader = rd.SoftState.RaftState == raft.StateLeader
				if oldIsLeader != rn.isLeader {
					if rn.isLeader {
						log.Printf("[DEBUG] Node %s became LEADER", rn.cfg.NodeID)
					} else {
						log.Printf("[DEBUG] Node %s is no longer leader (state=%v)", rn.cfg.NodeID, rd.SoftState.RaftState)
					}
				}
				rn.mu.Unlock()
			}

			log.Printf("[DEBUG] Calling Advance()")
			rn.node.Advance()
			log.Printf("[DEBUG] Advance() returned, ready for next event")
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
			var addr string
			if len(cc.Context) > 0 {
				addr = strings.TrimSpace(string(cc.Context))
				// Backward-compat: if context was a nodeID, derive an address
				if addr != "" && !strings.Contains(addr, ":") {
					addr = rn.peerAddress(addr)
				}
			}

			if addr == "" {
				log.Printf("Added node: ID=%d (no address provided)", cc.NodeID)
				break
			}

			rn.mu.Lock()
			rn.peers[cc.NodeID] = addr
			delete(rn.pendingPeers, cc.NodeID)
			rn.mu.Unlock()

			if rn.transport != nil {
				rn.transport.AddPeer(cc.NodeID, addr)
			}
			log.Printf("Added node: ID=%d addr=%s", cc.NodeID, addr)
		case raftpb.ConfChangeRemoveNode:
			rn.mu.Lock()
			delete(rn.peers, cc.NodeID)
			delete(rn.pendingPeers, cc.NodeID)
			rn.mu.Unlock()
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
