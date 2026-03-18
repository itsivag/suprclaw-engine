---
sidebar_position: 3
---

# Troubleshooting

## General

### Web search says "API key configuration issue"

Expected if no search API key is configured. DuckDuckGo is the built-in fallback and requires no key.

### Content filtering errors

Some providers filter certain content. Try rephrasing or switch to a different model/provider.

### Agent accessing files outside workspace

Check `restrict_to_workspace` in your config. Set to `true` to re-enable the sandbox.

---

## Telegram

### "Conflict: terminated by other getUpdates"

Another instance is running. Only one `suprclaw gateway` should run at a time. Find and stop the other instance.

---

## OpenRouter

### "model ... not found in model_list" or "free is not a valid model ID"

The `model` field must be the **full** OpenRouter model ID, not a shorthand.

**Wrong:**
```json
{ "model": "free" }
```

**Right:**
```json
{ "model": "openrouter/free" }
```

Valid examples:
- `"openrouter/free"` — auto free-tier routing
- `"openrouter/google/gemini-2.0-flash-exp:free"`
- `"openrouter/meta-llama/llama-3.1-8b-instruct:free"`

Also ensure `agents.defaults.model_name` matches a `model_name` in `model_list`:

```json
{
  "agents": { "defaults": { "model_name": "openrouter-free" } },
  "model_list": [
    {
      "model_name": "openrouter-free",
      "model": "openrouter/free",
      "api_key": "sk-or-v1-YOUR_KEY"
    }
  ]
}
```

---

## Antigravity (Google Cloud Code Assist)

### "Token expired"

Re-authenticate:
```bash
suprclaw auth login --provider antigravity
```

### "Gemini for Google Cloud is not enabled"

Enable the API in your [Google Cloud Console](https://console.cloud.google.com/).

### 429 Rate Limit

Antigravity has strict quotas. The error message includes a `reset time`. Wait for the quota to reset, or switch to a different model.

### Empty response

The model may be restricted for your project. Try `gemini-3-flash` or `gemini-2.5-flash-lite`.

### 404 Not Found

Use a model ID from `suprclaw auth models`. Use the short ID (e.g. `gemini-3-flash`), not the full path.

---

## Credential Encryption

### "failed to decrypt api_key"

- Verify `SUPRCLAW_KEY_PASSPHRASE` is set correctly
- Verify `SUPRCLAW_SSH_KEY_PATH` points to `~/.ssh/suprclaw_ed25519.key` (or the key used during encryption)
- If SSH key is unavailable, set `SUPRCLAW_SSH_KEY_PATH=""` to use passphrase-only mode (only works if encrypted that way)

---

## Getting Help

- [GitHub Issues](https://github.com/itsivag/suprclaw-engine/issues) — report bugs or ask questions
- [CONTRIBUTING.md](https://github.com/itsivag/suprclaw-engine/blob/main/CONTRIBUTING.md) — contributing guide
