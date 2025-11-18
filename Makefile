.PHONY: help build test clean deploy delete bug1 bug2 bench docker-build install-crd

# Variables
DOCKER_REGISTRY ?= localhost:5000
SERVER_IMAGE ?= $(DOCKER_REGISTRY)/raft-kv
OPERATOR_IMAGE ?= $(DOCKER_REGISTRY)/raft-operator
VERSION ?= latest

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build server and operator binaries
	@echo "Building server..."
	cd server && go build -o raft-kv ./cmd/server
	@echo "Building operator..."
	cd operator && go build -o raft-operator ./cmd/operator
	@echo "Build complete!"

test: ## Run unit tests
	@echo "Running server tests..."
	cd server && go test -v ./...
	@echo "Running operator tests..."
	cd operator && go test -v ./...

docker-build: ## Build Docker images
	@echo "Building server Docker image..."
	docker build -t $(SERVER_IMAGE):$(VERSION) -f server/Dockerfile server/
	@echo "Building operator Docker image..."
	docker build -t $(OPERATOR_IMAGE):$(VERSION) -f operator/Dockerfile.multi .
	@echo "Docker images built successfully!"

docker-push: docker-build ## Push Docker images to registry
	docker push $(SERVER_IMAGE):$(VERSION)
	docker push $(OPERATOR_IMAGE):$(VERSION)

install-crd: ## Install CRD to Kubernetes cluster
	kubectl apply -f manifests/crd.yaml

deploy-operator: install-crd ## Deploy operator to Kubernetes
	kubectl apply -f manifests/rbac.yaml
	kubectl apply -f manifests/operator-deployment.yaml

deploy: deploy-operator ## Deploy a sample RaftCluster (safe mode)
	kubectl apply -f examples/raftcluster-safe.yaml
	@echo "Waiting for cluster to be ready..."
	kubectl wait --for=condition=Ready raftcluster/demo --timeout=300s

deploy-unsafe-early: deploy-operator ## Deploy cluster with unsafe-early-vote mode
	kubectl apply -f examples/raftcluster-unsafe-early-vote.yaml

deploy-unsafe-nojoint: deploy-operator ## Deploy cluster with unsafe-no-joint mode
	kubectl apply -f examples/raftcluster-unsafe-no-joint.yaml

delete: ## Delete RaftCluster and operator
	kubectl delete -f examples/raftcluster-safe.yaml --ignore-not-found=true
	kubectl delete -f examples/raftcluster-unsafe-early-vote.yaml --ignore-not-found=true
	kubectl delete -f examples/raftcluster-unsafe-no-joint.yaml --ignore-not-found=true
	kubectl delete -f manifests/operator-deployment.yaml --ignore-not-found=true
	kubectl delete -f manifests/rbac.yaml --ignore-not-found=true

bug1: ## Reproduce Bug 1 (Early-Vote Learner)
	@echo "Deploying cluster with unsafe-early-vote mode..."
	kubectl apply -f examples/raftcluster-unsafe-early-vote.yaml
	@echo "Waiting for cluster to be ready..."
	kubectl wait --for=condition=Ready raftcluster/bug1-demo --timeout=300s || true
	@echo "Starting load generation..."
	./test/fault-injection/bug1-reproduce.sh
	@echo "Checking for violations with Porcupine..."
	./test/correctness/check-linearizability.sh

bug2: ## Reproduce Bug 2 (No Joint Consensus)
	@echo "Deploying cluster with unsafe-no-joint mode..."
	kubectl apply -f examples/raftcluster-unsafe-no-joint.yaml
	@echo "Waiting for cluster to be ready..."
	kubectl wait --for=condition=Ready raftcluster/bug2-demo --timeout=300s || true
	@echo "Starting load generation and fault injection..."
	./test/fault-injection/bug2-reproduce.sh
	@echo "Checking for violations with Porcupine..."
	./test/correctness/check-linearizability.sh

bench: ## Run performance benchmarks
	@echo "Running steady-state benchmark..."
	./test/bench/steady-state.sh
	@echo "Running failover benchmark..."
	./test/bench/failover.sh
	@echo "Running reconfiguration benchmark..."
	./test/bench/reconfiguration.sh
	@echo "Running snapshot benchmark..."
	./test/bench/snapshot.sh
	@echo "Benchmarks complete! Results in bench-results/"

setup-kind: ## Setup local kind cluster
	@echo "Creating kind cluster..."
	kind create cluster --name raft-test --config examples/kind-config.yaml || true
	@echo "Setting up local registry..."
	./examples/setup-local-registry.sh

teardown-kind: ## Teardown kind cluster
	kind delete cluster --name raft-test

clean: ## Clean build artifacts
	rm -f server/raft-kv operator/raft-operator
	rm -rf test-results/ bench-results/
	rm -rf server/data/ server/wal/ server/snapshots/
	find . -name "*.log" -delete

fmt: ## Format Go code
	cd server && go fmt ./...
	cd operator && go fmt ./...

vet: ## Run go vet
	cd server && go vet ./...
	cd operator && go vet ./...

lint: fmt vet ## Run linters
	@echo "Linting complete!"

all: lint build docker-build ## Build everything
