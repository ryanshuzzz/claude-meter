#!/usr/bin/env python3
"""Generate matplotlib charts and a markdown report from normalized Claude logs."""

import argparse
import datetime as dt
import json
import sys
from pathlib import Path

try:
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt
except ImportError:
    print("matplotlib is required: pip install matplotlib", file=sys.stderr)
    sys.exit(1)

sys.path.insert(0, str(Path(__file__).resolve().parent))

import analyze_normalized_log as analyzer


def load_records_from_path(path):
    """Load normalized JSONL records from a file or directory.

    Returns (records, malformed_count) tuple.
    """
    path = Path(path)
    files = []
    if path.is_file():
        files = [path]
    elif path.is_dir():
        normalized_dir = path / "normalized"
        if normalized_dir.is_dir():
            files = sorted(normalized_dir.glob("*.jsonl"))
        else:
            files = sorted(path.glob("*.jsonl"))
    else:
        print(f"warning: path does not exist: {path}", file=sys.stderr)

    records = []
    malformed_count = 0
    for filepath in files:
        with filepath.open() as handle:
            for line in handle:
                line = line.strip()
                if not line:
                    continue
                try:
                    records.append(json.loads(line))
                except (json.JSONDecodeError, ValueError) as exc:
                    malformed_count += 1
                    print(
                        f"warning: skipping malformed line in {filepath}: {exc}",
                        file=sys.stderr,
                    )
    return records, malformed_count


def _parse_timestamps(entries, key="timestamp"):
    """Parse ISO timestamp strings into datetime objects for plotting."""
    dates = []
    for entry in entries:
        ts = entry.get(key, "")
        if ts.endswith("Z"):
            ts = ts[:-1] + "+00:00"
        try:
            dates.append(dt.datetime.fromisoformat(ts))
        except (ValueError, TypeError):
            dates.append(None)
    return dates


def generate_utilization_chart(records, output_dir):
    """Chart 1: utilization % over time for the 5h window."""
    time_series = analyzer.build_utilization_time_series(records, window="5h")
    if not time_series:
        return
    dates = _parse_timestamps(time_series)
    values = [entry["utilization"] * 100 for entry in time_series]

    fig, ax = plt.subplots(figsize=(10, 5))
    ax.plot(dates, values, marker="." if len(values) < 50 else None, linewidth=1)
    ax.set_xlabel("Time")
    ax.set_ylabel("Utilization %")
    ax.set_title("5h Window Utilization Over Time")
    ax.set_ylim(0, 100)
    fig.autofmt_xdate()
    fig.tight_layout()
    fig.savefig(output_dir / "charts" / "utilization_5h.png", dpi=100)
    plt.close(fig)


def generate_raw_vs_weighted_chart(records, output_dir):
    """Chart 2: raw / price-weighted ratio over time."""
    ratios = analyzer.build_raw_vs_weighted_ratios(records, window="5h")
    if not ratios:
        return
    dates = _parse_timestamps(ratios)
    values = [entry["ratio"] for entry in ratios if entry["ratio"] is not None]
    plot_dates = [d for d, entry in zip(dates, ratios) if entry["ratio"] is not None]

    if not values:
        return

    fig, ax = plt.subplots(figsize=(10, 5))
    ax.plot(plot_dates, values, marker="." if len(values) < 50 else None, linewidth=1)
    ax.set_xlabel("Time")
    ax.set_ylabel("Raw / Weighted Ratio")
    ax.set_title("Raw vs Price-Weighted Token Ratio (5h)")
    fig.autofmt_xdate()
    fig.tight_layout()
    fig.savefig(output_dir / "charts" / "raw_vs_weighted.png", dpi=100)
    plt.close(fig)


def generate_per_model_chart(records, output_dir):
    """Chart 3: horizontal bar chart of median implied cap per model."""
    caps = analyzer.build_per_model_caps(records, window="5h", meter="price_equivalent_5m")
    if not caps:
        return
    models = sorted(caps.keys())
    values = [caps[m] for m in models]

    fig, ax = plt.subplots(figsize=(10, max(3, len(models) * 0.6)))
    ax.barh(models, values)
    ax.set_xlabel("Median Implied Cap (price-equivalent)")
    ax.set_title("Per-Model Median Implied Cap (5h)")
    fig.tight_layout()
    fig.savefig(output_dir / "charts" / "per_model_cost.png", dpi=100)
    plt.close(fig)


