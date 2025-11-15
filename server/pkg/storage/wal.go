package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.etcd.io/etcd/raft/v3"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

// WAL represents a write-ahead log
type WAL struct {
	dir string
	mu  sync.Mutex
	f   *os.File

	// Current state
	hardState raftpb.HardState
	entries   []raftpb.Entry
}

// WALRecord represents a record in the WAL
type WALRecord struct {
	Type      string           `json:"type"` // "state" or "entry"
	HardState *raftpb.HardState `json:"hard_state,omitempty"`
	Entry     *raftpb.Entry     `json:"entry,omitempty"`
}

// NewWAL creates a new WAL
func NewWAL(dir string) (*WAL, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	wal := &WAL{
		dir:     dir,
		entries: make([]raftpb.Entry, 0),
	}

	// Open or create WAL file
	walPath := filepath.Join(dir, "wal.log")
	f, err := os.OpenFile(walPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL file: %w", err)
	}
	wal.f = f

	return wal, nil
}

// Save saves the hard state and entries to WAL
func (w *WAL) Save(hardState raftpb.HardState, entries []raftpb.Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Save hard state if changed
	if !raft.IsEmptyHardState(hardState) && !hardState.Equal(&w.hardState) {
		record := WALRecord{
			Type:      "state",
			HardState: &hardState,
		}
		if err := w.writeRecord(record); err != nil {
			return err
		}
		w.hardState = hardState
	}

	// Save entries
	for i := range entries {
		record := WALRecord{
			Type:  "entry",
			Entry: &entries[i],
		}
		if err := w.writeRecord(record); err != nil {
			return err
		}
	}

	// Sync to disk
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("failed to sync WAL: %w", err)
	}

	w.entries = append(w.entries, entries...)
	return nil
}

// ReadAll reads all records from the WAL
func (w *WAL) ReadAll() (raftpb.HardState, []raftpb.Entry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	walPath := filepath.Join(w.dir, "wal.log")
	data, err := os.ReadFile(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return raftpb.HardState{}, nil, nil
		}
		return raftpb.HardState{}, nil, fmt.Errorf("failed to read WAL: %w", err)
	}

	var hardState raftpb.HardState
	var entries []raftpb.Entry

	// Parse newline-delimited JSON
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var record WALRecord
		if err := decoder.Decode(&record); err != nil {
			return raftpb.HardState{}, nil, fmt.Errorf("failed to decode WAL record: %w", err)
		}

		switch record.Type {
		case "state":
			if record.HardState != nil {
				hardState = *record.HardState
			}
		case "entry":
			if record.Entry != nil {
				entries = append(entries, *record.Entry)
			}
		}
	}

	w.hardState = hardState
	w.entries = entries
	return hardState, entries, nil
}

// Compact removes entries up to and including the given index
func (w *WAL) Compact(index uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Filter entries
	newEntries := make([]raftpb.Entry, 0)
	for _, entry := range w.entries {
		if entry.Index > index {
			newEntries = append(newEntries, entry)
		}
	}

	// Rewrite WAL
	walPath := filepath.Join(w.dir, "wal.log")
	tmpPath := walPath + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp WAL: %w", err)
	}

	// Write hard state
	if !raft.IsEmptyHardState(w.hardState) {
		record := WALRecord{
			Type:      "state",
			HardState: &w.hardState,
		}
		data, err := json.Marshal(record)
		if err != nil {
			f.Close()
			return fmt.Errorf("failed to marshal hard state: %w", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			return fmt.Errorf("failed to write hard state: %w", err)
		}
	}

	// Write remaining entries
	for i := range newEntries {
		record := WALRecord{
			Type:  "entry",
			Entry: &newEntries[i],
		}
		data, err := json.Marshal(record)
		if err != nil {
			f.Close()
			return fmt.Errorf("failed to marshal entry: %w", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			return fmt.Errorf("failed to write entry: %w", err)
		}
	}

	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("failed to sync temp WAL: %w", err)
	}
	f.Close()

	// Replace old WAL with new
	if err := os.Rename(tmpPath, walPath); err != nil {
		return fmt.Errorf("failed to replace WAL: %w", err)
	}

	// Reopen WAL file
	w.f.Close()
	newF, err := os.OpenFile(walPath, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to reopen WAL: %w", err)
	}
	w.f = newF
	w.entries = newEntries

	return nil
}

// Close closes the WAL
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.f != nil {
		return w.f.Close()
	}
	return nil
}

// writeRecord writes a record to the WAL
func (w *WAL) writeRecord(record WALRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal record: %w", err)
	}

	if _, err := w.f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write record: %w", err)
	}

	return nil
}
