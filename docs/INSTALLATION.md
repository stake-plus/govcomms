# Installation Guide

This guide walks through provisioning Stake Plus GovComms from scratch. Follow it before configuring systemd services or integrating the Chaos DAO Governance Bot.

## 1. Prerequisites

| Requirement | Notes |
| --- | --- |
| Operating system | Linux (Ubuntu 22.04+ recommended) or Windows 11 with PowerShell 7+. |
| Go toolchain | Go 1.23+ (repo pins toolchain `go1.24.2` in `go.mod`). |
| Git | Required for cloning the repository. |
| MySQL/MariaDB | MySQL 8.x (or MariaDB 10.6+) reachable over TCP. UTF8MB4 charset is mandatory. |
| Discord bot | A Discord application with bot permissions, `applications.commands` scope, and intents: Guilds, Guild Messages, Message Content, Message Reactions. |
| AI providers | At least one of: OpenAI (GPT‑5/4o), Anthropic (Sonnet/Haiku/Opus), Google Gemini 2.5, DeepSeek v3.2, or xAI Grok 4. Research/Team analyzers currently **require** an OpenAI key even if other providers are configured. |
| Polkassembly (optional) | sr25519 seed phrases for every network that should mirror feedback. |

## 2. Clone the Repository

```bash
git clone https://github.com/stake-plus/govcomms
cd govcomms
```

If you plan to deploy multiple environments, clone into `/opt/govcomms` (Linux) or `D:\Apps\govcomms` (Windows) so the provided systemd unit and scripts line up with common paths.

## 3. Build the Binary

```bash
# Install Go dependencies
go mod download

# Build in-place
go build -o bin/govcomms ./src

# or use the Makefile (adds OS-aware suffixes)
make build
```

Run the tests whenever you upgrade libraries or modify the code:

```bash
go test ./...
```

## 4. Provision the Database

1. Create the database and user:
   ```sql
   CREATE DATABASE govcomms CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
   CREATE USER 'govcomms'@'%' IDENTIFIED BY 'strong-password';
   GRANT ALL PRIVILEGES ON govcomms.* TO 'govcomms'@'%';
   FLUSH PRIVILEGES;
   ```

2. Load the schema and seed data:
   ```bash
   mysql -u govcomms -p govcomms < db/database.sql
   ```

The seed inserts placeholder settings, two networks (Polkadot/Kusama), and default RPC endpoints. Update these rows after installation (see `docs/CONFIGURATION.md`).

## 5. Prepare Environment Variables

1. Copy the sample file:
   ```bash
   cp config/env.sample /opt/govcomms/.env.govcomms
   ```
   (Use an equivalent path on Windows, e.g. `C:\govcomms\.env.govcomms`.)

2. Populate the file with real credentials:
   - `MYSQL_DSN` must point to the database created above.
   - Discord token/guild ID must match the bot you intend to register.
   - Provide the AI provider keys you plan to use (`OPENAI_API_KEY`, `CLAUDE_API_KEY`, `GEMINI_API_KEY`, `DEEPSEEK_API_KEY`, `GROK_API_KEY`). `/research` and `/team` still require an OpenAI key for GPT‑5 claims analysis.
   - Set `QA_TEMP_DIR` to a writable cache directory.
   - Leave the per-agent toggles (`ENABLE_AGENT_*`) at `1` unless you intentionally want to disable the new agents runtime (see `docs/AGENTS.md`).

> GovComms reads settings from MySQL first, then falls back to environment variables. Keeping the `.env` file in sync with the database makes migrations easier.

## 6. Run GovComms Locally

```bash
source /opt/govcomms/.env.govcomms   # or use direnv / dotenv on Windows
./bin/govcomms --enable-qa --enable-research --enable-feedback
```

On first boot, watch the logs for:

- `question: logged in as …` and `research: logged in as …` (Discord sessions connected).
- `feedback: slash command registered` followed by `feedback: starting network indexer service`.
- `<network> indexer: Current block height …` messages confirming RPC connectivity per network.

Stop the process with `Ctrl+C` once you confirm everything works. You are now ready to configure persistent services (see `docs/OPERATIONS.md`) and to integrate the Chaos DAO Governance Bot (`docs/CHAOS-DAO-INTEGRATION.md`).

