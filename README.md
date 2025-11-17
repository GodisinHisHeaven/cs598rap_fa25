# CS598RAP: Protocol-Aware Kubernetes Operator for Raft

A protocol-aware Kubernetes operator that safely manages Raft cluster reconfiguration using explicit Joint Consensus. This project demonstrates the importance of protocol awareness in distributed systems through safe and unsafe baseline implementations.

## Project Overview

This project implements:
- **Raft-based Key-Value Store** with WAL persistence and snapshots
- **Kubernetes Operator** for declarative Raft cluster management
- **Joint Consensus Workflow** for safe membership changes
- **Unsafe Baselines** that reproduce real safety violations
- **Linearizability Testing** using Porcupine for correctness verification

## Architecture

```
kubectl / CRD (RaftCluster CR)
    |
    v
Kubernetes Operator (Controller)
    |
    v
StatefulSet (kv-0..kv-N-1)
    |
    +-- WAL + Snapshots (PVC)
    +-- Raft RPCs (Headless Service)
    +-- Admin RPCs (reconfig)
```

## Safety Modes

### Safe Mode (Default)
Implements full Joint Consensus with 6-step workflow:
1. **AddLearner** - Create new pod as non-voting learner
2. **CatchUp** - Stream log entries until durable
3. **EnterJoint** - Commit joint configuration (Old ∪ New)
4. **CommitNew** - Commit new configuration
5. **LeaveJoint** - Finalize to new set
6. **Leader Transfer** - Safe transfer before removal

### Unsafe Baselines

**unsafe-early-vote**: Bug 1 - Early-Vote Leader
- Adds voter immediately without learner stage
- Demonstrates Election Safety violation
- Stale node can win election with incomplete log

**unsafe-no-joint**: Bug 2 - No Joint Consensus
- Skips joint consensus phase
- Demonstrates quorum intersection violation
- Two conflicting commits possible at same log index

## Prerequisites

- Go 1.22 or later
- Docker
- Kubernetes cluster (kind, minikube, or GKE)
- kubectl configured

## Quick Start

### 1. Setup Local Kubernetes Cluster

```bash
# Create kind cluster with local registry
make setup-kind
```

### 2. Build and Deploy

```bash
# Build binaries
make build

# Build Docker images
make docker-build

# Deploy operator and CRD
make deploy-operator

# Deploy a safe RaftCluster
make deploy
```

### 3. Verify Deployment

```bash
# Check operator status
kubectl get pods -n default | grep raft-operator

# Check RaftCluster status
kubectl get raftcluster

# View RaftCluster details
kubectl describe raftcluster demo

# Check Raft pods
kubectl get pods -l app=raft-kv
```

## Usage

### Deploy a RaftCluster

```yaml
apiVersion: storage.sys/v1
kind: RaftCluster
metadata:
  name: demo
spec:
  replicas: 3
  image: localhost:5000/raft-kv:latest
  storageSize: 1Gi
  snapshotInterval: "30s"
  electionTimeoutMs: 300
  safetyMode: safe  # safe | unsafe-early-vote | unsafe-no-joint
  reconfig:
    rateLimit: "1/min"
    maxParallel: 1
```

```bash
kubectl apply -f examples/raftcluster-safe.yaml
```

### Interact with the KV Store

```bash
# Port-forward to a Raft node
kubectl port-forward kv-0 8080:8080

# PUT a value
curl -X POST http://localhost:8080/kv/mykey -d '{"value":"myvalue"}'

# GET a value
curl http://localhost:8080/kv/mykey
```

### Scale the Cluster

```bash
# Scale up to 5 replicas
kubectl patch raftcluster demo --type=merge -p '{"spec":{"replicas":5}}'

# Watch the operator perform Joint Consensus
kubectl get raftcluster demo -w

# Scale back down to 3
kubectl patch raftcluster demo --type=merge -p '{"spec":{"replicas":3}}'
```

## Reproducing Safety Violations

### Bug 1: Early-Vote Learner

```bash
# Deploy unsafe cluster and reproduce election safety violation
make bug1

# This will:
# 1. Deploy cluster with unsafe-early-vote mode
# 2. Write initial data
# 3. Add new voter without learner stage
# 4. Create network partition
# 5. Verify linearizability violation with Porcupine
```

### Bug 2: No Joint Consensus

