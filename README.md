# Claude Meter

Local research proxy for understanding Anthropic rate-limit behavior.

Current scope:
- transparent pass-through proxy for `api.anthropic.com`
- async full raw exchange capture
- async normalized-record derivation in the background
- offline analysis scripts for log normalization and quota estimation

Raw capture behavior:
- request and response bodies are persisted locally
- sensitive headers are redacted before they hit disk
- raw log directories and files are created with private permissions

Normalized record behavior:
- normalized JSONL is written under `~/.claude-meter/normalized/`
- records include parsed Anthropic rate-limit headers when present
- records include a declared `plan_tier` for later cohorting
- `/v1/messages` SSE responses are parsed explicitly
- `/v1/messages/count_tokens` responses are parsed explicitly
- partial gzip event streams are handled best-effort
- unknown endpoints fall back to a header-driven generic record

Run the proxy:

```bash
go run ./cmd/claude-meter start --plan-tier max_20x
```

Backfill normalized records from existing raw logs:

```bash
go run ./cmd/claude-meter backfill-normalized --log-dir ~/.claude-meter --plan-tier max_20x
```

Run the analysis scripts:

```bash
python3 analysis/normalize_sniffer_log.py ~/.claude/api-sniffer/sniffer-*.jsonl > normalized.jsonl
python3 analysis/analyze_normalized_log.py normalized.jsonl --pretty
```

Raw proxy logs are written under `~/.claude-meter/raw/` by default.
Normalized proxy logs are written under `~/.claude-meter/normalized/` by default.
