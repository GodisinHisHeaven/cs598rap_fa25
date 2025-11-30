# Bench & Evaluation Playbook

A concise playbook for generating presentation-ready figures. Run from repo root unless noted.

## Common setup
- `./build.sh` if present, or `go build ./...` (ensure loadgen built in `test/bench`).
- Start baseline 3-node cluster (follow project docs) before load-driven tests.
- Metrics are emitted by loadgen stdout; capture with `tee` for plotting.

## 1) Steady-State Throughput vs. Latency
- Goal: show stable performance until saturation.
- Script: `test/bench/steady-state.sh`
- Run:
  ```bash
  test/bench/steady-state.sh --qps "100,300,600,900,1200,1500,1800,2100" --duration 20s | tee out/steady-state.log
  ```
- What to collect: throughput, p50, p99 per QPS point (parse log JSON/CSV).
- Figure: dual-axis line (x=throughput), left y=p99 ms, right y=p50 ms; expect flat until hockey-stick spike at saturation.

## 2) Reconfiguration Cost (Money Slide)
- Goal: visualize slowdown during 6-phase Joint Consensus.
- Script: `test/bench/reconfiguration.sh`
- Run:
  ```bash
  test/bench/reconfiguration.sh --background-qps <50%-of-max> --t-scale-up 10s --t-scale-down 30s | tee out/reconfig.log
  ```
- What to collect: throughput over time; annotate AddLearner, CatchUp, EnterJoint, CommitNew, LeaveJoint.
- Figure: time-series throughput; expect dips at EnterJoint/CommitNew, no zero-throughput.

## 3) Leader Failover MTTR
- Goal: measure unavailable window after killing leader.
- Scripts: `test/bench/failover.sh`, `test/fault-injection/leader-kill.sh`
- Run:
  ```bash
  test/bench/failover.sh --background-qps <sustain> --failover-script test/fault-injection/leader-kill.sh | tee out/failover.log
  ```
- What to collect: timestamped success/error counts; duration of zero-success window ≈ MTTR (~electionTimeoutMs).
- Figure: short-window timeline; sharp drop to zero, gap, sharp recovery.

## 4) Safety Verification (Porcupine)
- Goal: compare Safe vs Unsafe modes on linearizability.
- Scripts: `test/fault-injection/bug1-reproduce.sh`, `test/fault-injection/bug2-reproduce.sh`
- Runs (10x each):
  ```bash
  for i in {1..10}; do test/fault-injection/bug1-reproduce.sh >> out/bug1.log; done
  for i in {1..10}; do test/fault-injection/bug2-reproduce.sh >> out/bug2.log; done
  for i in {1..10}; do test/bench/reconfiguration.sh --safe-mode >> out/safe.log; done
  ```
- What to collect: Porcupine linearizability violation counts per scenario.
- Figure: grouped bars (Safe, Unsafe-Early-Vote, Unsafe-No-Joint); expect 0, >0, >0.

## 5) Snapshot Storage Overhead
- Goal: show WAL growth with/without snapshots.
- Script: `test/bench/snapshot.sh`
- Run:
  ```bash
  test/bench/snapshot.sh --qps <heavy> --duration 120s --snapshots enabled | tee out/snapshot-on.log
  test/bench/snapshot.sh --qps <heavy> --duration 120s --snapshots disabled | tee out/snapshot-off.log
  ```
- What to collect: time vs WAL disk usage (e.g., `du -sm path/to/wal`).
- Figure: line/area; snapshots show sawtooth, disabled grows linearly.

## Figure recipes (quick start)
- Parse logs to CSV (throughput, p50, p99, t, scenario, violations, wal_mb) with a small Python/R script.
- Plotting hints:
  - Steady-state: dual-axis line; highlight saturation knee.
  - Reconfig: time-series with vertical lines for phases.
  - Failover: zoomed timeline around kill; label gap width as MTTR.
  - Safety: grouped bars with values atop bars.
  - Snapshot: overlay on/off lines; annotate compaction drops.

## Output hygiene
- Keep raw logs in `out/`; keep generated CSV/figures in `out/figures/`.
- Record exact command lines in `out/commands.txt` for reproducibility.
