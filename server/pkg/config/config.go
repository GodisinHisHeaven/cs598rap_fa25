package config

import (
	"strconv"
	"strings"
)

// Config holds server configuration
type Config struct {
	NodeID            string
	RaftAddr          string
	APIAddr           string
	AdminAddr         string
	DataDir           string
	InitialPeers      string
	SafetyMode        string
	SnapshotInterval  string `json:"snapshotInterval"`
	ElectionTimeoutMs int
	Namespace         string
	ClusterName       string
	PeerPort          int
	PeerService       string
	EnableDiscovery   bool
	DiscoveryMaxPeers int
}

// GetPeers parses the initial peers string into a map of node ID to address
func (c *Config) GetPeers() map[string]string {
	peers := make(map[string]string)
	if c.InitialPeers == "" {
		return peers
	}

	for _, peer := range strings.Split(c.InitialPeers, ",") {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			continue
		}

		nodeID, addr := parsePeerEntry(peer)
		if nodeID == "" || addr == "" {
			continue
		}
		peers[nodeID] = addr
	}

	return peers
}

// parsePeerEntry parses either nodeID=addr or addr-only peer declarations.
func parsePeerEntry(entry string) (nodeID string, addr string) {
	// Preferred format: nodeID=addr
	if strings.Contains(entry, "=") {
		parts := strings.SplitN(entry, "=", 2)
		nodeID = strings.TrimSpace(parts[0])
		addr = strings.TrimSpace(parts[1])
		return
	}

	// Backward compatible format: addr only, derive nodeID from host segment
	addr = entry
	host := entry

	if colon := strings.Index(host, ":"); colon != -1 {
		host = host[:colon]
	}
	if dot := strings.Index(host, "."); dot != -1 {
		host = host[:dot]
	}

	nodeID = strings.TrimSpace(host)
	return
}

// IsSafeMode returns true if running in safe mode
func (c *Config) IsSafeMode() bool {
	return c.SafetyMode == "safe"
}

// IsUnsafeEarlyVote returns true if running in unsafe-early-vote mode
func (c *Config) IsUnsafeEarlyVote() bool {
	return c.SafetyMode == "unsafe-early-vote"
}

// IsUnsafeNoJoint returns true if running in unsafe-no-joint mode
func (c *Config) IsUnsafeNoJoint() bool {
	return c.SafetyMode == "unsafe-no-joint"
}

// DeriveClusterName attempts to infer the cluster name from a StatefulSet-style
// node ID like "kv-0". If the suffix is numeric, everything before the final
// dash is treated as the cluster name.
func DeriveClusterName(nodeID string) string {
	if idx := strings.LastIndex(nodeID, "-"); idx > 0 {
		if _, err := strconv.Atoi(nodeID[idx+1:]); err == nil {
			return nodeID[:idx]
		}
	}
	return nodeID
}
