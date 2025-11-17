# System Architecture & Workflow Diagrams

## 1. Overall System Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         User Interface                              │
│                                                                     │
│  $ kubectl apply -f raftcluster.yaml                               │
│  $ kubectl scale raftcluster demo --replicas=5                     │
│  $ curl http://kv-0:8080/kv/mykey -d '{"value":"foo"}'            │
└────────────────────────────┬────────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    Kubernetes Control Plane                         │
│                                                                     │
│  ┌──────────────────────────────────────────────────────┐          │
│  │         RaftCluster Custom Resource                  │          │
│  │  ┌────────────────────────────────────────┐          │          │
│  │  │ apiVersion: storage.sys/v1             │          │          │
│  │  │ kind: RaftCluster                      │          │          │
│  │  │ metadata:                              │          │          │
│  │  │   name: demo                           │          │          │
│  │  │ spec:                                  │          │          │
│  │  │   replicas: 3                          │          │          │
│  │  │   safetyMode: safe                     │          │          │
│  │  │ status:                                │          │          │
│  │  │   phase: Running                       │          │          │
│  │  │   leader: kv-1                         │          │          │
│  │  │   reconfigStep: -                      │          │          │
│  │  └────────────────────────────────────────┘          │          │
│  └──────────────┬───────────────────────────────────────┘          │
│                 │ watch/update                                     │
│                 ▼                                                  │
│  ┌──────────────────────────────────────────────────────┐          │
│  │         Raft Operator (Reconciliation Loop)          │          │
│  │                                                       │          │
│  │  ┌─────────────────────────────────────────┐         │          │
│  │  │  1. Observe desired state (spec)        │         │          │
│  │  │  2. Observe current state (status)      │         │          │
│  │  │  3. Calculate diff                      │         │          │
│  │  │  4. Execute reconfiguration workflow    │         │          │
│  │  │  5. Update status                       │         │          │
│  │  └─────────────────────────────────────────┘         │          │
│  │                                                       │          │
│  │  Admin RPC Client ────────────────────┐              │          │
│  └───────────────────────────────────────┼──────────────┘          │
│                                          │                         │
└──────────────────────────────────────────┼─────────────────────────┘
                                           │ gRPC
                                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│                       Raft Cluster (Data Plane)                     │
│                                                                     │
│  ┌─────────────────┐      ┌─────────────────┐      ┌─────────────┐ │
│  │    Pod: kv-0    │      │    Pod: kv-1    │      │  Pod: kv-2  │ │
│  │   (Follower)    │◀────▶│    (Leader)     │◀────▶│ (Follower)  │ │
│  │                 │      │                 │      │             │ │
│  │ ┌─────────────┐ │      │ ┌─────────────┐ │      │ ┌──────────┐│ │
│  │ │  KV Store   │ │      │ │  KV Store   │ │      │ │ KV Store ││ │
│  │ │  (State     │ │      │ │  (State     │ │      │ │ (State   ││ │
│  │ │  Machine)   │ │      │ │  Machine)   │ │      │ │ Machine) ││ │
│  │ └──────┬──────┘ │      │ └──────┬──────┘ │      │ └────┬─────┘│ │
│  │        │        │      │        │        │      │      │      │ │
│  │ ┌──────▼──────┐ │      │ ┌──────▼──────┐ │      │ ┌────▼─────┐│ │
│  │ │ Raft Node   │ │      │ │ Raft Node   │ │      │ │Raft Node ││ │
│  │ │ (etcd/raft) │ │      │ │ (etcd/raft) │ │      │ │etcd/raft ││ │
│  │ └──────┬──────┘ │      │ └──────┬──────┘ │      │ └────┬─────┘│ │
│  │        │        │      │        │        │      │      │      │ │
│  │ ┌──────▼──────┐ │      │ ┌──────▼──────┐ │      │ ┌────▼─────┐│ │
│  │ │   Storage   │ │      │ │   Storage   │ │      │ │ Storage  ││ │
│  │ │  WAL + Snap │ │      │ │  WAL + Snap │ │      │ │WAL + Snap││ │
│  │ └─────────────┘ │      │ └─────────────┘ │      │ └──────────┘│ │
│  │        │        │      │        │        │      │      │      │ │
│  │        ▼        │      │        ▼        │      │      ▼      │ │
│  │ ┌─────────────┐ │      │ ┌─────────────┐ │      │ ┌──────────┐│ │
│  │ │     PVC     │ │      │ │     PVC     │ │      │ │   PVC    ││ │
│  │ │  (1Gi disk) │ │      │ │  (1Gi disk) │ │      │ │(1Gi disk)││ │
│  │ └─────────────┘ │      │ └─────────────┘ │      │ └──────────┘│ │
│  └─────────────────┘      └─────────────────┘      └─────────────┘ │
│                                                                     │
│  HTTP Transport: AppendEntries, RequestVote, Snapshot RPCs         │
│  Admin gRPC: AddLearner, Promote, RemoveNode, CaughtUp, etc.       │
└─────────────────────────────────────────────────────────────────────┘
```

## 2. Safe Reconfiguration Workflow (Scale 3 → 5)

### Joint Consensus: 6-Step Process

```
Initial State: {kv-0, kv-1, kv-2} = Voters

