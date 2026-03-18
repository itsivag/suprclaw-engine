---
sidebar_position: 1
---

# Chat Channels Overview

SuprClaw connects to messaging platforms via the **gateway**. Start it with:

```bash
suprclaw gateway
```

All webhook-based channels share a single HTTP server at `gateway.host:gateway.port` (default `127.0.0.1:18790`).

## Available Channels

| Channel | Difficulty | Notes |
|---------|-----------|-------|
| [**Telegram**](./telegram) | Easy | Just a bot token |
| [**Discord**](./discord) | Easy | Bot token + message intent |
| [**WhatsApp**](./whatsapp) | Easy | QR scan (native) or bridge |
| [**Matrix**](./matrix) | Medium | Homeserver + access token |
| [**LINE**](./line) | Medium | Credentials + webhook URL (needs HTTPS) |

## Channel Config Structure

All channels go under the `channels` key in `config.json`:

```json
{
  "channels": {
    "telegram": { ... },
    "discord": { ... },
    "whatsapp": { ... },
    "matrix": { ... },
    "line": { ... }
  }
}
```

## Common Channel Options

Most channels support these fields:

| Field | Description |
|-------|-------------|
| `enabled` | Enable/disable the channel |
| `allow_from` | Whitelist of user IDs that can interact with the bot |
| `group_trigger` | Control how the bot responds in group chats |
| `placeholder` | Show a "Thinking..." message while the agent works |

### Allow List

Restrict who can use the bot:

```json
{
  "channels": {
    "telegram": {
      "allow_from": ["123456789", "987654321"]
    }
  }
}
```

Leave as `[]` to allow everyone (not recommended for public bots).

### Group Trigger

Control responses in group chats:

```json
{
  "channels": {
    "discord": {
      "group_trigger": {
        "mention_only": true
      }
    }
  }
}
```

## Webhook Setup

Webhook channels (Telegram, LINE) require the gateway to be reachable from the internet.

**Options:**
- Reverse proxy (nginx, Caddy)
- Tunnel (ngrok, Cloudflare Tunnel)
- Public VPS

Telegram also supports **polling** mode — no public URL needed.
