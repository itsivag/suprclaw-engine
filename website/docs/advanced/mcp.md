---
sidebar_position: 1
---

# MCP (Model Context Protocol)

SuprClaw supports the [Model Context Protocol](https://modelcontextprotocol.io), allowing integration with external tool servers.

## Basic Configuration

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "my-server": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
        }
      }
    }
  }
}
```

## Global Config

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | false | Enable MCP integration |
| `discovery` | object | `{}` | Tool Discovery configuration |
| `servers` | object | `{}` | Map of server name → server config |

## Per-Server Config

| Config | Type | Required | Description |
|--------|------|----------|-------------|
| `enabled` | bool | Yes | Enable this server |
| `type` | string | Auto | Transport: `stdio`, `sse`, `http` |
| `command` | string | stdio | Executable for stdio transport |
| `args` | array | No | Command arguments |
| `env` | object | No | Environment variables for the process |
| `env_file` | string | No | Path to an env file |
| `url` | string | sse/http | Endpoint URL for `sse`/`http` |
| `headers` | object | No | HTTP headers for `sse`/`http` |

**Transport auto-detection:**
- `url` set → `sse`
- `command` set → `stdio`

## Tool Discovery (Lazy Loading)

When connecting to many MCP servers with hundreds of tools, Tool Discovery keeps tools *hidden* by default and uses BM25 keyword search or regex to unlock them on demand.

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "discovery": {
        "enabled": true,
        "ttl": 5,
        "max_search_results": 5,
        "use_bm25": true,
        "use_regex": false
      }
    }
  }
}
```

### Discovery Config

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | false | Enable lazy tool loading |
| `ttl` | int | 5 | Turns a discovered tool stays unlocked |
| `max_search_results` | int | 5 | Max tools returned per search |
| `use_bm25` | bool | true | Natural language keyword search |
| `use_regex` | bool | false | Regex pattern search |

:::warning
If `discovery.enabled` is `true`, you must enable at least one search engine (`use_bm25` or `use_regex`).
:::

## Examples

### Filesystem (stdio)

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "filesystem": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
        }
      }
    }
  }
}
```

### Remote SSE/HTTP Server

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "remote-mcp": {
          "enabled": true,
          "type": "sse",
          "url": "https://example.com/mcp",
          "headers": {
            "Authorization": "Bearer YOUR_TOKEN"
          }
        }
      }
    }
  }
}
```

### Large Setup with Tool Discovery

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "discovery": {
        "enabled": true,
        "ttl": 5,
        "max_search_results": 5,
        "use_bm25": true
      },
      "servers": {
        "github": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-github"],
          "env": { "GITHUB_PERSONAL_ACCESS_TOKEN": "YOUR_TOKEN" }
        },
        "postgres": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-postgres", "postgresql://user:pass@localhost/db"]
        },
        "slack": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-slack"],
          "env": {
            "SLACK_BOT_TOKEN": "YOUR_TOKEN",
            "SLACK_TEAM_ID": "YOUR_TEAM_ID"
          }
        }
      }
    }
  }
}
```

In this setup, the LLM only sees `tool_search_tool_bm25`. It searches and unlocks GitHub or Postgres tools dynamically only when needed.
