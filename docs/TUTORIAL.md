# Hands-On Tutorial

Follow this walkthrough to bring GovComms online on a fresh Linux VM and wire it up for Chaos DAO.

## Step 0 – Prepare the Host

```bash
sudo apt update && sudo apt install -y git mysql-client mysql-server
wget https://go.dev/dl/go1.24.2.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.24.2.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.profile
source ~/.profile
```

## Step 1 – Clone the Repository

```bash
sudo mkdir -p /opt/govcomms && sudo chown $USER /opt/govcomms
git clone https://github.com/stake-plus/govcomms /opt/govcomms
cd /opt/govcomms
go mod download
```

## Step 2 – Build and Test

```bash
make build
go test ./...
```

This produces `bin/govcomms`. If tests fail, resolve them before proceeding.

## Step 3 – Configure MySQL

```sql
CREATE DATABASE govcomms CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'govcomms'@'localhost' IDENTIFIED BY 'replace-with-strong-password';
GRANT ALL PRIVILEGES ON govcomms.* TO 'govcomms'@'localhost';
FLUSH PRIVILEGES;
```

Import the schema and seed data:

```bash
mysql -u govcomms -p govcomms < db/database.sql
```

## Step 4 – Create the Environment File

```bash
cp config/env.sample /opt/govcomms/.env.govcomms
chmod 600 /opt/govcomms/.env.govcomms
```

Edit `.env.govcomms`:

- Fill in `MYSQL_DSN`, `DISCORD_TOKEN`, `GUILD_ID`.
- Add role IDs (if applicable).
- Provide the AI provider keys you need (`OPENAI_API_KEY`, `CLAUDE_API_KEY`, `GEMINI_API_KEY`, `DEEPSEEK_API_KEY`, `GROK_API_KEY`). Research/Team modules still require an OpenAI key for GPT‑5 analysis.

## Step 5 – Update Seed Data

Use your preferred SQL client to edit:

- `settings`: confirm tokens, role IDs, AI configuration, and set `gc_url` to the DAO’s public discussion page (no environment fallback).
- `networks`: set the correct `discord_channel_id`, `polkassembly_seed`, and `ss58_prefix` per network.
- `network_rpcs`: add reliable RPC URLs (e.g., `wss://rpc.polkadot.io`).

## Step 6 – Smoke Test Locally

```bash
source /opt/govcomms/.env.govcomms
./bin/govcomms --enable-feedback --enable-research --enable-qa
```

In Discord:

1. Create (or reuse) a referendum thread named `"<ref-id>: Title"`.
2. Run `/question` with a test query.
3. Run `/research` and `/team` to see placeholder messages update.
4. Submit `/feedback` to confirm the embed posts and the database stores the entry.

Stop the binary with `Ctrl+C` after confirming functionality.

## Step 7 – Install as a Service

```bash
sudo cp docs/systemd/govcomms.service /etc/systemd/system/govcomms.service
sudo systemctl daemon-reload
sudo systemctl enable --now govcomms
journalctl -u govcomms -f
```

Logs should show all action modules plus `[agents]` log lines for any enabled background agents (see `docs/AGENTS.md`). Leave the service running.

## Step 8 – Integrate the Chaos DAO Governance Bot

1. Obtain the Chaos DAO Governance Bot source/binaries (see the DAO’s repository).
2. Configure it to point at the **same** Discord guild, channels, and MySQL database GovComms manages.
3. Start the Chaos DAO bot after GovComms is stable so it can register its commands without conflicts.
4. Use `docs/CHAOS-DAO-INTEGRATION.md` as a checklist (shared roles, thread naming, API keys, etc.).

## Step 9 – Final Verification

- Confirm both GovComms and the Chaos DAO bot appear online in Discord.
- Run each slash command from a referendum thread.
- Check Polkassembly for mirrored comments (if seeds were configured).
- Watch the indexer logs over the next hour to ensure block heights increase and referenda sync properly.

You now have GovComms running in production-ready mode, integrated with the Chaos DAO Governance Bot.

