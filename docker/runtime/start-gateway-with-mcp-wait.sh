#!/usr/bin/env bash
set -euo pipefail

ENGINE_USER="suprclaw"
RUNTIME_HOME="${SUPRCLAW_HOME:-${SUPRCLAW_ENGINE_HOME:-/home/${ENGINE_USER}/.suprclaw}}"
RUNTIME_CONFIG="${SUPRCLAW_CONFIG:-${SUPRCLAW_ENGINE_CONFIG:-${RUNTIME_HOME}/config.json}}"
SUPABASE_MCP_PROXY_SCRIPT="/etc/suprclaw/mcp-proxy/supabase-mcp-proxy.js"
SUPABASE_MCP_PROXY_ENV="/etc/suprclaw/mcp-proxy/supabase-mcp-proxy.env"
SUPABASE_MCP_WAIT_SECONDS="${SUPRCLAW_SUPABASE_MCP_WAIT_SECONDS:-30}"

wait_for_supabase_proxy=0
if [ -f "$SUPABASE_MCP_PROXY_SCRIPT" ] && [ -r "$SUPABASE_MCP_PROXY_ENV" ]; then
  wait_for_supabase_proxy=1
fi

if [ "$wait_for_supabase_proxy" -eq 1 ]; then
  ready=0
  for _ in $(seq 1 "$SUPABASE_MCP_WAIT_SECONDS"); do
    if python3 - <<'PY'
import socket
import sys

s = socket.socket()
s.settimeout(0.5)
try:
    s.connect(("127.0.0.1", 18791))
except Exception:
    sys.exit(1)
finally:
    s.close()
sys.exit(0)
PY
    then
      ready=1
      break
    fi
    sleep 1
  done

  if [ "$ready" -ne 1 ]; then
    echo "supabase-mcp-proxy not reachable on 127.0.0.1:18791" >&2
    exit 1
  fi
fi

exec su - "$ENGINE_USER" -s /bin/bash -lc \
  "SUPRCLAW_HOME=${RUNTIME_HOME} SUPRCLAW_CONFIG=${RUNTIME_CONFIG} SUPRCLAW_ENGINE_HOME=${RUNTIME_HOME} SUPRCLAW_ENGINE_CONFIG=${RUNTIME_CONFIG} suprclaw gateway"
