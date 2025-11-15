package adminclient

import (
	"context"
	"fmt"
	"time"

	pb "github.com/cs598rap/raft-kubernetes/server/pkg/adminpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminClient provides a client for calling admin RPCs on Raft nodes
type AdminClient struct {
	conn   *grpc.ClientConn
	client pb.AdminServiceClient
	addr   string
}

// NewAdminClient creates a new admin client for the given address
func NewAdminClient(addr string) (*AdminClient, error) {
	// Create connection with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	client := pb.NewAdminServiceClient(conn)

	return &AdminClient{
		conn:   conn,
		client: client,
		addr:   addr,
	}, nil
}

// Close closes the client connection
func (c *AdminClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// AddLearner adds a new node as a learner
func (c *AdminClient) AddLearner(ctx context.Context, nodeID string, addr string) error {
	req := &pb.AddLearnerRequest{
		NodeId:  nodeID,
		Address: addr,
	}

	resp, err := c.client.AddLearner(ctx, req)
	if err != nil {
		return fmt.Errorf("AddLearner RPC failed: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("AddLearner failed: %s", resp.Message)
	}

	return nil
}

// Promote promotes a learner to a voting member
func (c *AdminClient) Promote(ctx context.Context, nodeID string) error {
	req := &pb.PromoteRequest{
		NodeId: nodeID,
	}

	resp, err := c.client.Promote(ctx, req)
	if err != nil {
		return fmt.Errorf("Promote RPC failed: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("Promote failed: %s", resp.Message)
	}

	return nil
}

// RemoveNode removes a node from the cluster
func (c *AdminClient) RemoveNode(ctx context.Context, nodeID string) error {
	req := &pb.RemoveNodeRequest{
		NodeId: nodeID,
	}

	resp, err := c.client.RemoveNode(ctx, req)
	if err != nil {
		return fmt.Errorf("RemoveNode RPC failed: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("RemoveNode failed: %s", resp.Message)
	}

	return nil
}

// CaughtUp checks if a learner has caught up
func (c *AdminClient) CaughtUp(ctx context.Context, nodeID string) (bool, error) {
	req := &pb.CaughtUpRequest{
		NodeId: nodeID,
	}

	resp, err := c.client.CaughtUp(ctx, req)
	if err != nil {
		return false, fmt.Errorf("CaughtUp RPC failed: %w", err)
	}

	return resp.CaughtUp, nil
}

// TransferLeader transfers leadership to another node
func (c *AdminClient) TransferLeader(ctx context.Context, targetNodeID string) error {
	req := &pb.TransferLeaderRequest{
		TargetNodeId: targetNodeID,
	}

	resp, err := c.client.TransferLeader(ctx, req)
	if err != nil {
		return fmt.Errorf("TransferLeader RPC failed: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("TransferLeader failed: %s", resp.Message)
	}

	return nil
}

// GetStatus gets the current cluster status
func (c *AdminClient) GetStatus(ctx context.Context) (*pb.StatusResponse, error) {
	resp, err := c.client.GetStatus(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("GetStatus RPC failed: %w", err)
	}

	return resp, nil
}

// ClientPool manages a pool of admin clients for different nodes
type ClientPool struct {
	clients map[string]*AdminClient
}

// NewClientPool creates a new client pool
func NewClientPool() *ClientPool {
	return &ClientPool{
		clients: make(map[string]*AdminClient),
	}
}

// GetClient gets or creates a client for the given address
func (p *ClientPool) GetClient(addr string) (*AdminClient, error) {
	if client, exists := p.clients[addr]; exists {
		return client, nil
	}

	client, err := NewAdminClient(addr)
	if err != nil {
		return nil, err
	}

	p.clients[addr] = client
	return client, nil
}

// Close closes all clients in the pool
func (p *ClientPool) Close() error {
	for _, client := range p.clients {
		if err := client.Close(); err != nil {
			return err
		}
	}
	p.clients = make(map[string]*AdminClient)
	return nil
}

// FindLeader attempts to find the leader by querying all nodes
func (p *ClientPool) FindLeader(ctx context.Context, nodeAddrs []string) (string, *AdminClient, error) {
	for _, addr := range nodeAddrs {
		client, err := p.GetClient(addr)
		if err != nil {
			continue
		}

		status, err := client.GetStatus(ctx)
		if err != nil {
			continue
		}

		if status.Leader != "" {
			// Try to get client for leader
			// In production, maintain a mapping of node IDs to addresses
			return status.Leader, client, nil
		}
	}

	return "", nil, fmt.Errorf("no leader found")
}
