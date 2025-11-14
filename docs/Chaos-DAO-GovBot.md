# Chaos DAO Governance Bot Deployment Guide

This repository (`github.com/stake-plus/govcomms`) powers the **Chaos DAO Governance Bot**. The single Go binary `govcomms` hosts three Discord-facing modules that Chaos DAO runs inside its governance server:

- **AI Q&A** – `/question`, `/refresh`, `/context`
- **Research** – `/research`, `/team`
- **Feedback** – `/feedback` plus the on-chain referendum indexer and Polkassembly bridge

Each module can be toggled at launch. All of them share one MySQL database, one Discord application, the same AI provider credentials, and an optional Polkassembly signer per network.

---

## Repository Map

| Path | Purpose |
| --- | --- |
| `src/gov-comms.go` | CLI entrypoint / module wiring |
| `src/ai-qa` | Q&A bot + proposal content processor/cache |
| `src/research-bot` | Claim and team analyzers that call OpenAI |
| `src/feedback` | Feedback bot, referendum indexer, Polkassembly mirroring |
| `src/shared` | Shared config, Discord helpers, AI factory, Polkassembly client, Substrate governance models |
| `src/polkadot-go` | Lightweight Substrate RPC + referenda/preimage decoders used by the indexer |
| `db/database.sql` | Authoritative schema and seed data |
| `docs/systemd/govcomms.service` | Sample unit file |

---

## Installation Steps

1. **Prerequisites**
   - Go 1.23+ with toolchain `go1.24.2` (matching `go.mod`)
   - MySQL 8.x (or compatible) reachable via TCP
   - Discord application with a bot token, slash-command scope, and intents: Guilds, Guild Messages, Message Content, (plus Reactions for Feedback)
   - Optional: Polkassembly account seeds (sr25519) for every network you want the bot to post into

2. **Clone & bootstrap**
   ```bash
   git clone https://github.com/stake-plus/govcomms chaos-dao-govbot
   cd chaos-dao-govbot
   go mod download
   ```

3. **Build the binary**
   ```bash
   # Native build
   go build -o bin/govcomms ./src

   # or via Makefile (also handles Windows suffixes)
   make build
   ```

4. **Provision MySQL**
   ```bash
   mysql -u root -p -e "CREATE DATABASE govcomms CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
   mysql -u root -p govcomms < db/database.sql
   ```
   - Create a dedicated MySQL user and grant it full access to the `govcomms` database.
   - Update `settings`, `networks`, and `network_rpcs` rows with Chaos DAO values (see tables below).

