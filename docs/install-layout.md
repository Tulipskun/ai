# Installation layout

The installed executable is separate from application state. Runtime state is always stored under `~/.local/share/ai`.

```text
/usr/local/bin/ai                    executable
~/.local/share/ai/                    application state
├── config/
│   ├── entry.json                   transport configuration
│   ├── provider.json                provider configuration and API keys
│   └── browser.json                 browser configuration
├── data/
│   ├── jobs.json                    persistent background job state
│   └── sessions/                    one SQLite database per session
│       ├── <session>.db
│       └── ...
├── ai.pid                           daemon PID
└── ai.log                           daemon log
```

There is no application `.ai` runtime directory.

`jobs.json` is a persistent tool-state store. Every job records its owning session ID, and `check_job`/`close_job` can only access jobs owned by the current session. Jobs that were still running when the process stopped are restored as failed rather than being treated as active orphaned processes.

Each file under `data/sessions/` contains exactly one session. The session database stores that session's settings, turns, provider request/response records, and Discord channel mappings.

All runtime configuration lives in `config/*.json` under the state directory; there is no environment-variable configuration. `workspace` in `config/system.json` controls the filesystem root used by file, command, and job tools: it defaults to the user home directory (`~`), accepts `~/...` paths (for example `~/my-project`), and is created automatically when it does not exist. The system prompt lives in `config/system.json` (managed with `ai system`, `ai system set <prompt>`, `ai system clear`): the file wins when set, otherwise the built-in default applies. `config/system.json` also holds the default `provider`, `model`, and `max_output_tokens` for new sessions.

Browser automation is implemented directly in Go using Chrome DevTools Protocol. It does not require Node.js, Playwright, or a separate browser worker.

`ai update` replaces the executable in the binary directory and restarts the daemon only when it was already running.

`scripts/keepalive.sh` watches `ai.pid` and restarts the daemon when the process is gone. Browser startup failure no longer stops the daemon; it logs the error and continues without browser automation instead.
