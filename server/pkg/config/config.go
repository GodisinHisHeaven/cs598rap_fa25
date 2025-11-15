package config

import "strings"

// Config holds server configuration
type Config struct {
	NodeID            string
	RaftAddr          string
	APIAddr           string
	AdminAddr         string
	DataDir           string
	InitialPeers      string
	SafetyMode        string
	SnapshotInterval  int
	ElectionTimeoutMs int
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

		parts := strings.Split(peer, ":")
		if len(parts) >= 2 {
			// Extract node ID from address (e.g., "kv-0:9000" -> "kv-0")
			nodeID := parts[0]
			addr := peer
			peers[nodeID] = addr
		}
	}

	return peers
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
