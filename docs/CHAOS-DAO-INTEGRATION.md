# Chaos DAO Integration Guide

GovComms is the Stake Plus backend that the Chaos DAO Governance Bot relies on. This document explains how the two systems fit together and what you must configure so they operate smoothly in the same Discord guild.

## 1. Conceptual Model

| Component | Responsibility |
| --- | --- |
| **GovComms** | Registers Discord slash commands, manages AI pipelines, stores referendum data, pushes/pulls Polkassembly comments, and runs the Substrate indexer. |
| **Chaos DAO Governance Bot** | Community-facing orchestration layer (announcements, voting reminders, additional workflows). It expects GovComms to provide authoritative referendum data and slash-command functionality. |

## 2. Installation Order

1. **Deploy GovComms** (follow `docs/INSTALLATION.md`, `docs/CONFIGURATION.md`, and `docs/OPERATIONS.md`).
2. **Verify** GovComms commands (`/question`, `/research`, `/feedback`) from a referendum thread.
3. **Deploy the Chaos DAO Governance Bot** (from the DAO’s repository or distribution).
4. **Point the Chaos DAO bot** at the same Discord guild, channels, and MySQL database (if it consumes GovComms data directly).

Only after GovComms is online should you start the Chaos DAO bot. This prevents slash-command conflicts and ensures the DAO bot sees populated referenda tables from the moment it boots.

## 3. Shared Configuration Checklist

| Item | Requirement |
| --- | --- |
| Discord application | Either share one application (both binaries use the same token) or create two applications with distinct tokens—just ensure they operate in the same guild. |
| Guild & channels | `guild_id` and `networks.discord_channel_id` in GovComms must match the Chaos DAO bot’s configuration. Thread names must begin with the referendum number (e.g., `123: Treasury Refill`). |
| MySQL access | The Chaos DAO bot can read from the GovComms database to display referenda, QA history, or feedback. Provide it with a read-only user if needed. |
| Polkassembly credentials | Store sr25519 seeds in GovComms. The Chaos DAO bot will see mirrored comments automatically via Discord. |
| Environment files | Keep GovComms `.env` and the Chaos DAO bot’s env in sync regarding guild IDs, role IDs, and channel mappings. |

## 4. Recommended Deployment Topology

```
                         +---------------------------+
                         | Chaos DAO Governance Bot  |
                         |  - Announcements          |
                         |  - Community workflows    |
                         +-------------+-------------+
                                       |
           Discord Gateway (shared guild/channels/intents)
                                       |
+-------------------+    +-------------v-------------+
| Substrate RPCs    |    | Stake Plus GovComms       |
| (per network)     |    |  - AI Q&A / Research      |
+-------------------+    |  - Feedback + Polkassembly|
                          |  - Referendum indexer    |
                          +-------------+------------+
                                        |
                              MySQL (govcomms database)
```

- GovComms keeps the database, Discord threads, and Polkassembly in sync.
- The Chaos DAO bot consumes that state (via Discord and/or MySQL) to provide higher-level DAO functionality.

## 5. Integration Steps

1. **Create shared roles:** `qa_role_id`, `research_role_id`, `feedback_role_id`. Assign them in Discord and store the IDs in both GovComms and the Chaos DAO bot configs.
2. **Populate referenda threads:** Make sure each DAO referendum thread follows the `<id>: <title>` naming scheme so GovComms can map and the Chaos DAO bot can reference the same thread IDs.
3. **Validate Polkassembly mirroring:** Submit `/feedback`, confirm GovComms posts to Polkassembly, then ensure the Chaos DAO bot surfaces or links to those messages as needed.
4. **Coordinate maintenance windows:** Restart GovComms first, wait for it to stabilize, then restart the Chaos DAO bot. This ensures the DAO bot never sees an empty database or missing slash commands.

## 6. Troubleshooting Integration Issues

| Symptom | Diagnosis | Fix |
| --- | --- | --- |
| Chaos DAO bot reports missing referenda | GovComms indexer not running or RPC endpoint unavailable | Check `docs/OPERATIONS.md` to verify the indexer and RPC URLs. |
| Slash commands collide or fail to register | Bots share the same application but register conflicting commands simultaneously | Start GovComms first; stagger restarts so commands are registered sequentially. |
| Feedback posted by GovComms does not appear in Chaos DAO UI | DAO bot not reading from the same database or lacks permissions | Confirm both services point to the same MySQL instance and tables. Provide read-only credentials if necessary. |
| Polkassembly replies aren’t propagated | GovComms Polkassembly credentials missing | Update `networks.polkassembly_seed` and restart GovComms. |

## 7. Security Considerations

- Store Discord tokens and Polkassembly seeds in environment files that are **not** committed to source control.
- Grant the Chaos DAO bot **read-only** DB access if it does not need write permissions.
- Use role-based controls in Discord to limit who can invoke `/question`, `/research`, and `/feedback`.

With this integration in place, GovComms handles all data ingestion, storage, and AI workflows, while the Chaos DAO Governance Bot remains focused on DAO-facing experiences.

