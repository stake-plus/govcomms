# Agents

Governance due diligence now has a dedicated `agents` subsystem that mirrors
the `actions` package but is geared toward backend workflows rather than
Discord commands.

## Architecture

- `core` – shared interfaces (`Agent`, `Mission`, `Result`), lifecycle manager,
  and runtime dependency wiring.
- `start.go` – loads configuration (`config.AgentsConfig`), constructs the
  manager, and wires enabled agents.
- `socialpresence`, `aliashunter`, `grantwatch` – the first three concrete
  agents that can be executed by any backend module.

Agents expose a simple contract:

```go
result, err := agentManager.Execute(ctx, "social_presence", agents.Mission{
    ID:      "mission-123",
    Kind:    agents.MissionKindSocialProfile,
    Subject: agents.Subject{
        Type:       agents.SubjectSocialAccount,
        Platform:   "twitter",
        Identifier: "polkadot",
    },
})
```

Each `Mission` can carry:

- `Subject` + optional `Aliases`
- arbitrary `Inputs map[string]any`
- structured `Artifacts []Artifact` for richer context (e.g., grant history
  records, cached social stats, alias evidence)

The `Manager` keeps track of registered agents, handles lifecycle (`Start`,
`Stop`), and exposes `Execute` / `Describe`.

## Bundled agents

1. **`social_presence`** – analyzes an individual social-media account (creation
   age, follower counts, liveliness, bot risk). Ships with a `ManualProbe` that
   consumes any stats already gathered inside the mission; additional probes
   (scrapers, APIs, LLM workflows) can be registered later by extending
   `Config.Providers`.
2. **`alias_hunter`** – correlates handles, github usernames, aliases, and
   other identity clues to form clusters with confidence scoring.
3. **`grant_watch`** – reviews grant / treasury history to detect ecosystem
   hopping, failure rates, and rapid award patterns.

## Configuration

`config.LoadAgentsConfig()` reads environment/database settings to gate each
agent individually (`ENABLE_AGENTS`, `ENABLE_AGENT_SOCIAL`, etc.) and shares
the global AI provider setup. The HTTP timeout, social probe list, alias
thresholds, and grant lookback windows can all be tuned via settings.

