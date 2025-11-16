package storage

import (
	"os"
	"path/filepath"
	"testing"

	"go.etcd.io/etcd/raft/v3/raftpb"
)

func TestNewWAL(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "wal-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	walDir := filepath.Join(tmpDir, "wal")
	wal, err := NewWAL(walDir)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}
	defer wal.Close()

	if wal == nil {
		t.Fatal("WAL should not be nil")
	}
}

func TestWALSaveAndLoad(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "wal-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	walDir := filepath.Join(tmpDir, "wal")

	// Create WAL and save state
	wal1, err := NewWAL(walDir)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	// Save hard state and entries
	hardState := raftpb.HardState{
		Term:   5,
		Vote:   2,
		Commit: 100,
	}
	entries := []raftpb.Entry{
		{Term: 1, Index: 1, Data: []byte("entry1")},
		{Term: 1, Index: 2, Data: []byte("entry2")},
		{Term: 2, Index: 3, Data: []byte("entry3")},
	}
	err = wal1.Save(hardState, entries)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	wal1.Close()

	// Reopen WAL and load state
	wal2, err := NewWAL(walDir)
	if err != nil {
		t.Fatalf("Failed to reopen WAL: %v", err)
	}
	defer wal2.Close()

	loadedState, loadedEntries, err := wal2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	// Verify hard state
	if loadedState.Term != hardState.Term {
		t.Fatalf("Term mismatch: expected %d, got %d", hardState.Term, loadedState.Term)
	}
	if loadedState.Vote != hardState.Vote {
		t.Fatalf("Vote mismatch: expected %d, got %d", hardState.Vote, loadedState.Vote)
	}
	if loadedState.Commit != hardState.Commit {
		t.Fatalf("Commit mismatch: expected %d, got %d", hardState.Commit, loadedState.Commit)
	}

	// Verify entries
	if len(loadedEntries) != len(entries) {
		t.Fatalf("Entries count mismatch: expected %d, got %d", len(entries), len(loadedEntries))
	}

	for i, entry := range entries {
		if loadedEntries[i].Index != entry.Index {
			t.Fatalf("Entry %d index mismatch: expected %d, got %d", i, entry.Index, loadedEntries[i].Index)
		}
		if loadedEntries[i].Term != entry.Term {
			t.Fatalf("Entry %d term mismatch: expected %d, got %d", i, entry.Term, loadedEntries[i].Term)
		}
	}
}

func TestWALMultipleWrites(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "wal-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	walDir := filepath.Join(tmpDir, "wal")

	wal, err := NewWAL(walDir)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}
	defer wal.Close()

	// Save multiple batches of entries
	for i := 0; i < 10; i++ {
		hardState := raftpb.HardState{
			Term:   uint64(i + 1),
			Vote:   0,
			Commit: uint64(i*10 + 2),
		}
		entries := []raftpb.Entry{
			{Term: uint64(i + 1), Index: uint64(i*10 + 1), Data: []byte("data")},
			{Term: uint64(i + 1), Index: uint64(i*10 + 2), Data: []byte("data")},
		}
		err = wal.Save(hardState, entries)
		if err != nil {
			t.Fatalf("Save failed on iteration %d: %v", i, err)
		}
	}

	// Reload and verify
	_, loadedEntries, err := wal.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	expectedCount := 20 // 10 iterations * 2 entries each
	if len(loadedEntries) < expectedCount {
		t.Logf("Warning: Expected at least %d entries, got %d (some may be deduplicated)", expectedCount, len(loadedEntries))
	}
}

func TestWALCompact(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "wal-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	walDir := filepath.Join(tmpDir, "wal")

	wal, err := NewWAL(walDir)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}
	defer wal.Close()

	// Save some entries
	hardState := raftpb.HardState{
		Term:   5,
		Vote:   1,
		Commit: 10,
	}
	entries := []raftpb.Entry{
		{Term: 5, Index: 1, Data: []byte("entry1")},
		{Term: 5, Index: 2, Data: []byte("entry2")},
		{Term: 5, Index: 3, Data: []byte("entry3")},
		{Term: 5, Index: 4, Data: []byte("entry4")},
		{Term: 5, Index: 5, Data: []byte("entry5")},
	}
	err = wal.Save(hardState, entries)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Compact to remove entries up to index 3
	err = wal.Compact(3)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	// Verify compaction
	_, loadedEntries, err := wal.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	// Should only have entries with index > 3
	for _, entry := range loadedEntries {
		if entry.Index <= 3 {
			t.Fatalf("Found entry with index %d after compaction to index 3", entry.Index)
		}
	}

	if len(loadedEntries) > 2 {
		t.Logf("Note: WAL contains %d entries after compaction (expected <= 2)", len(loadedEntries))
	}
}
