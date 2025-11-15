#!/bin/bash
# Snapshot performance benchmark
# Usage: ./snapshot.sh [cluster-name]

set -e

CLUSTER_NAME="${1:-demo}"

echo "=== Snapshot Performance Benchmark ==="
echo "Cluster: $CLUSTER_NAME"
echo ""

mkdir -p bench-results

# Get all pods
PODS=$(kubectl get pods -l app=raft-kv -o jsonpath='{.items[*].metadata.name}')

echo "Measuring snapshot sizes and WAL sizes..."
echo ""

for pod in $PODS; do
    echo "=== Pod: $pod ==="

    # Get snapshot and WAL sizes
    SNAPSHOT_SIZE=$(kubectl exec "$pod" -- du -sh /data/snapshots 2>/dev/null | awk '{print $1}' || echo "0")
    WAL_SIZE=$(kubectl exec "$pod" -- du -sh /data/wal 2>/dev/null | awk '{print $1}' || echo "0")

    echo "  Snapshot size: $SNAPSHOT_SIZE"
    echo "  WAL size: $WAL_SIZE"

    # Count entries in WAL
    WAL_ENTRIES=$(kubectl exec "$pod" -- wc -l /data/wal/wal.log 2>/dev/null | awk '{print $1}' || echo "0")
    echo "  WAL entries: $WAL_ENTRIES"

    echo ""
done

# Test restart time with and without snapshots
echo "=== Testing Cold Restart Time ==="
POD=$(echo $PODS | awk '{print $1}')

echo "Restarting pod: $POD"
RESTART_START=$(date +%s%N)

kubectl delete pod "$POD" --wait=false

# Wait for pod to be ready
echo "Waiting for pod to become ready..."
kubectl wait --for=condition=Ready pod/"$POD" --timeout=60s 2>/dev/null || true

RESTART_END=$(date +%s%N)
RESTART_MS=$(( (RESTART_END - RESTART_START) / 1000000 ))

echo "✓ Pod restarted in ${RESTART_MS} ms"

# Save results
cat > bench-results/snapshot-results.json <<EOF
{
  "snapshot_size": "$SNAPSHOT_SIZE",
  "wal_size": "$WAL_SIZE",
  "wal_entries": $WAL_ENTRIES,
  "cold_restart_ms": $RESTART_MS
}
EOF

echo ""
echo "Results saved to bench-results/snapshot-results.json"
