# Install AI

## One command

Linux and macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/Tulipskun/ai/main/scripts/install.sh | bash
```

The installer:

1. downloads only the matching prebuilt binary from `bin/`;
2. verifies it against `bin/checksums.txt`;
3. installs the `ai` command;
4. creates `.config`, `.data`, and `.ai` runtime directories.

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
├── .config/
│   ├── provider.json
│   ├── input.json
│   └── browser.json
├── .data/
│   └── browser/
│       └── profile/
└── .ai/
```

There is no `.env` file. Copy the example JSON files from the repository into `.config/` and edit them as needed.

For Discord and CLI input, configure `.config/input.json`.

For providers, configure `.config/provider.json`, or add providers interactively with `/provider` or `/provider add ...`.

For browser automation, configure `.config/browser.json`. Browser automation uses the installed Chrome, Chromium, or Edge browser through Chrome DevTools Protocol and is headed by default.

Example:

```json
{
  "enabled": true,
  "headless": false,
  "browser": "auto",
  "profile": ".data/browser/profile",
  "allow_private": false,
  "idle_timeout": "30m",
  "navigation_timeout": "30s",
  "action_timeout": "10s",
  "snapshot_timeout": "10s"
}
```

On Linux, headed browser automation requires a graphical session.
