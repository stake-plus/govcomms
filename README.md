# Stake Plus GovComms

GovComms is the Stake Plus communications stack that powers Chaos DAO’s on-chain governance workflows. It bundles three Discord-first services—AI Q&A, Research, and Feedback/Polkassembly—into a single Go binary. The Chaos DAO Governance Bot depends on GovComms for proposal context, research tooling, and storage, so GovComms must be deployed first and kept up-to-date.

## Relationship to the Chaos DAO Governance Bot

- **Separate projects:** GovComms is the backend/middleware. The Chaos DAO Governance Bot (deployed separately) is the community-facing bot.
- **Shared resources:** Both need access to the same Discord guild, channels, and MySQL database so referenda, feedback, and AI history stay in sync.
- **Install order:** Deploy GovComms, load the schema, and populate configuration. Then install and configure the Chaos DAO bot so it can rely on GovComms’ data and slash-command handlers.

See `docs/CHAOS-DAO-INTEGRATION.md` for the joint deployment checklist.

## Features

- **AI Q&A (`src/ai-qa`)** – `/question`, `/refresh`, `/context`; caches proposal and linked docs, stores Q&A transcripts.
- **Research (`src/research-bot`)** – `/research`, `/team`; extracts claims and team members, runs sequential web-backed verification using OpenAI or Claude.
- **Feedback & Polkassembly (`src/feedback`)** – `/feedback`; persists feedback in MySQL, posts embeds, mirrors the first DAO response to Polkassembly, polls for replies, and runs the Substrate referendum indexer.
- **Shared packages (`src/shared`, `src/polkadot-go`)** – Config loaders, AI provider abstractions, Discord helpers, Polkassembly client, MySQL accessors, and a lightweight Substrate RPC toolkit.

## Repository Layout

```
├── README.md                # High-level overview (this file)
├── .env.example             # Sample environment configuration
├── db/database.sql          # Canonical schema + seed data
├── docs/                    # Installation, configuration, operations, tutorials
│   ├── INSTALLATION.md
│   ├── CONFIGURATION.md
│   ├── OPERATIONS.md
│   ├── TUTORIAL.md
│   └── CHAOS-DAO-INTEGRATION.md
├── docs/systemd/govcomms.service  # Reference service unit
├── Makefile                 # Convenience targets (build, clean, test)
├── src/                     # Application source code
└── tmp/                     # Scratch/cache directories (safe to delete)
```

## Quick Start

1. **Read** `docs/INSTALLATION.md` for prerequisites and build instructions.
2. **Configure** the database and environment variables using `docs/CONFIGURATION.md` and `.env.example`.
3. **Run** GovComms locally (`./bin/govcomms --enable-feedback`) or via the provided systemd unit.
4. **Integrate** the Chaos DAO Governance Bot by following `docs/CHAOS-DAO-INTEGRATION.md`.

## Documentation Map

| Document | Purpose |
| --- | --- |
| `docs/INSTALLATION.md` | Platform prerequisites, build steps, database provisioning. |
| `docs/CONFIGURATION.md` | Environment variables, settings table, network and Polkassembly configuration. |
| `docs/OPERATIONS.md` | Runtime commands, systemd usage, maintenance, troubleshooting. |
| `docs/TUTORIAL.md` | Step-by-step walkthrough for a fresh deployment. |
| `docs/CHAOS-DAO-INTEGRATION.md` | How GovComms interfaces with the Chaos DAO Governance Bot. |

## Contributing & License

- Format and lint with standard Go tooling (`go fmt`, `go test ./...`).
- Submit changes via pull request with accompanying documentation if configuration or deployment steps change.
- Licensed under Apache 2.0 (see `LICENSE`).

