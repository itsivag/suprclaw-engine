# SuprClaw Browser Relay Extension

Chrome-first MV3 extension for the `browser-relay` subsystem.

## Load Unpacked

1. Open `chrome://extensions`.
2. Enable **Developer mode**.
3. Click **Load unpacked** and select this `web/extension` directory.
4. Open extension **Options** and set relay URL (for cloud: `wss://api.suprclaw.com/browser-relay/extension`).
5. Click **Google Sign-In** (uses `https://auth.suprclaw.com`).
6. Click **Auto Setup** to fetch and save relay token automatically.

## Mobile Pairing (QR)

1. Open extension popup.
2. Click **Pair Mobile (QR)**.
3. Scan QR in your mobile app and claim via `POST /api/browser-relay/pairing/claim?code=<CODE>`.
4. Mobile client stores `session_token` + `refresh_token` + expiry fields from claim response.

## Local Smoke

1. Start web backend (`web/backend`) so relay endpoints are available.
2. Open extension **Options** and set local relay URL.
3. Click **Google Sign-In** (cloud) or provide a token manually for local dev.
4. Click **Auto Setup**.
5. Verify targets: `suprclaw browser-relay targets`.

## Test

```bash
cd web/extension
npm test
```
