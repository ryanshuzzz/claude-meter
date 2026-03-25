import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import analyze_normalized_log as analyzer


class TestAnalyzeNormalizedLog(unittest.TestCase):
    def _write_jsonl(self, records):
        tmp = tempfile.NamedTemporaryFile("w", delete=False)
        with tmp:
            for record in records:
                tmp.write(json.dumps(record) + "\n")
        return Path(tmp.name)

    def test_summarizes_observed_windows(self):
        records = [
            {
                "id": 1,
                "status": 200,
                "response_model": "claude-sonnet-4-6",
                "ratelimit": {
                    "windows": {
                        "5h": {"status": "allowed", "utilization": 0.10},
                        "7d": {"status": "allowed", "utilization": 0.20},
                    }
                },
            },
            {
                "id": 2,
                "status": 429,
                "response_model": "claude-sonnet-4-6",
                "ratelimit": {
                    "windows": {
                        "5h": {"status": "rejected", "utilization": 1.0},
                    }
                },
            },
        ]

        summary = analyzer.summarize_windows(records)

        self.assertEqual(
            summary,
            {
                "5h": {
                    "count": 2,
                    "statuses": {"allowed": 1, "rejected": 1},
                    "min_utilization": 0.1,
                    "max_utilization": 1.0,
                    "models": ["claude-sonnet-4-6"],
                },
                "7d": {
                    "count": 1,
                    "statuses": {"allowed": 1},
                    "min_utilization": 0.2,
                    "max_utilization": 0.2,
                    "models": ["claude-sonnet-4-6"],
                },
            },
        )

    def test_builds_adjacent_window_deltas_for_successful_requests(self):
        records = [
            {
                "id": 1,
                "request_timestamp": "2026-03-25T20:00:00.000+00:00",
                "session_id": "session-1",
                "status": 200,
                "response_model": "claude-sonnet-4-6",
                "usage": {
                    "input_tokens": 10,
                    "cache_creation_input_tokens": 20,
                    "cache_read_input_tokens": 30,
                    "output_tokens": 40,
                },
                "ratelimit": {
                    "windows": {
                        "5h": {"status": "allowed", "utilization": 0.10},
                    }
                },
            },
            {
                "id": 2,
                "request_timestamp": "2026-03-25T20:05:00.000+00:00",
                "session_id": "session-1",
                "status": 200,
                "response_model": "claude-sonnet-4-6",
                "usage": {
                    "input_tokens": 5,
                    "cache_creation_input_tokens": 10,
                    "cache_read_input_tokens": 15,
                    "output_tokens": 20,
                },
                "ratelimit": {
                    "windows": {
                        "5h": {"status": "allowed", "utilization": 0.15},
                    }
                },
            },
            {
                "id": 3,
                "request_timestamp": "2026-03-25T20:06:00.000+00:00",
                "session_id": "session-2",
                "status": 200,
                "response_model": "claude-sonnet-4-6",
                "usage": {
                    "input_tokens": 7,
                    "cache_creation_input_tokens": 0,
                    "cache_read_input_tokens": 0,
                    "output_tokens": 0,
                },
                "ratelimit": {
                    "windows": {
                        "5h": {"status": "allowed", "utilization": 0.17},
                    }
                },
            },
        ]

        deltas = analyzer.build_adjacent_deltas(records)

        self.assertEqual(
            deltas,
            [
                {
                    "session_id": "session-1",
                    "window": "5h",
                    "previous_id": 1,
                    "current_id": 2,
                    "previous_timestamp": "2026-03-25T20:00:00.000+00:00",
                    "current_timestamp": "2026-03-25T20:05:00.000+00:00",
                    "response_model": "claude-sonnet-4-6",
                    "utilization_before": 0.10,
                    "utilization_after": 0.15,
                    "delta_utilization": 0.05,
                    "effective_tokens": 50,
                    "implied_cap_tokens": 1000.0,
                }
            ],
        )

    def test_cli_outputs_summary_json(self):
        log_path = self._write_jsonl(
            [
                {
                    "id": 1,
                    "request_timestamp": "2026-03-25T20:00:00.000+00:00",
                    "session_id": "session-1",
                    "status": 200,
                    "response_model": "claude-sonnet-4-6",
                    "usage": {
                        "input_tokens": 10,
                        "cache_creation_input_tokens": 0,
                        "cache_read_input_tokens": 0,
                        "output_tokens": 0,
                    },
                    "ratelimit": {
                        "windows": {
                            "5h": {"status": "allowed", "utilization": 0.10},
                        }
                    },
                }
            ]
        )

        output = analyzer.render_analysis(log_path)
        parsed = json.loads(output)

        self.assertEqual(parsed["record_count"], 1)
        self.assertIn("5h", parsed["window_summary"])


if __name__ == "__main__":
    unittest.main()
