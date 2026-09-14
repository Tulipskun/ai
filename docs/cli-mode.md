# CLI mode

`ai start` starts the background Harness service and returns to the shell. `ai cli` opens the foreground interactive AI session.

## Interactive UX

The CLI provides persistent input history in `.data/cli.history`, arrow-key history navigation, cursor editing, Home/End, Ctrl+U/Ctrl+K, Ctrl+C turn cancellation, and Ctrl+D exit. Normal input is sent to the active model with a single non-streaming Generate call. Tool activity and retry waits are rendered as compact status lines.

The CLI never prints raw model chain-of-thought. The `/thinking` command changes the session thinking setting and the UI only reports the selected level.

## Commands

| Command | Purpose |
| --- | --- |
| `/help` | Show commands and keyboard controls |
| `/new` | Start a fresh session |
| `/clear` | Clear the terminal and start a fresh session |
| `/sessions` | List persisted sessions |
| `/resume [id]` | Resume a saved session; without an id resumes the most recently updated session |
| `/continue [id]` | Alias for `/resume` |
| `/provider` | List configured providers |
| `/provider add <name> <adapter> <url> <api-key>` | Add a provider and refresh its models |
| `/models [provider]` | Discover/list models |
| `/model <model>` | Select the current model; `provider/model` also selects a provider |
| `/thinking <none\|low\|medium\|high\|off>` | Set session thinking level |
| `/temperature <0..2\|off>` | Set session temperature |
| `/status` or `/details` | Show current session configuration |
| `/export` | Write the current session to `.data/transcripts/` as Markdown |
| `/compact` | Show context-window status; automatic context management remains enabled |
| `/quit`, `/exit`, `/q` | Exit the CLI |

Provider/model settings are session-scoped and are persisted with the SQLite session database.