┌────────────────────────────────────────────────────────────────────┐
│ Step 1: AddLearner                                                 │
├────────────────────────────────────────────────────────────────────┤
│  Operator:  kubectl patch raftcluster demo -p '{"spec":{"replicas":5}}' │
│             ▼                                                       │
│  Controller: Detect desired=5, current=3                           │
│             ▼                                                       │
│  Create Pods: kv-3, kv-4 (StatefulSet scales)                     │
│             ▼                                                       │
│  Admin RPC: AddLearner(kv-3), AddLearner(kv-4)                    │
│             ▼                                                       │
│  Raft:     {kv-0, kv-1, kv-2} = Voters                            │
│            {kv-3, kv-4} = Learners (receive log, can't vote)      │
│                                                                     │
│  ✓ Safety: Learners don't affect quorum                           │
└────────────────────────────────────────────────────────────────────┘
                              ▼
┌────────────────────────────────────────────────────────────────────┐
│ Step 2: CatchUp                                                    │
├────────────────────────────────────────────────────────────────────┤
│  Controller: Poll CaughtUp(kv-3), CaughtUp(kv-4) every 2s         │
│             ▼                                                       │
│  Raft Node: Check if Match[kv-3] ≈ Commit                         │
│            Check if Match[kv-4] ≈ Commit                           │
│             ▼                                                       │
│  Wait until: kv-3.Match >= Leader.Commit - 10                     │
│              kv-4.Match >= Leader.Commit - 10                      │
│             ▼                                                       │
│  ✓ Safety: New nodes are up-to-date before voting                 │
└────────────────────────────────────────────────────────────────────┘
                              ▼
┌────────────────────────────────────────────────────────────────────┐
│ Step 3: EnterJoint                                                 │
├────────────────────────────────────────────────────────────────────┤
│  Admin RPC: ProposeConfChange(C_old,new)                          │
│             ▼                                                       │
│  Raft:     C_old,new = {                                           │
│              Voters:    {kv-0, kv-1, kv-2, kv-3, kv-4}            │
│              Joint:     true                                       │
│            }                                                        │
│             ▼                                                       │
│  Quorums:  Need majority of OLD: 2/3                              │
│            AND majority of NEW: 3/5                                │
│             ▼                                                       │
│  ✓ Safety: Impossible for C_old and C_new to both have quorum     │
│            independently → no split-brain                          │
└────────────────────────────────────────────────────────────────────┘
                              ▼
┌────────────────────────────────────────────────────────────────────┐
│ Step 4: CommitNew                                                  │
├────────────────────────────────────────────────────────────────────┤
│  Admin RPC: ProposeConfChange(C_new)                              │
│             ▼                                                       │
│  Raft:     C_new = {                                               │
│              Voters: {kv-0, kv-1, kv-2, kv-3, kv-4}               │
│              Joint:  false                                         │
│            }                                                        │
│             ▼                                                       │
│  Quorums:  Need 3/5 (simple majority of new config)               │
│             ▼                                                       │
│  ✓ Safety: Still requires C_old,new quorum to commit C_new        │
└────────────────────────────────────────────────────────────────────┘
                              ▼
┌────────────────────────────────────────────────────────────────────┐
│ Step 5: LeaveJoint                                                 │
├────────────────────────────────────────────────────────────────────┤
│  Controller: Wait for C_new to be applied                         │
│             ▼                                                       │
│  Status:    {kv-0, kv-1, kv-2, kv-3, kv-4} all Voters             │
│             ▼                                                       │
│  ✓ Safety: Reconfiguration complete, single quorum                │
└────────────────────────────────────────────────────────────────────┘
                              ▼
┌────────────────────────────────────────────────────────────────────┐
│ Step 6: LeaderTransfer (for scale-down)                           │
├────────────────────────────────────────────────────────────────────┤
│  If scaling down and leader is being removed:                     │
│             ▼                                                       │
│  Admin RPC: TransferLeader(to=kv-0)                               │
│             ▼                                                       │
│  Raft:     Leader steps down, transfers to kv-0                   │
│             ▼                                                       │
│  ✓ Safety: Prevent removing leader mid-operation                  │
└────────────────────────────────────────────────────────────────────┘

Final State: {kv-0, kv-1, kv-2, kv-3, kv-4} = Voters
```

## 3. Unsafe Mode Comparison

### Bug 1: unsafe-early-vote (Election Safety Violation)

```
Initial: {kv-0, kv-1, kv-2} - Leader: kv-1

┌─────────────────────────────────────────────────────────────────┐
│ Unsafe Workflow                                                 │
├─────────────────────────────────────────────────────────────────┤
│  1. Create kv-3                                                 │
│  2. ❌ AddVoter(kv-3) immediately (NO LEARNER STAGE)            │
│  3. kv-3 joins with EMPTY LOG                                  │
│  4. Network partition: {kv-0, kv-1} | {kv-2, kv-3}             │
│                                                                  │
│  Partition A: {kv-0, kv-1} - has committed entries [1..100]    │
│  Partition B: {kv-2, kv-3} - kv-3 has empty log []             │
│                                                                  │
│  5. kv-3 times out, starts election                            │
│  6. kv-2 votes for kv-3 (term 2)                               │
│  7. ❌ kv-3 becomes leader with STALE LOG                       │
│  8. kv-3 starts accepting writes at index 1 (overwriting!)     │
│                                                                  │
│  Result: COMMITTED ENTRIES LOST                                 │
│          Election Safety VIOLATED                               │
└─────────────────────────────────────────────────────────────────┘

Safe Mode: Learner catches up to ~index 100 BEFORE promotion
```

### Bug 2: unsafe-no-joint (Split-Brain Violation)

```
Initial: {kv-0, kv-1, kv-2} - Leader: kv-1

┌─────────────────────────────────────────────────────────────────┐
│ Unsafe Workflow                                                 │
├─────────────────────────────────────────────────────────────────┤
│  1. Create kv-3, kv-4                                           │
│  2. ❌ Direct transition: C_old → C_new (NO JOINT PHASE)        │
│  3. Some nodes see C_old, others see C_new                     │
│                                                                  │
│  Timeline:                                                       │
│  ─────────────────────────────────────────────────────────────  │
│  t=0:  kv-0, kv-1, kv-2 have C_old = {kv-0, kv-1, kv-2}        │
│  t=1:  Leader proposes C_new = {kv-0, kv-1, kv-2, kv-3, kv-4}  │
│  t=2:  kv-0, kv-1 apply C_new (2/5 quorum)                     │
│        kv-2, kv-3, kv-4 still have C_old / catch-up            │
│                                                                  │
│  Network partition: {kv-0, kv-1} | {kv-2, kv-3, kv-4}          │
│                                                                  │
│  Partition A:                           Partition B:            │
│    Config: C_new {kv-0..kv-4}            Config: C_old/mixed   │
│    Leader: kv-1 (term 5)                 Leader: kv-3 (term 6) │
│    Quorum: 2/5 ✓ (kv-0, kv-1)           Quorum: 2/3 ✓ (kv-2,kv-3) │
│    PUT key=x val=A at index=200         PUT key=x val=B at idx=200 │
│                                                                  │
│  Result: TWO DIFFERENT VALUES AT SAME INDEX                     │
│          State Machine Safety VIOLATED                          │
└─────────────────────────────────────────────────────────────────┘

Safe Mode: C_old,new requires BOTH quorums → impossible to have split-brain
```

## 4. API Request Flow (Linearizable Write)

```
Client                  API Server              Raft Node           State Machine
  │                         │                        │                    │
  │ POST /kv/key1          │                        │                    │
  │ {"value":"hello"}      │                        │                    │
  ├───────────────────────▶│                        │                    │
  │                        │                        │                    │
  │                        │ Propose(PUT key1=hello)│                    │
  │                        ├───────────────────────▶│                    │
  │                        │                        │                    │
  │                        │                        │ [Raft Consensus]   │
  │                        │                        │  1. Append to log  │
  │                        │                        │  2. Replicate      │
  │                        │                        │  3. Commit         │
  │                        │                        │                    │
  │                        │                        │ Apply(PUT key1)    │
  │                        │                        ├───────────────────▶│
  │                        │                        │                    │
  │                        │                        │                    │ [Execute]
  │                        │                        │                    │ store[key1]="hello"
  │                        │                        │                    │
  │                        │                        │◀───────────────────┤
  │                        │                        │  OK                │
  │                        │                        │                    │
  │                        │◀───────────────────────┤                    │
  │                        │  Committed at index=42 │                    │
  │                        │                        │                    │
  │◀───────────────────────┤                        │                    │
  │ 200 OK                 │                        │                    │
  │ {"success":true,       │                        │                    │
  │  "index":42}           │                        │                    │
  │                        │                        │                    │

Key Properties:
  ✓ Linearizability: Client doesn't get response until commit
  ✓ Durability: Entry is on majority before response
  ✓ Consistency: All replicas apply in same order (index 42)
```

## 5. Storage Layer (WAL + Snapshots)

```
                    Raft Log Lifecycle

┌─────────────────────────────────────────────────────────────────┐
│                        Write-Ahead Log                          │
├─────────────────────────────────────────────────────────────────┤
│  [Entry 1][Entry 2][Entry 3]...[Entry 1000]...[Entry 10000]   │
│                                                                  │
│  Each entry:                                                     │
│    - Index: 42                                                   │
│    - Term: 5                                                     │
│    - Type: Normal | ConfChange                                   │
│    - Data: {"op":"put", "key":"x", "value":"y"}                 │
└─────────────────────────────────────────────────────────────────┘
                             │
                             │ Log grows unbounded
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Snapshot Trigger                           │
├─────────────────────────────────────────────────────────────────┤
│  If log.size > snapshotThreshold (e.g., 10,000 entries):       │
│    1. Serialize state machine: snapshot = KVStore.Dump()       │
│    2. Save snapshot to disk: /data/snapshots/snap-10000        │
│    3. Record metadata: index=10000, term=5                     │
│    4. Compact WAL: delete entries [1..10000]                   │
└─────────────────────────────────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                      After Snapshot                             │
├─────────────────────────────────────────────────────────────────┤
│  Snapshot (index 10000):                                        │
│    ┌──────────────────────────────────────┐                    │
│    │ key1 → "value1"                      │                    │
│    │ key2 → "value2"                      │                    │
│    │ ...                                   │                    │
│    │ key9999 → "value9999"                │                    │
│    └──────────────────────────────────────┘                    │
│                                                                  │
│  WAL (entries 10001+):                                          │
│    [Entry 10001][Entry 10002]...[Entry 10500]                  │
│                                                                  │
│  Total storage: snapshot.size + wal.size << full log           │
└─────────────────────────────────────────────────────────────────┘
                             │
                             │ Node restarts
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                         Recovery                                │
├─────────────────────────────────────────────────────────────────┤
│  1. Load snapshot → KVStore.Restore(snap-10000)                │
│  2. Replay WAL entries 10001..10500                            │
│  3. State machine now at index 10500                           │
│                                                                  │
│  Recovery time: O(WAL entries since snapshot) ≪ O(full log)    │
└─────────────────────────────────────────────────────────────────┘
```

## 6. Fault Tolerance

```
3-Node Cluster Quorum: ⌈3/2⌉ = 2 nodes

┌──────────────────────────────────────────────────────────────┐
│ Scenario 1: Leader Failure                                  │
├──────────────────────────────────────────────────────────────┤
│  t=0:   kv-0 (Leader), kv-1 (Follower), kv-2 (Follower)    │
│  t=1:   kv-0 crashes ❌                                      │
│  t=2:   kv-1, kv-2 timeout (no heartbeats)                  │
│  t=3:   kv-2 starts election (term 6)                       │
│  t=4:   kv-1 votes for kv-2                                 │
│  t=5:   kv-2 becomes leader (2/3 quorum) ✓                  │
│                                                              │
│  Unavailability: ~300ms (election timeout)                  │
│  Data loss: None (committed entries on majority)            │
└──────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────┐
│ Scenario 2: Network Partition                               │
├──────────────────────────────────────────────────────────────┤
│  Partition: {kv-0, kv-1} | {kv-2}                           │
│                                                              │
│  Majority partition: {kv-0, kv-1}                           │
│    - 2/3 quorum ✓                                           │
│    - Can elect leader, commit writes                        │
│                                                              │
│  Minority partition: {kv-2}                                 │
│    - 1/3 quorum ❌                                           │
│    - Cannot commit writes                                   │
│    - Enters read-only mode                                  │
│                                                              │
│  Result: No split-brain, minority safely degrades           │
└──────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────┐
│ Scenario 3: Two Node Failures (Majority Lost)               │
├──────────────────────────────────────────────────────────────┤
│  kv-0 ✓, kv-1 ❌, kv-2 ❌                                     │
│                                                              │
│  Quorum: 1/3 ❌                                              │
│  Result: Cluster UNAVAILABLE                                │
│          No leader can be elected                           │
│          Writes blocked until majority restored             │
│                                                              │
│  Recovery: Bring up kv-1 or kv-2                            │
│            WAL replay restores state                        │
│            Leader election proceeds                         │
└──────────────────────────────────────────────────────────────┘
```

---

## Summary

This architecture demonstrates:

✅ **Protocol Awareness**: Operator understands Raft semantics
✅ **Safety First**: Joint Consensus prevents split-brain
✅ **Linearizability**: Strong consistency guarantees
✅ **Fault Tolerance**: Survives (N-1)/2 failures
✅ **Persistence**: WAL + snapshots for durability
✅ **Verifiability**: Porcupine checks correctness

For more details, see:
- [PROGRESS.md](../PROGRESS.md) - Implementation details
- [README.md](../README.md) - Usage guide
- [CS598RAP_Design_Doc.pdf](../CS598RAP_Design_Doc.pdf) - Full design
