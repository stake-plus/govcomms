# Operations Guide

This document covers day‑to‑day GovComms operations: running the binary, managing services, monitoring, and troubleshooting.

## 1. Running the Binary

### Local execution

```bash
source /opt/govcomms/.env.govcomms
./bin/govcomms --enable-qa --enable-research --enable-feedback
```

Flags can be combined (or disabled) as needed:

```bash
./bin/govcomms --enable-feedback=false --enable-qa=true --enable-research=true
```

Environment variables `ENABLE_QA`, `ENABLE_RESEARCH`, and `ENABLE_FEEDBACK` mirror these flags.

### Makefile targets

| Target | Description |
| --- | --- |
| `make build` | Builds `bin/govcomms` for the host OS/arch. |
| `make build-linux`, `make build-windows` | Cross-compiles for the specified platform (amd64). |
| `make clean` | Removes the `bin/` directory. |
| `make test` | Runs `go test ./...`. |

## 2. Systemd Deployment

1. Copy `docs/systemd/govcomms.service` to `/etc/systemd/system/govcomms.service`.
2. Adjust the `User`, `Group`, `WorkingDirectory`, and `EnvironmentFile` fields.
3. Reload and start:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now govcomms
   ```
4. Tail logs:
   ```bash
   journalctl -u govcomms -f
   ```

### Environment file

Store secrets and configuration in `/opt/govcomms/.env.govcomms` (referenced by the systemd unit). Keep the file readable only by the service account.

### Health checklist

After restarting the service, confirm:

- `question: logged in as …` and `research: logged in as …` appear once Discord sessions connect.
- `feedback: slash command registered` and `feedback: starting network indexer service` show that the feedback module is healthy.
- `<network> indexer: Current block height …` appears for every configured network.
- Polkassembly login succeeds when seeds are configured (watch for HTTP errors in `feedback:` logs).
- Discord commands respond promptly in referendum threads.

## 3. Monitoring & Logs

GovComms logs to stdout/stderr. Key log messages include:

- **Discord sessions:** `question: logged in as …`, `research: logged in as …`, `Feedback bot logged in as …`.
- **Slash registration/indexer start:** `feedback: slash command registered`, `feedback: starting network indexer service`, and `Starting indexer service with …`.
- **Indexer status:** `<network> indexer: Current block height ...`.
- **Thread mapping:** `feedback: thread ... mapping failed` (action required).
- **Polkassembly:** success/failure logs for posting comments and syncing replies (e.g., `feedback: polkassembly monitor error: ...`).

### Suggested alerting

- Monitor for repeated RPC failures in `src/actions/feedback/data/indexer.go`.
- Alert if slash-command registration fails (e.g., invalid token or missing intents).
- Watch for `MYSQL_DSN is not set` to catch misconfigured services.

## 4. Maintenance Tasks

| Task | Frequency | Notes |
| --- | --- | --- |
| Update RPC endpoints | As nodes change | Edit the `network_rpcs` table and restart GovComms. |
| Rotate Polkassembly seeds | As required | Update `networks.polkassembly_seed`; no restart needed. |
| Vacuum caches | Optional | Remove `QA_TEMP_DIR` contents to force `/refresh` to reload proposals. |
| Backup database | Nightly | `mysqldump govcomms > backup.sql`. Include `refs`, `ref_messages`, `qa_history`. |
| Review dependencies | Quarterly | `go list -u -m all` and run tests before upgrading. |

## 5. Upgrades

1. Stop the service: `sudo systemctl stop govcomms`.
2. Pull updates and rebuild:
   ```bash
   git pull origin main
   go mod tidy
   make build
   ```
3. Apply schema changes from `db/database.sql` if new columns were introduced.
4. Start the service and monitor logs.

## 6. Troubleshooting

| Symptom | Likely Cause | Resolution |
| --- | --- | --- |
| `MYSQL_DSN is not set` on boot | Missing environment variable | Add to `.env` and restart. |
| Slash commands disappear | Discord token revoked or guild ID mismatch | Regenerate token, verify `guild_id`, restart service. |
| `/question` reports “must be used in a referendum thread” | Thread not mapped | Ensure thread title starts with `<ref-id>:` and `networks.discord_channel_id` matches the parent forum. |
| Indexer logs `Failed to get referendum count` | RPC unreachable | Update `network_rpcs` or verify node health. |
| Polkassembly mirroring skipped | Missing `polkassembly_seed` or authentication failure | Add seed, verify the `gc_url` setting, review logs for HTTP 401/403. |
| AI modules fall back to cached content | Provider rate limit or key invalid | Check provider dashboards, rotate keys, or enable alternative provider. |

## 7. Data Hygiene & Safety

- **Never share** `\*.env` files or Polkassembly seeds in source control.
- Restrict MySQL access to the GovComms and Chaos DAO bot hosts.
- Keep `refs` and `ref_messages` backed up; they contain DAO deliberation history.

## 8. Staging vs. Production

You can run multiple GovComms instances by pointing each at a different database and Discord guild:

| Component | Staging | Production |
| --- | --- | --- |
| Database | `govcomms_staging` | `govcomms` |
| Discord Guild | Test server | Chaos DAO live server |
| Module flags | Minimal set (`--enable-feedback=false`) | All modules enabled |
| RPC endpoints | Public, low-tier nodes | Dedicated or community-hosted endpoints |

Keep environment files separate (`.env.staging`, `.env.production`) and ensure the Chaos DAO Governance Bot connects to the matching environment.

