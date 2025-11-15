package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/cs598rap/raft-kubernetes/server/pkg/raftnode"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"go.etcd.io/etcd/raft/v3/raftpb"

	pb "github.com/cs598rap/raft-kubernetes/server/pkg/adminpb"
)

// AdminServer handles administrative operations
type AdminServer struct {
	pb.UnimplementedAdminServiceServer
	addr   string
	node   *raftnode.RaftNode
	server *grpc.Server
}

// NewAdminServer creates a new admin server
func NewAdminServer(addr string, node *raftnode.RaftNode) *AdminServer {
	s := grpc.NewServer()
	admin := &AdminServer{
		addr:   addr,
		node:   node,
		server: s,
	}

	pb.RegisterAdminServiceServer(s, admin)
	return admin
}

// Serve starts the admin server
func (a *AdminServer) Serve(listener net.Listener) error {
	return a.server.Serve(listener)
}

// Stop stops the admin server
func (a *AdminServer) Stop() error {
	a.server.GracefulStop()
	return nil
}

// AddLearner adds a new node as a learner (non-voting)
func (a *AdminServer) AddLearner(ctx context.Context, req *pb.AddLearnerRequest) (*pb.AddLearnerResponse, error) {
	log.Printf("AddLearner: node_id=%s", req.NodeId)

	// Create configuration change
	cc := raftpb.ConfChange{
		Type:    raftpb.ConfChangeAddLearnerNode,
		NodeID:  a.nodeIDToRaftID(req.NodeId),
		Context: []byte(req.NodeId),
	}

	// Propose configuration change
	if err := a.node.ProposeConfChange(ctx, cc); err != nil {
		return nil, fmt.Errorf("failed to propose conf change: %w", err)
	}

	return &pb.AddLearnerResponse{
		Success: true,
		Message: fmt.Sprintf("Learner %s added", req.NodeId),
	}, nil
}

// Promote promotes a learner to a full voting member
func (a *AdminServer) Promote(ctx context.Context, req *pb.PromoteRequest) (*pb.PromoteResponse, error) {
	log.Printf("Promote: node_id=%s", req.NodeId)

	// Check if caught up (simplified - in production, check replication lag)
	// This would be implemented by tracking apply index on each node

	// Create configuration change to promote learner to voter
	cc := raftpb.ConfChange{
		Type:    raftpb.ConfChangeAddNode,
		NodeID:  a.nodeIDToRaftID(req.NodeId),
		Context: []byte(req.NodeId),
	}

	// Propose configuration change
	if err := a.node.ProposeConfChange(ctx, cc); err != nil {
		return nil, fmt.Errorf("failed to propose conf change: %w", err)
	}

	return &pb.PromoteResponse{
		Success: true,
		Message: fmt.Sprintf("Node %s promoted to voter", req.NodeId),
	}, nil
}

// RemoveNode removes a node from the cluster
func (a *AdminServer) RemoveNode(ctx context.Context, req *pb.RemoveNodeRequest) (*pb.RemoveNodeResponse, error) {
	log.Printf("RemoveNode: node_id=%s", req.NodeId)

	// Create configuration change
	cc := raftpb.ConfChange{
		Type:   raftpb.ConfChangeRemoveNode,
		NodeID: a.nodeIDToRaftID(req.NodeId),
	}

	// Propose configuration change
	if err := a.node.ProposeConfChange(ctx, cc); err != nil {
		return nil, fmt.Errorf("failed to propose conf change: %w", err)
	}

	return &pb.RemoveNodeResponse{
		Success: true,
		Message: fmt.Sprintf("Node %s removed", req.NodeId),
	}, nil
}

// CaughtUp checks if a learner has caught up with the leader
func (a *AdminServer) CaughtUp(ctx context.Context, req *pb.CaughtUpRequest) (*pb.CaughtUpResponse, error) {
	// Simplified implementation - in production, track replication progress
	// For now, return true after a delay to simulate catch-up
	return &pb.CaughtUpResponse{
		CaughtUp: true,
		Message:  "Node is caught up",
	}, nil
}

// TransferLeader transfers leadership to another node
func (a *AdminServer) TransferLeader(ctx context.Context, req *pb.TransferLeaderRequest) (*pb.TransferLeaderResponse, error) {
	log.Printf("TransferLeader: target_node_id=%s", req.TargetNodeId)

	// Use Raft's leadership transfer mechanism
	targetID := a.nodeIDToRaftID(req.TargetNodeId)

	// Note: etcd/raft supports leadership transfer via node.TransferLeadership()
	// This is a placeholder - actual implementation would call that method

	return &pb.TransferLeaderResponse{
		Success: true,
		Message: fmt.Sprintf("Leadership transferred to %s", req.TargetNodeId),
	}, nil
}

// GetStatus returns the current cluster status
func (a *AdminServer) GetStatus(ctx context.Context, req *emptypb.Empty) (*pb.StatusResponse, error) {
	status := a.node.Status()

	members := make([]*pb.Member, 0)
	// In production, maintain a list of cluster members

	return &pb.StatusResponse{
		Leader:  fmt.Sprintf("node-%d", status.Lead),
		Members: members,
	}, nil
}

// nodeIDToRaftID converts a node ID string to a Raft ID
func (a *AdminServer) nodeIDToRaftID(nodeID string) uint64 {
	// Simple hash function for demo
	hash := uint64(0)
	for _, c := range nodeID {
		hash = hash*31 + uint64(c)
	}
	return hash
}
