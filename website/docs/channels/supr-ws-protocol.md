---
sidebar_position: 2
---

# Supr WebSocket Protocol

This page documents the WebSocket protocol used by SuprClaw web chat (`supr` channel).

Source of truth in code:

- `pkg/channels/supr/`
- `pkg/bus/types.go`
- `pkg/agent/activity_events.go`
- `web/backend/api/supr.go`
- `web/frontend/src/features/chat/protocol.ts`

Note: some frontend symbols still use legacy `pico` naming, but the actual protocol/channel is `supr`.

## Endpoint and handshake

- WebSocket endpoint: `GET /supr/ws`
- Query:
  - `session_id` (optional; server generates one if omitted)

Example:

```text
ws://<host>:<port>/supr/ws?session_id=sess-123
```

Authentication (any one succeeds):

1. `Authorization: Bearer <token>`
2. `Sec-WebSocket-Protocol: token.<token>`
3. `?token=<token>` only when `channels.supr.allow_token_query=true`

## Connection behavior

- One active connection per `session_id` (new connection replaces old one).
- First frame sent by server is `agent.list`.
- Server sends WS ping frames periodically.
- Protocol-level `ping` / `pong` JSON messages also exist.

## Wire envelopes

## 1) Typed frame (`SuprMessage`)

```json
{
  "type": "string",
  "id": "optional string",
  "session_id": "optional string",
  "timestamp": 1710000000000,
  "payload": {}
}
```

## 2) Canonical activity envelope (`ActivityEventEnvelope`)

```json
{
  "v": "1.0",
  "event_id": "evt_xxx",
  "event_type": "run.started",
  "timestamp": "2026-03-30T10:12:41.203Z",
  "sequence": 1,
  "session_id": "sess-123",
  "run_id": "run_xxx",
  "parent_run_id": null,
  "agent_id": "optional",
  "trace_id": "optional",
  "span_id": "optional",
  "idempotency_key": "run_xxx_1",
  "replay": false,
  "data": {}
}
```

`agent_id` is the actual responding agent. For canonical activity events,
`data.agent_id` mirrors top-level `agent_id` and both must match.

## Client -> server messages

## `message.send`

Required:

- `payload.content: string` (non-empty)

Optional:

- `payload.agent_id: string`
- `payload.model: string`
- `payload.reasoning: "off" | "low" | "medium" | "high" | "xhigh" | "adaptive"`

Example:

```json
{
  "type": "message.send",
  "id": "msg-1",
  "payload": {
    "content": "Summarize this log",
    "agent_id": "main",
    "reasoning": "high"
  }
}
```

## `media.send`

Two modes:

1. Scalar fields (`data` or `url`)
2. `attachments` array

Attachment item fields:

- `data?: string` (base64)
- `url?: string`
- `filename?: string`
- `content_type?: string`
- `caption?: string`

Rules:

- Each item must include `data` or `url`.
- Max decoded media size per item: 25 MB.
- Optional `agent_id`, `model`, `reasoning` are also supported.

## `ping`

Server replies with typed `pong` and mirrors `id` when present.

## `run.stop`

Requests cancellation of the active run for the session.

Payload fields:

- `run_id?: string`
- `reason?: string`

Validation:

- If there is no active run: typed `error` with `code: "no_active_run"`.
- If `run_id` is provided and does not match the active run: typed `error` with `code: "run_mismatch"`.
- On success, no typed ack is sent; cancellation outcome is reported via canonical events.

Example:

```json
{
  "type": "run.stop",
  "payload": {
    "run_id": "run_abc123",
    "reason": "Stopped by user."
  }
}
```

## Server -> client messages

## Typed frames

- `agent.list` (sent on connect)
- `pong`
- `error`
- `media.create`

### `agent.list` example

```json
{
  "type": "agent.list",
  "timestamp": 1710000000000,
  "payload": {
    "agents": [{ "id": "main", "name": "Main Agent" }],
    "default": "main"
  }
}
```

### `error` example

```json
{
  "type": "error",
  "timestamp": 1710000000000,
  "payload": {
    "code": "invalid_reasoning",
    "message": "invalid reasoning \"ultra\". Allowed: off|low|medium|high|xhigh|adaptive"
  }
}
```

Common protocol error codes:

- `invalid_message`
- `unknown_type`
- `empty_content`
- `invalid_reasoning`
- `no_active_run`
- `run_mismatch`
- `media_store_unavailable`
- `invalid_media_data`
- `media_write_failed`

### `media.create` example

```json
{
  "type": "media.create",
  "session_id": "sess-123",
  "timestamp": 1710000000000,
  "payload": {
    "type": "image",
    "data": "<base64>",
    "filename": "diagram.png",
    "content_type": "image/png",
    "caption": "optional"
  }
}
```

## Canonical activity events

Observed `event_type` values:

- `run.started`
- `run.completed`
- `run.failed`
- `message.started`
- `message.completed`
- `step.started`
- `step.updated`
- `step.completed`
- `step.failed`
- `reasoning.summary`
- `tool.called`
- `tool.progress`
- `tool.completed`
- `tool.failed`
- `error.raised`

Important `data` keys used by the frontend timeline:

- message: `message_id`, `text`, `format`, `agent_id`
- step: `step_id`, `kind`, `title`, `headline`, `summary`, `message`
- tool: `tool_call_id`, `tool_name`, `display_name`, `arg_preview`, `result_preview`
- error: `scope`, `code`, `message`, `retryable`, `agent_id`

Optional routing/observability keys may be included:

- `model_used`
- `resolved_agent_id`
- `route_matched_by`

Cancellation outcome for accepted `run.stop`:

1. `message.completed` with stop text (default: `"Stopped by user."`)
2. `run.failed` with `error_code: "RUN_CANCELLED"`

## Ordering and dedupe

- `sequence` is monotonic per `run_id`.
- `event_id` is unique and used for dedupe.
- `idempotency_key` is emitted as `<run_id>_<sequence>`.
- Frontend sorts events by `sequence`.

## Config (`channels.supr`)

- `enabled`
- `token`
- `allow_token_query`
- `allow_origins`
- `ping_interval` (seconds)
- `read_timeout` (seconds)
- `write_timeout` (seconds)
- `max_connections`
- `allow_from`

Defaults:

- `ping_interval = 30`
- `read_timeout = 60`
- `write_timeout = 10`
- `max_connections = 100`

## Related HTTP endpoints

- `GET /api/supr/token`
- `POST /api/supr/token`
- `POST /api/supr/setup`

## Full internal spec

For the repository-level full spec (same protocol, expanded detail), see:

- [`docs/supr_ws_protocol.md`](https://github.com/itsivag/suprclaw-engine/blob/main/docs/supr_ws_protocol.md)
