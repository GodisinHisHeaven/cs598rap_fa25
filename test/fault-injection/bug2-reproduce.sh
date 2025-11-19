#!/bin/bash
# Reproduce Bug 2: No Joint Consensus (Quorum Intersection Violation)
# This script demonstrates split-brain when joint consensus is skipped

set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

CLUSTER_NAME="bug2-demo"

echo "=== Bug 2: No Joint Consensus Reproduction ==="
echo ""

# Step 1: Wait for initial cluster to be ready
echo "Step 1: Waiting for initial 3-node cluster to be ready..."
kubectl wait --for=condition=Ready raftcluster/"$CLUSTER_NAME" --timeout=120s || true
sleep 5

# Step 2: Write initial data
echo ""
echo "Step 2: Writing initial data..."
LEADER=$(kubectl get raftcluster "$CLUSTER_NAME" -o jsonpath='{.status.leader}')
if [ -z "$LEADER" ]; then
    LEADER="${CLUSTER_NAME}-0"
fi
kubectl exec "$LEADER" -- curl -X POST http://localhost:8080/kv/key1 -d '{"value":"initial"}' || true
sleep 2

# Step 3: Start reconfiguration to 5 nodes (no joint consensus - the bug!)
echo ""
echo "Step 3: Scaling to 5 replicas (unsafe: no joint consensus)..."
kubectl patch raftcluster "$CLUSTER_NAME" --type=merge -p '{"spec":{"replicas":5}}'
sleep 10

# Step 4: Create partition during reconfiguration
echo ""
echo "Step 4: Creating network partition during reconfiguration..."
echo "  Group A (old config): ${CLUSTER_NAME}-0, ${CLUSTER_NAME}-1, ${CLUSTER_NAME}-2"
echo "  Group B (new config): ${CLUSTER_NAME}-1, ${CLUSTER_NAME}-3, ${CLUSTER_NAME}-4"
echo "  Note: ${CLUSTER_NAME}-1 is in both groups (the overlap)"

# Partition: 0,2 vs 3,4 (leaving 1 as the bridge that we'll partition selectively)
"$SCRIPT_DIR/partition.sh" "${CLUSTER_NAME}-0,${CLUSTER_NAME}-2" "${CLUSTER_NAME}-3,${CLUSTER_NAME}-4"
sleep 5

# Step 5: Both groups can form quorums without joint consensus
echo ""
echo "Step 5: Both groups can form independent quorums..."
echo "  BUG: Old config {0,1,2} and new config {1,3,4} can both achieve quorum"
echo "  This violates quorum intersection!"

# Step 6: Write conflicting data to both partitions
echo ""
echo "Step 6: Writing conflicting values..."
echo "  Writing 'value-A' to partition A..."
kubectl exec "${CLUSTER_NAME}-0" -- curl -X POST http://localhost:8080/kv/split -d '{"value":"value-A"}' || \
    echo "  (Write may fail)"

sleep 2

echo "  Writing 'value-B' to partition B..."
kubectl exec "${CLUSTER_NAME}-3" -- curl -X POST http://localhost:8080/kv/split -d '{"value":"value-B"}' || \
    echo "  (Write may fail)"

sleep 2

# Step 7: Heal partition
echo ""
echo "Step 7: Healing partition..."
"$SCRIPT_DIR/heal.sh" "$CLUSTER_NAME"
sleep 5

# Step 8: Check for split-brain
echo ""
echo "Step 8: Checking for split-brain..."
echo "  Reading 'split' key from all nodes..."
for i in 0 1 2 3 4; do
    POD="${CLUSTER_NAME}-${i}"
    kubectl get pod "$POD" &>/dev/null || continue
    VALUE=$(kubectl exec "$POD" -- curl -s http://localhost:8080/kv/split 2>/dev/null | grep -o '"value":"[^"]*"' || echo "N/A")
    echo "  $POD: $VALUE"
done

echo ""
echo "=== Bug 2 Reproduction Complete ==="
echo "Expected: Different nodes may have different values for 'split' key"
echo "This demonstrates the quorum intersection violation!"
echo ""

# Step 9: Run Porcupine linearizability check
echo "Step 9: Running Porcupine linearizability check..."
CHECKER_SCRIPT="$(dirname "$0")/../correctness/check-linearizability.sh"

if [ -f "$CHECKER_SCRIPT" ]; then
    if "$CHECKER_SCRIPT" "$CLUSTER_NAME"; then
        echo ""
        echo "⚠️  WARNING: No linearizability violation detected"
        echo "   Bug may not have been triggered"
    else
        echo ""
        echo "✓ Linearizability violation confirmed (as expected for Bug 2)"
    fi
else
    echo "  (Porcupine checker not found at $CHECKER_SCRIPT)"
fi
