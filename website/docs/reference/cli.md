---
sidebar_position: 1
---

# CLI Reference

## Commands

| Command | Description |
|---------|-------------|
| `suprclaw onboard` | Initialize config & workspace |
| `suprclaw agent -m "..."` | Send a one-shot message |
| `suprclaw agent` | Interactive chat mode |
| `suprclaw gateway` | Start the gateway |
| `suprclaw status` | Show status |
| `suprclaw cron list` | List scheduled jobs |
| `suprclaw cron add ...` | Add a scheduled job |
| `suprclaw auth login --provider <name>` | Authenticate with a provider |
| `suprclaw auth models` | List available models (Antigravity) |

## Global Flags

| Flag | Description |
|------|-------------|
| `--config <path>` | Path to config file (overrides `SUPRCLAW_CONFIG`) |
| `--version` | Show version |

## `suprclaw agent`

```bash
suprclaw agent [flags]
```

| Flag | Description |
|------|-------------|
| `-m, --message <text>` | One-shot message |
| `--model <name>` | Override model for this command |

**Examples:**

```bash
# One-shot
suprclaw agent -m "Summarize the README"

# Interactive
suprclaw agent

# Override model
suprclaw agent -m "Hello" --model claude-opus-4-6-thinking
```

## `suprclaw gateway`

Starts the gateway server and all configured chat channels.

```bash
suprclaw gateway
```

All channels (Telegram, Discord, etc.) connect automatically based on `config.json`.

## `suprclaw onboard`

Initializes SuprClaw:
- Creates `~/.suprclaw/config.json` with default values
- Creates the workspace at `~/.suprclaw/workspace/`
- Generates `~/.ssh/suprclaw_ed25519.key` (for credential encryption)
- Encrypts any existing plaintext API keys in config

```bash
suprclaw onboard
```

## `suprclaw auth`

```bash
suprclaw auth login --provider <name>   # Authenticate with a provider
suprclaw auth models                    # List available models
```

Supported providers with OAuth: `antigravity`, `github-copilot`

## `suprclaw cron`

```bash
suprclaw cron list          # List all scheduled jobs
suprclaw cron add <spec>    # Add a scheduled job
```

You can also create scheduled tasks conversationally:

```bash
suprclaw agent -m "Remind me every day at 9am to check email"
```

## `suprclaw status`

Shows the current status of the gateway and active channels.
