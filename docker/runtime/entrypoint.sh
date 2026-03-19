#!/usr/bin/env bash
set -euo pipefail

RUNTIME_HOME="${SUPRCLAW_HOME:-${SUPRCLAW_ENGINE_HOME:-${PICOCLAW_HOME:-/home/suprclaw/.suprclaw}}}"
RUNTIME_CONFIG="${SUPRCLAW_CONFIG:-${SUPRCLAW_ENGINE_CONFIG:-${PICOCLAW_CONFIG:-$RUNTIME_HOME/config.json}}}"
WORKSPACE_DIR="$RUNTIME_HOME/workspace"
TEMPLATE_DIR="/opt/suprclaw/workspace"

mkdir -p "$RUNTIME_HOME" "$WORKSPACE_DIR"
chown -R suprclaw:suprclaw /home/suprclaw "$RUNTIME_HOME"

if [ -d "$TEMPLATE_DIR" ]; then
  cp -a "$TEMPLATE_DIR/." "$WORKSPACE_DIR/"
  chown -R suprclaw:suprclaw "$WORKSPACE_DIR"
fi

MODEL_NAME="${SUPRCLAW_ENGINE_MODEL_NAME:-${PICOCLAW_MODEL_NAME:-suprclaw-default}}"
if [ -n "${SUPRCLAW_ENGINE_MODEL_ID:-}" ]; then
  MODEL_ID="$SUPRCLAW_ENGINE_MODEL_ID"
elif [ -n "${PICOCLAW_MODEL_ID:-}" ]; then
  MODEL_ID="$PICOCLAW_MODEL_ID"
elif [ -n "${LITELLM_API_BASE:-}" ]; then
  MODEL_ID="${LITELLM_MODEL_ID:-litellm/auto}"
elif [ -n "${OPENAI_API_KEY:-}" ]; then
  MODEL_ID="${OPENAI_MODEL_ID:-openai/gpt-4.1-mini}"
else
  MODEL_ID="${OPENAI_MODEL_ID:-openai/gpt-4.1-mini}"
fi

if [ -n "${SUPRCLAW_ENGINE_PROVIDER_API_BASE:-}" ]; then
  MODEL_API_BASE="$SUPRCLAW_ENGINE_PROVIDER_API_BASE"
elif [ -n "${PICOCLAW_PROVIDER_API_BASE:-}" ]; then
  MODEL_API_BASE="$PICOCLAW_PROVIDER_API_BASE"
