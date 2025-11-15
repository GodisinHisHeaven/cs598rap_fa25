package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RaftClusterSpec defines the desired state of RaftCluster
type RaftClusterSpec struct {
	// Replicas is the desired number of Raft nodes
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=7
	Replicas int32 `json:"replicas"`

	// Image is the container image for the Raft KV server
	Image string `json:"image"`

	// StorageSize is the size of the PVC for each node
	// +kubebuilder:default="1Gi"
	StorageSize string `json:"storageSize,omitempty"`

	// SnapshotInterval is the interval for creating snapshots
	// +kubebuilder:default="30s"
	SnapshotInterval string `json:"snapshotInterval,omitempty"`

	// ElectionTimeoutMs is the election timeout in milliseconds
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=100
	// +kubebuilder:validation:Maximum=10000
	ElectionTimeoutMs int `json:"electionTimeoutMs,omitempty"`

	// SafetyMode determines the reconfiguration safety mode
	// +kubebuilder:validation:Enum=safe;unsafe-early-vote;unsafe-no-joint
	// +kubebuilder:default="safe"
	SafetyMode string `json:"safetyMode,omitempty"`

	// ReconfigPolicy controls reconfiguration behavior
	ReconfigPolicy ReconfigPolicy `json:"reconfig,omitempty"`
}

// ReconfigPolicy defines reconfiguration policies
type ReconfigPolicy struct {
	// RateLimit limits the rate of reconfiguration operations
	// +kubebuilder:default="1/min"
	RateLimit string `json:"rateLimit,omitempty"`

	// MaxParallel is the maximum number of parallel reconfigurations
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1
	MaxParallel int `json:"maxParallel,omitempty"`

	// CooldownSeconds is the cooldown period between reconfigurations
	// +kubebuilder:default=60
	CooldownSeconds int `json:"cooldownSeconds,omitempty"`
}

// RaftClusterStatus defines the observed state of RaftCluster
type RaftClusterStatus struct {
	// Phase is the current phase of the cluster
	// +kubebuilder:validation:Enum=Pending;Ready;Reconfiguring;Degraded
	Phase ClusterPhase `json:"phase,omitempty"`

	// Leader is the current leader node ID
	Leader string `json:"leader,omitempty"`

	// Members is the list of cluster members
	Members []MemberStatus `json:"members,omitempty"`

	// JointState is the current joint consensus state
	// +kubebuilder:validation:Enum=none;entering;committed;leaving
	JointState JointConsensusState `json:"jointState,omitempty"`

	// LastReconfigAt is the timestamp of the last reconfiguration
	LastReconfigAt *metav1.Time `json:"lastReconfigAt,omitempty"`

	// Conditions represent the latest available observations of the cluster's state
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// CurrentReconfigStep is the current step in the reconfiguration workflow
	// +kubebuilder:validation:Enum=;AddLearner;CatchUp;EnterJoint;CommitNew;LeaveJoint;LeaderTransfer
	CurrentReconfigStep ReconfigStep `json:"currentReconfigStep,omitempty"`

	// ConfigurationIndex is the monotonically increasing configuration index
	ConfigurationIndex uint64 `json:"configurationIndex,omitempty"`
}

// MemberStatus represents the status of a cluster member
type MemberStatus struct {
	// NodeID is the node identifier
	NodeID string `json:"nodeID"`

	// Role is the member's role (voter or learner)
	// +kubebuilder:validation:Enum=voter;learner
	Role MemberRole `json:"role"`

	// CaughtUp indicates if the member has caught up with the leader
	CaughtUp bool `json:"caughtUp"`

	// LastHeartbeat is the timestamp of the last heartbeat
	LastHeartbeat *metav1.Time `json:"lastHeartbeat,omitempty"`
}

// ClusterPhase represents the phase of the cluster
type ClusterPhase string

const (
	PhasePending       ClusterPhase = "Pending"
	PhaseReady         ClusterPhase = "Ready"
	PhaseReconfiguring ClusterPhase = "Reconfiguring"
	PhaseDegraded      ClusterPhase = "Degraded"
)

// JointConsensusState represents the state of joint consensus
type JointConsensusState string

const (
	JointStateNone      JointConsensusState = "none"
	JointStateEntering  JointConsensusState = "entering"
	JointStateCommitted JointConsensusState = "committed"
	JointStateLeaving   JointConsensusState = "leaving"
)

// MemberRole represents the role of a cluster member
type MemberRole string

const (
	RoleVoter   MemberRole = "voter"
	RoleLearner MemberRole = "learner"
)

// ReconfigStep represents a step in the reconfiguration workflow
type ReconfigStep string

const (
	ReconfigStepNone           ReconfigStep = ""
	ReconfigStepAddLearner     ReconfigStep = "AddLearner"
	ReconfigStepCatchUp        ReconfigStep = "CatchUp"
	ReconfigStepEnterJoint     ReconfigStep = "EnterJoint"
	ReconfigStepCommitNew      ReconfigStep = "CommitNew"
	ReconfigStepLeaveJoint     ReconfigStep = "LeaveJoint"
	ReconfigStepLeaderTransfer ReconfigStep = "LeaderTransfer"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Leader",type=string,JSONPath=`.status.leader`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RaftCluster is the Schema for the raftclusters API
type RaftCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RaftClusterSpec   `json:"spec,omitempty"`
	Status RaftClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RaftClusterList contains a list of RaftCluster
type RaftClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RaftCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RaftCluster{}, &RaftClusterList{})
}
