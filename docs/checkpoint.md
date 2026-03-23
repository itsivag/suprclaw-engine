# Git-like State Versioning (Checkpoints)

SuprClaw includes a lightweight checkpoint system that captures snapshots of agent session history and workspace files. This lets operators roll back to any saved state and audit every tool call the agent has made.

---

## What Gets Saved

| What | Where |
|---|---|
| Per-agent action log (every tool call) | `~/.suprclaw/audit/{agentId}.jsonl` |
| Commit manifests (content-addressed) | `~/.suprclaw/checkpoints/{agentId}/{commitId}.json` |
| Workspace file snapshots | `~/.suprclaw/checkpoints/{agentId}/{commitId}.snap.d/` |
| Session message snapshots | `~/.suprclaw/checkpoints/{agentId}/{commitId}.snap.d/_session.json` |

## What Cannot Be Rolled Back

- **External messages** — sent Telegram/Discord/WhatsApp/etc. messages cannot be recalled. The rollback API always enumerates `unrestoable_side_effects` so you know what was sent.
- **MCP tool calls** — there is no standard MCP undo primitive. Compensations (see below) are opt-in.
- **WhatsApp's internal SQLite** — not snapshotted.
- **`config.json`** — not versioned by this system.

---

## Configuration

Add to `agents.defaults` in `config.json`:

```json
{
  "agents": {
    "defaults": {
      "checkpoint": {
        "enabled": true,
        "every_n_tool_calls": 5,
        "checkpoint_before": ["write_file", "edit_file", "exec", "shell"],
        "store_snap_data": true,
        "max_snap_file_size": 5242880,
        "max_commits_per_agent": 100
      }
    }
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | false | Enable the checkpoint system |
| `every_n_tool_calls` | int | 0 | Take a periodic checkpoint every N tool calls (0 = disabled) |
| `checkpoint_before` | string[] | [] | Tool names that automatically trigger a pre-call checkpoint |
| `store_snap_data` | bool | false | Copy workspace files + session into the snapshot (required for workspace/session rollback) |
| `max_snap_file_size` | int | 5242880 | Maximum file size (bytes) to include in a snapshot (default 5 MB) |
| `max_commits_per_agent` | int | 100 | Maximum commits to retain per agent (oldest pruned automatically) |

---

## Side-Effect Classification

Every tool call is classified for the audit log:

| Category | Tools | Meaning |
|---|---|---|
| `none` | `read_file`, `list_dir`, `search` | Read-only, no state changes |
| `local` | `write_file`, `edit_file`, `append_file` | Workspace files only |
| `external` | All MCP tools, `exec`, `shell`, `send_message`, `send_file` | Calls external services/processes |

Unknown tools default to `external` (safest assumption). For `external` calls, the full args and result are stored in the audit log for forensic value.

Custom tools can declare their category by implementing the `SideEffectClassifier` interface from `pkg/tools/base.go`:

```go
type SideEffectClassifier interface {
    SideEffectType() string // "none", "local", or "external"
}
```

---

## Admin API

All endpoints require `Authorization: Bearer {admin_secret}`. Enable via `gateway.remote_admin_control: true`.

### List commits

```
GET /api/admin/checkpoints?agentId=main&sessionKey=
```

Returns all commits for an agent, newest-first. `sessionKey` is optional.

### Manual checkpoint

```
POST /api/admin/checkpoints
Content-Type: application/json

{"agentId": "main", "sessionKey": "agent:main:telegram:123456", "label": "before big refactor"}
```

### Rollback

```
POST /api/admin/checkpoints/{commitId}/rollback
Content-Type: application/json

