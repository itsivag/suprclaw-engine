# Browser Relay Smoke Flow

## Goal
Validate end-to-end relay behavior with the custom Chrome extension.

## Steps

1. Start launcher/backend (default `http://127.0.0.1:18800`).
2. Run setup:

```bash
suprclaw browser-relay setup
```

3. Load unpacked extension from `web/extension`.
4. Open extension options and paste:
- `extension_ws_url`
- `token`
5. Connect relay from popup and attach the active tab.
6. Verify runtime targets:

```bash
suprclaw browser-relay targets
```

`tabs.list` now returns mixed target sources:
- `ext:<tabId>` for extension-controlled active tabs
- `ab:<sessionId>:main` for dedicated `agent-browser` sessions

7. Run sample actions against an attached `target_id`:

```bash
curl -sS -X POST http://127.0.0.1:18800/api/browser-relay/actions/navigate \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"target_id":"<target_id>","url":"https://example.com"}'

curl -sS -X POST http://127.0.0.1:18800/api/browser-relay/actions/screenshot \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"target_id":"<target_id>"}'
```

8. Run dedicated session actions (agent-browser path):

```bash
curl -sS -X POST http://127.0.0.1:18800/api/browser-relay/actions/session.create \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}'

curl -sS -X POST http://127.0.0.1:18800/api/browser-relay/actions/session.list \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{}'
```

9. Detach tab in popup and reconnect extension to confirm ownership cleanup and session recovery.
