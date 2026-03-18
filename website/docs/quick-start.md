---
sidebar_position: 3
---

# Quick Start

## 1. Initialize

```bash
suprclaw onboard
```

This creates `~/.suprclaw/config.json` and the workspace at `~/.suprclaw/workspace/`.

## 2. Configure

Edit `~/.suprclaw/config.json` and add your AI provider:

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

See [Providers](./configuration/providers) for all supported providers.

## 3. Chat

**One-shot message:**
```bash
suprclaw agent -m "What is 2+2?"
```

**Interactive mode:**
```bash
suprclaw agent
```

**Specify a model:**
```bash
suprclaw agent -m "Hello" --model claude-opus-4-6-thinking
```

## 4. Start the Gateway (Optional)

To use chat channels (Telegram, Discord, etc.):

```bash
suprclaw gateway
```

See [Chat Channels](./channels/overview) for setup instructions.

## What's Next?

- **Connect a chat channel** — [Telegram](./channels/telegram), [Discord](./channels/discord), [WhatsApp](./channels/whatsapp)
- **Add more providers** — [Configuration → Providers](./configuration/providers)
- **Configure tools** — [Configuration → Tools](./configuration/tools)
- **Schedule tasks** — [Advanced → Scheduled Tasks](./advanced/scheduled-tasks)
