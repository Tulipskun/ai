# Install AI

## One command

Linux arm64:

```bash
curl -fsSL https://raw.githubusercontent.com/Tulipskun/ai/main/scripts/install.sh | bash
```

The installer downloads only the `ai-linux-arm64` binary from GitHub Releases with `gh release download --repo Tulipskun/ai`, verifies it against the release `checksums.txt`, and installs the `ai` command.

No Git, Go, Node.js, or Playwright installation is required for normal use.

Start:

```bash
ai start
```

Update later:

```bash
ai update
```

## Configuration

Runtime state is stored in `~/.local/share/ai` by default:

```text
~/.local/share/ai/
├── config/
│   ├── entry.json
│   ├── provider.json
│   ├── browser.json
│   └── attachment.json
├── data/
│   ├── jobs.json
│   ├── sessions/
│   │   ├── <session-1>.db
│   │   ├── <session-2>.db
│   │   └── ...
│   └── attachments/
│       └── <session>/
│           ├── manifest.json
│           └── files/
├── ai.pid
└── ai.log
```

For Discord and CLI input, configure `config/entry.json`.

For providers, configure `config/provider.json`, or add providers interactively with `/provider` or `/provider add ...`.

Each session is stored independently as `<session-id>.db` under `data/sessions/`. A session database contains only that session's persistent settings, turns, and request/response records.

Background jobs are persisted in `data/jobs.json`. Every job records its owning session ID, and job inspection/termination is restricted to that session.

For browser automation, configure `config/browser.json`. Browser automation uses the installed Chrome, Chromium, or Edge browser through Chrome DevTools Protocol and is headed by default. Playwright and a separate Node.js browser worker are not used.

The attachment file store lives under `data/attachments/`: one directory per session, each holding a `manifest.json` of metadata and a `files/` directory named by opaque attachment ID. File content is never stored in a session database, and a caller receives only a name, content type, size, and relative path. Configure the store in `config/attachment.json` (`enabled`, `root`, `max_file_bytes`, `max_session_bytes`, `ttl`); `root` is relative to the state directory and must stay inside it. Four optional keys bound the Discord file transfers instead of the store: `download_timeout`, `upload_timeout`, `max_send_file_bytes`, and `max_send_file_count`; each falls back to its own default when omitted.

Example:

```json
{
  "enabled": true,
  "mode": "managed",
  "headless": false,
  "browser": "auto",
  "profile": "data/browser/profile",
  "allow_private": false,
  "idle_timeout": "30m",
  "navigation_timeout": "30s",
  "action_timeout": "10s",
  "snapshot_timeout": "10s"
}
```

On Linux, headed browser automation requires a graphical session.

## Runtime files

The daemon PID and log are stored as `ai.pid` and `ai.log` under the runtime state directory. Provider, transport, and browser configuration live under `config/`. Session databases and persistent job state live under `data/`.