elif [[ "$MODEL_ID" == litellm/* ]]; then
  MODEL_API_BASE="${LITELLM_API_BASE:-}"
elif [[ "$MODEL_ID" == openai/* ]]; then
  MODEL_API_BASE="https://api.openai.com/v1"
else
  MODEL_API_BASE=""
fi

if [ -n "${SUPRCLAW_ENGINE_PROVIDER_API_KEY:-}" ]; then
  MODEL_API_KEY="$SUPRCLAW_ENGINE_PROVIDER_API_KEY"
elif [ -n "${PICOCLAW_PROVIDER_API_KEY:-}" ]; then
  MODEL_API_KEY="$PICOCLAW_PROVIDER_API_KEY"
elif [[ "$MODEL_ID" == litellm/* ]]; then
  MODEL_API_KEY="${LITELLM_API_KEY:-}"
elif [[ "$MODEL_ID" == openai/* ]]; then
  MODEL_API_KEY="${OPENAI_API_KEY:-}"
else
  MODEL_API_KEY=""
fi

if [ -n "${AWS_BEARER_TOKEN_BEDROCK:-}" ] && [ -z "${SUPRCLAW_ENGINE_PROVIDER_API_BASE:-}" ] && [ -z "${SUPRCLAW_ENGINE_PROVIDER_API_KEY:-}" ] && [ -z "${PICOCLAW_PROVIDER_API_BASE:-}" ] && [ -z "${PICOCLAW_PROVIDER_API_KEY:-}" ]; then
  cat >&2 <<'WARN'
[suprclaw] AWS_BEARER_TOKEN_BEDROCK is set, but SuprClaw Engine does not support amazon-bedrock as a native model protocol.
[suprclaw] Use LiteLLM as the shared Bedrock proxy by setting LITELLM_API_BASE and optionally LITELLM_API_KEY.
[suprclaw] Otherwise set SUPRCLAW_ENGINE_MODEL_ID and SUPRCLAW_ENGINE_PROVIDER_API_KEY for another supported provider.
WARN
fi

if [[ "$MODEL_ID" == litellm/* ]] && [ -z "$MODEL_API_BASE" ]; then
  cat >&2 <<'WARN'
[suprclaw] LiteLLM is selected, but LITELLM_API_BASE is not set.
[suprclaw] Set LITELLM_API_BASE to your shared LiteLLM endpoint, for example http://litellm.internal:4000/v1.
WARN
fi

if [ -z "${SUPRCLAW_ENGINE_PROVIDER_API_KEY:-}" ] && [ -z "${PICOCLAW_PROVIDER_API_KEY:-}" ] && [ -z "${LITELLM_API_KEY:-}" ] && [ -z "${OPENAI_API_KEY:-}" ]; then
  cat >&2 <<'WARN'
[suprclaw] No model provider API key is configured for this container.
[suprclaw] The SuprClaw Engine gateway will start for provisioning, but inference requests will fail until a provider key is supplied.
WARN
fi
SUPR_CHANNEL_TOKEN="${GATEWAY_TOKEN:-${SUPRCLAW_CHANNELS_SUPR_TOKEN:-${SUPRCLAW_ENGINE_CHANNEL_TOKEN:-${PICOCLAW_CHANNELS_PICO_TOKEN:-suprclaw-pico-token}}}}"
GATEWAY_ADMIN_SECRET="${SUPRCLAW_GATEWAY_ADMIN_SECRET:-${GATEWAY_TOKEN:-}}"
CRON_WEBHOOK_ENABLED="${SUPRCLAW_TOOLS_CRON_WEBHOOK_ENABLED:-}"
CRON_WEBHOOK_ENDPOINT="${SUPRCLAW_TOOLS_CRON_WEBHOOK_ENDPOINT:-}"
CRON_WEBHOOK_SECRET="${SUPRCLAW_TOOLS_CRON_WEBHOOK_SECRET:-}"
CRON_WEBHOOK_USER_ID="${SUPRCLAW_TOOLS_CRON_WEBHOOK_USER_ID:-}"
if [ -n "${SUPRCLAW_GATEWAY_REMOTE_ADMIN_CONTROL:-}" ]; then
  GATEWAY_REMOTE_ADMIN_CONTROL="${SUPRCLAW_GATEWAY_REMOTE_ADMIN_CONTROL}"
elif [ -n "$GATEWAY_ADMIN_SECRET" ]; then
  GATEWAY_REMOTE_ADMIN_CONTROL="true"
else
  GATEWAY_REMOTE_ADMIN_CONTROL="false"
fi
if [ -n "${SUPRCLAW_GATEWAY_HOT_RELOAD:-}" ]; then
  GATEWAY_HOT_RELOAD="${SUPRCLAW_GATEWAY_HOT_RELOAD}"
elif [ "$GATEWAY_REMOTE_ADMIN_CONTROL" = "true" ]; then
  GATEWAY_HOT_RELOAD="true"
else
  GATEWAY_HOT_RELOAD="false"
fi
EXISTING_TOOLS_JSON='{}'
if [ -f "$RUNTIME_CONFIG" ]; then
  EXISTING_TOOLS_JSON="$(jq -c '.tools // {}' "$RUNTIME_CONFIG" 2>/dev/null || printf '{}')"
fi

jq -n \
  --arg workspace "$WORKSPACE_DIR" \
  --arg modelName "$MODEL_NAME" \
  --arg modelId "$MODEL_ID" \
  --arg apiBase "$MODEL_API_BASE" \
  --arg apiKey "$MODEL_API_KEY" \
  --arg suprToken "$SUPR_CHANNEL_TOKEN" \
  --arg gatewayAdminSecret "$GATEWAY_ADMIN_SECRET" \
  --arg gatewayRemoteAdmin "$GATEWAY_REMOTE_ADMIN_CONTROL" \
  --arg gatewayHotReload "$GATEWAY_HOT_RELOAD" \
  --arg cronWebhookEnabled "$CRON_WEBHOOK_ENABLED" \
  --arg cronWebhookEndpoint "$CRON_WEBHOOK_ENDPOINT" \
  --arg cronWebhookSecret "$CRON_WEBHOOK_SECRET" \
  --arg cronWebhookUserId "$CRON_WEBHOOK_USER_ID" \
  --argjson existingTools "$EXISTING_TOOLS_JSON" \
  '{
    agents: {
      defaults: {
        workspace: $workspace,
        restrict_to_workspace: true,
        model_name: $modelName,
        max_tool_iterations: 50,
        status_updates: true,
        summarize_message_threshold: 20,
        summarize_token_percent: 75
      },
      list: [
        {
          id: "main",
          default: true,
          name: "Lead",
          workspace: $workspace,
          model: $modelName
        }
      ]
    },
    model_list: [
      ({
        model_name: $modelName,
        model: $modelId
      }
      + (if $apiBase != "" then {api_base: $apiBase} else {} end)
      + (if $apiKey != "" then {api_key: $apiKey} else {} end))
    ],
    gateway: ({
      host: "0.0.0.0",
      port: 18790,
      hot_reload: ($gatewayHotReload == "true"),
      remote_admin_control: ($gatewayRemoteAdmin == "true")
    } + (if $gatewayAdminSecret != "" then {admin_secret: $gatewayAdminSecret} else {} end)),
    channels: {
      supr: {
        enabled: true,
        token: $suprToken
      }
    },
    tools: ($existingTools + {
      cron: (($existingTools.cron // {}) + {
        webhook: ((($existingTools.cron // {}).webhook // {})
          + (if $cronWebhookEnabled != "" then {enabled: ($cronWebhookEnabled == "true")} else {} end)
          + (if $cronWebhookEndpoint != "" then {endpoint: $cronWebhookEndpoint} else {} end)
          + (if $cronWebhookSecret != "" then {secret: $cronWebhookSecret} else {} end)
          + (if $cronWebhookUserId != "" then {user_id: $cronWebhookUserId} else {} end))
      }),
      mcp: (($existingTools.mcp // {}) + {
        servers: ($existingTools.mcp.servers // {})
      })
    })
  }' > "$RUNTIME_CONFIG"

chown suprclaw:suprclaw "$RUNTIME_CONFIG"

exec /usr/bin/supervisord -c /etc/supervisor/supervisord.conf -n
