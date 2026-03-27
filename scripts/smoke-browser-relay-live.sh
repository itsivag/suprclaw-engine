#!/usr/bin/env bash
set -euo pipefail

SSH_HOST="${1:-${SSH_HOST:-46.225.190.134}}"
SSH_USER="${SSH_USER:-suprclaw}"
SSH_KEY="${SSH_KEY:-$HOME/suprclaw-new}"
SSH_OPTS=(
  -o BatchMode=yes
  -o StrictHostKeyChecking=accept-new
  -o ServerAliveInterval=20
  -o ServerAliveCountMax=10
)

if [[ ! -f "$SSH_KEY" ]]; then
  echo "SSH key not found: $SSH_KEY" >&2
  exit 1
fi

run_id="relay-e2e-$(date -u +%Y%m%d%H%M%S)-$RANDOM"
echo "== Relay smoke run id: $run_id"
echo "== Host: $SSH_USER@$SSH_HOST"

tmp_remote="$(mktemp)"
cat >"$tmp_remote" <<'EOS'
set -euo pipefail

run_id="$1"
cid="$(podman ps --format '{{.ID}} {{.Names}}' | awk '/suprclaw-engine/{print $1; exit}')"
if [[ -z "$cid" ]]; then
  echo "No running suprclaw-engine container found" >&2
  exit 2
fi
echo "== Container: $cid"

message="$(cat <<'MSG'
Browser relay smoke test.

Hard requirements:
1. Use browser relay capabilities only (not generic browser automation).
   - Use only Browser Relay V2 tools (`browser_relay_v2_targets_list`, `browser_relay_v2_action`, `browser_relay_v2_batch`).
2. List relay targets and identify source tags.
3. Before any snapshot/action, establish an attached target deterministically:
   - if a target has `attached=true`, select that target
   - otherwise call `tabs.select` on the first target, then continue
4. If at least one target exists, follow a ref-first flow:
   - take one compact snapshot first (`mode=compact`, interactive elements only)
   - use snapshot refs for click/type operations where possible
   - use `batch` for multi-step interactions when safe
   - do not loop snapshots: before the first interaction, snapshot at most once
   - after that, only snapshot again if navigation/state changed
5. Execute deterministic commerce steps:
   - navigate to https://www.amazon.in (or https://www.amazon.com if redirected)
   - search for: iphone 17
   - open a relevant iPhone 17 product page
   - if selected product page does not expose Add to Cart, go back and try another result (max 3 products)
   - click Add to Cart
6. Strict ref policy:
   - for click/type, only use `@eN` refs from snapshot output
   - do not use raw CSS/XPath for click/type
   - for the final purchase interaction, click only refs whose visible text/name contains `Add to Cart` or `Add to Basket`
   - do not click refs containing `Without Exchange`, `Buy Now`, `Protection`, `See details`
   - if refs are stale/missing, re-snapshot (respecting loop constraints) and continue
   - if relay returns `snapshot_progress_blocked` or `relay_loop_guard_triggered`, stop and return failure JSON (do not blind retry)
7. Verify Add to Cart succeeded (cart count increment, cart confirmation, or explicit cart state check).
8. If no target exists, report paired=false and include a clear error.
9. Output exactly one final line:
RELAY_SMOKE_RESULT::<json>

JSON schema:
{
  "paired": boolean,
  "targets": [string],
  "target_id": string,
  "amazon_open_ok": boolean,
  "search_ok": boolean,
  "add_to_cart_ok": boolean,
  "cart_signal": string,
  "errors": [string]
}
MSG
)"

printf -v msg_escaped '%q' "$message"
cmd="SUPRCLAW_HOME=/home/suprclaw/.suprclaw SUPRCLAW_CONFIG=/home/suprclaw/.suprclaw/config.json suprclaw agent --session cli:${run_id} --message ${msg_escaped}"

set +e
raw="$(podman exec "$cid" su - suprclaw -s /bin/bash -lc "timeout 900s bash -lc $(printf '%q' "$cmd")" 2>&1)"
agent_exit=$?
set -e

echo "== Agent exit: $agent_exit"
echo "== Agent output (tail) =="
printf '%s\n' "$raw" | tail -n 200

raw_clean="$(printf '%s\n' "$raw" | perl -pe 's/\e\[[0-9;]*[A-Za-z]//g')"
result_line="$(
  printf '%s\n' "$raw_clean" \
    | grep -oE 'RELAY_SMOKE_RESULT::\{.*\}' \
    | tail -n 1 || true
)"
if [[ -z "$result_line" ]]; then
  echo "Missing valid RELAY_SMOKE_RESULT::{...} marker in agent output" >&2
  exit 10
fi

result_json="${result_line#*RELAY_SMOKE_RESULT::}"
export RESULT_JSON="$result_json"

python3 - <<'PY'
import json
import os
import sys

raw = os.environ.get("RESULT_JSON", "").strip()
if not raw:
    print("Empty RESULT_JSON payload", file=sys.stderr)
    sys.exit(11)

try:
    data = json.loads(raw)
except Exception as exc:  # pragma: no cover - smoke parse path
    print(f"Invalid RESULT_JSON: {exc}", file=sys.stderr)
    print(raw, file=sys.stderr)
    sys.exit(12)

required = [
    "paired",
    "targets",
    "amazon_open_ok",
    "search_ok",
    "add_to_cart_ok",
    "errors",
]
missing = [k for k in required if k not in data]
if missing:
    print(f"Missing keys in result JSON: {missing}", file=sys.stderr)
    sys.exit(13)

paired = bool(data.get("paired"))
amazon_ok = bool(data.get("amazon_open_ok"))
search_ok = bool(data.get("search_ok"))
add_ok = bool(data.get("add_to_cart_ok"))
targets = data.get("targets") or []
target_id = str(data.get("target_id") or "")
errors = data.get("errors") or []

if not paired:
    print("Relay not paired (paired=false).", file=sys.stderr)
    print(json.dumps(data, ensure_ascii=False), file=sys.stderr)
    sys.exit(14)

if not isinstance(targets, list) or len(targets) == 0:
    print("No relay targets found in targets list.", file=sys.stderr)
    print(json.dumps(data, ensure_ascii=False), file=sys.stderr)
    sys.exit(15)

if not target_id:
    print("target_id is empty", file=sys.stderr)
    print(json.dumps(data, ensure_ascii=False), file=sys.stderr)
    sys.exit(16)

if not amazon_ok or not search_ok or not add_ok:
    print("Relay commerce flow failed (amazon/search/add_to_cart).", file=sys.stderr)
    print(json.dumps(data, ensure_ascii=False), file=sys.stderr)
    sys.exit(17)

print("== Parsed smoke result ==")
print(json.dumps(data, ensure_ascii=False, indent=2))
if errors:
    print("== Non-fatal errors reported ==")
    print(json.dumps(errors, ensure_ascii=False, indent=2))
PY
EOS

ssh -i "$SSH_KEY" "${SSH_OPTS[@]}" "$SSH_USER@$SSH_HOST" \
  "bash -s -- '$run_id'" <"$tmp_remote"

rm -f "$tmp_remote"
echo "== Relay smoke passed."
