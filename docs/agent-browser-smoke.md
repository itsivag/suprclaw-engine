# Agent Browser MCP Smoke Flow

## Goal
Validate browser automation through middleware MCP (`agent_browser`) with extension-attached local tabs.

## Preconditions
1. Middleware is reachable and exposes `POST /api/mcp/agent-browser`.
2. Container `config.json` includes only:
   - `tools.agent_browser.enabled=true`
   - `tools.mcp.servers.agent_browser` with URL to middleware `/api/mcp/agent-browser`
3. Browser extension is paired, authenticated, and an active tab is attached.

## Validate Tool Surface
Use MCP discovery from the agent/container and confirm these tools exist:
- `agent_browser_targets_list`
- `agent_browser_action`
- `agent_browser_batch`

No legacy relay tool names should be present.

## Minimal Action Sequence
1. Call `agent_browser_targets_list` and pick a target.
2. Call `agent_browser_action` with `action=snapshot` (compact default).
3. Use returned `@eN` refs for `click` / `type` via `agent_browser_action` or `agent_browser_batch`.
4. Re-snapshot only after DOM-changing actions.

## Failure Handling
- Do not retry on `retry_class=never`.
- On `snapshot_ref_not_found`, take a fresh compact snapshot and continue.
- On `snapshot_ref_required`, fix caller to use `@eN` refs (no raw selectors).

## Live Smoke Targets
1. Amazon: `amazon.in -> search iPhone 17 -> open product -> add to cart`.
2. Skyscanner: search Chennai -> Bangkok with flexible dates and extract fare/date/flight.
