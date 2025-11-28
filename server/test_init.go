package main

import (
	"fmt"
	"go.etcd.io/etcd/raft/v3"
)

func main() {
	storage := raft.NewMemoryStorage()
	
	c := &raft.Config{
		ID:              1,
		ElectionTick:    10,
		HeartbeatTick:   1,
		Storage:         storage,
		MaxSizePerMsg:   1024 * 1024,
		MaxInflightMsgs: 256,
	}
	
	peers := []raft.Peer{{ID: 1}}
	
	// Check initial storage state
	firstIndex, _ := storage.FirstIndex()
	lastIndex, _ := storage.LastIndex()
	fmt.Printf("Before StartNode: FirstIndex=%d, LastIndex=%d\n", firstIndex, lastIndex)
	
	n := raft.StartNode(c, peers)
	defer n.Stop()
	
	// Pump raft events
	ready := <-n.Ready()
	fmt.Printf("Entries in Ready: %d\n", len(ready.Entries))
	for i, ent := range ready.Entries {
		fmt.Printf("  Entry[%d]: Index=%d, Term=%d, Type=%v\n", i, ent.Index, ent.Term, ent.Type)
	}
	
	storage.Append(ready.Entries)
	n.Advance()
	
	// Check after StartNode
	firstIndex, _ = storage.FirstIndex()
	lastIndex, _ = storage.LastIndex()
	fmt.Printf("After appending: FirstIndex=%d, LastIndex=%d\n", firstIndex, lastIndex)
}
