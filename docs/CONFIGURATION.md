# Configuration Reference

GovComms loads most settings from MySQL and only falls back to environment variables when a key is missing in the database. `config/env.sample` shows the available keys.

1. **MySQL `settings` table** – populated via `db/database.sql` or direct edits. Values are cached in memory at startup.
2. **Environment variables** – `config.GetSetting` falls back to env values when the DB entry is empty. Some toggles (`ai_enable_*`, `gc_url`, and indexer tuning knobs) are DB-only. `MYSQL_DSN` is env-only.

Use this guide to understand every tunable and how it maps back to the source code.

## 1. Environment Variables

| Variable | Required | Description | Source |
| --- | --- | --- | --- |
| `MYSQL_DSN` | ✅ | Full DSN including `parseTime=true` and UTF8MB4 settings. Example:<br>`govcomms:password@tcp(127.0.0.1:3306)/govcomms?parseTime=true&charset=utf8mb4` | `src/data/mysql.go` |
| `DISCORD_TOKEN` | ✅ | Discord bot token. Shared with the Chaos DAO Governance Bot if they run under one application. | `src/config/base.go` |
| `GUILD_ID` | ✅ | Target Discord guild for slash commands. | `src/config/base.go` |
| `QA_ROLE_ID` / `RESEARCH_ROLE_ID` / `FEEDBACK_ROLE_ID` | Optional | Restrict slash commands to specific roles. Leave empty to allow everyone. | `src/config/services.go` |
| `OPENAI_API_KEY` / `CLAUDE_API_KEY` | At least one | API keys for AI providers registered in `src/ai`. | `src/config/services.go` |
| `AI_PROVIDER` | Optional | `openai` (default) or `claude`. Other providers are registered but not fully implemented. | `src/config/services.go` |
| `AI_MODEL` | Optional | Model ID (defaults to `gpt-4o-mini` or `claude-3-haiku-20240307`). | `src/config/services.go` |
| `AI_SYSTEM_PROMPT` | Optional | Custom prompt injected into AI calls. | `src/config/services.go` |
| `ENABLE_QA` / `ENABLE_RESEARCH` / `ENABLE_FEEDBACK` | Optional | Mirrors CLI flags. Accepts `1`, `true`, `false`, `0`. | `src/gov-comms.go` |
| `QA_TEMP_DIR` | Optional | Directory used to cache proposal content and documents. Must be writable. | `src/config/services.go`, `src/cache` |
| `POLKASSEMBLY_ENDPOINT` | Optional | Override API base (default `https://api.polkassembly.io/api/v1`). | `src/config/services.go`, `src/actions/feedback/module.go` |

> `AI_ENABLE_WEB_SEARCH`, `AI_ENABLE_DEEP_SEARCH`, and `GC_URL` currently need to be set via the `settings` table. The legacy environment keys remain in `config/env.sample` but are ignored at runtime.

Store environment values in `/opt/govcomms/.env.govcomms` (Linux) or `C:\govcomms\.env.govcomms` (Windows). The sample at `config/env.sample` covers the fallback keys that are actually read at runtime.

## 2. Settings Table Keys

`db/database.sql` seeds the following keys. Update them via SQL or any preferred admin tool.

| Key | Description | Fallback Env |
| --- | --- | --- |
| `discord_token` | Discord bot token. | `DISCORD_TOKEN` |
| `guild_id` | Guild where slash commands register. | `GUILD_ID` |
| `qa_role_id` / `research_role_id` / `feedback_role_id` | Role IDs gating slash commands. | respective env vars |
| `openai_api_key` / `claude_api_key` | AI provider keys. | env vars |
| `ai_provider`, `ai_model`, `ai_system_prompt` | AI behavior tuning. | env vars |
| `ai_enable_web_search`, `ai_enable_deep_search` | `"1"` to enable optional tools. | — (DB only) |
| `qa_temp_dir` | Proposal cache directory. | `QA_TEMP_DIR` |
| `indexer_workers` | Concurrency level for `src/actions/feedback/data/indexer.go`. Default `10`. | — (DB only) |
| `indexer_interval_minutes` | Minutes between indexer passes. Default `60`. | — (DB only) |
| `polkassembly_endpoint` | API base for Polkassembly. | `POLKASSEMBLY_ENDPOINT` |
| `gc_url` | Base link appended to Polkassembly comments (points readers back to DAO context). | — (DB only) |

> **Tip:** Keep the database as the authoritative source. Use environment variables only for secrets you cannot store in MySQL.

## 3. Network & Thread Configuration

### `networks` table

| Column | Purpose |
| --- | --- |
| `id` | Tinyint primary key that maps to `refs.network_id`. |
| `name` / `symbol` / `url` | Human-readable identifiers. |
| `discord_channel_id` | The parent forum/channel ID containing referendum threads. |
| `polkassembly_seed` | sr25519 seed used to authenticate with Polkassembly (optional but required for mirroring). |
| `ss58_prefix` | Display prefix for addresses (GovComms still authenticates with prefix `42` when posting to Polkassembly). |

### `network_rpcs` table

Provide at least one active RPC endpoint per network. The indexer rotates through these to stay synced with the chain. Keep URLs up to date when nodes are retired.

### Thread mapping rules

- GovComms expects referendum threads to be named `"<ref-id>: rest-of-title"`.
- When a thread is created or updated in the configured channel, `src/actions/feedback/module.go` parses the ref ID and populates `ref_threads`.
- `GuildThreadsActive` reconciles mappings periodically in case the bot restarts.

## 4. Polkassembly Integration

1. For each network you want to mirror feedback to, add a valid sr25519 seed (e.g., `//Alice` style or mnemonic).
2. Ensure the `gc_url` setting points to the DAO’s discussion hub (configured via the `settings` table) so Polkassembly readers can follow links back.
3. GovComms will:
   - Post the first Discord feedback message to Polkassembly.
   - Save the returned comment ID to `ref_messages`.
   - Poll every 15 minutes for replies and echo them back into Discord threads.

## 5. Module Toggles & CLI Flags

Use either CLI flags or environment variables (`ENABLE_QA`, etc.) when starting the binary:

```bash
./bin/govcomms \
  --enable-qa \
  --enable-research \
  --enable-feedback
```

Setting a flag to `false` (or the env variable to `0`) skips module startup—useful for staging environments or troubleshooting.

## 6. Cache & Storage Locations

| Path | Purpose |
| --- | --- |
| `QA_TEMP_DIR` | Proposal text and downloaded documents. Wipe to force a cache rebuild. |
| `tmp/dbinspect`, `tmp/scratch` | Developer scratch space. Safe to delete between runs. |

Ensure the service account running GovComms can read/write these directories.

## 7. Verifying Configuration

1. Run `go test ./...` to ensure code compiles against the configured environment.
2. Start GovComms with `--enable-feedback` and monitor logs for:
   - `question: logged in as …` and `research: logged in as …` (Discord sessions ready).
   - `feedback: slash command registered` followed by `feedback: starting network indexer service`.
   - `<network> indexer: Current block height …` for each network RPC.
   - Polkassembly login notices (if seeds are configured).
3. Use Discord to invoke `/question`, `/research`, and `/feedback` inside a referendum thread. Confirm:
   - AI answers appear with cached context.
   - Research module posts interim and final updates.
   - Feedback embed includes file attachments for long bodies and Polkassembly mirror IDs when applicable.

