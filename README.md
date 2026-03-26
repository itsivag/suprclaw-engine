# SuprClaw

**Ultra-lightweight personal AI assistant written in Go.**

Runs on $10 hardware with <10MB RAM. Single binary, 1-second boot, works across x86_64, ARM64, MIPS, and RISC-V.

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go&logoColor=white)
![License](https://img.shields.io/badge/license-Elastic%20License%202.0-blue)

**[Documentation](https://itsivag.github.io/suprclaw-engine/)**

---

## Features

- **<10MB RAM** — 99% lighter than Electron-based alternatives
- **1s boot** — even on 0.6GHz single-core hardware
- **Single binary** — no runtime dependencies, drop-in deployment
- **Multi-arch** — x86_64, ARM64, MIPS, RISC-V
- **Self-hosted** — no telemetry, no tracking, all data stays local
- **Multi-channel** — Telegram, Discord, WhatsApp, Matrix, LINE, and more
- **Scheduled tasks** — built-in cron
- **Sandboxed** — agent restricted to workspace by default
- **Checkpoints** — git-like session + workspace rollback with full audit log

---

## Install

### One-liner

```sh
curl -fsSL https://raw.githubusercontent.com/itsivag/suprclaw-engine/main/install.sh | sh
```

Auto-detects OS and architecture (x86_64, ARM64, ARMv7, RISC-V, MIPS, LoongArch). Installs to `/usr/local/bin` or `~/.local/bin`.

### Precompiled binary

Download from the [releases](https://github.com/itsivag/suprclaw-engine/releases) page.

### From source

```bash
git clone https://github.com/itsivag/suprclaw-engine.git
cd suprclaw-engine
make deps
make build
```

Build targets:
```bash
make build-all          # All platforms
make build-linux-arm64  # ARM64
make build-linux-arm    # ARM 32-bit
make install            # Build and install to PATH
```

### Docker

```bash
# Clone
git clone https://github.com/itsivag/suprclaw-engine.git
cd suprclaw-engine

# First run — generates docker/data/config.json then exits
docker compose -f docker/docker-compose.yml --profile gateway up

# Edit config
vim docker/data/config.json

# Start
docker compose -f docker/docker-compose.yml --profile gateway up -d

# Logs
docker compose -f docker/docker-compose.yml logs -f suprclaw-gateway

# Stop
docker compose -f docker/docker-compose.yml --profile gateway down
```

> [!TIP]
> By default the Gateway listens on `127.0.0.1`. Set `SUPRCLAW_GATEWAY_HOST=0.0.0.0` to expose it to the host when running in Docker.

**Web console (launcher mode):**

```bash
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

Open http://localhost:18800. The launcher manages the gateway process automatically.

> [!WARNING]
> The web console has no authentication. Do not expose it to the public internet.

---

## Quick Start

**1. Initialize**

```bash
suprclaw onboard
```

**2. Configure** (`~/.suprclaw/config.json`)

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.suprclaw/workspace",
      "model_name": "claude-sonnet-4.6",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  },
  "model_list": [
    {
      "model_name": "claude-sonnet-4.6",
      "model": "anthropic/claude-sonnet-4.6",
      "api_key": "sk-ant-your-key"
    }
  ]
}
```

**3. Chat**

```bash
suprclaw agent -m "What is 2+2?"
suprclaw agent   # interactive mode
```

---

## Chat Channels

SuprClaw connects to messaging platforms via the gateway. Run `suprclaw gateway` to start.

> All webhook-based channels share a single HTTP server at `gateway.host:gateway.port` (default `127.0.0.1:18790`).

| Channel      | Difficulty                          |
| ------------ | ----------------------------------- |
| **Telegram** | Easy — just a bot token             |
| **Discord**  | Easy — bot token + message intent   |
| **WhatsApp** | Easy — QR scan (native) or bridge   |
| **Matrix**   | Medium — homeserver + access token  |
| **LINE**     | Medium — credentials + webhook URL  |

<details>
<summary><b>Telegram</b></summary>

1. Message `@BotFather` on Telegram → `/newbot` → copy the token
2. Get your user ID from `@userinfobot`

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allow_from": ["YOUR_USER_ID"]
    }
  }
}
```

</details>

<details>
<summary><b>Discord</b></summary>

1. Create an app at https://discord.com/developers/applications → Bot → copy token
2. Enable **MESSAGE CONTENT INTENT** in Bot settings
3. Get your user ID: Settings → Advanced → Developer Mode → right-click avatar → Copy User ID

```json
{
  "channels": {
    "discord": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allow_from": ["YOUR_USER_ID"]
    }
  }
}
```

Invite the bot via OAuth2 → URL Generator with scopes `bot` and permissions `Send Messages`, `Read Message History`.

Restrict to @-mentions only:
```json
{
  "channels": {
    "discord": {
      "group_trigger": { "mention_only": true }
    }
  }
}
```

</details>

<details>
<summary><b>WhatsApp</b></summary>

**Native (recommended)** — build with `-tags whatsapp_native`:

```bash
make build-whatsapp-native
```

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "use_native": true,
      "allow_from": []
    }
  }
}
```

