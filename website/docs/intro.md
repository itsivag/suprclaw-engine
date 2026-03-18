---
sidebar_position: 1
---

# Introduction

**SuprClaw** is an ultra-lightweight personal AI assistant written in Go.

Runs on $10 hardware with **&lt;10MB RAM**. Single binary, **1-second boot**, works across x86_64, ARM64, MIPS, and RISC-V.

## Why SuprClaw?

Most AI assistants require Electron, Node.js, or Docker with hundreds of megabytes of dependencies. SuprClaw is a single Go binary that:

- **Boots in 1 second** on a 0.6GHz single-core board
- **Uses less than 10MB RAM** — 99% lighter than Electron alternatives
- **Has no runtime dependencies** — drop it anywhere and run
- **Works on any architecture** — x86_64, ARM64, ARMv7, MIPS, RISC-V, LoongArch
- **Keeps your data local** — no telemetry, no tracking

## Key Features

| Feature | Description |
|---------|-------------|
| **Multi-Channel** | Telegram, Discord, WhatsApp, Matrix, LINE, and more |
| **Multi-Provider** | Anthropic, OpenAI, Gemini, Groq, Ollama, OpenRouter, Azure, and more |
| **Scheduled Tasks** | Built-in cron scheduler |
| **MCP Support** | Model Context Protocol integration with lazy tool loading |
| **Skills System** | Extend agent capabilities with custom skills |
| **Security Sandbox** | Agent restricted to workspace by default |
| **Credential Encryption** | AES-256-GCM encrypted API keys in config |
| **Web Search** | Perplexity, Brave, SearXNG, DuckDuckGo |
| **Voice Transcription** | Audio messages auto-transcribed via Groq Whisper |

## How It Works

```
You ──► Chat Channel ──► Gateway ──► Agent ──► AI Provider
         (Telegram,         │          │         (Anthropic,
          Discord,          │          │          OpenAI, ...)
          WhatsApp, ...)     │          │
                            │          └──► Tools (web search,
                            │               file ops, exec, ...)
                            └──► Web Console (optional)
```

The **gateway** receives messages from all configured chat channels, routes them to an **agent**, which calls the configured AI provider and available tools, then sends the response back.

## Next Steps

- [Install SuprClaw](./installation) — one-liner or precompiled binary
- [Quick Start](./quick-start) — configure and send your first message
- [Chat Channels](./channels/overview) — connect to Telegram, Discord, and more
- [Configuration](./configuration/overview) — full config reference