def generate_budget_band_chart(records, output_dir):
    """Chart 4: histogram of implied_cap values from filtered estimate band intervals."""
    intervals = analyzer.build_utilization_intervals(records, meter="price_equivalent_5m")
    filtered = analyzer.filter_estimate_band_intervals(intervals)
    caps = [
        interval["implied_cap"]
        for interval in filtered
        if isinstance(interval.get("implied_cap"), (int, float))
    ]
    if not caps:
        return

    fig, ax = plt.subplots(figsize=(10, 5))
    ax.hist(caps, bins=min(10, len(caps)), edgecolor="black")
    ax.set_xlabel("Implied Cap")
    ax.set_ylabel("Count")
    ax.set_title("Budget Band Distribution (5h, price-equivalent)")
    fig.tight_layout()
    fig.savefig(output_dir / "charts" / "budget_band_dist.png", dpi=100)
    plt.close(fig)


def generate_report(records, output_dir, malformed_count=0):
    """Generate all charts and the markdown report."""
    output_dir = Path(output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    if not records:
        report = f"# Claude Meter Report\n\n**Date:** {dt.date.today().isoformat()}\n\nNo data found.\n"
        if malformed_count > 0:
            report += f"\n{malformed_count} malformed JSONL line(s) skipped.\n"
        (output_dir / "report.md").write_text(report)
        return

    charts_dir = output_dir / "charts"
    charts_dir.mkdir(parents=True, exist_ok=True)

    generate_utilization_chart(records, output_dir)
    generate_raw_vs_weighted_chart(records, output_dir)
    generate_per_model_chart(records, output_dir)
    generate_budget_band_chart(records, output_dir)

    # Gather summary info
    window_summary = analyzer.summarize_windows(records)
    windows_seen = sorted(window_summary.keys())
    all_models = set()
    for ws in window_summary.values():
        all_models.update(ws.get("models", []))
    models_seen = sorted(all_models)

    time_series = analyzer.build_utilization_time_series(records, window="5h")
    resets = analyzer.detect_resets(time_series)

    lines = [
        f"# Claude Meter Report",
        f"",
        f"**Date:** {dt.date.today().isoformat()}",
        f"**Records:** {len(records)}",
        f"",
        f"## Summary",
        f"",
        f"- **Windows seen:** {', '.join(windows_seen) if windows_seen else 'none'}",
        f"- **Models seen:** {', '.join(models_seen) if models_seen else 'none'}",
        f"- **Resets detected (5h):** {len(resets)}",
    ]

    if malformed_count > 0:
        lines.append(f"- **Malformed lines skipped:** {malformed_count}")

    lines.append("")
    lines.append("## Charts")
    lines.append("")

    chart_files = [
        ("utilization_5h.png", "5h Window Utilization"),
        ("raw_vs_weighted.png", "Raw vs Weighted Token Ratio"),
        ("per_model_cost.png", "Per-Model Median Implied Cap"),
        ("budget_band_dist.png", "Budget Band Distribution"),
    ]
    for filename, title in chart_files:
        if (charts_dir / filename).exists():
            lines.append(f"### {title}")
            lines.append(f"")
            lines.append(f"![{title}](charts/{filename})")
            lines.append(f"")

    report = "\n".join(lines)
    (output_dir / "report.md").write_text(report)


def main():
    parser = argparse.ArgumentParser(
        description="Generate charts and markdown report from normalized Claude logs."
    )
    parser.add_argument(
        "log_path",
        help="Path to a normalized JSONL file or a log directory",
    )
    parser.add_argument(
        "--output",
        default=".",
        help="Output directory for report.md and charts/ (default: current dir)",
    )
    args = parser.parse_args()

    records, malformed_count = load_records_from_path(args.log_path)
    generate_report(records, args.output, malformed_count=malformed_count)

    output_dir = Path(args.output)
    print(f"Report written to {output_dir / 'report.md'}")
    if records:
        chart_count = len(list((output_dir / "charts").glob("*.png")))
        print(f"{chart_count} chart(s) generated in {output_dir / 'charts'}")


if __name__ == "__main__":
    main()
