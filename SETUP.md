# Setup and Development Guide

This guide walks through setting up the CS598RAP Raft Kubernetes Operator project from scratch.

## Prerequisites

- Go 1.22 or later
- Docker
- kubectl
- kind (Kubernetes in Docker) - for local development
- make

### Install Prerequisites on macOS

```bash
# Install Go
brew install go

# Install Docker Desktop
# Download from: https://www.docker.com/products/docker-desktop

# Install kubectl
brew install kubectl

# Install kind
brew install kind

# Install make (usually pre-installed on macOS)
xcode-select --install
```

## Project Structure

```
cs598rap/
├── server/                  # Raft KV store implementation
│   ├── cmd/server/         # Server entry point
│   ├── pkg/
│   │   ├── config/         # Configuration
│   │   ├── kvstore/        # KV store logic
│   │   ├── raftnode/       # Raft integration
│   │   ├── server/         # API and Admin servers
│   │   ├── storage/        # WAL and snapshots
│   │   └── adminpb/        # gRPC definitions
│   ├── go.mod
│   └── Dockerfile
├── operator/               # Kubernetes operator
│   ├── cmd/operator/      # Operator entry point
│   ├── api/v1/            # RaftCluster CRD
│   ├── controllers/       # Reconciliation logic
│   ├── go.mod
│   └── Dockerfile
├── manifests/             # Kubernetes manifests
│   ├── crd.yaml          # RaftCluster CRD
│   ├── rbac.yaml         # Operator permissions
│   └── operator-deployment.yaml
├── examples/              # Example configurations
│   ├── raftcluster-safe.yaml
│   ├── raftcluster-unsafe-early-vote.yaml
│   ├── raftcluster-unsafe-no-joint.yaml
│   ├── kind-config.yaml
│   └── setup-local-registry.sh
├── test/                  # Testing infrastructure
│   ├── fault-injection/  # Fault injection scripts
│   │   ├── partition.sh
│   │   ├── heal.sh
│   │   ├── leader-kill.sh
│   │   ├── delay.sh
│   │   ├── bug1-reproduce.sh
│   │   └── bug2-reproduce.sh
│   └── correctness/      # Linearizability checking
│       └── check-linearizability.sh
├── Makefile
└── README.md
```

## Quick Start (5 minutes)

### 1. Setup Local Kubernetes Cluster

```bash
# Create kind cluster with local registry
make setup-kind

# Verify cluster is running
kubectl cluster-info
kubectl get nodes
```

### 2. Build and Deploy

```bash
# Download Go dependencies
cd server && go mod download && cd ..
cd operator && go mod download && cd ..

# Build Docker images
make docker-build

# Load images into kind
kind load docker-image localhost:5000/raft-kv:latest --name raft-test
kind load docker-image localhost:5000/raft-operator:latest --name raft-test

# Deploy operator
make deploy-operator

# Verify operator is running
kubectl get pods
kubectl logs -l app=raft-operator -f
```

### 3. Deploy a Raft Cluster

```bash
# Deploy safe mode cluster
make deploy

# Watch cluster status
kubectl get raftcluster demo -w

# Check pods
kubectl get pods -l cluster=demo

# View detailed status
kubectl describe raftcluster demo
```

### 4. Test the KV Store

```bash
# Port-forward to a node
kubectl port-forward demo-0 8080:8080 &

# PUT a value
curl -X POST http://localhost:8080/kv/test -d '{"value":"hello"}'

# GET the value
curl http://localhost:8080/kv/test

# Check health
curl http://localhost:8080/health
```

## Development Workflow

### Building from Source

```bash
# Build server binary
cd server
go build -o raft-kv ./cmd/server
./raft-kv -h

# Build operator binary
cd operator
go build -o raft-operator ./cmd/operator
./raft-operator -h
```

### Running Tests

```bash
# Run all tests
make test

# Run server tests only
cd server && go test ./...

# Run operator tests only
cd operator && go test ./...
```

### Code Quality

```bash
# Format code
make fmt

# Run linters
make vet

# Run all quality checks
make lint
```

### Iterating on Changes

After making code changes:

```bash
# Rebuild Docker images
make docker-build

# Reload into kind
kind load docker-image localhost:5000/raft-kv:latest --name raft-test
kind load docker-image localhost:5000/raft-operator:latest --name raft-test

# Restart operator (to pick up new image)
kubectl rollout restart deployment raft-operator

# Restart Raft cluster
kubectl delete raftcluster demo
make deploy
```

## Reproducing Safety Violations

### Bug 1: Early-Vote Learner

This demonstrates what happens when a new node can vote before catching up:

```bash
# Deploy unsafe cluster
kubectl apply -f examples/raftcluster-unsafe-early-vote.yaml

# Run reproduction script
cd test/fault-injection
./bug1-reproduce.sh

# Check for violations
cd ../correctness
./check-linearizability.sh bug1-demo
```

**Expected Result:** Election safety violation - a stale node wins election

### Bug 2: No Joint Consensus

This demonstrates split-brain when joint consensus is skipped:

```bash
# Deploy unsafe cluster
kubectl apply -f examples/raftcluster-unsafe-no-joint.yaml

# Run reproduction script
cd test/fault-injection
./bug2-reproduce.sh

# Check for violations
cd ../correctness
./check-linearizability.sh bug2-demo
```

