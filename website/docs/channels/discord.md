---
sidebar_position: 3
---

# Discord

## Setup

**1. Create a bot**

1. Go to https://discord.com/developers/applications
2. Click **New Application** → give it a name
3. Go to **Bot** → click **Reset Token** → copy the token
4. Under **Privileged Gateway Intents**, enable **MESSAGE CONTENT INTENT**

**2. Get your user ID**

Settings → Advanced → Enable **Developer Mode** → Right-click your avatar → **Copy User ID**

**3. Add to config**

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

**4. Invite the bot to your server**

In the Developer Portal: OAuth2 → URL Generator

- Scopes: `bot`
- Permissions: `Send Messages`, `Read Message History`

Copy and open the generated URL to invite the bot.

**5. Start the gateway**

```bash
suprclaw gateway
```

## Configuration Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | Yes | Enable/disable Discord |
| `token` | string | Yes | Bot token |
| `allow_from` | []string | No | Whitelist of Discord user IDs |
| `group_trigger` | object | No | Group trigger strategy |
| `placeholder` | object | No | Placeholder message config |

## Mention-Only Mode

Respond only when the bot is @-mentioned in a channel:

```json
{
  "channels": {
    "discord": {
      "group_trigger": { "mention_only": true }
    }
  }
}
```
