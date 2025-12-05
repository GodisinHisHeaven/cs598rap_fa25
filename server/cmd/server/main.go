package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/cs598rap/raft-kubernetes/server/pkg/config"
	"github.com/cs598rap/raft-kubernetes/server/pkg/kvstore"
	"github.com/cs598rap/raft-kubernetes/server/pkg/raftnode"
	"github.com/cs598rap/raft-kubernetes/server/pkg/server"
)

func main() {
	// Command line flags
	var (
		nodeID            = flag.String("id", "", "Node ID (e.g., kv-0, kv-1)")
		raftAddr          = flag.String("raft-addr", ":9000", "Raft RPC address")
		apiAddr           = flag.String("api-addr", ":8080", "API server address")
		adminAddr         = flag.String("admin-addr", ":9090", "Admin RPC address")
		dataDir           = flag.String("data-dir", "./data", "Data directory for WAL and snapshots")
		initialPeers      = flag.String("peers", "", "Comma-separated list of initial peers (nodeID=addr, e.g., kv-0=kv-0:9000)")
		safetyMode        = flag.String("safety-mode", "safe", "Safety mode: safe, unsafe-early-vote, unsafe-no-joint")
		snapshotInterval  = flag.String("snapshot-interval", "10000", "Snapshot interval (duration string or integer ms)")
		electionTimeoutMs = flag.Int("election-timeout", 300, "Election timeout in ms")
		namespace         = flag.String("namespace", "", "Kubernetes namespace for peer discovery (defaults to POD_NAMESPACE)")
		clusterName       = flag.String("cluster-name", "", "Logical cluster name used for peer discovery (defaults to derived from pod name)")
		peerPort          = flag.Int("peer-port", 9000, "Peer port used for discovery")
		peerService       = flag.String("peer-service", "", "Headless service name for peer discovery (defaults to <cluster>-raft)")
		enableDiscovery   = flag.Bool("peer-discovery", true, "Enable DNS-based peer discovery when peers are not provided")
		discoveryLimit    = flag.Int("peer-discovery-limit", 9, "Maximum peers to probe via discovery")
	)
	flag.Parse()

	if *nodeID == "" {
		// Try to get from hostname (Kubernetes pod name)
		hostname, err := os.Hostname()
		if err != nil {
			log.Fatal("node ID not specified and cannot determine hostname")
		}
		*nodeID = hostname
	}

	// Fill namespace and cluster defaults
	if *namespace == "" {
		if envNS := os.Getenv("POD_NAMESPACE"); envNS != "" {
			*namespace = envNS
		} else {
			*namespace = "default"
		}
	}
	if *clusterName == "" {
		*clusterName = config.DeriveClusterName(*nodeID)
	}

	log.Printf("Starting Raft KV Server")
	log.Printf("Node ID: %s", *nodeID)
	log.Printf("Safety Mode: %s", *safetyMode)
	log.Printf("Raft Address: %s", *raftAddr)
	log.Printf("API Address: %s", *apiAddr)
	log.Printf("Admin Address: %s", *adminAddr)
	log.Printf("Data Directory: %s", *dataDir)

	// Create configuration
	cfg := &config.Config{
		NodeID:            *nodeID,
		RaftAddr:          *raftAddr,
		APIAddr:           *apiAddr,
		AdminAddr:         *adminAddr,
		DataDir:           *dataDir,
		InitialPeers:      *initialPeers,
		SafetyMode:        *safetyMode,
		SnapshotInterval:  *snapshotInterval,
		ElectionTimeoutMs: *electionTimeoutMs,
		Namespace:         *namespace,
		ClusterName:       *clusterName,
		PeerPort:          *peerPort,
		PeerService:       *peerService,
		EnableDiscovery:   *enableDiscovery,
		DiscoveryMaxPeers: *discoveryLimit,
	}

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Create KV store
	store := kvstore.NewKVStore()

	// Create Raft node
	node, err := raftnode.NewRaftNode(cfg, store)
	if err != nil {
		log.Fatalf("Failed to create Raft node: %v", err)
	}

	// Start Raft node
	if err := node.Start(); err != nil {
		log.Fatalf("Failed to start Raft node: %v", err)
	}

	// Create API server
	apiServer := server.NewAPIServer(cfg.APIAddr, node, store)
	go func() {
		log.Printf("Starting API server on %s", cfg.APIAddr)
		if err := apiServer.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("API server failed: %v", err)
		}
	}()

	// Create Admin server
	adminServer := server.NewAdminServer(cfg.AdminAddr, node)
	go func() {
		log.Printf("Starting Admin server on %s", cfg.AdminAddr)
		listener, err := net.Listen("tcp", cfg.AdminAddr)
		if err != nil {
			log.Fatalf("Failed to listen on admin address: %v", err)
		}
		if err := adminServer.Serve(listener); err != nil {
			log.Fatalf("Admin server failed: %v", err)
		}
	}()

	log.Printf("Raft KV Server started successfully")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Printf("Shutting down...")

	// Graceful shutdown
	if err := node.Stop(); err != nil {
		log.Printf("Error stopping Raft node: %v", err)
	}

	if err := apiServer.Stop(); err != nil {
		log.Printf("Error stopping API server: %v", err)
	}

	log.Printf("Server stopped")
}
