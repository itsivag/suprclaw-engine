# Surclaw Browser Connect Extension

Chrome-first MV3 extension for the `agent-browser` MCP transport flow.

## Load Unpacked

1. Open `chrome://extensions`.
2. Enable **Developer mode**.
3. Click **Load unpacked** and select this `web/extension` directory.
4. Click the extension icon to open the **side panel**.
5. Open the top-right menu and choose **Manual Setup**.
6. Set relay URL (for cloud: `wss://api.suprclaw.com/agent-browser/extension`).
7. Click **Google Sign-In** (uses `https://auth.suprclaw.com`); auto setup runs automatically after successful auth.

## Mobile Pairing (QR)

1. Click the extension icon to open the side panel.
2. If the relay is not paired, a QR card is shown automatically.
3. Click **Refresh QR** if you need a new code.
3. Scan QR in your mobile app and claim via `POST /api/agent-browser/pairing/claim?code=<CODE>`.
4. Mobile client stores `session_token` + `refresh_token` + expiry fields from claim response.

## Local Smoke

1. Start web backend (`web/backend`) so relay endpoints are available.
2. Open extension side panel, then open **Manual Setup** from the top-right menu and set local relay URL.
3. Click **Google Sign-In** (cloud) or provide a token manually for local dev.
4. If using Google auth, auto setup runs after sign-in. For manual token mode, click **Save**.
5. Verify targets via MCP (`agent_browser_targets_list`).

## Test

```bash
cd web/extension
npm test
```
