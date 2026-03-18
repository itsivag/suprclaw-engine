---
sidebar_position: 3
---

# Tools Configuration

All tool configuration lives under the `tools` key in `config.json`.

```json
{
  "tools": {
    "web": { ... },
    "mcp": { ... },
    "exec": { ... },
    "cron": { ... },
    "skills": { ... }
  }
}
```

## Web Tools

### Web Fetcher

General settings for fetching and processing webpage content.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | true | Enable webpage fetching |
| `fetch_limit_bytes` | int | 10485760 | Max payload size (default 10MB) |
| `format` | string | `"plaintext"` | Output format: `plaintext` or `markdown` |

### Web Search Providers

SuprClaw picks the best available search provider automatically:

1. **Perplexity** — AI-powered with citations
2. **Brave Search** — $5/1000 queries
3. **SearXNG** — self-hosted, free
4. **DuckDuckGo** — default fallback, no key required

```json
{
  "tools": {
    "web": {
      "duckduckgo": { "enabled": true, "max_results": 5 },
      "brave": { "enabled": false, "api_key": "YOUR_KEY", "max_results": 5 },
      "perplexity": { "enabled": false, "api_key": "YOUR_KEY", "max_results": 5 },
      "searxng": { "enabled": false, "base_url": "http://your-server:8888", "max_results": 5 },
      "tavily": { "enabled": false, "api_key": "YOUR_KEY", "max_results": 5 }
    }
  }
}
```

#### DuckDuckGo

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | true | Enable DuckDuckGo (no key needed) |
| `max_results` | int | 5 | Maximum results |

#### Brave

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | false | Enable Brave Search |
| `api_key` | string | — | Brave Search API key |
| `max_results` | int | 5 | Maximum results |

#### Perplexity

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | false | Enable Perplexity |
| `api_key` | string | — | Perplexity API key |
| `max_results` | int | 5 | Maximum results |

## Exec Tool

Controls shell command execution by the agent.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enable_deny_patterns` | bool | true | Enable dangerous command blocking |
| `custom_deny_patterns` | array | `[]` | Custom deny regex patterns |

### Default Blocked Commands

- Delete: `rm -rf`, `del /f/q`, `rmdir /s`
- Disk: `format`, `mkfs`, `diskpart`, `dd if=`, `/dev/sd*`
- System: `shutdown`, `reboot`, `poweroff`
- Shell injection: `$()`, `${}`, backticks, `| sh`, `| bash`
- Privilege: `sudo`, `chmod`, `chown`
- Process: `pkill`, `killall`, `kill -9`
- Remote: `curl | sh`, `wget | sh`, `ssh`
- Package managers: `apt`, `yum`, `dnf`, `npm install -g`, `pip install --user`
- Containers: `docker run`, `docker exec`
- Git: `git push`, `git force`

:::warning
The exec guard only validates the top-level command. It does **not** recursively inspect child processes spawned by build tools (`make run`, `go run`, etc.). Use containers or VMs for untrusted code.
:::

```json
{
  "tools": {
    "exec": {
      "enable_deny_patterns": true,
      "custom_deny_patterns": [
        "\\brm\\s+-r\\b",
        "\\bkillall\\s+python"
      ]
    }
  }
}
```

## Cron Tool

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `exec_timeout_minutes` | int | 5 | Execution timeout (0 = no limit) |

## MCP Tool

See [Advanced → MCP](../advanced/mcp) for the full MCP configuration reference.

## Skills Tool

The skills tool configures skill discovery via registries like ClawHub.

```json
{
  "tools": {
    "skills": {
      "registries": {
        "clawhub": {
          "enabled": true,
          "base_url": "https://clawhub.ai",
          "auth_token": ""
        }
      }
    }
  }
}
```

| Config | Default | Description |
|--------|---------|-------------|
| `registries.clawhub.enabled` | true | Enable ClawHub registry |
| `registries.clawhub.base_url` | `https://clawhub.ai` | ClawHub URL |
| `registries.clawhub.auth_token` | `""` | Bearer token for higher rate limits |

## Environment Variable Overrides

All tool settings can be overridden via env vars: `SUPRCLAW_TOOLS_<SECTION>_<KEY>`

```bash
SUPRCLAW_TOOLS_WEB_BRAVE_ENABLED=true
SUPRCLAW_TOOLS_EXEC_ENABLE_DENY_PATTERNS=false
SUPRCLAW_TOOLS_CRON_EXEC_TIMEOUT_MINUTES=10
SUPRCLAW_TOOLS_MCP_ENABLED=true
```
