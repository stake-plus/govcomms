# GovComms Documentation

## Overview

GovComms currently ships as a single Go binary (`govcomms`) that can host up to three Discord-facing services:

- **AI Q&A bot (`/question`, `/refresh`, `/context`)** – Answers referendum questions using the shared AI client.
- **Research bot (`/research`, `/team`)** – Runs deeper AI-assisted analysis on proposals.
- **Feedback bot (`/feedback`)** – Accepts feedback inside referendum threads, stores it in MySQL, publishes a Redis event, and posts an embed back into the thread.

Each module is optional and can be toggled on or off at runtime. The codebase no longer includes the REST API or React UI that earlier documentation referenced.

## Architecture Snapshot

```
./src/cmd/govcomms      # single entry point
./src/ai-qa             # AI Q&A module
./src/research-bot      # Research module
./src/feedback          # Feedback module + indexer helpers
./src/shared            # shared packages (config, AI provider adapters, Discord helpers, governance types, etc.)
```

The services share:

- One MySQL connection (GORM) for configuration and governance data
- Optional Redis client (feedback bot only) for publishing queue events
- Shared AI provider factory (OpenAI / Claude) and HTTP tooling

## Building & Running

```bash
# Build the combined binary
GOOS=$(go env GOOS) GOARCH=$(go env GOARCH) go build -o bin/govcomms ./src/cmd/govcomms

# Run with the desired modules
./bin/govcomms --enable-feedback            # QA + Research + Feedback
./bin/govcomms --enable-research=false      # QA + Feedback only
ENABLE_QA=0 ./bin/govcomms                  # Research only (via env overrides)
```

### Runtime Flags

| Flag | Env var | Default | Purpose |
| --- | --- | --- | --- |
| `--enable-qa` | `ENABLE_QA` | `true` | Start the AI Q&A module |
| `--enable-research` | `ENABLE_RESEARCH` | `true` | Start the Research module |
| `--enable-feedback` | `ENABLE_FEEDBACK` | `false` | Start the Feedback module |

## Configuration

Settings are read from the `settings` table first (via `shared/data/settings.go`) and fall back to environment variables. Key values:

| Setting / Env | Description |
| --- | --- |
| `discord_token` / `DISCORD_TOKEN` | Discord bot token (all modules) |
| `guild_id` / `GUILD_ID` | Guild ID where slash commands are registered |
| `qa_role_id` / `QA_ROLE_ID` | Role allowed to use `/question` |
| `research_role_id` / `RESEARCH_ROLE_ID` | Role allowed to use `/research` and `/team` |
| `feedback_role_id` / `FEEDBACK_ROLE_ID` | Role allowed to use `/feedback` |
| `openai_api_key` / `OPENAI_API_KEY` | OpenAI API key (AI/Research) |
| `claude_api_key` / `CLAUDE_API_KEY` | Claude API key (optional alternative) |
| `ai_provider` / `AI_PROVIDER` | `openai` (default) or `claude` |
| `ai_model` / `AI_MODEL` | Model name (falls back to provider defaults) |
| `ai_enable_web_search` | `1` to turn on web search in the AI Q&A flow |
| `redis_url` / `REDIS_URL` | Required only when `--enable-feedback` is true |
| `indexer_workers` | Number of block indexer workers (feedback bot) |
| `indexer_interval_minutes` | Interval for referendum sync (feedback bot) |

The shared config loader (`shared/config/services.go`) can be consulted for the full list of fields per module.

## Discord Slash Commands

| Module | Command | Description |
| --- | --- | --- |
| AI Q&A | `/question <text>` | Ask a question about the current referendum thread |
| AI Q&A | `/refresh` | Refresh cached proposal content from Polkassembly |
| AI Q&A | `/context` | Display recent Q&A history |
| Research | `/research` | Produce AI-generated claim verification inside the thread |
| Research | `/team` | Produce AI-generated team analysis |
| Feedback | `/feedback <message>` | Store feedback, publish a Redis event, and post an embed |

## Current Limitations

- There is **no running REST API or web UI** in this repository. Earlier documentation referenced these components but they are not present.
- The feedback bot publishes events and posts in-thread embeds, but **does not currently relay web responses back to Discord** or auto-post to Polkassembly.
- Rate limiting, moderation workflows, and Polkassembly publishing are pending reimplementation.
- The feedback bot assumes MySQL contains referendum rows; the new upsert logic will create placeholders if the indexer has not run yet.

These gaps should be addressed before relying on the documentation for production deployments.

## Suggested Next Steps

1. Review `docs/GCBot.md` for module-specific behavior and configuration notes.
2. Update `docs/systemd/govcomms.service` with environment values appropriate for your deployment.
3. Create a runbook describing any external processors that consume the Redis stream produced by `/feedback`.
4. Reintroduce (or remove references to) the API/UI components if they are part of your target architecture.