# Discord Modules

This repository currently exposes three Discord-facing modules that all run inside the `govcomms` binary. Each module registers its own slash commands when the bot logs in and shares the same connection and database handle.

## Module Summary

| Module | Commands | Purpose |
| --- | --- | --- |
| AI Q&A | `/question`, `/refresh`, `/context` | Surfaced proposal content and prior Q&A history on demand. |
| Research | `/research`, `/team` | Generates deeper AI-assisted summaries about claims and teams. |
| Feedback | `/feedback` | Accepts written feedback inside referendum threads, posts an in-thread embed, and optionally mirrors the first message to Polkassembly. |

All commands must be invoked from an existing referendum thread that the bot can map back to a `shared.gov.Ref` record.

## Slash Commands

### `/question`
- **Module**: AI Q&A
- **Options**: `question` (string)
- **Role requirement**: `qa_role_id`
- **Flow**:
  1. Validates the command is executed inside a mapped referendum thread.
  2. Fetches cached proposal content (refreshing from Polkassembly if necessary).
  3. Calls the shared AI provider (`shared/ai`) to produce an answer.
  4. Stores the question/answer pair in MySQL and replies in-thread.

### `/refresh`
- Rebuilds cached proposal content from Polkassembly.
- Same role requirements and thread validation as `/question`.

### `/context`
- Displays the most recent Q&A entries for the referendum.

### `/research`
- **Module**: Research bot
- **Role requirement**: `research_role_id`
- Generates claim verification results asynchronously, posts progress messages per claim, and edits them with results.

### `/team`
- Runs an AI-assisted team analysis, posting interim messages per team member before editing them with the final summary.

### `/feedback`
- **Module**: Feedback bot
- **Role requirement**: `feedback_role_id`
- Ensures the thread can be mapped to a `Ref` record, persists the message, and posts a Discord embed summarising the submission. Long messages include a `.txt` attachment. The bot also mirrors the first DAO comment to Polkassembly when credentials are configured.

## Configuration Checklist

1. **Database**
   - A MySQL DSN is required for all modules.
   - `shared/config/services.go` outlines every field loaded from the database or environment.
   - Set `gc_url` in the `settings` table so feedback embeds can link back to your external discussion UI when mirroring to Polkassembly.

2. **Discord Setup**
   - The bot requires the following gateway intents: Guilds, Guild Messages, Message Content.
   - Permissions: Read/Send Messages, Embed Links, Manage Threads, Read Message History.
   - Slash commands are registered per guild. Ensure `guild_id` (or `GUILD_ID`) is set correctly.

3. **Role IDs**
   - Store role IDs in the `settings` table (`qa_role_id`, `research_role_id`, `feedback_role_id`) or export the equivalent environment variables.

4. **AI Providers**
   - Provide either `openai_api_key` (`OPENAI_API_KEY`) or `claude_api_key` (`CLAUDE_API_KEY`).
   - Set `ai_provider` / `AI_PROVIDER` if you wish to force a specific provider, and optionally override `ai_model`.

5. **Polkassembly credentials**
   - Add `polkassembly_seed` (and optionally `ss58_prefix`) to each row in the `networks` table where you want automatic Polkassembly posting. Without a seed, the feedback bot will skip the outbound publish.

6. **Flags**
   - Start the binary with `--enable-qa`, `--enable-research`, and/or `--enable-feedback` depending on which modules you want to run.

## Known Limitations

- There is no web UI or REST API in this repository; earlier documentation referenced modules that no longer exist.
- The feedback module does not yet relay web-originated replies back into Discord; only inbound feedback from Discord is handled. (It will, however, post the first Discord feedback message to Polkassembly when a seed is configured.)
- Rate limiting and moderation policies still need to be reintroduced.

## Observability

- Each module logs a startup message when it successfully registers slash commands.
- Feedback embeds use a consistent colour (`0x5865F2`) and include the Discord user tag in the footer.

## Updating Thread Mappings

- Thread mapping is kept up-to-date by:
  - Handling `THREAD_CREATE` and `THREAD_UPDATE` events.
  - The feedback bot’s periodic sync (`GuildThreadsActive`) which reconciles active threads.
  - `UpsertThreadMapping` creates placeholder `Ref` rows if the indexer has not populated them yet.

## Deployment Notes

- Use the sample unit file in `docs/systemd/govcomms.service` as a starting point.
- When running multiple modules, ensure the role IDs and intents support all commands.
- Consider seeding the `settings` table before the bot starts so that slash commands register cleanly on first boot.