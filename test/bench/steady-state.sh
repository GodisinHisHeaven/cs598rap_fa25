#!/bin/bash
# Steady-state throughput and latency benchmark
# Usage: ./steady-state.sh [cluster-name]

set -e

CLUSTER_NAME="${1:-demo}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== Steady-State Performance Benchmark ==="
echo "Cluster: $CLUSTER_NAME"
echo ""

# Build load generator if needed
if [ ! -f "$SCRIPT_DIR/loadgen" ]; then
    echo "Building load generator..."
    (cd "$SCRIPT_DIR" && go build -o loadgen loadgen.go)
    echo ""
fi

# Create results directory
mkdir -p bench-results

# Get a node to send requests to
POD=$(kubectl get pods -l app=raft-kv -o jsonpath='{.items[0].metadata.name}')
echo "Port-forwarding to $POD..."
kubectl port-forward "$POD" 8080:8080 &
PF_PID=$!

# Wait for port-forward
sleep 2

# Cleanup function
cleanup() {
    echo "Cleaning up..."
    kill $PF_PID 2>/dev/null || true
}
trap cleanup EXIT

# Test different QPS levels
QPS_LEVELS=(10 50 100 200 500 1000)
DURATION=30

echo "Testing different QPS levels..."
echo ""

for qps in "${QPS_LEVELS[@]}"; do
    echo "=== Testing QPS: $qps ==="
    OUTPUT_FILE="bench-results/steady-state-qps-${qps}.json"

    "$SCRIPT_DIR/loadgen" \
        --urls "http://localhost:8080" \
        --qps "$qps" \
        --duration "${DURATION}s" \
        --workers 20 \
        --keys 1000 \
        --value-size 100 \
        --read-ratio 0.7 \
        --output "$OUTPUT_FILE"

    echo ""
    sleep 2
done

echo ""
echo "=== Benchmark Complete ==="
echo "Results saved to bench-results/"
echo ""

# Generate summary
echo "Summary:"
for qps in "${QPS_LEVELS[@]}"; do
    FILE="bench-results/steady-state-qps-${qps}.json"
    if [ -f "$FILE" ]; then
        ACTUAL_QPS=$(jq -r '.stats.total_ops / .config.duration' "$FILE")
        P99=$(jq -r '.stats.p99_latency_ns / 1000000' "$FILE")
        printf "  QPS %4d: actual %.1f ops/s, P99 %.2f ms\n" "$qps" "$ACTUAL_QPS" "$P99"
    fi
done
