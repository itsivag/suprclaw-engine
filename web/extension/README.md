# SuprClaw Browser Relay Extension

Chrome-first MV3 extension for the `browser-relay` subsystem.

## Load Unpacked

1. Open `chrome://extensions`.
2. Enable **Developer mode**.
3. Click **Load unpacked** and select this `web/extension` directory.
4. Open extension **Options** and set relay URL (for cloud: `wss://api.suprclaw.com/browser-relay/extension`).
5. Click **Auto Setup** to fetch and save relay token automatically.

## Mobile Pairing (QR)

1. Open extension popup.
2. Click **Connect Relay**.
3. Attach a normal web tab.
4. Click **Pair Mobile (QR)**.
5. Scan QR in your mobile app and claim via `POST /api/browser-relay/pairing/claim?code=<CODE>`.
6. Mobile client stores `session_token` + `refresh_token` + expiry fields from claim response.
7. Use **Hard Stop** in popup to explicitly terminate the sticky relay lease and revoke session tokens.

## Local Smoke

1. Start web backend (`web/backend`) so relay endpoints are available.
2. Open extension **Options** and set local relay URL.
3. Click **Auto Setup**.
4. Connect relay, then attach active tab from popup.
5. Verify targets: `suprclaw browser-relay targets`.

## Test

```bash
cd web/extension
npm test
```
