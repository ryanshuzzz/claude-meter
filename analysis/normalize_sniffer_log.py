#!/usr/bin/env python3
"""Normalize Claude sniffer JSONL logs into analysis-friendly JSONL."""

import argparse
import json
from pathlib import Path


USAGE_KEYS = (
    "input_tokens",
    "cache_creation_input_tokens",
    "cache_read_input_tokens",
    "output_tokens",
)

WINDOW_FIELDS = {
    "status": "status",
    "reset": "reset_ts",
    "utilization": "utilization",
    "surpassed-threshold": "surpassed_threshold",
}

TOP_LEVEL_FIELDS = {
    "status": "status",
    "representative-claim": "representative_claim",
    "fallback-percentage": "fallback_percentage",
    "overage-disabled-reason": "overage_disabled_reason",
    "overage-status": "overage_status",
}


def _coerce_number(value):
    if isinstance(value, (int, float)):
        return value
    if not isinstance(value, str):
        return value
    try:
        if any(ch in value for ch in (".", "e", "E")):
            return float(value)
        return int(value)
    except ValueError:
        return value


def _extract_session_id(metadata):
    if not isinstance(metadata, dict):
        return ""

    user_id = metadata.get("user_id")
    if isinstance(user_id, dict):
        return user_id.get("session_id", "")

    if isinstance(user_id, str):
        try:
            parsed = json.loads(user_id)
        except json.JSONDecodeError:
            parsed = None

        if isinstance(parsed, dict):
            return parsed.get("session_id", "")

        if "_session_" in user_id:
            return user_id.split("_session_", 1)[1]

    return ""


def _normalize_usage(usage):
    usage = usage if isinstance(usage, dict) else {}
    return {key: usage.get(key) for key in USAGE_KEYS}


def _normalize_ratelimit(headers):
    headers = headers if isinstance(headers, dict) else {}
    normalized = {"windows": {}}
    prefix = "anthropic-ratelimit-unified-"

    for key, value in headers.items():
        lower_key = key.lower()
        if lower_key == "retry-after":
            normalized["retry_after_s"] = _coerce_number(value)
            continue
        if not lower_key.startswith(prefix):
            continue

        suffix = lower_key[len(prefix):]
        if suffix == "reset":
            continue
        if suffix in TOP_LEVEL_FIELDS:
            normalized[TOP_LEVEL_FIELDS[suffix]] = _coerce_number(value)
            continue

        window_name = None
        field_name = None

        for candidate in WINDOW_FIELDS:
            needle = f"-{candidate}"
            if suffix.endswith(needle):
                window_name = suffix[: -len(needle)]
                field_name = candidate
                break

        if window_name:
            window = normalized["windows"].setdefault(window_name, {})
            window[WINDOW_FIELDS[field_name]] = _coerce_number(value)
            continue

        normalized[suffix.replace("-", "_")] = _coerce_number(value)

    return normalized


def _normalize_response(request, response):
    request = request if isinstance(request, dict) else {}
    request_body = request.get("body", {}) if isinstance(request.get("body"), dict) else {}
    response_headers = response.get("headers", {})

    return {
        "id": response.get("id"),
        "request_timestamp": request.get("timestamp"),
        "response_timestamp": response.get("timestamp"),
        "method": request.get("method"),
        "path": request.get("path"),
        "status": response.get("status"),
        "latency_ms": response.get("latency_ms"),
        "streaming": response.get("streaming"),
        "request_model": request_body.get("model"),
        "response_model": response.get("model") or request_body.get("model"),
        "session_id": _extract_session_id(request_body.get("metadata")),
        "request_id": response_headers.get("x-request-id"),
        "usage": _normalize_usage(response.get("usage")),
        "ratelimit": _normalize_ratelimit(response_headers),
    }


def normalize_log(log_path):
    requests = {}
    with Path(log_path).open() as handle:
        for line in handle:
            line = line.strip()
            if not line:
                continue

            entry = json.loads(line)
            entry_type = entry.get("type")
            if entry_type == "request":
                requests[entry.get("id")] = entry
                continue
            if entry_type != "response":
                continue

            yield _normalize_response(requests.get(entry.get("id")), entry)


def normalize_logs(log_paths):
    for log_path in log_paths:
        yield from normalize_log(log_path)


def main():
    parser = argparse.ArgumentParser(
        description="Normalize Claude sniffer JSONL into analysis-friendly JSONL."
    )
    parser.add_argument(
        "log_paths",
        nargs="+",
        help="One or more sniffer JSONL log files",
    )
    parser.add_argument(
        "--pretty",
        action="store_true",
        help="Pretty-print each normalized JSON object",
    )
    args = parser.parse_args()

    for record in normalize_logs(args.log_paths):
        if args.pretty:
            print(json.dumps(record, indent=2, sort_keys=True))
        else:
            print(json.dumps(record, separators=(",", ":"), sort_keys=True))


if __name__ == "__main__":
    main()
