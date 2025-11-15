# Agents Runtime

GovComms now ships a background **agents** runtime that complements the
interactive Discord actions. Agents run long-lived “missions” for due diligence
tasks such as social verification, alias correlation, and grant-history
analysis. The runtime is enabled by default whenever you start `govcomms`.

## Architecture

- `agents/core` implements the `Agent`, `Mission`, and `Result` interfaces plus
  the lifecycle `Manager`.
- `agents/start.go` loads configuration (`config.LoadAgentsConfig`), builds a
  shared AI client + HTTP client, registers enabled agents, and keeps them
  running alongside the Discord modules (`main.go` starts both managers).
- Each agent advertises a string ID (`social_presence`, `alias_hunter`,
  `grant_watch`). Backend workflows (e.g., the research module or future jobs)
  dispatch missions through the shared manager.

Typical execution flow:

```go
result, err := agentManager.Execute(ctx, "social_presence", agents.Mission{
    ID: "mission-123",
    Kind: agents.MissionKindSocialProfile,
    Subject: agents.Subject{
        Type:       agents.SubjectSocialAccount,
        Platform:   "twitter",
        Identifier: "polkadot",
    },
})
```

The manager tracks every registered agent, wires shared dependencies
(`*gorm.DB`, HTTP client, AI client, logger), and exposes `Start`, `Stop`,
`Execute`, and `Describe`.

## Bundled Agents

1. **`social_presence`** – Scores the health of an individual social account
   (creation age, follower count, liveliness, bot risk). Ships with a
   `ManualProbe` that consumes cached stats supplied in the mission payload and
   can fan out to extra probes configured under `agents_social_providers`.
2. **`alias_hunter`** – Correlates usernames, on-chain addresses, and other
   aliases to build identity clusters with confidence scoring. Tuned via
   `agents_alias_min_confidence` and `agents_alias_max_suggestions`.
3. **`grant_watch`** – Reviews grant/treasury history to detect ecosystem
   hopping, failure rates, and rapid award patterns. Tuned via
   `agents_grant_lookback_days` and `agents_grant_repeat_threshold`.

Agents share the same AI provider stack as the Discord modules, so make sure at
least one provider key is configured. You can now set `ai_provider=consensus`
(or `AI_PROVIDER=consensus`) to let the agents automatically fan out across the
vendors listed in `ai_consensus_*` and return a voted-on answer.

## Configuration

| Setting / Env | Purpose | Default |
| --- | --- | --- |
| `enable_agents` / `ENABLE_AGENTS` | Global on/off switch for the runtime. | `true` |
| `enable_agent_social` / `ENABLE_AGENT_SOCIAL` | Toggle the social presence agent. | `true` |
| `enable_agent_alias` / `ENABLE_AGENT_ALIAS` | Toggle the alias hunter. | `true` |
| `enable_agent_grantwatch` / `ENABLE_AGENT_GRANTWATCH` | Toggle grant watch. | `true` |
| `agents_http_timeout_seconds` | Shared HTTP client timeout for outbound probes. | `90` |
| `agents_social_providers` | CSV/space separated list of social probes (e.g., `manual,twitter`). | `manual` |
| `agents_alias_min_confidence` | Confidence threshold (0–1) for alias suggestions. | `0.6` |
| `agents_alias_max_suggestions` | Max alias suggestions returned per mission. | `10` |
| `agents_grant_lookback_days` | How far back in history the grant watcher scans. | `540` |
| `agents_grant_repeat_threshold` | Number of grants before flagging repetition. | `3` |

> These keys live in the MySQL `settings` table. Environment fallbacks with the
> same uppercase name exist for the boolean toggles.

## Monitoring & Operations

- Agents log with the `[agents]` prefix; look for `agents: ... disabled` lines
  during startup to confirm gating works.
- Use the same process flags (`ENABLE_AGENTS=0 ./bin/govcomms ...`) if you need
  to isolate Discord modules from the agents runtime during debugging.
- Failures inside individual missions do not crash the service—they surface as
  log lines from the specific agent. Add alerting for repeated mission errors if
  you depend on the results downstream.

Refer back to `docs/CONFIGURATION.md` for the full list of settings and
`docs/OPERATIONS.md` for runtime health checklists.


