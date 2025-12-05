#!/usr/bin/env python3
"""Generate steady-state throughput vs latency figure from benchmark JSON files."""

import json
import os
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MPL_CONFIG_DIR = ROOT / "out" / ".mplconfig"
MPL_CONFIG_DIR.mkdir(parents=True, exist_ok=True)
os.environ.setdefault("MPLCONFIGDIR", str(MPL_CONFIG_DIR))

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt


RESULTS_DIR = ROOT / "bench-results"
FIG_DIR = ROOT / "out" / "figures"


def load_points():
    points = []
    for path in sorted(RESULTS_DIR.glob("steady-state-qps-*.json")):
        target_qps = int(path.stem.split("-")[-1])
        with path.open() as f:
            payload = json.load(f)

        stats = payload["stats"]
        config = payload["config"]
        duration = config.get("duration") or config.get("duration_seconds") or 1

        throughput = stats["success_ops"] / duration
        p99_ms = stats["p99_latency_ns"] / 1_000_000
        p50_ms = stats["p50_latency_ns"] / 1_000_000

        points.append(
            {
                "target_qps": target_qps,
                "throughput": throughput,
                "p99_ms": p99_ms,
                "p50_ms": p50_ms,
            }
        )

    return points


def main():
    points = load_points()
    if not points:
        raise SystemExit("No steady-state results found in bench-results/")

    # Sort by requested QPS so lines progress left-to-right even if achieved
    # throughput dips due to saturation.
    points.sort(key=lambda p: p["target_qps"])

    FIG_DIR.mkdir(parents=True, exist_ok=True)

    fig, ax_p99 = plt.subplots(figsize=(12, 6.5))
    ax_p50 = ax_p99.twinx()

    x = [p["target_qps"] for p in points]
    p99 = [p["p99_ms"] for p in points]
    p50 = [p["p50_ms"] for p in points]

    p99_color = "firebrick"
    p50_color = "#1f77b4"

    ax_p99.plot(x, p99, color=p99_color, marker="o", label="P99 latency (ms)")
    ax_p50.plot(
        x,
        p50,
        color=p50_color,
        linestyle="--",
        marker="s",
        label="P50 latency (ms)",
    )

    for point in points:
        ax_p99.annotate(
            str(point["target_qps"]),
            (point["throughput"], point["p99_ms"]),
            textcoords="offset points",
            xytext=(0, 8),
            ha="center",
            color=p99_color,
            fontsize=9,
        )

    ax_p99.set_xlabel("Requested QPS")
    ax_p99.set_ylabel("P99 latency (ms)", color=p99_color)
    ax_p50.set_ylabel("P50 latency (ms)", color=p50_color)

    ax_p99.tick_params(axis="y", labelcolor=p99_color)
    ax_p50.tick_params(axis="y", labelcolor=p50_color)

    ax_p99.grid(True, linestyle="--", alpha=0.4)

    fig.tight_layout()
    output_path = FIG_DIR / "steady_state.png"
    fig.savefig(output_path, dpi=200)
    print(f"Wrote {output_path}")


if __name__ == "__main__":
    main()