On first run, scan the QR code with WhatsApp → Linked Devices. Session stored in `<workspace>/whatsapp/`.

**Bridge** — connect to an external WebSocket bridge:

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "bridge_url": "ws://localhost:3001"
    }
  }
}
```

</details>

<details>
<summary><b>Matrix</b></summary>

```json
{
  "channels": {
    "matrix": {
      "enabled": true,
      "homeserver": "https://matrix.org",
      "user_id": "@your-bot:matrix.org",
      "access_token": "YOUR_ACCESS_TOKEN",
      "allow_from": []
    }
  }
}
```

See [Matrix Channel Guide](docs/channels/matrix/README.md) for full options.

</details>

<details>
<summary><b>LINE</b></summary>

1. Create a Messaging API channel at https://developers.line.biz/
2. Copy **Channel Secret** and **Channel Access Token**
3. LINE requires HTTPS — use a reverse proxy or tunnel (e.g. `ngrok http 18790`)
4. Set webhook URL to `https://your-domain/webhook/line` in LINE Developers Console

```json
{
  "channels": {
    "line": {
      "enabled": true,
      "channel_secret": "YOUR_CHANNEL_SECRET",
      "channel_access_token": "YOUR_CHANNEL_ACCESS_TOKEN",
      "allow_from": []
    }
  }
}
```

</details>

---

## Configuration

Config file: `~/.suprclaw/config.json`

### Environment Variables

| Variable                                          | Description                              | Default                   |
| ------------------------------------------------- | ---------------------------------------- | ------------------------- |
| `SUPRCLAW_CONFIG`                                 | Path to config file                      | `~/.suprclaw/config.json` |
| `SUPRCLAW_HOME`                                   | Root directory for all suprclaw data     | `~/.suprclaw`             |
| `SUPRCLAW_BUILTIN_SKILLS`                         | Override builtin skills path             | —                         |
| `SUPRCLAW_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE`  | Restrict agent to workspace              | `true`                    |

### Workspace Layout

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

### Skill Sources

Skills are loaded in this order:

1. `~/.suprclaw/workspace/skills`
2. `~/.suprclaw/skills`
3. `<cwd>/skills`

### Security Sandbox

The agent is restricted to the workspace by default:

```json
{
  "agents": {
    "defaults": {
      "restrict_to_workspace": true
    }
  }
}
```

| Tool          | Restriction when enabled               |
| ------------- | -------------------------------------- |
| `read_file`   | Workspace only                         |
| `write_file`  | Workspace only                         |
| `list_dir`    | Workspace only                         |
| `edit_file`   | Workspace only                         |
| `exec`        | Command paths within workspace only    |

The `exec` tool always blocks dangerous commands regardless of workspace setting: `rm -rf`, `format`, `mkfs`, `dd if=`, `shutdown`, fork bombs, and direct disk writes.

> ⚠️ Set `"restrict_to_workspace": false` or `SUPRCLAW_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE=false` to allow full system access. Use with caution.

---

## Providers & Models

SuprClaw uses a `model_list` config — add any provider with just `api_base` + `api_key`, no code changes needed.

### Supported Providers

