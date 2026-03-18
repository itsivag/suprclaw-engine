---
sidebar_position: 2
---

# Telegram

## Setup

**1. Create a bot**

Message `@BotFather` on Telegram:
- Send `/newbot`
- Follow the prompts
- Copy the **bot token**

**2. Get your user ID**

Message `@userinfobot` on Telegram to get your numeric user ID.

**3. Add to config**

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

**4. Start the gateway**

```bash
suprclaw gateway
```

## Configuration Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | Yes | Enable/disable Telegram |
| `token` | string | Yes | Bot token from @BotFather |
| `allow_from` | []string | No | Whitelist of Telegram user IDs |
| `group_trigger` | object | No | Group chat trigger strategy |
| `placeholder` | object | No | Placeholder message config |

## Group Chats

By default the bot responds to all messages in groups it's added to. To respond only when mentioned:

```json
{
  "channels": {
    "telegram": {
      "group_trigger": {
        "mention_only": true
      }
    }
  }
}
```

## Troubleshooting

**"Conflict: terminated by other getUpdates"**

Another instance is running. Only one `suprclaw gateway` should run at a time. Find and stop the other instance.