**Expected Result:** Quorum intersection violation - conflicting writes succeed

## Manual Fault Injection

### Network Partition

```bash
# Create partition between two groups
cd test/fault-injection
./partition.sh "demo-0,demo-1" "demo-2"

# Heal partition
./heal.sh demo
```

### Leader Failure

```bash
# Kill current leader
./leader-kill.sh demo

# Watch new leader election
kubectl get raftcluster demo -w
```

### Network Delay

```bash
# Inject 200ms delay on demo-2
./delay.sh demo-2 200 50

# Remove delay
./heal.sh demo
```

## Scaling Operations

### Scale Up

```bash
# Scale from 3 to 5 replicas
kubectl patch raftcluster demo --type=merge -p '{"spec":{"replicas":5}}'

# Watch reconfiguration
kubectl get raftcluster demo -w

# See reconfiguration steps
kubectl describe raftcluster demo
```

### Scale Down

```bash
# Scale from 5 to 3 replicas
kubectl patch raftcluster demo --type=merge -p '{"spec":{"replicas":3}}'

# Watch reconfiguration
kubectl get raftcluster demo -w
```

## Monitoring and Debugging

### Check Operator Logs

```bash
kubectl logs -l app=raft-operator -f
```

### Check Raft Node Logs

```bash
# View logs from a specific node
kubectl logs demo-0

# Follow logs
kubectl logs demo-0 -f

# View all node logs
for i in 0 1 2; do
  echo "=== demo-$i ==="
  kubectl logs demo-$i --tail=20
done
```

### Check Cluster Status

```bash
# Get cluster status
kubectl get raftcluster demo -o yaml

# Get StatefulSet status
kubectl get statefulset demo

# Get pod status
kubectl get pods -l cluster=demo

# Get events
kubectl get events --field-selector involvedObject.name=demo
```

### Debug Pod Issues

```bash
# Describe pod
kubectl describe pod demo-0

# Check persistent volumes
kubectl get pvc -l cluster=demo

# Execute commands in pod
kubectl exec -it demo-0 -- sh

# Inside pod:
ls -la /data
cat /data/wal/wal.log
ls /data/snapshots/
```

## Performance Evaluation

Run benchmarks to evaluate:
- Throughput vs QPS
- Latency percentiles (p50, p95, p99)
- Leader failover time
- Reconfiguration duration
- Snapshot overhead

```bash
# Run all benchmarks
make bench

# Results will be in bench-results/
ls -l bench-results/
```

## Cleanup

### Delete Raft Cluster

```bash
make delete
```

### Delete Operator

```bash
kubectl delete -f manifests/operator-deployment.yaml
kubectl delete -f manifests/rbac.yaml
```

### Delete CRD

```bash
kubectl delete -f manifests/crd.yaml
```

### Delete kind Cluster

```bash
make teardown-kind
```

### Clean Build Artifacts

```bash
make clean
```

## Troubleshooting

### Pods Not Starting

**Problem:** Pods stuck in `Pending` or `ImagePullBackOff`

**Solution:**
```bash
# Check pod status
kubectl describe pod demo-0

# Verify images are available
docker images | grep raft

# Reload images into kind
kind load docker-image localhost:5000/raft-kv:latest --name raft-test
```

### Operator Not Working

**Problem:** Operator pod in `CrashLoopBackOff`

**Solution:**
```bash
# Check operator logs
kubectl logs -l app=raft-operator

# Verify CRD is installed
kubectl get crd raftclusters.storage.sys

# Verify RBAC is configured
kubectl get serviceaccount raft-operator
kubectl get clusterrolebinding raft-operator
```

### Cluster Stuck in Degraded

**Problem:** RaftCluster phase is `Degraded`

**Solution:**
```bash
# Check cluster status
kubectl describe raftcluster demo

# Check for errors in operator logs
kubectl logs -l app=raft-operator --tail=50

# Check pod logs
kubectl logs demo-0

# Try recreating cluster
kubectl delete raftcluster demo
make deploy
```

### Network Partition Not Working

**Problem:** Partition scripts fail

**Solution:**
```bash
# Pods may need privileged mode for iptables
# Edit deployment to add:
# securityContext:
#   privileged: true

# Or use alternative partition method with network policies
```

## Next Steps

1. **Implement Complete Raft Protocol**
   - Full etcd/raft integration
   - Proper RPC transport layer
   - Membership tracking

2. **Enhance Operator**
   - Real catch-up detection via Admin RPC
   - Leader transfer implementation
   - More robust error handling

3. **Add Porcupine Integration**
   - Write Go program to run Porcupine checker
   - Automated linearizability verification

4. **Performance Optimization**
   - Tune Raft parameters
   - Optimize snapshot frequency
   - Add read leases (stretch goal)

5. **Production Hardening**
   - Add TLS/mTLS
   - Implement authentication
   - Add comprehensive metrics
   - Write more unit tests

## Resources

- [Raft Paper](https://raft.github.io/raft.pdf)
- [etcd/raft Documentation](https://pkg.go.dev/go.etcd.io/etcd/raft/v3)
- [Kubernetes Operators](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)
- [Porcupine](https://github.com/anishathalye/porcupine)
- [Design Document](./CS598RAP_Design_Doc.pdf)
