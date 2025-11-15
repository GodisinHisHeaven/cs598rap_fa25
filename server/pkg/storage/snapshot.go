package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.etcd.io/etcd/raft/v3/raftpb"
)

// Snapshotter manages Raft snapshots
type Snapshotter struct {
	dir string
	mu  sync.Mutex
}

// NewSnapshotter creates a new snapshotter
func NewSnapshotter(dir string) *Snapshotter {
	return &Snapshotter{
		dir: dir,
	}
}

// Save saves a snapshot to disk
func (s *Snapshotter) Save(snapshot raftpb.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	filename := fmt.Sprintf("snapshot-%d.json", snapshot.Metadata.Index)
	filepath := filepath.Join(s.dir, filename)

	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %w", err)
	}

	if err := os.WriteFile(filepath, data, 0644); err != nil {
		return fmt.Errorf("failed to write snapshot: %w", err)
	}

	// Clean up old snapshots (keep last 3)
	if err := s.cleanup(3); err != nil {
		// Log but don't fail
		fmt.Printf("Warning: failed to cleanup old snapshots: %v\n", err)
	}

	return nil
}

// Load loads the latest snapshot from disk
func (s *Snapshotter) Load() (*raftpb.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	files, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("failed to read snapshot directory: %w", err)
	}

	// Find latest snapshot
	var latestFile string
	var latestIndex uint64

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()
		if !strings.HasPrefix(name, "snapshot-") || !strings.HasSuffix(name, ".json") {
			continue
		}

		// Extract index from filename
		indexStr := strings.TrimPrefix(name, "snapshot-")
		indexStr = strings.TrimSuffix(indexStr, ".json")
		index, err := strconv.ParseUint(indexStr, 10, 64)
		if err != nil {
			continue
		}

		if index > latestIndex {
			latestIndex = index
			latestFile = name
		}
	}

	if latestFile == "" {
		return nil, os.ErrNotExist
	}

	// Load snapshot
	filepath := filepath.Join(s.dir, latestFile)
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot file: %w", err)
	}

	var snapshot raftpb.Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to unmarshal snapshot: %w", err)
	}

	return &snapshot, nil
}

// cleanup removes old snapshots, keeping only the latest N
func (s *Snapshotter) cleanup(keep int) error {
	files, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("failed to read snapshot directory: %w", err)
	}

	type snapshotFile struct {
		name  string
		index uint64
	}

	var snapshots []snapshotFile
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()
		if !strings.HasPrefix(name, "snapshot-") || !strings.HasSuffix(name, ".json") {
			continue
		}

		indexStr := strings.TrimPrefix(name, "snapshot-")
		indexStr = strings.TrimSuffix(indexStr, ".json")
		index, err := strconv.ParseUint(indexStr, 10, 64)
		if err != nil {
			continue
		}

		snapshots = append(snapshots, snapshotFile{name: name, index: index})
	}

	if len(snapshots) <= keep {
		return nil
	}

	// Sort by index (descending)
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].index > snapshots[j].index
	})

	// Remove old snapshots
	for i := keep; i < len(snapshots); i++ {
		filepath := filepath.Join(s.dir, snapshots[i].name)
		if err := os.Remove(filepath); err != nil {
			return fmt.Errorf("failed to remove old snapshot: %w", err)
		}
	}

	return nil
}
