#!/usr/bin/env python3
"""Generate two failover summary plots (MTTR and latency) without external deps."""

import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RESULTS = ROOT / "bench-results" / "failover-results.json"
FIG_DIR = ROOT / "out" / "figures"


def load_payload():
    if not RESULTS.exists():
        raise SystemExit(f"Missing results file: {RESULTS}")
    with RESULTS.open() as f:
        return json.load(f)


def write_svg(name: str, width: int, height: int, body: str):
    FIG_DIR.mkdir(parents=True, exist_ok=True)
    path = FIG_DIR / name
    svg = (
        f"<svg xmlns='http://www.w3.org/2000/svg' width='{width}' height='{height}' "
        f"viewBox='0 0 {width} {height}'>\n"
        "<style>text{font-family:Arial,sans-serif;}</style>\n"
        f"{body}\n</svg>\n"
    )
    path.write_text(svg)
    print(f"Wrote {path}")
    return path


def plot_mttr(payload):
    mttr = payload.get("failover_time_ms") or 0
    width, height = 420, 320
    bar_width = 120
    margin_x = (width - bar_width) // 2
    max_h = height - 140
    h = max_h if mttr == 0 else max_h
    y_base = height - 80
    bar_h = 0 if mttr == 0 else max_h
    body = []
    body.append("<text x='210' y='32' text-anchor='middle' font-size='18' font-weight='bold'>Leader failover MTTR</text>")
    body.append(
        f"<rect x='{margin_x}' y='{y_base - bar_h:.2f}' width='{bar_width}' height='{bar_h:.2f}' fill='#3778c2' rx='6' />"
    )
    body.append(
        f"<text x='{margin_x + bar_width/2:.2f}' y='{y_base - bar_h - 10:.2f}' text-anchor='middle' font-size='14'>{mttr:.0f} ms</text>"
    )
    body.append(
        "<text x='210' y='{y}' text-anchor='middle' font-size='12' fill='#555'>Mean time to recover</text>".format(
            y=y_base + 24
        )
    )
    return write_svg("failover_mttr.svg", width, height, "\n".join(body))


def plot_latency(payload):
    stats = payload["stats"]
    metrics = [("P50", stats["p50_latency_ns"] / 1_000_000), ("P95", stats["p95_latency_ns"] / 1_000_000), ("P99", stats["p99_latency_ns"] / 1_000_000)]
    width, height = 520, 340
    bar_w = 80
    gap = 50
    max_val = max(v for _, v in metrics) or 1
    max_h = height - 160
    y_base = height - 80
    body = []
    body.append("<text x='{x}' y='32' text-anchor='middle' font-size='18' font-weight='bold'>Latency during failover run</text>".format(x=width/2))
    for idx, (label, val) in enumerate(metrics):
        x = gap + idx * (bar_w + gap)
        h = (val / max_val) * max_h if max_val else 0
        y = y_base - h
        color = "#4c72b0" if label == "P50" else "#dd8452" if label == "P95" else "#c44e52"
        body.append(f"<rect x='{x}' y='{y:.2f}' width='{bar_w}' height='{h:.2f}' fill='{color}' rx='6' />")
        body.append(f"<text x='{x + bar_w/2:.2f}' y='{y - 8:.2f}' text-anchor='middle' font-size='13'>{val:.1f} ms</text>")
        body.append(f"<text x='{x + bar_w/2:.2f}' y='{y_base + 20:.2f}' text-anchor='middle' font-size='13'>{label}</text>")
    body.append(f"<text x='{width/2}' y='{y_base + 45}' text-anchor='middle' font-size='12' fill='#555'>Latency statistics (ms)</text>")
    return write_svg("failover_latency.svg", width, height, "\n".join(body))


def main():
    payload = load_payload()
    plot_mttr(payload)
    plot_latency(payload)


if __name__ == "__main__":
    main()
