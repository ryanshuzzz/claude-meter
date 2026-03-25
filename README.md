# Claude Meter

Local research proxy for understanding Anthropic rate-limit behavior.

Current scope:
- transparent pass-through proxy for `api.anthropic.com`
- async full raw exchange capture
- local JSONL storage only
- offline analysis scripts for log normalization and quota estimation

Raw capture behavior:
- request and response bodies are persisted locally
- sensitive headers are redacted before they hit disk
- raw log directories and files are created with private permissions

Run the proxy:

```bash
go run ./cmd/claude-meter start
```

Run the analysis scripts:

```bash
python3 analysis/normalize_sniffer_log.py ~/.claude/api-sniffer/sniffer-*.jsonl > normalized.jsonl
python3 analysis/analyze_normalized_log.py normalized.jsonl --pretty
```

Raw proxy logs are written under `~/.claude-meter/raw/` by default.