5. **Seed configuration**
   - Populate the `settings` table with at least the keys listed in [Settings keys](#settings-table-keys).
   - Ensure every network Chaos DAO participates in has:
     - `networks`: `id`, `name`, `url`, `discord_channel_id`, optional `polkassembly_seed`, `ss58_prefix`
     - `network_rpcs`: one or more active RPC endpoints

6. **Populate environment variables or `.env` file**
   - `MYSQL_DSN`, Discord token/IDs, AI provider keys, feature toggles (see [Environment variables](#environment-variables)).

7. **Launch `govcomms`**
   - Run manually or via `systemd` using `docs/systemd/govcomms.service`, updating all `Environment=` lines for your deployment.

---

## Database Schema & Settings

### Core tables

| Table | Purpose | Key Columns |
| --- | --- | --- |
| `settings` | Global key/value overrides (loaded at startup) | `name`, `value`, `active` |
| `networks` | Governance networks Chaos DAO tracks | `name`, `symbol`, `url`, `discord_channel_id`, `polkassembly_seed`, `ss58_prefix` |
| `network_rpcs` | RPC endpoints for each network | `network_id`, `url`, `active` |
| `refs` | Canonical referenda/proposals synced from chain | Governance metadata, tallies, Polkassembly IDs |
| `ref_threads` | Discord thread ↔ referendum mapping | `thread_id`, `ref_db_id`, `network_id`, `ref_id` |
| `ref_messages` | Feedback + Polkassembly replies | `author`, `body`, `polkassembly_*` columns |
| `qa_history` | Stored Q&A context per referendum | `network_id`, `ref_id`, `question`, `answer` |
| `ref_proponents` | Delegates/team addresses (optional) | `ref_id`, `address`, `role` |

Import `db/database.sql` any time you need to recreate the schema; it also inserts default `settings`, `networks`, and `network_rpcs` rows you can edit in place.

### Settings table keys

All settings are read from the DB first and fall back to environment variables. Populate at least:

| Key | Env Fallback | Description |
| --- | --- | --- |
| `discord_token` | `DISCORD_TOKEN` | Discord bot token |
| `guild_id` | `GUILD_ID` | Discord guild (server) ID where slash commands register |
| `qa_role_id` | `QA_ROLE_ID` | Role allowed to use `/question`, `/refresh`, `/context` |
| `research_role_id` | `RESEARCH_ROLE_ID` | Role for `/research` and `/team` |
| `feedback_role_id` | `FEEDBACK_ROLE_ID` | Role for `/feedback` |
| `openai_api_key` | `OPENAI_API_KEY` | Required unless you supply `claude_api_key` |
| `claude_api_key` | `CLAUDE_API_KEY` | Optional Anthropic alternative |
| `ai_provider` | `AI_PROVIDER` | `openai` (default) or `claude` |
| `ai_model` | `AI_MODEL` | Overrides per-provider default |
| `ai_system_prompt` | `AI_SYSTEM_PROMPT` | Custom system prompt for AI responses |
| `ai_enable_web_search` | `AI_ENABLE_WEB_SEARCH` | Set to `"1"` to allow web-search tool calls |
| `ai_enable_deep_search` | `AI_ENABLE_DEEP_SEARCH` | Enables the optional deep-search tool |
| `qa_temp_dir` | `QA_TEMP_DIR` | Proposal cache directory (defaults to `/tmp/govcomms-qa`) |
| `indexer_workers` | — | Go routines used by the referendum indexer (`10` default) |
| `indexer_interval_minutes` | — | Minutes between chain sync batches (`60` default) |
| `polkassembly_endpoint` | `POLKASSEMBLY_ENDPOINT` | API base (default `https://api.polkassembly.io/api/v1`) |
| `gc_url` | — | Base URL linked in Polkassembly mirror comments |
| `site_name`, `polkassembly_intro`, `polkassembly_outro` | — | Optional copy used in embeds/posts |

> **Mandatory note:** `MYSQL_DSN` is **not** read from the `settings` table. You must export it as an environment variable for the binary to start.

---

## Configuration

### Environment variables

| Variable | Required | Description |
| --- | --- | --- |
| `MYSQL_DSN` | ✅ | MySQL DSN (`user:pass@tcp(host:3306)/govcomms?parseTime=true&charset=utf8mb4`) |
| `DISCORD_TOKEN` | ✅ | Bot token if not stored in `settings` |
| `GUILD_ID` | ✅ | Guild ID if not stored in `settings` |
| `QA_ROLE_ID`, `RESEARCH_ROLE_ID`, `FEEDBACK_ROLE_ID` | ✅ | Role IDs unless stored in `settings` |
| `OPENAI_API_KEY` / `CLAUDE_API_KEY` | ✅ | At least one AI provider key |
| `AI_PROVIDER`, `AI_MODEL`, `AI_SYSTEM_PROMPT` | Optional overrides |
| `AI_ENABLE_WEB_SEARCH`, `AI_ENABLE_DEEP_SEARCH` | Optional `"1"` toggles |
| `ENABLE_QA`, `ENABLE_RESEARCH`, `ENABLE_FEEDBACK` | `"1"`/`"0"` env toggles that also back the CLI flags |
| `QA_TEMP_DIR` | Optional cache override |
| `POLKASSEMBLY_ENDPOINT` | Optional service override |

Use an `.env.govcomms` file and reference it from the systemd unit if you prefer not to export variables globally.

### CLI flags & launch settings

| Flag | Env mirror | Default | Module |
| --- | --- | --- | --- |
| `--enable-qa` | `ENABLE_QA` | `true` | AI Q&A slash commands |
| `--enable-research` | `ENABLE_RESEARCH` | `true` | Research slash commands |
| `--enable-feedback` | `ENABLE_FEEDBACK` | `true` | Feedback bot + indexer |

Example launch commands:

```bash
# Run all modules
./bin/govcomms

# Disable feedback (indexer + Polkassembly) for a lightweight Chaos DAO staging run
./bin/govcomms --enable-feedback=false
```

### Discord requirements for Chaos DAO

- Gateway intents: Guilds, Guild Messages, Message Content, Guild Message Reactions (feedback).
- Slash commands auto-register per guild when the bot connects; ensure the bot user has `applications.commands` scope authorized.
- `network.discord_channel_id` must match each parent channel that hosts referendum threads so the feedback bot can map threads to networks.

### Polkassembly integration

- Add `polkassembly_seed` (sr25519 mnemonic) to each `networks` row that should mirror the first Chaos DAO feedback message to Polkassembly.
- `shared/polkassembly.Service` forces SS58 prefix `42` when creating the signer. If your network uses another prefix, set `ss58_prefix` for display only; Polkassembly still requires generic Substrate encoding at login time.
- The bot will retry posting the first feedback message up to five times (5-minute backoff) and will poll every 15 minutes for replies to relay back into Discord threads.

---

## Chaos DAO Runtime Workflow

1. **Referendum indexer (`feedback/data/indexer.go`)**
   - Periodically connects to every RPC in `network_rpcs`, infers SS58 prefixes, and syncs referenda metadata into `refs`.
   - Creates placeholder records if Chaos DAO opens the Discord thread before the on-chain referendum exists locally.

2. **Discord thread mapping**
   - `feedback/bot` watches thread create/update events and maps thread titles (expects `<ref-id>: ...`) to `refs` via `shared/gov.ReferendumManager`.
   - Background sync (`GuildThreadsActive`) ensures long-lived Chaos DAO threads remain mapped even after restarts.

3. **AI Q&A module**
   - Downloads proposal + linked documents from Polkassembly into `QA_TEMP_DIR`.
   - Answers `/question` by combining cached content with the last 10 Q&A rows in `qa_history`, optionally using web search tools.
   - `/refresh` rebuilds the cache and `/context` prints stored answers for quick Chaos DAO review.

4. **Research module**
   - Extracts verifiable claims and team members from proposal content, then verifies them sequentially with OpenAI web-search tools.
   - Posts interim Discord messages in the referendum thread and edits them as evidence arrives, giving Chaos DAO reviewers a live audit trail.

5. **Feedback module**
   - Validates role access, persists submissions in `ref_messages`, posts embeds (with `.txt` attachments for long logs), and schedules Polkassembly mirroring.
   - Pulls Polkassembly replies back into Discord as embeds so the DAO sees two-way conversation continuity.

---

## Launching & Operating

1. **Manual run**
   ```bash
   export MYSQL_DSN="govcomms:password@tcp(127.0.0.1:3306)/govcomms?parseTime=true&charset=utf8mb4"
   export DISCORD_TOKEN="..."
   export GUILD_ID="..."
   export OPENAI_API_KEY="sk-..."
   ./bin/govcomms --enable-feedback --enable-research --enable-qa
   ```

2. **Systemd deployment**
   - Copy `docs/systemd/govcomms.service` to `/etc/systemd/system/govcomms.service`.
   - Update Environment lines (or reference `/opt/govcomms/.env.govcomms`).
   - `sudo systemctl daemon-reload && sudo systemctl enable --now govcomms`

3. **Logs & health**
   - The bots log to stdout/stderr; review via `journalctl -u govcomms`.
   - The feedback module logs indexer progress, Polkassembly sync status, and thread mapping errors—watch for these when onboarding new Chaos DAO networks.

4. **Upgrades**
   - Pull latest code, rebuild, restart the service.
   - Schema changes land in `db/database.sql`; apply them manually or manage them via migrations if you add new fields.

---

## Troubleshooting Checklist

- **Immediate crash with `MYSQL_DSN is not set`** – export `MYSQL_DSN` even if you store database credentials elsewhere.
- **Slash commands are missing** – confirm `GUILD_ID`, `DISCORD_TOKEN`, and that the bot has `applications.commands` permission inside the Chaos DAO guild.
- **Referenda never map to threads** – ensure thread titles start with the referendum number plus colon (e.g., `123: My Proposal`) and that `networks.discord_channel_id` matches the parent forum channel.
- **Polkassembly mirroring skipped** – verify `polkassembly_seed` is present, `gc_url` resolves, and the service logs show successful login; seeds must be sr25519 phrases.
- **Research/AI calls fail** – confirm at least one AI key is configured; watch for rate-limit logs in `shared/ai`.
- **Indexer stalled** – check `network_rpcs` URLs are reachable and update them when Chaos DAO rotates to new RPC pools.

---

## Additional Notes for Chaos DAO

- The binary is multi-tenant; you can enable/disable modules per environment to fit Chaos DAO staging vs. production workflows.
- Proposal caches (`QA_TEMP_DIR`) and the `tmp/` directory should be writable by the service account so `/question` can reuse downloaded documents.
- The project remains ASCII-only; keep environment files, SQL migrations, and Polkassembly intro/outro text similarly formatted to avoid encoding surprises.

By following this guide you can provision, configure, and operate the Chaos DAO Governance Bot with accurate database settings, installation instructions, runtime configuration, and clear ties to the overall Chaos DAO governance workflow.