```bash
# Deploy unsafe cluster and reproduce quorum intersection violation
make bug2

# This will:
# 1. Deploy cluster with unsafe-no-joint mode
# 2. Perform membership change without joint consensus
# 3. Create split-brain scenario
# 4. Verify state machine safety violation with Porcupine
```

## Performance Evaluation

```bash
# Run all benchmarks
make bench

# Results will be in bench-results/:
# - throughput-vs-qps.json
# - latency-percentiles.json
# - failover-times.json
# - reconfig-durations.json
# - snapshot-metrics.json
```

Individual benchmarks:
- `./test/bench/steady-state.sh` - Throughput and latency under varying load
- `./test/bench/failover.sh` - Leader failover recovery time
- `./test/bench/reconfiguration.sh` - Reconfiguration impact on performance
- `./test/bench/snapshot.sh` - Snapshot size and restart time

## Fault Injection

Manual fault injection scripts are available in `test/fault-injection/`:

```bash
# Create network partition between kv-0,kv-1 and kv-2,kv-3
./test/fault-injection/partition.sh kv-0,kv-1 kv-2,kv-3

# Heal partition
./test/fault-injection/heal.sh

# Kill current leader
./test/fault-injection/leader-kill.sh

# Inject 200ms latency
./test/fault-injection/delay.sh kv-2 200ms
```

## Project Structure

```
cs598rap/
├── server/                 # Raft KV store implementation
│   ├── cmd/server/        # Server entry point
│   ├── raft/              # Raft protocol implementation
│   ├── kvstore/           # KV store logic
│   ├── storage/           # WAL and snapshot management
│   ├── api/               # gRPC/HTTP APIs
│   └── admin/             # Admin RPC handlers
├── operator/              # Kubernetes operator
│   ├── cmd/operator/      # Operator entry point
│   ├── api/v1/            # RaftCluster CRD definitions
│   ├── controllers/       # Reconciliation controller
│   └── pkg/               # Utility packages
├── manifests/             # Kubernetes manifests
│   ├── crd.yaml           # RaftCluster CRD
│   ├── rbac.yaml          # Operator permissions
│   └── operator-deployment.yaml
├── test/                  # Testing infrastructure
│   ├── fault-injection/   # Fault injection scripts
│   ├── bench/             # Benchmarking tools
│   └── correctness/       # Porcupine linearizability checking
├── examples/              # Example configurations
└── docs/                  # Additional documentation
```

## Development

### Build from Source

```bash
# Build server
cd server && go build -o raft-kv ./cmd/server

# Build operator
cd operator && go build -o raft-operator ./cmd/operator
```

### Run Tests

```bash
# Unit tests
make test

# Lint code
make lint

# Clean build artifacts
make clean
```

### Local Development with kind

```bash
# Setup kind cluster
make setup-kind

# Build and load images into kind
make docker-build
kind load docker-image localhost:5000/raft-kv:latest --name raft-test
kind load docker-image localhost:5000/raft-operator:latest --name raft-test

# Deploy
make deploy

# Teardown
make teardown-kind
```

## Key Safety Invariants

The operator enforces these invariants:
1. **Election Safety** - Only up-to-date candidates win elections
2. **Log Matching** - Committed entries are immutable
3. **State Machine Safety** - No conflicting values at same index
4. **Joint Consensus** - Quorums required in both Old and New configs
5. **Readiness Gating** - Promotion only after durable catch-up
6. **Monotonic Configuration Index** - No rollbacks or interleaving
7. **One Reconfiguration In-Flight** - Serialized membership changes

## Correctness Verification

The project uses **Porcupine** for linearizability checking:
- Operation history logged during experiments
- Offline verification against linearizability model
- Safe mode: 0 violations expected
- Unsafe modes: violations detected under fault injection

## Design Document

For detailed design rationale, see [CS598RAP_Design_Doc.pdf](./CS598RAP_Design_Doc.pdf)

## Cleanup

```bash
# Delete all resources
make delete

# Clean build artifacts
make clean

# Delete kind cluster
make teardown-kind
```

## Troubleshooting

### Operator not starting
```bash
kubectl logs -l app=raft-operator
kubectl describe deployment raft-operator
```

### RaftCluster stuck in Degraded state
```bash
kubectl describe raftcluster demo
kubectl get events --field-selector involvedObject.name=demo
```

### Pods not starting
```bash
kubectl logs kv-0
kubectl describe pod kv-0
```

## License

MIT

## Contributors

CS598 Cloud Storage Systems Team
