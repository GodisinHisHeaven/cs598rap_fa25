#!/bin/bash
# Simple Demo Script: Protocol-Aware Raft Operator
# This script demonstrates the key capabilities of the system

set -e

CLUSTER_NAME="demo"
NAMESPACE="default"

echo "=========================================="
echo "  Raft Kubernetes Operator Demo"
echo "=========================================="
echo ""

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

demo_step() {
    echo -e "${BLUE}==>${NC} $1"
}

demo_result() {
    echo -e "${GREEN}✓${NC} $1"
}

demo_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

# Check prerequisites
demo_step "Checking prerequisites..."
command -v kubectl >/dev/null 2>&1 || { echo "kubectl not found"; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "curl not found"; exit 1; }
demo_result "Prerequisites OK"
echo ""

# Check if cluster exists
demo_step "Checking Kubernetes cluster..."
if kubectl cluster-info >/dev/null 2>&1; then
    demo_result "Cluster is running"
    kubectl get nodes
else
    demo_warning "No Kubernetes cluster found. Run: make setup-kind"
    exit 1
fi
echo ""

# Deploy operator (if not already deployed)
demo_step "Checking Raft Operator..."
if kubectl get deployment raft-operator >/dev/null 2>&1; then
    demo_result "Operator is already deployed"
else
    demo_warning "Operator not found. Deploy with: make deploy-operator"
    exit 1
fi
echo ""

# Deploy a 3-node Raft cluster
demo_step "Deploying 3-node Raft cluster (safe mode)..."
cat <<EOF | kubectl apply -f -
apiVersion: storage.sys/v1
kind: RaftCluster
metadata:
  name: ${CLUSTER_NAME}
spec:
  replicas: 3
  image: localhost:5000/raft-kv:latest
  storageSize: 1Gi
  snapshotInterval: "30s"
  electionTimeoutMs: 300
  safetyMode: safe
  reconfig:
    rateLimit: "1/min"
    maxParallel: 1
EOF

demo_result "RaftCluster created"
echo ""

# Wait for cluster to be ready
demo_step "Waiting for cluster to be ready..."
kubectl wait --for=condition=Ready pod -l cluster=${CLUSTER_NAME} --timeout=180s 2>/dev/null || true
sleep 5
demo_result "Cluster is running"
kubectl get pods -l cluster=${CLUSTER_NAME}
echo ""

# Show cluster status
demo_step "Checking cluster status..."
kubectl get raftcluster ${CLUSTER_NAME} -o wide
echo ""

# Test KV operations
demo_step "Testing KV store operations..."
POD_NAME="${CLUSTER_NAME}-0"

# Port forward in background
kubectl port-forward ${POD_NAME} 8080:8080 >/dev/null 2>&1 &
PF_PID=$!
sleep 2

# Cleanup function
cleanup() {
    echo ""
    demo_step "Cleaning up..."
    kill $PF_PID 2>/dev/null || true
}
trap cleanup EXIT

# PUT operation
demo_result "PUT key1 = 'Hello, Raft!'"
curl -s -X POST http://localhost:8080/kv/key1 -d '{"value":"Hello, Raft!"}' | jq .
sleep 1

# PUT another key
demo_result "PUT key2 = 'Protocol awareness matters'"
curl -s -X POST http://localhost:8080/kv/key2 -d '{"value":"Protocol awareness matters"}' | jq .
sleep 1

# GET operations
demo_result "GET key1"
curl -s http://localhost:8080/kv/key1 | jq .
sleep 1

demo_result "GET key2"
curl -s http://localhost:8080/kv/key2 | jq .
sleep 1

echo ""

# DELETE operation
demo_result "DELETE key1"
curl -s -X DELETE http://localhost:8080/kv/key1 | jq .
sleep 1

# GET deleted key
demo_result "GET key1 (should be not found)"
curl -s http://localhost:8080/kv/key1 | jq .
echo ""

# Show cluster reconfiguration capability
demo_step "Demonstrating safe reconfiguration..."
echo "Current replicas: 3"
kubectl get raftcluster ${CLUSTER_NAME} -o jsonpath='{.spec.replicas}'
echo ""

demo_step "Scaling to 5 replicas (this will trigger Joint Consensus)..."
kubectl patch raftcluster ${CLUSTER_NAME} --type=merge -p '{"spec":{"replicas":5}}'

echo ""
demo_warning "Watch reconfiguration steps:"
echo "  kubectl get raftcluster ${CLUSTER_NAME} -w"
echo ""
echo "You should see the operator go through:"
echo "  1. AddLearner  - Add new pods as non-voting learners"
echo "  2. CatchUp     - Wait for learners to catch up"
echo "  3. EnterJoint  - Enter joint consensus (C_old,new)"
echo "  4. CommitNew   - Commit new configuration"
echo "  5. LeaveJoint  - Leave joint consensus"
echo ""

# Wait a bit for reconfiguration to start
sleep 5
kubectl get raftcluster ${CLUSTER_NAME} -o jsonpath='{.status.reconfigStep}'
echo ""

echo ""
echo "=========================================="
echo "  Demo Complete!"
echo "=========================================="
echo ""
echo "Try these commands:"
echo "  • Watch cluster status:  kubectl get raftcluster ${CLUSTER_NAME} -w"
echo "  • View operator logs:    kubectl logs -l app=raft-operator -f"
echo "  • View node logs:        kubectl logs ${CLUSTER_NAME}-0"
echo "  • Scale down:            kubectl patch raftcluster ${CLUSTER_NAME} -p '{\"spec\":{\"replicas\":3}}'"
echo "  • Delete cluster:        kubectl delete raftcluster ${CLUSTER_NAME}"
echo ""
echo "For safety violation demos, see:"
echo "  • Bug 1 (early-vote):    make bug1"
echo "  • Bug 2 (no-joint):      make bug2"
echo ""
