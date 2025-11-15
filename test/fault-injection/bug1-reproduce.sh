#!/bin/bash
# Reproduce Bug 1: Early-Vote Learner (Election Safety Violation)
# This script demonstrates what happens when a new node can vote before catching up

set -e

CLUSTER_NAME="bug1-demo"

echo "=== Bug 1: Early-Vote Learner Reproduction ==="
echo ""

# Step 1: Wait for initial cluster to be ready
echo "Step 1: Waiting for initial 3-node cluster to be ready..."
kubectl wait --for=condition=Ready raftcluster/"$CLUSTER_NAME" --timeout=120s || true
sleep 5

# Step 2: Write initial data
echo ""
echo "Step 2: Writing initial data (K=1)..."
LEADER=$(kubectl get raftcluster "$CLUSTER_NAME" -o jsonpath='{.status.leader}')
if [ -z "$LEADER" ]; then
    LEADER="${CLUSTER_NAME}-0"
fi
kubectl exec "$LEADER" -- curl -X POST http://localhost:8080/kv/test -d '{"value":"1"}' || true
sleep 2

# Step 3: Scale to 4 replicas (adds node immediately as voter - the bug!)
echo ""
echo "Step 3: Scaling to 4 replicas (unsafe: new node added as voter immediately)..."
kubectl patch raftcluster "$CLUSTER_NAME" --type=merge -p '{"spec":{"replicas":4}}'
sleep 5

# Step 4: Create partition: {kv-0, kv-1} vs {kv-2, kv-3}
echo ""
echo "Step 4: Creating network partition..."
echo "  Group A (old, with data): ${CLUSTER_NAME}-0, ${CLUSTER_NAME}-1"
echo "  Group B (includes new node): ${CLUSTER_NAME}-2, ${CLUSTER_NAME}-3"

./partition.sh "${CLUSTER_NAME}-0,${CLUSTER_NAME}-1" "${CLUSTER_NAME}-2,${CLUSTER_NAME}-3"
sleep 5

# Step 5: The new node (kv-3) might win election with stale log
echo ""
echo "Step 5: Monitoring for election..."
echo "  BUG: Node ${CLUSTER_NAME}-3 can vote despite incomplete log"
echo "  This violates Election Safety!"
sleep 10

# Step 6: Write to partition B (may succeed if kv-3 became leader)
echo ""
echo "Step 6: Attempting write to partition B..."
kubectl exec "${CLUSTER_NAME}-2" -- curl -X POST http://localhost:8080/kv/test -d '{"value":"2"}' || \
    echo "  (Write may fail if partition B has no quorum)"
sleep 2

# Step 7: Heal partition
echo ""
echo "Step 7: Healing partition..."
./heal.sh "$CLUSTER_NAME"
sleep 5

# Step 8: Check for inconsistency
echo ""
echo "Step 8: Checking for data inconsistency..."
echo "  Reading from all nodes..."
for i in 0 1 2 3; do
    POD="${CLUSTER_NAME}-${i}"
    VALUE=$(kubectl exec "$POD" -- curl -s http://localhost:8080/kv/test | grep -o '"value":"[^"]*"' || echo "N/A")
    echo "  $POD: $VALUE"
done

echo ""
echo "=== Bug 1 Reproduction Complete ==="
echo "Expected: Linearizability violation if kv-3 won election"
echo "Next: Run Porcupine checker to verify violation"
