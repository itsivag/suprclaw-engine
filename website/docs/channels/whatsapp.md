---
sidebar_position: 4
---

# WhatsApp

Two modes are supported: **Native** (recommended) and **Bridge**.

## Native Mode (Recommended)

Native mode connects directly to WhatsApp using the WhatsApp Web protocol. No third-party bridge needed.

**1. Build with native WhatsApp support**

```bash
make build-whatsapp-native
```

Or download a `*-whatsapp` binary from the [releases page](https://github.com/itsivag/suprclaw-engine/releases).

**2. Configure**

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

**3. Scan the QR code**

On first run, a QR code will be displayed in the terminal. Scan it with WhatsApp:

**WhatsApp → Settings → Linked Devices → Link a Device**

The session is stored in `<workspace>/whatsapp/` and persists across restarts.

## Bridge Mode

Connect to an external WebSocket bridge:

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

## Configuration Reference

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | bool | Enable/disable WhatsApp |
| `use_native` | bool | Use native WhatsApp Web protocol |
| `bridge_url` | string | WebSocket bridge URL (bridge mode) |
| `allow_from` | []string | Whitelist of phone numbers (empty = all) |
