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
    "port": 18790,
    "remote_admin_control": false,
    "admin_secret": ""
  }
}
```

Set `host` to `0.0.0.0` to expose to the network (e.g. when running in Docker).

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `host` | string | `127.0.0.1` | Listen host |
| `port` | int | `18790` | Listen port |
| `remote_admin_control` | bool | `false` | Enable the embedded Admin REST API |
| `admin_secret` | string | — | Bearer token required to call admin endpoints |

## Admin REST API

When `remote_admin_control` is `true` and `admin_secret` is set, the gateway exposes a REST API on the same host/port under `/api/admin/`. All requests must include `Authorization: Bearer <admin_secret>`.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/admin/cron/jobs` | List all scheduled jobs |
| `POST` | `/api/admin/cron/jobs` | Add a scheduled job |
| `DELETE` | `/api/admin/cron/jobs/{id}` | Remove a job |
| `PATCH` | `/api/admin/cron/jobs/{id}` | Enable/disable a job |
| `GET` | `/api/admin/config` | Read current config |
| `PUT` | `/api/admin/config` | Replace entire config |
| `PATCH` | `/api/admin/config` | Partial config update |
| `POST` | `/api/admin/agents` | Upsert an agent definition |
| `DELETE` | `/api/admin/agents/{agentId}` | Remove an agent |
| `POST` | `/api/admin/agents/{agentId}/wake` | Run a one-shot agent message |
| `POST` | `/api/admin/runtime/reload` | Restart the gateway process |
| `POST` | `/api/admin/workspaces/bootstrap` | Create/populate an agent workspace |
| `DELETE` | `/api/admin/workspaces/{agentId}` | Delete an agent workspace |
| `GET` | `/api/admin/workspaces/{agentId}/files` | List workspace files |
| `GET` | `/api/admin/workspaces/{agentId}/files/{fileName}` | Read a workspace file |
| `POST` | `/api/admin/marketplace/install` | Sparse-clone a skill repo into a workspace |
| `POST` | `/api/admin/mcp/configure` | Set MCP server config |

### Example: add a cron job

```bash
curl -X POST http://localhost:18790/api/admin/cron/jobs \
  -H "Authorization: Bearer <admin_secret>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "daily-digest",
    "message": "Send me the daily news summary",
    "deliver": true,
    "channel": "supr",
    "to": "supr:my-session",
    "schedule": { "cron": "0 9 * * *" }
  }'
```

### Example: wake an agent

```bash
curl -X POST http://localhost:18790/api/admin/agents/my-agent/wake \
  -H "Authorization: Bearer <admin_secret>" \
  -H "Content-Type: application/json" \
  -d '{ "sessionKey": "my-session", "message": "Good morning!" }'
```

## Environment Variables

See [Environment Variables Reference](../reference/env-vars) for the full list.

| Variable | Description |
|----------|-------------|
| `SUPRCLAW_CONFIG` | Path to config file |
| `SUPRCLAW_HOME` | Root directory for all suprclaw data |
| `SUPRCLAW_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE` | Override workspace restriction |
