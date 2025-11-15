#!/bin/bash
# Setup local Docker registry for kind
set -e

echo "Setting up local Docker registry for kind..."

# Create registry container unless it already exists
reg_name='kind-registry'
reg_port='5000'

if [ "$(docker inspect -f '{{.State.Running}}' "${reg_name}" 2>/dev/null || true)" != 'true' ]; then
  echo "Creating registry container..."
  docker run \
    -d --restart=always -p "127.0.0.1:${reg_port}:5000" --name "${reg_name}" \
    registry:2
else
  echo "Registry container already exists"
fi

# Connect the registry to the kind network if not already connected
if [ "$(docker inspect -f='{{json .NetworkSettings.Networks.kind}}' "${reg_name}")" = 'null' ]; then
  echo "Connecting registry to kind network..."
  docker network connect "kind" "${reg_name}" || true
fi

# Document the local registry
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "localhost:${reg_port}"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF

echo ""
echo "✅ Local registry setup complete!"
echo "   Registry: localhost:${reg_port}"
echo ""
echo "Push images with:"
echo "  docker tag my-image localhost:5000/my-image:tag"
echo "  docker push localhost:5000/my-image:tag"
