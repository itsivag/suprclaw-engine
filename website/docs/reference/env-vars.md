---
sidebar_position: 2
---

# Environment Variables

## Core

| Variable | Default | Description |
|----------|---------|-------------|
| `SUPRCLAW_CONFIG` | `~/.suprclaw/config.json` | Path to config file |
| `SUPRCLAW_HOME` | `~/.suprclaw` | Root directory for all suprclaw data |
| `SUPRCLAW_BUILTIN_SKILLS` | — | Override builtin skills path |

## Agent

| Variable | Default | Description |
|----------|---------|-------------|
| `SUPRCLAW_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE` | `true` | Restrict agent to workspace |
| `SUPRCLAW_AGENTS_DEFAULTS_MODEL` | — | Override default model name |

## Gateway

| Variable | Default | Description |
|----------|---------|-------------|
| `SUPRCLAW_GATEWAY_HOST` | `127.0.0.1` | Gateway listen host |
| `SUPRCLAW_GATEWAY_PORT` | `18790` | Gateway listen port |

## Credential Encryption

| Variable | Required | Description |
|----------|----------|-------------|
| `SUPRCLAW_KEY_PASSPHRASE` | Yes (for `enc://`) | Passphrase for AES-256-GCM decryption |
| `SUPRCLAW_SSH_KEY_PATH` | No | Path to SSH private key (auto-detected if not set) |

## Tools

All tool settings can be overridden: `SUPRCLAW_TOOLS_<SECTION>_<KEY>`

| Variable | Description |
|----------|-------------|
| `SUPRCLAW_TOOLS_WEB_BRAVE_ENABLED` | Enable Brave Search |
| `SUPRCLAW_TOOLS_WEB_DUCKDUCKGO_ENABLED` | Enable DuckDuckGo |
| `SUPRCLAW_TOOLS_EXEC_ENABLE_DENY_PATTERNS` | Enable exec deny patterns |
| `SUPRCLAW_TOOLS_CRON_EXEC_TIMEOUT_MINUTES` | Cron execution timeout |
| `SUPRCLAW_TOOLS_MCP_ENABLED` | Enable MCP integration |

## Format

Environment variables follow the pattern: `SUPRCLAW_` + config path in uppercase with underscores.

Examples:
```bash
# Equivalent to: agents.defaults.restrict_to_workspace = false
export SUPRCLAW_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE=false

# Equivalent to: gateway.host = "0.0.0.0"
export SUPRCLAW_GATEWAY_HOST=0.0.0.0
```