{"agentId": "main", "scope": "all"}
```

`scope` is `"session"`, `"workspace"`, or `"all"`. Requires `store_snap_data: true`.

Response:

```json
{
  "restored_commit": {...},
  "session_messages_restored": 42,
  "workspace_files_restored": ["notes.md", "data/plan.md"],
  "unrestoable_side_effects": [
    {"tool_name": "send_message", "result_preview": "Message sent to Telegram", ...}
  ],
  "compensations": [
    {"seq": 12, "tool_name": "mcp_db_insert", "comp_tool": "mcp_db_delete", "comp_args": {"id": "99"}, "result": "deleted row 99"}
  ]
}
```

`unrestoable_side_effects` lists external calls that happened after the target commit that cannot be automatically undone. Successfully compensated entries are excluded from this list.

### Query audit log

```
GET /api/admin/audit/actions?agentId=main&limit=50
```

Returns the most recent tool calls for an agent.

### Revoke commit

```
POST /api/admin/commits/{commitId}/revoke
Content-Type: application/json

{"agentId": "main"}
```

Marks the commit as revoked in its manifest (`revoked: true`). Does not delete snap data.

---

## Agent Self-Checkpointing Tool

When checkpoints are enabled, the agent has access to a `checkpoint` tool it can use mid-task:

> "Save a named checkpoint before refactoring this module."

The agent calls `checkpoint` with a `label` parameter. The commit is created immediately and the ID is returned to the LLM.

---

## MCP Compensation System

For external tool calls (MCP tools, `exec`, etc.), automatic rollback is not possible — there is no standard undo primitive. The compensation system lets you define **inverse tool calls** that are executed in reverse order during rollback.

### How It Works

1. **At call time**: when an external tool is called and a matching compensation rule exists, the inverse args are resolved immediately (via Go `text/template`) and stored in the audit log entry.
2. **At rollback time**: before restoring session/workspace, all compensation plans for entries after the target commit are executed in **reverse chronological order** (newest call undone first).
3. Entries that were successfully compensated are removed from `unrestoable_side_effects` in the rollback response.

### Configuration

```json
{
  "agents": {
    "defaults": {
      "checkpoint": {
        "enabled": true,
        "store_snap_data": true,
        "compensations": [
          {
            "tool_name": "mcp_db_insert",
            "inverse_tool": "mcp_db_delete",
            "inverse_args": "{\"id\": \"{{index .Result \\\"id\\\"}}\"}",
          },
          {
            "tool_name": "mcp_github_create_issue",
            "inverse_tool": "mcp_github_close_issue",
            "inverse_args": "{\"issue_number\": {{index .Result \"number\"}}}"
          }
        ]
      }
    }
  }
}
```

| Field | Type | Description |
|---|---|---|
| `tool_name` | string | The tool whose calls should have a compensation plan |
| `inverse_tool` | string | The tool to call to undo the effect |
| `inverse_args` | string | Go `text/template` producing a JSON object for the inverse call's args |

### Template Variables

| Variable | Type | Description |
|---|---|---|
| `.Args` | `map[string]any` | The original tool call's arguments |
| `.Result` | `map[string]any` | The tool call's result parsed as JSON (nil if result is not valid JSON) |
| `.ResultRaw` | `string` | The tool call's raw result string |

### Behavior

- If template resolution fails (bad template, result not JSON, etc.), the compensation is silently skipped and `Compensation` is stored as `null` in the audit log.
- Compensations are executed using the **live** agent's tool registry. If the agent is unavailable at rollback time, compensations are skipped (marked `skipped: true` in the response).
- Only `external` side-effect entries can have compensation plans.
- Compensation does **not** fail the rollback if it errors — the error is recorded in `compensations[].error` and the entry remains in `unrestoable_side_effects`.

---

## Snap Data Layout

```
~/.suprclaw/checkpoints/{agentId}/{commitId}.snap.d/
    notes.md                  ← workspace file copy
    data/config.md            ← mirrors workspace subdirectory structure
    _session.json             ← JSON array of session messages
```

- Only files listed in the commit manifest are stored.
- Files larger than `max_snap_file_size` are excluded from snapshots (their metadata is still recorded in the manifest).
- The `sessions/` subdirectory of the workspace is always skipped during workspace walks (it's covered by `_session.json`).

---

## Hard Limits

- No block-level copy-on-write (no ZFS/Btrfs) — snapshots are file copies.
- No mid-computation checkpoint — can only snapshot between tool calls.
- No PITR — there is no WAL or SQL database to replay.
- No cross-agent rollback — each agent has its own independent checkpoint history.
