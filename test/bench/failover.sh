#!/bin/bash
# Leader failover benchmark
# Usage: ./failover.sh [cluster-name]

set -e

CLUSTER_NAME="${1:-demo}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== Leader Failover Benchmark ==="
echo "Cluster: $CLUSTER_NAME"
echo ""

# Build load generator if needed
if [ ! -f "$SCRIPT_DIR/loadgen" ]; then
    echo "Building load generator..."
    (cd "$SCRIPT_DIR" && go build -o loadgen loadgen.go)
    echo ""
fi

mkdir -p bench-results

# Get a node to send requests to
POD=$(kubectl get pods -l app=raft-kv -o jsonpath='{.items[0].metadata.name}')
echo "Port-forwarding to $POD..."
kubectl port-forward "$POD" 8080:8080 &
PF_PID=$!
sleep 2

# Cleanup function
cleanup() {
    echo "Cleaning up..."
    kill $PF_PID 2>/dev/null || true
}
trap cleanup EXIT

# Start load in background
echo "Starting background load (100 QPS)..."
"$SCRIPT_DIR/loadgen" \
    --urls "http://localhost:8080" \
    --qps 100 \
    --duration 60s \
    --workers 10 \
    --keys 500 \
    --value-size 100 \
    --read-ratio 0.5 \
    --output bench-results/failover-load.json &
LOAD_PID=$!

# Wait for load to stabilize
echo "Waiting for load to stabilize (10s)..."
sleep 10

# Find current leader
echo ""
echo "Detecting current leader..."
LEADER=$(kubectl get raftcluster "$CLUSTER_NAME" -o jsonpath='{.status.leader}' 2>/dev/null || echo "")

if [ -z "$LEADER" ]; then
    # Try to detect leader from pods
    for pod in $(kubectl get pods -l app=raft-kv -o jsonpath='{.items[*].metadata.name}'); do
        LEADER_CHECK=$(kubectl exec "$pod" -- curl -s http://localhost:8080/health 2>/dev/null | grep -o '"is_leader":true' || true)
        if [ -n "$LEADER_CHECK" ]; then
            LEADER="$pod"
            break
        fi
    done
fi

if [ -z "$LEADER" ]; then
    echo "❌ Could not detect leader"
    kill $LOAD_PID 2>/dev/null || true
    exit 1
fi

echo "Current leader: $LEADER"

# Kill the leader and measure failover time
echo ""
echo "Killing leader pod: $LEADER"
KILL_TIME=$(date +%s%N)

kubectl delete pod "$LEADER" --force --grace-period=0

echo "Waiting for new leader election..."

# Poll for new leader
MAX_WAIT=30
WAITED=0
NEW_LEADER=""

while [ $WAITED -lt $MAX_WAIT ]; do
    sleep 0.5
    WAITED=$((WAITED + 1))

    # Check if cluster has a new leader
    NEW_LEADER=$(kubectl get raftcluster "$CLUSTER_NAME" -o jsonpath='{.status.leader}' 2>/dev/null || echo "")

    if [ -n "$NEW_LEADER" ] && [ "$NEW_LEADER" != "$LEADER" ]; then
        RECOVERY_TIME=$(date +%s%N)
        FAILOVER_MS=$(( (RECOVERY_TIME - KILL_TIME) / 1000000 ))
        echo ""
        echo "✓ New leader elected: $NEW_LEADER"
        echo "  Failover time: ${FAILOVER_MS} ms"
        break
    fi
done

# Wait for load to complete
echo ""
echo "Waiting for load test to complete..."
wait $LOAD_PID 2>/dev/null || true

# Calculate downtime from load results
if [ -f bench-results/failover-load.json ]; then
    echo ""
    echo "=== Results ==="
    TOTAL_OPS=$(jq -r '.stats.total_ops' bench-results/failover-load.json)
    FAILED_OPS=$(jq -r '.stats.failed_ops' bench-results/failover-load.json)
    FAILURE_RATE=$(jq -r '(.stats.failed_ops / .stats.total_ops * 100)' bench-results/failover-load.json)

    echo "Total operations: $TOTAL_OPS"
    echo "Failed operations: $FAILED_OPS"
    echo "Failure rate during failover: ${FAILURE_RATE}%"
    echo "Measured failover time: ${FAILOVER_MS} ms"

    # Save failover results
    jq --arg failover_ms "$FAILOVER_MS" '. + {failover_time_ms: ($failover_ms | tonumber)}' \
        bench-results/failover-load.json > bench-results/failover-results.json
fi

echo ""
echo "Results saved to bench-results/failover-results.json"
