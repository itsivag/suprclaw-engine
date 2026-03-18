---
sidebar_position: 5
---

# Matrix

## Setup

**1. Create a Matrix bot account**

Create a new account on any Matrix homeserver (e.g. matrix.org) or your own homeserver.

**2. Get an access token**

Log in with the bot account and retrieve the access token from the session settings.

**3. Configure**

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

**4. Start the gateway**

```bash
suprclaw gateway
```

The bot will connect and start receiving messages.

## Configuration Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | Yes | Enable/disable Matrix |
| `homeserver` | string | Yes | Matrix homeserver URL |
| `user_id` | string | Yes | Bot Matrix user ID (`@bot:server`) |
| `access_token` | string | Yes | Bot access token |
| `device_id` | string | No | Optional Matrix device ID |
| `join_on_invite` | bool | No | Auto-join invited rooms |
| `allow_from` | []string | No | Whitelist of Matrix user IDs |
| `group_trigger` | object | No | Group trigger strategy |
| `placeholder` | object | No | Placeholder message config |
| `reasoning_channel_id` | string | No | Channel for reasoning output |
| `message_format` | string | No | `"richtext"` (default) or `"plain"` |

## Message Format

| Value | Description |
|-------|-------------|
| `"richtext"` | Renders markdown as HTML (bold, italic, code blocks, etc.) |
| `"plain"` | Sends plain text only |

## Features

- Text message send/receive with markdown rendering
- Incoming image/audio/video/file download
- Incoming audio normalization (auto-transcription flow)
- Outgoing file upload and send
- Group trigger rules (mention-only mode)
- Typing state indicator
- Placeholder message + final reply replacement
- Auto-join invited rooms

## Mention-Only Mode

```json
{
  "channels": {
    "matrix": {
      "group_trigger": { "mention_only": true }
    }
  }
}
```
