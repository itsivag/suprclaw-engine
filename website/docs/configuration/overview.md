---
sidebar_position: 1
---

# Configuration Overview

Config file: `~/.suprclaw/config.json`

## Config File Structure

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.suprclaw/workspace",
      "model_name": "claude-sonnet-4.6",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20,
      "restrict_to_workspace": true
    }
  },
  "model_list": [...],
  "channels": {...},
  "tools": {...},
  "gateway": {
    "host": "127.0.0.1",
    "port": 18790
  }
}
```

## Agent Defaults

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `workspace` | string | `~/.suprclaw/workspace` | Workspace directory |
| `model_name` | string | — | Active model (must match a `model_list` entry) |
| `max_tokens` | int | 8192 | Max tokens per response |
| `temperature` | float | 0.7 | Response temperature |
| `max_tool_iterations` | int | 20 | Max tool call rounds per session |
| `restrict_to_workspace` | bool | true | Sandbox agent to workspace |

## Workspace Layout

```
~/.suprclaw/workspace/
├── sessions/       # Conversation history
├── memory/         # Long-term memory (MEMORY.md)
├── state/          # Persistent state
├── cron/           # Scheduled jobs
├── skills/         # Custom skills
├── AGENTS.md       # Agent behavior guide
├── HEARTBEAT.md    # Periodic task prompts
├── IDENTITY.md     # Agent identity
├── SOUL.md         # Agent soul
└── USER.md         # User preferences
```

## Skill Sources

Skills are loaded in this order (later sources override earlier ones):

1. `~/.suprclaw/workspace/skills`
2. `~/.suprclaw/skills`
3. `<cwd>/skills`

## Gateway Config

The gateway serves webhook channels (Telegram, Discord, etc.):

```json
{
  "gateway": {
    "host": "127.0.0.1",
    "port": 18790
  }
}
```

Set `host` to `0.0.0.0` to expose to the network (e.g. when running in Docker).

## Environment Variables

See [Environment Variables Reference](../reference/env-vars) for the full list.

| Variable | Description |
|----------|-------------|
| `SUPRCLAW_CONFIG` | Path to config file |
| `SUPRCLAW_HOME` | Root directory for all suprclaw data |
| `SUPRCLAW_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE` | Override workspace restriction |
