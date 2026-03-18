---
sidebar_position: 4
---

# Antigravity (Google Cloud Code Assist)

Antigravity is Google's Cloud Code Assist provider — it gives access to Claude and Gemini models via Google's infrastructure, often at no cost with generous quotas.

## Prerequisites

- A Google account
- Google Cloud Code Assist enabled (via "Gemini for Google Cloud" onboarding)

## Authentication

```bash
suprclaw auth login --provider antigravity
```

This opens a browser for Google OAuth. On completion, credentials are saved to `~/.suprclaw/auth.json`.

### Headless / VPS Authentication

If running on a server without a browser:

1. Run the auth command — it displays a URL
2. Open the URL in your **local** browser
3. Complete Google login
4. Your browser redirects to `localhost:51121` (which fails to load)
5. **Copy the full URL** from your browser's address bar
6. **Paste it back** into the terminal where SuprClaw is waiting

## Configuration

```json
{
  "model_list": [
    {
      "model_name": "gemini-flash",
      "model": "antigravity/gemini-3-flash",
      "auth_method": "oauth"
    }
  ],
  "agents": {
    "defaults": { "model_name": "gemini-flash" }
  }
}
```

## Available Models

```bash
suprclaw auth models
```

Reliable models based on testing:

| Model | Description |
|-------|-------------|
| `gemini-3-flash` | Fast, highly available |
| `gemini-2.5-flash-lite` | Lightweight |
| `claude-opus-4-6-thinking` | Powerful, includes reasoning |

Use the short ID (e.g. `gemini-3-flash`), not the full path.

## Copying Credentials to a Server

If authenticated locally, copy credentials to the server:

```bash
scp ~/.suprclaw/auth.json user@your-server:~/.suprclaw/
```

## Troubleshooting

| Issue | Fix |
|-------|-----|
| Empty response | Model may be restricted. Try `gemini-3-flash` |
| 429 Rate Limit | Quota exhausted — check reset time in error message |
| 404 Not Found | Use a model ID from `suprclaw auth models` |
| Token expired | Re-run `suprclaw auth login --provider antigravity` |
| Gemini not enabled | Enable in Google Cloud Console |
