# Installation layout

The installed executable lives in the standard binary directory and runtime state is kept separately.

```text
/usr/local/bin/ai              executable
~/.local/share/ai/             application state
├── .config/provider.json      provider configuration
├── .data/sessions.db          sessions
├── .data/cli.history          CLI history
├── .ai/ai.pid                 daemon PID
└── .ai/ai.log                 daemon log
```

`AI_BIN_DIR` can override the executable directory and `AI_DATA_DIR` can override the state directory.

`ai update` replaces the executable in the binary directory and restarts the daemon using the state directory.
