# AI Provider Smoke Test

Use the `cmd/ai-smoketest` helper to verify that each configured model is reachable before enabling it in production. The tool instantiates every registered provider via the shared `ai/core` factory, sends a sample prompt, and prints the response (or any error) to stdout.

## Requirements

1. Export the API keys you plan to test — the helper reads the same variables as the runtime:
   - `OPENAI_API_KEY` (GPT‑5 / GPT‑4o mini)
   - `CLAUDE_API_KEY` (Sonnet/Haiku)
   - `GEMINI_API_KEY`
   - `DEEPSEEK_API_KEY`
   - `GROK_API_KEY`
2. Optional: set `AI_SYSTEM_PROMPT` or `AI_MODEL` to override defaults, or pass the `-system` / `-model` flags.

## Running the script

From the repo root:

```sh
go run ./cmd/ai-smoketest -providers all -mode both
```

Key flags:

| Flag | Description |
| --- | --- |
| `-providers` | Comma/space separated list or `all` (default: `gpt5`) |
| `-mode` | `respond`, `qa`, or `both` to exercise the two client paths |
| `-prompt` | User prompt for respond mode |
| `-content` / `-question` | Inputs for QA mode |
| `-timeout` | Per-provider timeout (default 45s) |
| `-web` | Request the `web_search` tool during respond tests |
| `-max-bytes` | Clip printed output to keep logs readable |

Example: run only Claude and DeepSeek using custom text and enable browsing:

```sh
go run ./cmd/ai-smoketest -providers "sonnet45, haiku45, deepseek32" \
  -mode respond \
  -prompt "List three talking points about OpenGov decentralization." \
  -timeout 30s \
  -web
```

The script reports `respond ✅` / `qa ✅` alongside latency for successes, or prints the specific error (HTTP status, auth failure, etc.) so you can diagnose configuration issues per provider. Once a provider returns healthy responses here it should work in the live modules without further changes.