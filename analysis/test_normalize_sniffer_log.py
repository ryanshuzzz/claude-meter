import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import normalize_sniffer_log as normalizer


class TestNormalizeSnifferLog(unittest.TestCase):
    def _write_log(self, entries):
        tmp = tempfile.NamedTemporaryFile("w", delete=False)
        with tmp:
            for entry in entries:
                tmp.write(json.dumps(entry) + "\n")
        return Path(tmp.name)

    def test_normalizes_successful_response_usage_and_session_id(self):
        log_path = self._write_log(
            [
                {
                    "type": "request",
                    "id": 10,
                    "timestamp": "2026-03-25T20:00:00.000+00:00",
                    "method": "POST",
                    "path": "/v1/messages?beta=true",
                    "body": {
                        "model": "claude-sonnet-4-6",
                        "metadata": {
                            "user_id": json.dumps(
                                {
                                    "device_id": "device-1",
                                    "account_uuid": "acct-1",
                                    "session_id": "session-123",
                                }
                            )
                        },
                    },
                },
                {
                    "type": "response",
                    "id": 10,
                    "timestamp": "2026-03-25T20:00:05.000+00:00",
                    "status": 200,
                    "latency_ms": 5000,
                    "streaming": True,
                    "model": "claude-sonnet-4-6",
                    "usage": {
                        "input_tokens": 10,
                        "cache_creation_input_tokens": 20,
                        "cache_read_input_tokens": 30,
                        "output_tokens": 40,
                    },
                    "headers": {
                        "x-request-id": "req_abc123",
                    },
                },
            ]
        )

        records = list(normalizer.normalize_log(log_path))

        self.assertEqual(len(records), 1)
        self.assertEqual(
            records[0],
            {
                "id": 10,
                "request_timestamp": "2026-03-25T20:00:00.000+00:00",
                "response_timestamp": "2026-03-25T20:00:05.000+00:00",
                "method": "POST",
                "path": "/v1/messages?beta=true",
                "status": 200,
                "latency_ms": 5000,
                "streaming": True,
                "request_model": "claude-sonnet-4-6",
                "response_model": "claude-sonnet-4-6",
                "session_id": "session-123",
                "request_id": "req_abc123",
                "usage": {
                    "input_tokens": 10,
                    "cache_creation_input_tokens": 20,
                    "cache_read_input_tokens": 30,
                    "output_tokens": 40,
                },
                "ratelimit": {"windows": {}},
            },
        )

    def test_parses_unified_rate_limit_headers_from_429_response(self):
        log_path = self._write_log(
            [
                {
                    "type": "request",
                    "id": 11,
                    "timestamp": "2026-03-25T20:25:05.303+00:00",
                    "method": "POST",
                    "path": "/v1/messages?beta=true",
                    "body": {
                        "model": "claude-haiku-4-5-20251001",
                        "metadata": {
                            "user_id": json.dumps(
                                {
                                    "device_id": "device-1",
                                    "account_uuid": "acct-1",
                                    "session_id": "session-429",
                                }
                            )
                        },
                    },
                },
                {
                    "type": "response",
                    "id": 11,
                    "timestamp": "2026-03-25T20:25:05.493+00:00",
                    "status": 429,
                    "latency_ms": 190,
                    "streaming": False,
                    "model": "claude-haiku-4-5-20251001",
                    "usage": {},
                    "headers": {
                        "anthropic-ratelimit-unified-status": "rejected",
                        "anthropic-ratelimit-unified-5h-status": "rejected",
                        "anthropic-ratelimit-unified-5h-reset": "1774472400",
                        "anthropic-ratelimit-unified-5h-utilization": "1.0",
                        "anthropic-ratelimit-unified-7d-status": "allowed",
                        "anthropic-ratelimit-unified-7d-reset": "1774580400",
                        "anthropic-ratelimit-unified-7d-utilization": "0.59",
                        "anthropic-ratelimit-unified-representative-claim": "five_hour",
                        "anthropic-ratelimit-unified-fallback-percentage": "0.5",
                        "anthropic-ratelimit-unified-overage-disabled-reason": "out_of_credits",
                        "anthropic-ratelimit-unified-overage-status": "rejected",
                        "retry-after": "2094",
                    },
                },
            ]
        )

        records = list(normalizer.normalize_log(log_path))

        self.assertEqual(len(records), 1)
        self.assertEqual(
            records[0]["ratelimit"],
            {
                "status": "rejected",
                "representative_claim": "five_hour",
                "fallback_percentage": 0.5,
                "overage_disabled_reason": "out_of_credits",
                "overage_status": "rejected",
                "retry_after_s": 2094,
                "windows": {
                    "5h": {
                        "status": "rejected",
                        "reset_ts": 1774472400,
                        "utilization": 1.0,
                    },
                    "7d": {
                        "status": "allowed",
                        "reset_ts": 1774580400,
                        "utilization": 0.59,
                    },
                },
            },
        )

    def test_normalizes_multiple_log_files(self):
        first_log = self._write_log(
            [
                {
                    "type": "request",
                    "id": 1,
                    "timestamp": "2026-03-25T20:00:00.000+00:00",
                    "method": "POST",
                    "path": "/v1/messages?beta=true",
                    "body": {"model": "claude-haiku-4-5-20251001"},
                },
                {
                    "type": "response",
                    "id": 1,
                    "timestamp": "2026-03-25T20:00:01.000+00:00",
                    "status": 200,
                    "latency_ms": 1000,
                    "streaming": False,
                    "model": "claude-haiku-4-5-20251001",
                    "usage": {"input_tokens": 8},
                },
            ]
        )
        second_log = self._write_log(
            [
                {
                    "type": "request",
                    "id": 2,
                    "timestamp": "2026-03-25T20:01:00.000+00:00",
                    "method": "POST",
                    "path": "/v1/messages?beta=true",
                    "body": {"model": "claude-sonnet-4-6"},
                },
                {
                    "type": "response",
                    "id": 2,
                    "timestamp": "2026-03-25T20:01:02.000+00:00",
                    "status": 200,
                    "latency_ms": 2000,
                    "streaming": True,
                    "model": "claude-sonnet-4-6",
                    "usage": {"output_tokens": 42},
                },
            ]
        )

        records = list(normalizer.normalize_logs([first_log, second_log]))

        self.assertEqual([record["id"] for record in records], [1, 2])
        self.assertEqual(records[0]["response_model"], "claude-haiku-4-5-20251001")
        self.assertEqual(records[1]["response_model"], "claude-sonnet-4-6")


if __name__ == "__main__":
    unittest.main()
