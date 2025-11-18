# Demo Walkthrough: Protocol-Aware Raft Operator

This document shows what the `simple-demo.sh` script demonstrates when run on a Kubernetes cluster.

## Prerequisites Check

```bash
✓ kubectl installed
✓ curl installed
✓ Kubernetes cluster running (kind)
✓ Raft operator deployed
```

## Demo Flow

### Step 1: Deploy a 3-node Raft Cluster

The demo creates a RaftCluster custom resource:

```yaml
apiVersion: storage.sys/v1
kind: RaftCluster
metadata:
  name: demo
spec:
  replicas: 3                      # Start with 3 nodes
  image: localhost:5000/raft-kv:latest
  storageSize: 1Gi
  snapshotInterval: "30s"
  electionTimeoutMs: 300
  safetyMode: safe                 # Uses Joint Consensus
  reconfig:
    rateLimit: "1/min"
    maxParallel: 1
```

**What happens:**
```
Operator detects new RaftCluster CR
  ↓
Creates StatefulSet with 3 pods
  ↓
Creates headless Service for Raft peer communication
  ↓
Pods start: demo-0, demo-1, demo-2
  ↓
Bootstrap Raft cluster (initial config: {demo-0, demo-1, demo-2})
  ↓
Leader election occurs (~300ms)
  ↓
Cluster reaches "Running" state
```

**Expected output:**
```
==> Deploying 3-node Raft cluster (safe mode)...
✓ RaftCluster created

==> Waiting for cluster to be ready...
pod/demo-0 condition met
pod/demo-1 condition met
pod/demo-2 condition met
✓ Cluster is running

NAME     READY   STATUS    RESTARTS   AGE
demo-0   1/1     Running   0          45s
demo-1   1/1     Running   0          43s
demo-2   1/1     Running   0          41s
```

### Step 2: Check Cluster Status

```bash
kubectl get raftcluster demo -o wide
```

**Expected output:**
```
NAME   PHASE    REPLICAS   LEADER   SAFETY_MODE   AGE
demo   Running  3          demo-1   safe          1m
```

**Status details:**
- **Phase**: Running (healthy)
- **Replicas**: 3/3 (all nodes up)
- **Leader**: demo-1 (elected leader)
- **Safety Mode**: safe (using Joint Consensus)

### Step 3: Test KV Store Operations

The demo port-forwards to `demo-0` and performs CRUD operations:

#### PUT Operation

```bash
curl -X POST http://localhost:8080/kv/key1 -d '{"value":"Hello, Raft!"}'
```

**What happens internally:**
```
Client → API Server → Raft Node
                       ↓
                   Propose(PUT key1="Hello, Raft!")
                       ↓
                   Append to leader's log (index 1, term 1)
                       ↓
                   Replicate to demo-1, demo-2 via AppendEntries RPC
                       ↓
                   Majority ACK (2/3)
                       ↓
                   Commit index advances to 1
                       ↓
                   Apply to state machine: store["key1"] = "Hello, Raft!"
                       ↓
                   Respond to client: {"success": true, "index": 1}
```

**Response:**
```json
{
  "success": true,
  "index": 1,
  "term": 1
}
```

#### PUT Another Key

```bash
curl -X POST http://localhost:8080/kv/key2 -d '{"value":"Protocol awareness matters"}'
```

**Response:**
```json
{
  "success": true,
  "index": 2,
  "term": 1
}
```

#### GET Operations

```bash
curl http://localhost:8080/kv/key1
```

**Response:**
```json
{
  "value": "Hello, Raft!",
  "index": 1,
  "ok": true
}
```

```bash
curl http://localhost:8080/kv/key2
```

**Response:**
```json
{
  "value": "Protocol awareness matters",
  "index": 2,
  "ok": true
}
```

**Note:** GET can be served from ANY replica (demo-0, demo-1, or demo-2) and will return the same result because Raft ensures all replicas have the same committed log.

#### DELETE Operation

```bash
curl -X DELETE http://localhost:8080/kv/key1
```

**What happens:**
- Same Raft consensus process
- State machine applies: `delete(store["key1"])`
- Entry logged at index 3

**Response:**
```json
{
  "success": true,
  "index": 3
}
```

#### GET Deleted Key

```bash
curl http://localhost:8080/kv/key1
```

**Response:**
```json
{
  "ok": false,
  "error": "key not found"
}
```

### Step 4: Demonstrate Safe Reconfiguration

The demo scales the cluster from 3 to 5 nodes:

```bash
kubectl patch raftcluster demo --type=merge -p '{"spec":{"replicas":5}}'
```

**What happens (6-step Joint Consensus):**

