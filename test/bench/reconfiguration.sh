#!/bin/bash
# Reconfiguration performance benchmark
# Usage: ./reconfiguration.sh [cluster-name]

set -e

CLUSTER_NAME="${1:-demo}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== Reconfiguration Performance Benchmark ==="
echo "Cluster: $CLUSTER_NAME"
echo ""

# Build load generator if needed
if [ ! -f "$SCRIPT_DIR/loadgen" ]; then
    echo "Building load generator..."
    (cd "$SCRIPT_DIR" && go build -o loadgen loadgen.go)
    echo ""
fi

mkdir -p bench-results

# Get initial replica count
INITIAL_REPLICAS=$(kubectl get raftcluster "$CLUSTER_NAME" -o jsonpath='{.spec.replicas}')
echo "Initial replicas: $INITIAL_REPLICAS"

# Port-forward to client service for stable access
echo "Port-forwarding to service ${CLUSTER_NAME}-client..."
kubectl port-forward "svc/${CLUSTER_NAME}-client" 8080:8080 &
PF_PID=$!
sleep 2

# Cleanup function
cleanup() {
    echo "Cleaning up..."
    kill $LOAD_PID 2>/dev/null || true
    kill $PF_PID 2>/dev/null || true

    # Restore original replica count
    echo "Restoring replica count to $INITIAL_REPLICAS..."
    kubectl patch raftcluster "$CLUSTER_NAME" --type=merge -p "{\"spec\":{\"replicas\":$INITIAL_REPLICAS}}" || true
}
trap cleanup EXIT

# Start continuous load
echo "Starting background load (50 QPS)..."
"$SCRIPT_DIR/loadgen" \
    --urls "http://localhost:8080" \
    --qps 50 \
    --duration 180s \
    --workers 10 \
    --keys 500 \
    --value-size 100 \
    --read-ratio 0.5 \
    --report-interval 5s \
    --output bench-results/reconfig-load.json &
LOAD_PID=$!

# Wait for load to stabilize
echo "Waiting for load to stabilize (15s)..."
sleep 15

# Scale up
TARGET_REPLICAS=5
echo ""
echo "=== Scale Up: $INITIAL_REPLICAS -> $TARGET_REPLICAS ==="
SCALE_START=$(date +%s)

kubectl patch raftcluster "$CLUSTER_NAME" --type=merge -p "{\"spec\":{\"replicas\":$TARGET_REPLICAS}}"

# Wait for scale up to complete
echo "Waiting for scale up to complete..."
while true; do
    READY_PODS=$(kubectl get pods -l app=raft-kv --field-selector=status.phase=Running 2>/dev/null | tail -n +2 | wc -l)
    if [ "$READY_PODS" -eq "$TARGET_REPLICAS" ]; then
        SCALE_END=$(date +%s)
        SCALE_UP_DURATION=$((SCALE_END - SCALE_START))
        echo "✓ Scale up completed in ${SCALE_UP_DURATION}s"
        break
    fi
    sleep 2
done

# Wait a bit
echo "Waiting for cluster to stabilize (20s)..."
sleep 20

# Scale down
echo ""
echo "=== Scale Down: $TARGET_REPLICAS -> $INITIAL_REPLICAS ==="
SCALE_START=$(date +%s)

kubectl patch raftcluster "$CLUSTER_NAME" --type=merge -p "{\"spec\":{\"replicas\":$INITIAL_REPLICAS}}"

# Wait for scale down to complete
echo "Waiting for scale down to complete..."
while true; do
    READY_PODS=$(kubectl get pods -l app=raft-kv --field-selector=status.phase=Running 2>/dev/null | tail -n +2 | wc -l)
    if [ "$READY_PODS" -eq "$INITIAL_REPLICAS" ]; then
        SCALE_END=$(date +%s)
        SCALE_DOWN_DURATION=$((SCALE_END - SCALE_START))
        echo "✓ Scale down completed in ${SCALE_DOWN_DURATION}s"
        break
    fi
    sleep 2
done

echo ""
echo "Waiting for load test to complete..."
wait $LOAD_PID 2>/dev/null || true

# Analyze results
if [ -f bench-results/reconfig-load.json ]; then
    echo ""
    echo "=== Results ==="
    TOTAL_OPS=$(jq -r '.stats.total_ops' bench-results/reconfig-load.json)
    FAILED_OPS=$(jq -r '.stats.failed_ops' bench-results/reconfig-load.json)
    FAILURE_RATE=$(jq -r '(.stats.failed_ops / .stats.total_ops * 100)' bench-results/reconfig-load.json)
    AVG_LATENCY=$(jq -r '(.stats.avg_latency_ns / 1000000)' bench-results/reconfig-load.json)
    P99_LATENCY=$(jq -r '(.stats.p99_latency_ns / 1000000)' bench-results/reconfig-load.json)

    echo "Total operations: $TOTAL_OPS"
    echo "Failed operations: $FAILED_OPS (${FAILURE_RATE}%)"
    echo "Average latency: ${AVG_LATENCY} ms"
    echo "P99 latency: ${P99_LATENCY} ms"
    echo "Scale up duration: ${SCALE_UP_DURATION}s"
    echo "Scale down duration: ${SCALE_DOWN_DURATION}s"

    # Save detailed results
    jq --arg scale_up "$SCALE_UP_DURATION" \
       --arg scale_down "$SCALE_DOWN_DURATION" \
       '. + {
           scale_up_duration_s: ($scale_up | tonumber),
           scale_down_duration_s: ($scale_down | tonumber)
       }' bench-results/reconfig-load.json > bench-results/reconfig-results.json
fi

echo ""
echo "Results saved to bench-results/reconfig-results.json"
