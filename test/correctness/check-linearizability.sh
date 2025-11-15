#!/bin/bash
# Check linearizability using Porcupine
# Usage: ./check-linearizability.sh [cluster-name] [node-url]

set -e

CLUSTER_NAME="${1:-demo}"
NODE_URL="${2:-}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECKER_DIR="$SCRIPT_DIR/porcupine"

echo "=== Porcupine Linearizability Check ==="
echo "Cluster: $CLUSTER_NAME"
echo ""

# Build the checker if needed
if [ ! -f "$CHECKER_DIR/checker" ]; then
    echo "Building Porcupine checker..."
    (cd "$CHECKER_DIR" && go build -o checker .)
    echo ""
fi

mkdir -p violations

# Collect history
if [ -n "$NODE_URL" ]; then
    # Fetch from specific URL
    echo "Fetching operation history from $NODE_URL/history..."
    curl -s "$NODE_URL/history" > violations/history.json
else
    # Collect from Kubernetes pods
    echo "Step 1: Collecting operation history from all nodes..."

    PODS=$(kubectl get pods -l app=raft-kv -o jsonpath='{.items[*].metadata.name}')

    for pod in $PODS; do
        echo "  Fetching history from $pod..."
        kubectl exec "$pod" -- curl -s http://localhost:8080/history > "violations/history-${pod}.json" 2>/dev/null || true
    done

    # Use first non-empty history file
    for f in violations/history-*.json; do
        if [ -f "$f" ] && [ -s "$f" ]; then
            cp "$f" violations/history.json
            break
        fi
    done
fi

# Count operations
OPS=$(grep -o '"type"' violations/history.json 2>/dev/null | wc -l || echo "0")
echo ""
echo "Total operations collected: $OPS"

if [ "$OPS" -eq "0" ]; then
    echo "⚠️  No operations found - skipping linearizability check"
    exit 0
fi

# Run Porcupine checker
echo ""
echo "Running Porcupine linearizability checker..."
echo ""

if "$CHECKER_DIR/checker" --file violations/history.json --output violations/visualization.html --verbose; then
    echo ""
    echo "=== RESULT: PASS ==="
    echo "✓ History is linearizable"
    exit 0
else
    EXIT_CODE=$?
    echo ""
    echo "=== RESULT: FAIL ==="
    echo "✗ Linearizability violation detected"
    echo ""
    echo "Details saved to: violations/"
    exit $EXIT_CODE
fi