```
┌─────────────────────────────────────────────────────────────┐
│ Step 1: AddLearner (t=0s)                                   │
├─────────────────────────────────────────────────────────────┤
│ Operator: Detect desired=5, current=3                       │
│ Action:   Scale StatefulSet to 5                            │
│           Pods demo-3, demo-4 created                       │
│           Admin RPC: AddLearner(demo-3), AddLearner(demo-4) │
│ State:    Voters: {demo-0, demo-1, demo-2}                  │
│           Learners: {demo-3, demo-4}                         │
│ Status:   reconfigStep: AddLearner                          │
└─────────────────────────────────────────────────────────────┘
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ Step 2: CatchUp (t=5s)                                      │
├─────────────────────────────────────────────────────────────┤
│ Operator: Poll CaughtUp(demo-3), CaughtUp(demo-4) every 2s │
│ Action:   Wait for Match[demo-3] ≈ Commit                   │
│           Wait for Match[demo-4] ≈ Commit                   │
│ State:    demo-3: Match=0 → 100 → 200 → ... → 495          │
│           demo-4: Match=0 → 95  → 180 → ... → 498          │
│           Commit=500                                         │
│           CaughtUp when Match >= 490 (within tolerance)     │
│ Status:   reconfigStep: CatchUp                             │
│ Duration: ~10-20s (depends on log size)                     │
└─────────────────────────────────────────────────────────────┘
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ Step 3: EnterJoint (t=25s)                                  │
├─────────────────────────────────────────────────────────────┤
│ Operator: Admin RPC: ProposeConfChange(C_old,new)          │
│ Action:   Raft commits joint configuration                  │
│ State:    C_old,new = {                                      │
│             Voters: {demo-0, demo-1, demo-2, demo-3, demo-4}│
│             Joint: true                                      │
│           }                                                  │
│ Quorum:   Need 2/3 (old) AND 3/5 (new)                     │
│ Status:   reconfigStep: EnterJoint                          │
│           jointState: InJoint                                │
└─────────────────────────────────────────────────────────────┘
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ Step 4: CommitNew (t=30s)                                   │
├─────────────────────────────────────────────────────────────┤
│ Operator: Admin RPC: ProposeConfChange(C_new)              │
│ Action:   Raft commits new configuration                    │
│ State:    C_new = {                                          │
│             Voters: {demo-0, demo-1, demo-2, demo-3, demo-4}│
│             Joint: false                                     │
│           }                                                  │
│ Quorum:   Need 3/5 (but still in joint, so both required)  │
│ Status:   reconfigStep: CommitNew                           │
└─────────────────────────────────────────────────────────────┘
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ Step 5: LeaveJoint (t=35s)                                  │
├─────────────────────────────────────────────────────────────┤
│ Operator: Wait for C_new to be applied                     │
│ Action:   Verify all nodes see C_new                        │
│ State:    All nodes now use simple majority: 3/5           │
│ Status:   reconfigStep: LeaveJoint                          │
│           jointState: NotInJoint                             │
│           phase: Running                                     │
└─────────────────────────────────────────────────────────────┘

Final State:
  Replicas: 5
  Voters: {demo-0, demo-1, demo-2, demo-3, demo-4}
  Quorum: 3/5
  Phase: Running
  Reconfiguration: Complete
```

**Watch reconfiguration:**

```bash
kubectl get raftcluster demo -w
```

**Expected output:**
```
NAME   PHASE          REPLICAS   RECONFIG_STEP   LEADER   AGE
demo   Reconfiguring  5          AddLearner      demo-1   3m
demo   Reconfiguring  5          CatchUp         demo-1   3m10s
demo   Reconfiguring  5          EnterJoint      demo-1   3m25s
demo   Reconfiguring  5          CommitNew       demo-1   3m40s
demo   Reconfiguring  5          LeaveJoint      demo-1   3m50s
demo   Running        5          -               demo-1   4m
```

### Step 5: Verify Cluster After Scaling

```bash
kubectl get pods -l cluster=demo
```

**Expected output:**
```
NAME     READY   STATUS    RESTARTS   AGE
demo-0   1/1     Running   0          4m30s
demo-1   1/1     Running   0          4m28s
demo-2   1/1     Running   0          4m26s
demo-3   1/1     Running   0          1m15s
demo-4   1/1     Running   0          1m15s
```

**Verify data is still accessible:**

```bash
curl http://localhost:8080/kv/key2
```

**Response:**
```json
{
  "value": "Protocol awareness matters",
  "index": 2,
  "ok": true
}
```

✅ **Data preserved during reconfiguration!**

## Safety Guarantees Demonstrated

### 1. Linearizability
- PUT waits for Raft commit before responding
- GET returns committed values
- All operations appear atomic and ordered

### 2. Election Safety
- Only one leader per term
- Leader has all committed entries
- Learners can't vote (prevents stale nodes from winning)

### 3. State Machine Safety
- No conflicting values at same index
- Joint consensus prevents split-brain
- All replicas apply entries in same order

### 4. Reconfiguration Safety
- AddLearner: Safe to add (doesn't affect quorum)
- CatchUp: Ensures new nodes are up-to-date
- EnterJoint: Requires both old AND new quorums
- CommitNew: Safely transitions to new config
- LeaveJoint: Finalizes without data loss

## What You Can Try Next

After the demo completes, you can:

### Scale Down
```bash
kubectl patch raftcluster demo --type=merge -p '{"spec":{"replicas":3}}'
```

This triggers the same 6-step workflow in reverse (with leader transfer if needed).

### Reproduce Bug 1 (Election Safety Violation)
```bash
make bug1
```

Demonstrates what happens with unsafe-early-vote mode.

### Reproduce Bug 2 (Split-Brain)
```bash
make bug2
```

Demonstrates what happens with unsafe-no-joint mode.

### Run Benchmarks
```bash
make bench
```

Measures:
- Throughput (ops/sec)
- Latency (p50, p95, p99)
- Leader failover time
- Reconfiguration duration

### Inject Faults Manually
```bash
# Kill leader
./test/fault-injection/leader-kill.sh demo

# Create network partition
./test/fault-injection/partition.sh "demo-0,demo-1" "demo-2,demo-3,demo-4"

# Heal partition
./test/fault-injection/heal.sh demo
```

## Summary

This demo shows:
✅ Declarative cluster management via Kubernetes CRD
✅ Linearizable KV store with strong consistency
✅ Safe reconfiguration using Joint Consensus
✅ Protocol-aware operator that understands Raft semantics
✅ Data preservation during membership changes
✅ Fault tolerance (survives minority failures)

**Time to complete:** ~5 minutes
**Prerequisites:** Kubernetes cluster + Docker images built
