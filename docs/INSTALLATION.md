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
   - Provide at least one AI provider key (`OPENAI_API_KEY` or `CLAUDE_API_KEY`).
   - Set `QA_TEMP_DIR` to a writable cache directory.

> GovComms reads settings from MySQL first, then falls back to environment variables. Keeping the `.env` file in sync with the database makes migrations easier.

## 6. Run GovComms Locally

```bash
source /opt/govcomms/.env.govcomms   # or use direnv / dotenv on Windows
./bin/govcomms --enable-qa --enable-research --enable-feedback
```

On first boot, watch the logs for:

- Successful MySQL connection.
- Slash command registration (one message per module).
- Indexer startup and RPC connectivity.

Stop the process with `Ctrl+C` once you confirm everything works. You are now ready to configure persistent services (see `docs/OPERATIONS.md`) and to integrate the Chaos DAO Governance Bot (`docs/CHAOS-DAO-INTEGRATION.md`).

