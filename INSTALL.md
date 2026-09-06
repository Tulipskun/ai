# Install AI

## One command

Linux and macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/Tulipskun/ai/main/scripts/install.sh | bash
```

The installer:

1. installs the repository under `~/.local/share/ai`;
2. downloads Go 1.25 automatically when a compatible Go toolchain is not available;
3. builds the `ai` binary;
4. installs `~/.local/bin/ai` as the command;
5. creates `.config`, `.data`, `.ai`, and a starter `.env` without overwriting existing state.

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
