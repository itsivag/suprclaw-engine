# Supr WebSocket Protocol and Schema

This document describes the WebSocket protocol used by the Supr web chat channel in this repository.

## Scope and source of truth

- Server channel implementation: `pkg/channels/supr/`
- Canonical activity envelope type: `pkg/bus/types.go`
- Activity event producer: `pkg/agent/activity_events.go` and `pkg/agent/loop.go`
- Web backend WS proxy and token API: `web/backend/api/supr.go`
- Frontend WS client/parser: `web/frontend/src/features/chat/controller.ts` and `web/frontend/src/features/chat/protocol.ts`

Note: some frontend symbols still use `Pico` naming, but the actual channel/protocol is `supr`.

## Transport and endpoint

- Browser connects to: `GET /supr/ws`
- Backend proxies `/supr/ws` to gateway Supr channel.
- URL query:
  - `session_id` (optional): client session identifier. If omitted, server generates one.

Example:

```text
ws://<host>:<port>/supr/ws?session_id=sess-123
```

## Authentication

A connection is accepted when one of these matches `channels.supr.token`:

1. `Authorization: Bearer <token>`
2. WebSocket subprotocol: `token.<token>` (used by frontend)
3. Query param `?token=<token>` only if `allow_token_query=true`

If auth fails, handshake returns `401 unauthorized`.

## Connection behavior

- One active WS connection per `session_id` (new connection replaces old one).
- First server frame after connect is typed `agent.list`.
- Server also sends low-level WS ping frames periodically.
- App-level ping/pong also exists as JSON frames (`type: "ping"` / `type: "pong"`).

## Wire envelopes

There are 2 envelope styles on the wire.

### 1) Typed frame (`SuprMessage`)

Used for client requests and some control/media/error frames.

```json
{
  "type": "string",
  "id": "optional string",
  "session_id": "optional string",
  "timestamp": 1710000000000,
  "payload": {}
}
```

### 2) Canonical activity envelope (`ActivityEventEnvelope`)

Used for run/step/tool/message lifecycle streaming.

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
  "agent_id": "optional string",
  "trace_id": "optional string",
  "span_id": "optional string",
  "idempotency_key": "run_xxx_1",
  "replay": false,
  "data": {}
}
```

`agent_id` represents the actual agent that responded. For canonical activity
events, `data.agent_id` mirrors top-level `agent_id` and both must match.

## Client -> server frame types

## `type: "message.send"`

Required payload fields:

- `content: string` (must be non-empty after trim)

Optional payload fields:

- `agent_id: string` (route preference)
- `model: string` (model override)
- `reasoning: "off" | "low" | "medium" | "high" | "xhigh" | "adaptive"`

Example:

```json
{
  "type": "message.send",
  "id": "msg-1",
  "payload": {
    "content": "Explain this error",
    "agent_id": "main",
    "reasoning": "high"
  }
}
```

## `type: "media.send"`

Supports 2 payload modes:

1. Scalar mode: top-level `data` or `url`
2. Batch mode: `attachments: []`

Attachment item fields:

- `data?: string` (base64)
- `url?: string` (download URL)
- `filename?: string`
- `content_type?: string`
- `caption?: string`

Rules:

- each media item needs `data` or `url`
- max decoded payload per item: 25 MB
- optional `reasoning`, `agent_id`, `model` same as `message.send`

## `type: "ping"`

Server replies with typed `pong` and mirrors `id` when provided.

## `type: "run.stop"`

Requests cancellation of the active run for the session.

Payload fields:

- `run_id?: string`
- `reason?: string`

Validation:

- If there is no active run: typed `error` with `code: "no_active_run"`.
- If `run_id` is provided and does not match the active run: typed `error` with `code: "run_mismatch"`.
- On success, no typed ack is sent; the result is reported via canonical activity events.

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

## Server -> client frame types

## Typed control/data frames

### `type: "agent.list"` (sent immediately on connect)

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

### `type: "pong"`

```json
{
  "type": "pong",
  "id": "same-as-ping-id",
  "timestamp": 1710000000000
}
```

### `type: "error"`

Used for protocol/validation errors (not model run failures).

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

Common error codes:

- `invalid_message`
- `unknown_type`
- `empty_content`
- `invalid_reasoning`
- `no_active_run`
- `run_mismatch`
- `media_store_unavailable`
- `invalid_media_data`
- `media_write_failed`

### `type: "media.create"`

Outbound media delivery to client:

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

## Canonical activity events (`event_type`)

Observed event types emitted in this project:

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

Important `data` fields used by client UI:

- message events:
  - `message_id`, `text`, `format`, `agent_id`
- step events:
  - `step_id`, `kind`, `title`, `headline`, `summary`, `message`
- tool events:
  - `tool_call_id`, `tool_name`, `display_name`, `arg_preview`, `result_preview`, `message`
- error events:
  - `scope`, `code`, `message`, `retryable`, `agent_id`

Additional optional observability/routing metadata may appear:

- `model_used`
- `resolved_agent_id`
- `route_matched_by`

Cancellation outcome for accepted `run.stop`:

1. `message.completed` with stop text (default: `"Stopped by user."`)
2. `run.failed` with `error_code: "RUN_CANCELLED"`

## Ordering and idempotency

- `sequence` is per-run monotonic order.
- `event_id` is unique identifier for dedupe.
- `idempotency_key` is also emitted (`<run_id>_<sequence>`).
- Frontend deduplicates repeated `event_id` and sorts by `sequence`.

## Config knobs (`channels.supr`)

Schema from `pkg/config/config.go`:

- `enabled: bool`
- `token: string`
- `allow_token_query: bool`
- `allow_origins: string[]`
- `ping_interval: int` (seconds)
- `read_timeout: int` (seconds)
- `write_timeout: int` (seconds)
- `max_connections: int`
- `allow_from: string[]`
- `placeholder: object` (reserved, not currently active in Supr WS flow)

Defaults from `pkg/config/defaults.go`:

- `ping_interval = 30`
- `read_timeout = 60`
- `write_timeout = 10`
- `max_connections = 100`

## HTTP token/config endpoints used by web app backend

- `GET /api/supr/token` -> `{ token, ws_url, enabled }`
- `POST /api/supr/token` -> regenerates token
- `POST /api/supr/setup` -> ensures Supr channel enabled/token/origins
