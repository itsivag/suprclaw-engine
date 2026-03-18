---
sidebar_position: 2
---

# Scheduled Tasks

SuprClaw has a built-in cron scheduler. You can create scheduled jobs via natural language through the agent, or via the CLI.

## Natural Language Scheduling

Ask the agent to schedule tasks:

```bash
suprclaw agent -m "Remind me every day at 9am to check email"
suprclaw agent -m "Remind me in 30 minutes"
suprclaw agent -m "Run a status check every hour"
```

The agent creates cron jobs stored in `~/.suprclaw/workspace/cron/`.

## CLI Commands

```bash
suprclaw cron list        # List all scheduled jobs
suprclaw cron add ...     # Add a scheduled job
```

## Cron Configuration

Timeout for each job execution:

```json
{
  "tools": {
    "cron": {
      "exec_timeout_minutes": 5
    }
  }
}
```

Set to `0` for no timeout limit.

## Job Storage

Jobs are stored as files in `~/.suprclaw/workspace/cron/` and run automatically when the gateway is active.
