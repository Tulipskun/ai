# Installation layout

The installed executable is separate from application state. Runtime state is stored under `AI_DATA_DIR`, which defaults to `~/.local/share/ai`.

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

`AI_BIN_DIR` can override the executable directory. `AI_DATA_DIR` can override the application state directory. `AI_WORKSPACE` controls the filesystem root used by file, command, and job tools: it defaults to the user home directory (`~`), accepts `~/...` paths (for example `AI_WORKSPACE=~/my-project`), and is created automatically when it does not exist.

Browser automation is implemented directly in Go using Chrome DevTools Protocol. It does not require Node.js, Playwright, or a separate browser worker.

`ai update` replaces the executable in the binary directory and restarts the daemon only when it was already running.
