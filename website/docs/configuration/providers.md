---
sidebar_position: 2
---

# Providers & Models

SuprClaw uses a `model_list` config — add any provider with just `api_base` + `api_key`, no code changes needed.

## Supported Providers

| Provider | `model` prefix | Default API Base | Protocol |
|----------|---------------|------------------|----------|
| OpenAI | `openai/` | `https://api.openai.com/v1` | OpenAI |
| Anthropic | `anthropic/` | `https://api.anthropic.com/v1` | Anthropic |
| Google Gemini | `gemini/` | `https://generativelanguage.googleapis.com/v1beta` | OpenAI |
| Groq | `groq/` | `https://api.groq.com/openai/v1` | OpenAI |
| DeepSeek | `deepseek/` | `https://api.deepseek.com/v1` | OpenAI |
| OpenRouter | `openrouter/` | `https://openrouter.ai/api/v1` | OpenAI |
| Ollama | `ollama/` | `http://localhost:11434/v1` | OpenAI |
| Azure OpenAI | `azure/` | `https://{resource}.openai.azure.com` | Azure |
| Cerebras | `cerebras/` | `https://api.cerebras.ai/v1` | OpenAI |
| LiteLLM Proxy | `litellm/` | `http://localhost:4000/v1` | OpenAI |
| vLLM | `vllm/` | `http://localhost:8000/v1` | OpenAI |
| NVIDIA | `nvidia/` | `https://integrate.api.nvidia.com/v1` | OpenAI |
| GitHub Copilot | `github-copilot/` | `localhost:4321` | gRPC |
| Antigravity | `antigravity/` | Google Cloud Code Assist | OAuth |

:::tip
Groq also provides free **voice transcription** via Whisper — configure it once and audio messages from any channel are auto-transcribed.
:::

## Configuration Examples

### Anthropic

```json
{
  "model_list": [
    {
      "model_name": "claude-sonnet-4.6",
      "model": "anthropic/claude-sonnet-4.6",
      "api_key": "sk-ant-your-key"
    }
  ],
  "agents": { "defaults": { "model_name": "claude-sonnet-4.6" } }
}
```

### OpenAI

```json
{
  "model_list": [
    {
      "model_name": "gpt-4o",
      "model": "openai/gpt-4o",
      "api_key": "sk-your-key"
    }
  ]
}
```

### Ollama (Local)

```json
{
  "model_list": [
    {
      "model_name": "llama3",
      "model": "ollama/llama3"
    }
  ]
}
```

No API key needed — Ollama runs locally.

### OpenRouter (Access to All Models)

```json
{
  "model_list": [
    {
      "model_name": "claude-sonnet-4.6",
      "model": "openrouter/anthropic/claude-sonnet-4.6",
      "api_key": "sk-or-v1-your-key"
    }
  ]
}
```

### Free Tier via OpenRouter

```json
{
  "agents": {
    "defaults": {
      "model_name": "openrouter-free"
    }
  },
  "model_list": [
    {
      "model_name": "openrouter-free",
      "model": "openrouter/free",
      "api_key": "sk-or-v1-your-key",
      "api_base": "https://openrouter.ai/api/v1"
    }
  ]
}
```

:::note
Use the full model ID like `"openrouter/free"` — shorthand like `"free"` is rejected by OpenRouter.
:::

### Groq

```json
{
  "model_list": [
    {
      "model_name": "llama3-70b",
      "model": "groq/llama3-70b-8192",
      "api_key": "gsk_your-key"
    }
  ]
}
```

### Google Gemini

```json
{
  "model_list": [
    {
      "model_name": "gemini-flash",
      "model": "gemini/gemini-2.0-flash",
      "api_key": "your-google-api-key"
    }
  ]
}
```

### Load Balancing

Round-robin across multiple endpoints of the same model:

```json
{
  "model_list": [
    {
      "model_name": "gpt-4o",
      "model": "openai/gpt-4o",
      "api_base": "https://api1.example.com/v1",
      "api_key": "sk-key1"
    },
    {
      "model_name": "gpt-4o",
      "model": "openai/gpt-4o",
      "api_base": "https://api2.example.com/v1",
      "api_key": "sk-key2"
    }
  ]
}
```

## model_list Fields

| Field | Required | Description |
|-------|----------|-------------|
| `model_name` | Yes | Internal name — used in `agents.defaults.model_name` |
| `model` | Yes | Provider prefix + model ID sent to the API |
| `api_key` | No | API key (supports `enc://`, `file://` formats) |
| `api_base` | No | Override default API base URL |
| `auth_method` | No | Set to `"oauth"` for OAuth providers (e.g. Antigravity) |

## Credential Security

API keys support three formats:

| Format | Example | Behavior |
|--------|---------|----------|
| Plaintext | `sk-abc123` | Used as-is |
| File reference | `file://openai.key` | Read from config directory |
| Encrypted | `enc://<base64>` | Decrypted at startup |

See [Credential Encryption](./credential-encryption) for details.
