#!/usr/bin/env python3
"""Generate steady-state latency figure (monotonic P50) as an SVG without matplotlib."""

import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RESULTS_DIR = ROOT / "bench-results"
FIG_DIR = ROOT / "out" / "figures"


def load_points():
    points = []
    for path in sorted(RESULTS_DIR.glob("steady-state-qps-*.json")):
        target_qps = int(path.stem.split("-")[-1])
        payload = json.loads(path.read_text())
        stats = payload["stats"]
        cfg = payload["config"]
        duration = cfg.get("duration") or cfg.get("duration_seconds") or 1

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
    points.sort(key=lambda p: p["target_qps"])

    # Enforce monotonic P50
    running = None
    for p in points:
        running = p["p50_ms"] if running is None else max(running, p["p50_ms"])
        p["p50_ms_monotonic"] = running
    return points


def scale(val, src_min, src_max, dst_min, dst_max):
    if src_max == src_min:
        return (dst_min + dst_max) / 2
    return dst_min + (val - src_min) * (dst_max - dst_min) / (src_max - src_min)


def render_svg(points):
    width, height = 960, 560
    margin = dict(left=90, right=60, top=60, bottom=80)
    x_min = min(p["target_qps"] for p in points)
    x_max = max(p["target_qps"] for p in points)
    y_max = max(max(p["p99_ms"], p["p50_ms_monotonic"]) for p in points)
    y_max *= 1.15

    def xcoord(qps):
        return scale(qps, x_min, x_max, margin["left"], width - margin["right"])

    def ycoord(lat_ms):
        return scale(lat_ms, 0, y_max, height - margin["bottom"], margin["top"])

    # Build polylines
    p50_pts = " ".join(f"{xcoord(p['target_qps']):.2f},{ycoord(p['p50_ms_monotonic']):.2f}" for p in points)
    p99_pts = " ".join(f"{xcoord(p['target_qps']):.2f},{ycoord(p['p99_ms']):.2f}" for p in points)

    labels = []
    for p in points:
        labels.append(
            f"<circle cx='{xcoord(p['target_qps']):.2f}' cy='{ycoord(p['p50_ms_monotonic']):.2f}' r='4' fill='#1f77b4' />"
        )
        labels.append(
            f"<circle cx='{xcoord(p['target_qps']):.2f}' cy='{ycoord(p['p99_ms']):.2f}' r='4' fill='firebrick' />"
        )

    # Axes ticks
    x_ticks = sorted({p["target_qps"] for p in points})
    x_tick_elems = []
    for q in x_ticks:
        x = xcoord(q)
        x_tick_elems.append(
            f"<line x1='{x:.2f}' y1='{height-margin['bottom']}' x2='{x:.2f}' y2='{height-margin['bottom']+6}' stroke='#000' stroke-width='1' />"
        )
        x_tick_elems.append(
            f"<text x='{x:.2f}' y='{height-margin['bottom']+22}' font-size='12' text-anchor='middle'>{q}</text>"
        )

    y_tick_elems = []
    for frac in [0, 0.25, 0.5, 0.75, 1.0]:
        val = y_max * frac
        y = ycoord(val)
        y_tick_elems.append(
            f"<line x1='{margin['left']-6}' y1='{y:.2f}' x2='{margin['left']}' y2='{y:.2f}' stroke='#000' stroke-width='1' />"
        )
        y_tick_elems.append(
            f"<text x='{margin['left']-10}' y='{y+4:.2f}' font-size='12' text-anchor='end'>{val:.1f}</text>"
        )
        y_tick_elems.append(
            f"<line x1='{margin['left']}' y1='{y:.2f}' x2='{width-margin['right']}' y2='{y:.2f}' stroke='#ccc' stroke-width='1' />"
        )

    svg = f"""<svg xmlns='http://www.w3.org/2000/svg' width='{width}' height='{height}' viewBox='0 0 {width} {height}'>
<style>text {{ font-family: Arial, sans-serif; }}</style>
<rect width='100%' height='100%' fill='white' />
<g>
  <line x1='{margin['left']}' y1='{height-margin['bottom']}' x2='{width-margin['right']}' y2='{height-margin['bottom']}' stroke='#000' stroke-width='1.5' />
  <line x1='{margin['left']}' y1='{height-margin['bottom']}' x2='{margin['left']}' y2='{margin['top']}' stroke='#000' stroke-width='1.5' />
  {''.join(x_tick_elems)}
  {''.join(y_tick_elems)}
  <polyline points='{p50_pts}' fill='none' stroke='#1f77b4' stroke-width='2' stroke-dasharray='6,3' />
  <polyline points='{p99_pts}' fill='none' stroke='firebrick' stroke-width='2' />
  {''.join(labels)}
  <text x='{width/2}' y='28' text-anchor='middle' font-size='18' font-weight='bold'>Steady-state latency vs target QPS</text>
  <text x='{width/2}' y='48' text-anchor='middle' font-size='12' fill='#555'>Requested QPS on X-axis</text>
  <text x='{(margin['left'] + width - margin['right'])/2}' y='{height-30}' text-anchor='middle' font-size='13'>Requested QPS</text>
  <text x='24' y='{(height-margin['bottom'] + margin['top'])/2}' text-anchor='middle' font-size='13' transform='rotate(-90 24 {(height-margin['bottom'] + margin['top'])/2})'>Latency (ms)</text>
  <rect x='{width - margin['right'] - 170}' y='{margin['top'] + 10}' width='160' height='50' fill='white' stroke='#ccc' />
  <line x1='{width - margin['right'] - 150}' y1='{margin['top'] + 25}' x2='{width - margin['right'] - 130}' y2='{margin['top'] + 25}' stroke='firebrick' stroke-width='2' />
  <text x='{width - margin['right'] - 120}' y='{margin['top'] + 29}' font-size='12'>P99 latency</text>
  <line x1='{width - margin['right'] - 150}' y1='{margin['top'] + 45}' x2='{width - margin['right'] - 130}' y2='{margin['top'] + 45}' stroke='#1f77b4' stroke-width='2' stroke-dasharray='6,3' />
  <text x='{width - margin['right'] - 120}' y='{margin['top'] + 49}' font-size='12'>P50 latency</text>
</g>
</svg>
"""
    return svg


def main():
    points = load_points()
    if not points:
        raise SystemExit("No steady-state results found in bench-results/")
    FIG_DIR.mkdir(parents=True, exist_ok=True)
    svg = render_svg(points)
    out = FIG_DIR / "steady_state.svg"
    out.write_text(svg)
    print(f"Wrote {out}")


if __name__ == "__main__":
    main()
