#!/bin/bash
# Check linearizability using Porcupine
# Usage: ./check-linearizability.sh [cluster-name]

set -e

CLUSTER_NAME="${1:-demo}"

echo "=== Porcupine Linearizability Check ==="
echo "Cluster: $CLUSTER_NAME"
echo ""

# Step 1: Collect operation history from all nodes
echo "Step 1: Collecting operation history from all nodes..."

PODS=$(kubectl get pods -l cluster="$CLUSTER_NAME" -o jsonpath='{.items[*].metadata.name}')
mkdir -p violations

for pod in $PODS; do
    echo "  Fetching history from $pod..."
    kubectl exec "$pod" -- curl -s http://localhost:8080/history > "violations/history-${pod}.json" || true
done

# Step 2: Merge histories
echo ""
echo "Step 2: Merging operation histories..."

# Simple merge - in production, use a proper tool
cat violations/history-*.json > violations/combined-history.json 2>/dev/null || echo "[]" > violations/combined-history.json

# Step 3: Run Porcupine checker (placeholder - would use actual Go program)
echo ""
echo "Step 3: Running Porcupine linearizability checker..."
echo "(This is a placeholder - actual implementation would use Porcupine Go library)"

# Count operations
OPS=$(grep -o '"type"' violations/combined-history.json 2>/dev/null | wc -l || echo "0")
echo "  Total operations collected: $OPS"

# Step 4: Report results
echo ""
echo "=== Results ==="

if [ "$OPS" -eq "0" ]; then
    echo "⚠️  No operations found - may indicate a problem"
elif grep -q '"success":false' violations/combined-history.json 2>/dev/null; then
    echo "❌ VIOLATION DETECTED: Failed operations found"
    echo "   This may indicate linearizability violation"
else
    echo "✅ No obvious violations found"
    echo "   (Full Porcupine analysis would provide definitive result)"
fi

echo ""
echo "Operation history saved to: violations/combined-history.json"
echo ""
echo "To implement full checking, use the Porcupine Go library:"
echo "  https://github.com/anishathalye/porcupine"
