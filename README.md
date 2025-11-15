# Stake Plus GovComms

GovComms is the Stake Plus communications stack that powers Chaos DAO’s on-chain governance workflows. It bundles three Discord-first services—AI Q&A, Research, and Feedback/Polkassembly—into a single Go binary. The Chaos DAO Governance Bot depends on GovComms for proposal context, research tooling, and storage, so GovComms must be deployed first and kept up-to-date.

## Relationship to the Chaos DAO Governance Bot

- **Separate projects:** GovComms is the backend/middleware. The Chaos DAO Governance Bot (deployed separately) is the community-facing bot.
- **Shared resources:** Both need access to the same Discord guild, channels, and MySQL database so referenda, feedback, and AI history stay in sync.
- **Install order:** Deploy GovComms, load the schema, and populate configuration. Then install and configure the Chaos DAO bot so it can rely on GovComms’ data and slash-command handlers.

See `docs/CHAOS-DAO-INTEGRATION.md` for the joint deployment checklist.

## Features

- **AI Q&A (`src/actions/question`)** – Provides `/question`, `/refresh`, and `/context` commands, maintains proposal caches under `src/cache`, and records Q&A transcripts in MySQL.
- **Research & Team Analysis (`src/actions/research`, `src/actions/team`)** – Powers `/research` and `/team`, extracts claims, verifies evidence with the AI factory (`src/ai`), and publishes styled Discord updates.
- **Feedback & Polkassembly (`src/actions/feedback`)** – Handles `/feedback`, maps Discord referendum threads, mirrors DAO responses to Polkassembly, syncs replies, and runs the Substrate indexer in `src/actions/feedback/data`.
- **Agents runtime (`src/agents`)** – Runs continuous due-diligence missions (social presence, alias hunting, grant watch) using the same AI provider registry. See `docs/AGENTS.md`.
- **Core packages (`src/config`, `src/ai`, `src/cache`, `src/polkadot-go`)** – Hold configuration loaders, AI provider registry and clients, caching utilities, Discord helpers, Polkassembly client, and the lightweight Substrate RPC toolkit.

## Repository Layout

```
├── README.md                         # High-level overview (this file)
├── config/
│   └── env.sample                    # Sample environment configuration
├── db/database.sql                   # Canonical schema + seed data
├── docs/                             # Installation, configuration, operations, tutorials
│   ├── INSTALLATION.md
│   ├── CONFIGURATION.md
│   ├── OPERATIONS.md
│   ├── TUTORIAL.md
│   └── CHAOS-DAO-INTEGRATION.md
├── docs/systemd/govcomms.service     # Reference systemd unit
├── Makefile                          # Convenience targets (build, clean, test)
├── src/                              # Application source (actions, ai, cache, config, etc.)
└── tmp/                              # Scratch/cache directories (safe to delete)
```

## Quick Start

1. **Read** `docs/INSTALLATION.md` for prerequisites and build instructions.
2. **Configure** the database and environment variables using `docs/CONFIGURATION.md` and `config/env.sample`.
3. **Run** GovComms locally (`./bin/govcomms --enable-feedback`) or via the provided systemd unit.
4. **Integrate** the Chaos DAO Governance Bot by following `docs/CHAOS-DAO-INTEGRATION.md`.

## Documentation Map

| Document | Purpose |
| --- | --- |
| `docs/INSTALLATION.md` | Platform prerequisites, build steps, database provisioning. |
| `docs/CONFIGURATION.md` | Environment variables, settings table, AI providers, agent knobs. |
| `docs/OPERATIONS.md` | Runtime commands, systemd usage, monitoring, troubleshooting. |
| `docs/TUTORIAL.md` | Step-by-step walkthrough for a fresh deployment. |
| `docs/CHAOS-DAO-INTEGRATION.md` | How GovComms interfaces with the Chaos DAO Governance Bot. |
| `docs/AGENTS.md` | Architecture and configuration of the background agents runtime. |

## Contributing & License

- Format and lint with standard Go tooling (`go fmt`, `go test ./...`).
- Submit changes via pull request with accompanying documentation if configuration or deployment steps change.
- Licensed under Apache 2.0 (see `LICENSE`).

