# Install AI

## One command

Linux and macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/Tulipskun/ai/main/scripts/install.sh | bash
```

The installer:

1. downloads only the matching prebuilt binary from `bin/`;
2. verifies it against `bin/checksums.txt`;
3. installs `~/.local/bin/ai` as the command;
4. creates `.config`, `.data`, `.ai`, and a starter `.env` without overwriting existing state.

No Git or Go installation is required for normal use.

If necessary:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Start:

```bash
ai start
```

Update later:

```bash
ai update
```

## Build pipeline

GitHub Actions builds Linux amd64/arm64 and macOS amd64/arm64 binaries from `main` and commits them to:

```text
bin/ai-linux-amd64
bin/ai-linux-arm64
bin/ai-darwin-amd64
bin/ai-darwin-arm64
bin/checksums.txt
```

The workflow ignores changes under `bin/` when triggering, preventing its own generated commit from starting another build.

## Configuration

The installation directory is `~/.local/share/ai`. Put runtime configuration in:

```text
~/.local/share/ai/.env
~/.local/share/ai/.config/provider.json
```

For a terminal transport, set `AI_CLI_ENABLED=true`. Providers can then be added from the CLI with:

```text
/provider add <name> <adapter> <url> <api-key>
```

For Discord, set `DISCORD_BOT_TOKEN` and `DISCORD_OWNER_ID` in `.env`, then run `ai start`.
