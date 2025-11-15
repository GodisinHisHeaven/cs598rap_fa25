#!/bin/bash
# Heal network partition by removing all tc and iptables rules
# Usage: ./heal.sh

set -e

CLUSTER_NAME="${1:-demo}"

echo "Healing network partition for cluster: $CLUSTER_NAME"

# Get all pods for the cluster
PODS=$(kubectl get pods -l cluster="$CLUSTER_NAME" -o jsonpath='{.items[*].metadata.name}')

for pod in $PODS; do
    echo "Healing pod: $pod"

    # Remove tc rules
    kubectl exec "$pod" -- tc qdisc del dev eth0 root 2>/dev/null || true

    # Flush iptables OUTPUT chain
    kubectl exec "$pod" -- iptables -F OUTPUT 2>/dev/null || true

    echo "  ✓ Healed $pod"
done

echo "Network partition healed successfully!"
