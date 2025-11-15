#!/bin/bash
# Kill the current Raft leader
# Usage: ./leader-kill.sh [cluster-name]

set -e

CLUSTER_NAME="${1:-demo}"

echo "Finding and killing leader for cluster: $CLUSTER_NAME"

# Get cluster status
LEADER=$(kubectl get raftcluster "$CLUSTER_NAME" -o jsonpath='{.status.leader}')

if [ -z "$LEADER" ]; then
    echo "Error: No leader found for cluster $CLUSTER_NAME"
    exit 1
fi

echo "Current leader: $LEADER"
echo "Deleting leader pod..."

kubectl delete pod "$LEADER" --wait=false

echo "Leader pod deleted: $LEADER"
echo "Cluster will elect a new leader..."
echo ""
echo "Watch status with: kubectl get raftcluster $CLUSTER_NAME -w"
