# SuprClaw Browser Relay Extension

Chrome-first MV3 extension for the `browser-relay` subsystem.

## Load Unpacked

1. Open `chrome://extensions`.
2. Enable **Developer mode**.
3. Click **Load unpacked** and select this `web/extension` directory.
4. Open extension **Options** and set relay URL/token from:
   - `suprclaw browser-relay setup`

## Local Smoke

1. Start web backend (`web/backend`) so relay endpoints are available.
2. Run: `suprclaw browser-relay setup`
3. Configure Options with returned `extension_ws_url` + `token`.
4. Connect relay, then attach active tab from popup.
5. Verify targets: `suprclaw browser-relay targets`.

## Test

```bash
cd web/extension
npm test
```