| Provider       | `model` prefix    | Default API Base                                   | Protocol  |
| -------------- | ----------------- | -------------------------------------------------- | --------- |
| OpenAI         | `openai/`         | `https://api.openai.com/v1`                        | OpenAI    |
| Anthropic      | `anthropic/`      | `https://api.anthropic.com/v1`                     | Anthropic |
| Google Gemini  | `gemini/`         | `https://generativelanguage.googleapis.com/v1beta` | OpenAI    |
| Groq           | `groq/`           | `https://api.groq.com/openai/v1`                   | OpenAI    |
| DeepSeek       | `deepseek/`       | `https://api.deepseek.com/v1`                      | OpenAI    |
| OpenRouter     | `openrouter/`     | `https://openrouter.ai/api/v1`                     | OpenAI    |
| Ollama         | `ollama/`         | `http://localhost:11434/v1`                        | OpenAI    |
| Azure OpenAI   | `azure/`          | `https://{resource}.openai.azure.com`              | Azure     |
| Cerebras       | `cerebras/`       | `https://api.cerebras.ai/v1`                       | OpenAI    |
| LiteLLM Proxy  | `litellm/`        | `http://localhost:4000/v1`                         | OpenAI    |
| vLLM           | `vllm/`           | `http://localhost:8000/v1`                         | OpenAI    |
| NVIDIA         | `nvidia/`         | `https://integrate.api.nvidia.com/v1`              | OpenAI    |
| GitHub Copilot | `github-copilot/` | `localhost:4321`                                   | gRPC      |

> Groq also provides free **voice transcription** via Whisper — configure it once and audio messages from any channel are auto-transcribed.

### Example Configurations

**Anthropic:**
```json
{
  "model_list": [
    {
      "model_name": "claude-sonnet-4.6",
      "model": "anthropic/claude-sonnet-4.6",
      "api_key": "sk-ant-your-key"
    }
  ],
  "agents": { "defaults": { "model_name": "claude-sonnet-4.6" } }
}
```

**OpenAI:**
```json
{
  "model_list": [
    {
      "model_name": "gpt-4o",
      "model": "openai/gpt-4o",
      "api_key": "sk-your-key"
    }
  ]
}
```

**Ollama (local):**
```json
{
  "model_list": [
    {
      "model_name": "llama3",
      "model": "ollama/llama3"
    }
  ]
}
```

**OpenRouter (access to all models):**
```json
{
  "model_list": [
    {
      "model_name": "claude-sonnet-4.6",
      "model": "openrouter/anthropic/claude-sonnet-4.6",
      "api_key": "sk-or-v1-your-key"
    }
  ]
}
```

**Load balancing (round-robin across endpoints):**
```json
{
  "model_list": [
    { "model_name": "gpt-4o", "model": "openai/gpt-4o", "api_base": "https://api1.example.com/v1", "api_key": "sk-key1" },
    { "model_name": "gpt-4o", "model": "openai/gpt-4o", "api_base": "https://api2.example.com/v1", "api_key": "sk-key2" }
  ]
}
```

### Web Search

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
      "tavily": { "enabled": false, "api_key": "YOUR_KEY", "max_results": 5 },
      "perplexity": { "enabled": false, "api_key": "YOUR_KEY", "max_results": 5 },
      "searxng": { "enabled": false, "base_url": "http://your-server:8888", "max_results": 5 }
    }
  }
}
```

---

## CLI Reference

| Command                   | Description                   |
| ------------------------- | ----------------------------- |
| `suprclaw onboard`        | Initialize config & workspace |
| `suprclaw agent -m "..."` | Send a one-shot message       |
| `suprclaw agent`          | Interactive chat mode         |
| `suprclaw gateway`        | Start the gateway             |
| `suprclaw status`         | Show status                   |
| `suprclaw cron list`      | List scheduled jobs           |
| `suprclaw cron add ...`   | Add a scheduled job           |

### Scheduled Tasks

```bash
# Natural language scheduling via agent
suprclaw agent -m "Remind me every day at 9am to check email"
suprclaw agent -m "Remind me in 30 minutes"
```

Jobs are stored in `~/.suprclaw/workspace/cron/` and run automatically.

## Troubleshooting

**Web search says "API key configuration issue"**

Expected if no search API key is configured. DuckDuckGo is the built-in fallback and requires no key.

**Telegram: "Conflict: terminated by other getUpdates"**

Another instance is running. Only one `suprclaw gateway` should run at a time.

**Content filtering errors**

Some providers filter certain content. Try rephrasing or switch to a different model/provider.

**Agent accessing files outside workspace**

Check `restrict_to_workspace` in your config. Set to `true` to re-enable the sandbox.

---

## Contributing

PRs welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) and [ROADMAP.md](ROADMAP.md).

---

## License

Elastic License 2.0 — see [LICENSE](LICENSE).
