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
curl -sS -X POST http://127.0.0.1:18800/api/browser-relay/actions \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "request_id":"nav-1",
    "target":"<target_id>",
    "action":"navigate",
    "args":{"url":"https://example.com"}
  }'

curl -sS -X POST http://127.0.0.1:18800/api/browser-relay/actions \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "request_id":"shot-1",
    "target":"<target_id>",
    "action":"screenshot",
    "args":{}
  }'
```

### Batch execution (preferred for multi-step flows)

Use the V2 batch envelope to reduce round-trips and prevent command interleaving on the same tab:

```bash
curl -sS -X POST http://127.0.0.1:18800/api/browser-relay/actions \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "request_id":"batch-1",
    "target":"<target_id>",
    "steps":[
      {"action":"navigate","args":{"url":"https://example.com"}},
      {"action":"wait","args":{"wait_mode":"navigation","timeout_ms":15000}},
      {"action":"snapshot","args":{}}
    ],
    "execution_policy":{"stop_on_error":true}
  }'
```

Every response uses a shared envelope:
- `request_id`, `ok`, `result`, `error_code`, `error_message`, `retry_class`, `trace_id`, `timing`
- Batch details are returned under `result`.

### Snapshot refs for token-efficient follow-up

`snapshot` now returns optional `refs` and `ref_generation` fields when extraction succeeds.
Follow-up selector actions can use ref IDs such as `@e1`:

```bash
curl -sS -X POST http://127.0.0.1:18800/api/browser-relay/actions \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "request_id":"click-1",
    "target":"<target_id>",
    "action":"click",
    "args":{"selector":"@e1","ref_generation":"<generation>"}
  }'
```

If a ref is stale/missing, the relay returns `snapshot_ref_not_found`.

8. Run dedicated session actions (agent-browser path):

```bash
curl -sS -X POST http://127.0.0.1:18800/api/browser-relay/actions \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "request_id":"sess-create-1",
    "action":"session.create",
    "args":{"url":"https://example.com"}
  }'

curl -sS -X POST http://127.0.0.1:18800/api/browser-relay/actions \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "request_id":"sess-list-1",
    "action":"session.list",
    "args":{}
  }'
```

9. Detach tab in popup and reconnect extension to confirm ownership cleanup and session recovery.

## Retry guidance for agents

- Prefer `batch` for multi-step interactions.
- Retry only retriable transport failures (`relay request timed out`, transient websocket issues).
- Do not blindly retry on:
  - `relay_loop_guard_triggered`
  - `snapshot_ref_not_found`
  - ownership/attach conflicts (`target not attached`, `target already controlled`)
