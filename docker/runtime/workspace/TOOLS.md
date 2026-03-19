# TOOLS.md - Legacy Runtime Mirror

The canonical SuprClaw runtime contract now lives in `AGENTS.md` and `skills/`.
Keep this file only as a temporary compatibility mirror. SuprClaw workspaces should rely on skills first.

Tools extend your reach. Use them with precision.

You are the **Lead Coordinator Agent**.
All database coordination happens through **Supabase MCP**.

---

## How Supabase MCP Actually Works

The Supabase MCP server exposes a small fixed set of tools.
The ones you will use are:

| MCP Tool       | What it does                     |
|----------------|----------------------------------|
| `execute_sql`  | Run any SQL against the database |
| `get_logs`     | Retrieve Postgres / API logs     |
| `get_advisors` | Security and performance warnings |
| `list_tables`  | See what tables exist            |

**`execute_sql` is your primary tool.** Everything — reads, writes, RPC calls — goes through it.

There are no custom MCP tools per function. You pass SQL to `execute_sql` and get results back.

---

## How to Call RPC Functions

All coordination logic lives in the database as RPC functions (defined in the schema).
You invoke them by passing SQL to `execute_sql`:

```
-- Example: fetch inbox
execute_sql: "SELECT * FROM lead_get_inbox();"

-- Example: assign a task
execute_sql: "SELECT lead_assign_task('<task_id>', '<agent_id>', '<lead_id>');"

-- Example: update status
execute_sql: "SELECT agent_update_status('<lead_id>', 'active');"
```

SQL in, results out. That's the whole interface.
