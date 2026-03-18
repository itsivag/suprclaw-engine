---
sidebar_position: 6
---

# LINE

:::note
LINE requires HTTPS for webhooks. Use a reverse proxy or tunnel (e.g. ngrok).
:::

## Setup

**1. Create a Messaging API channel**

1. Go to https://developers.line.biz/
2. Create a provider and a **Messaging API** channel
3. Copy the **Channel Secret** and **Channel Access Token**

**2. Set up HTTPS**

LINE webhooks require HTTPS. Options:
- Reverse proxy (nginx + Let's Encrypt)
- Cloudflare Tunnel
- ngrok: `ngrok http 18790`

**3. Set the webhook URL**

In LINE Developers Console → Messaging API → Webhook URL:

```
https://your-domain/webhook/line
```

**4. Configure**

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

**5. Start the gateway**

```bash
suprclaw gateway
```

## Configuration Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | Yes | Enable/disable LINE |
| `channel_secret` | string | Yes | LINE Channel Secret |
| `channel_access_token` | string | Yes | LINE Channel Access Token |
| `allow_from` | []string | No | Whitelist of LINE user IDs |
