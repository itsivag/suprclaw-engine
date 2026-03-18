---
sidebar_position: 3
---

# Security Sandbox

By default, the agent is restricted to the workspace directory. This prevents accidental (or intentional) access to the rest of the filesystem.

## Configuration

```json
{
  "agents": {
    "defaults": {
      "restrict_to_workspace": true
    }
  }
}
```

Or via environment variable:

```bash
SUPRCLAW_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE=false
```

## Tool Restrictions

When `restrict_to_workspace` is `true`:

| Tool | Restriction |
|------|-------------|
| `read_file` | Workspace directory only |
| `write_file` | Workspace directory only |
| `list_dir` | Workspace directory only |
| `edit_file` | Workspace directory only |
| `exec` | Command paths within workspace only |

## Always-Blocked Commands

The `exec` tool always blocks dangerous commands, regardless of the workspace setting:

- `rm -rf`
- `format`, `mkfs`, `diskpart`
- `dd if=`
- Direct writes to `/dev/sd*`
- `shutdown`, `reboot`, `poweroff`
- Fork bombs

:::warning
Setting `restrict_to_workspace: false` grants the agent full system access. Use with caution.
:::

## Exec Guard Limitations

The exec guard only validates the **top-level command**. It does not recursively inspect child processes spawned by build tools after that command starts running.

**Examples that bypass the guard once the initial command is allowed:**
- `make run`
- `go run ./cmd/...`
- `cargo run`
- `npm run build`

For untrusted code in the workspace, use stronger isolation: containers, VMs, or an approval flow around build-and-run commands.

## Custom Deny Patterns

Add additional blocked command patterns:

```json
{
  "tools": {
    "exec": {
      "enable_deny_patterns": true,
      "custom_deny_patterns": [
        "\\brm\\s+-r\\b",
        "\\bkillall\\s+python"
      ]
    }
  }
}
```

Patterns are regular expressions matched against the full command string.
