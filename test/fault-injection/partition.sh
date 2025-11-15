#!/bin/bash
# Create network partition between two groups of pods
# Usage: ./partition.sh "kv-0,kv-1" "kv-2,kv-3"

set -e

GROUP_A="$1"
GROUP_B="$2"

if [ -z "$GROUP_A" ] || [ -z "$GROUP_B" ]; then
    echo "Usage: $0 GROUP_A GROUP_B"
    echo "Example: $0 'kv-0,kv-1' 'kv-2,kv-3'"
    exit 1
fi

echo "Creating network partition..."
echo "Group A: $GROUP_A"
echo "Group B: $GROUP_B"

# Convert comma-separated lists to arrays
IFS=',' read -ra PODS_A <<< "$GROUP_A"
IFS=',' read -ra PODS_B <<< "$GROUP_B"

# Block traffic from Group A to Group B
for pod_a in "${PODS_A[@]}"; do
    pod_a=$(echo "$pod_a" | xargs)  # Trim whitespace
    for pod_b in "${PODS_B[@]}"; do
        pod_b=$(echo "$pod_b" | xargs)
        echo "Blocking: $pod_a -> $pod_b"

        # Get the pod IP
        POD_B_IP=$(kubectl get pod "$pod_b" -o jsonpath='{.status.podIP}')

        # Add iptables rule to drop packets to the other pod
        kubectl exec "$pod_a" -- iptables -A OUTPUT -d "$POD_B_IP" -j DROP 2>/dev/null || \
            kubectl exec "$pod_a" -- sh -c "tc qdisc add dev eth0 root newtork delay 10000ms" 2>/dev/null || \
            echo "Warning: Could not add firewall rule (may need privileged mode)"
    done
done

# Block traffic from Group B to Group A
for pod_b in "${PODS_B[@]}"; do
    pod_b=$(echo "$pod_b" | xargs)
    for pod_a in "${PODS_A[@]}"; do
        pod_a=$(echo "$pod_a" | xargs)
        echo "Blocking: $pod_b -> $pod_a"

        POD_A_IP=$(kubectl get pod "$pod_a" -o jsonpath='{.status.podIP}')

        kubectl exec "$pod_b" -- iptables -A OUTPUT -d "$POD_A_IP" -j DROP 2>/dev/null || \
            kubectl exec "$pod_b" -- sh -c "tc qdisc add dev eth0 root netem delay 10000ms" 2>/dev/null || \
            echo "Warning: Could not add firewall rule (may need privileged mode)"
    done
done

echo "Partition created successfully!"
echo "To heal: ./heal.sh"
