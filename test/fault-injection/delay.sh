#!/bin/bash
# Inject network delay on a pod
# Usage: ./delay.sh POD_NAME DELAY_MS [JITTER_MS]

set -e

POD="$1"
DELAY="${2:-200}"
JITTER="${3:-50}"

if [ -z "$POD" ]; then
    echo "Usage: $0 POD_NAME [DELAY_MS] [JITTER_MS]"
    echo "Example: $0 kv-2 200 50"
    exit 1
fi

echo "Injecting network delay on pod: $POD"
echo "Delay: ${DELAY}ms, Jitter: ${JITTER}ms"

# Add delay using tc (traffic control)
kubectl exec "$POD" -- tc qdisc add dev eth0 root netem delay "${DELAY}ms" "${JITTER}ms"

echo "Delay injected successfully!"
echo "To remove: ./heal.sh"
