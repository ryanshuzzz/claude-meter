#!/usr/bin/env python3
"""Analyze normalized Claude sniffer logs for limit-estimation spikes."""

import argparse
import json
from pathlib import Path


def load_records(log_path):
    with Path(log_path).open() as handle:
        for line in handle:
            line = line.strip()
            if line:
                yield json.loads(line)


def _effective_tokens(record):
    usage = record.get("usage", {}) or {}
    total = 0
    for key in (
        "input_tokens",
        "cache_creation_input_tokens",
        "cache_read_input_tokens",
        "output_tokens",
    ):
        value = usage.get(key)
        if isinstance(value, (int, float)):
            total += value
    return total


def summarize_windows(records):
    summary = {}
    for record in records:
        windows = ((record.get("ratelimit") or {}).get("windows") or {})
        model = record.get("response_model")
        for window_name, window_data in windows.items():
            item = summary.setdefault(
                window_name,
                {
                    "count": 0,
                    "statuses": {},
                    "min_utilization": None,
                    "max_utilization": None,
                    "models": set(),
                },
            )
            item["count"] += 1

            status = window_data.get("status")
            if status:
                item["statuses"][status] = item["statuses"].get(status, 0) + 1

            utilization = window_data.get("utilization")
            if isinstance(utilization, (int, float)):
                if item["min_utilization"] is None or utilization < item["min_utilization"]:
                    item["min_utilization"] = utilization
                if item["max_utilization"] is None or utilization > item["max_utilization"]:
                    item["max_utilization"] = utilization

            if model:
                item["models"].add(model)

    rendered = {}
    for window_name, item in summary.items():
        rendered[window_name] = {
            "count": item["count"],
            "statuses": item["statuses"],
            "min_utilization": item["min_utilization"],
            "max_utilization": item["max_utilization"],
            "models": sorted(item["models"]),
        }
    return rendered


def build_adjacent_deltas(records):
    eligible = []
    for record in records:
        if not (200 <= (record.get("status") or 0) < 300):
            continue
        if not record.get("session_id"):
            continue
        if not record.get("request_timestamp"):
            continue
        eligible.append(record)

    eligible.sort(key=lambda record: (record.get("session_id"), record.get("request_timestamp")))

    deltas = []
    previous_by_session_window = {}
    for record in eligible:
        windows = ((record.get("ratelimit") or {}).get("windows") or {})
        for window_name, window_data in windows.items():
            utilization = window_data.get("utilization")
            if not isinstance(utilization, (int, float)):
                continue

            key = (record["session_id"], window_name)
            previous = previous_by_session_window.get(key)
            if previous is not None:
                prev_utilization = previous["window_data"].get("utilization")
                if isinstance(prev_utilization, (int, float)):
                    delta_utilization = round(utilization - prev_utilization, 10)
                    if delta_utilization > 0:
                        effective_tokens = _effective_tokens(record)
                        implied_cap = effective_tokens / delta_utilization if effective_tokens > 0 else None
                        deltas.append(
                            {
                                "session_id": record["session_id"],
                                "window": window_name,
                                "previous_id": previous["record"].get("id"),
                                "current_id": record.get("id"),
                                "previous_timestamp": previous["record"].get("request_timestamp"),
                                "current_timestamp": record.get("request_timestamp"),
                                "response_model": record.get("response_model"),
                                "utilization_before": prev_utilization,
                                "utilization_after": utilization,
                                "delta_utilization": delta_utilization,
                                "effective_tokens": effective_tokens,
                                "implied_cap_tokens": implied_cap,
                            }
                        )

            previous_by_session_window[key] = {
                "record": record,
                "window_data": window_data,
            }

    return deltas


def render_analysis(log_path):
    records = list(load_records(log_path))
    summary = {
        "record_count": len(records),
        "window_summary": summarize_windows(records),
        "adjacent_deltas": build_adjacent_deltas(records),
    }
    return json.dumps(summary, sort_keys=True)


def main():
    parser = argparse.ArgumentParser(
        description="Analyze normalized Claude sniffer logs."
    )
    parser.add_argument("log_path", help="Path to normalized JSONL output")
    parser.add_argument(
        "--pretty",
        action="store_true",
        help="Pretty-print the JSON summary",
    )
    args = parser.parse_args()

    rendered = render_analysis(args.log_path)
    if args.pretty:
        print(json.dumps(json.loads(rendered), indent=2, sort_keys=True))
    else:
        print(rendered)


if __name__ == "__main__":
    main()
