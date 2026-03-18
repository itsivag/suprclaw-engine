---
sidebar_position: 2
---

# Installation

## One-Liner (Recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/itsivag/suprclaw-engine/main/install.sh | sh
```

Auto-detects OS and architecture. Installs to `/usr/local/bin` or `~/.local/bin`.

**Supported platforms:** Linux x86_64, ARM64, ARMv7, RISC-V, MIPS, LoongArch — macOS x86_64, ARM64

## Precompiled Binary

Download from the [releases page](https://github.com/itsivag/suprclaw-engine/releases) and place in your `$PATH`.

## From Source

```bash
git clone https://github.com/itsivag/suprclaw-engine.git
cd suprclaw-engine
make deps
make build
```

### Build Targets

```bash
make build              # Current platform
make build-all          # All platforms
make build-linux-arm64  # ARM64
make build-linux-arm    # ARM 32-bit
make install            # Build and install to PATH
```

**WhatsApp native support:**

```bash
make build-whatsapp-native
```

## Docker

```bash
# Clone
git clone https://github.com/itsivag/suprclaw-engine.git
cd suprclaw-engine

# First run — generates docker/data/config.json then exits
docker compose -f docker/docker-compose.yml --profile gateway up

# Edit config
vim docker/data/config.json

# Start in background
docker compose -f docker/docker-compose.yml --profile gateway up -d

# View logs
docker compose -f docker/docker-compose.yml logs -f suprclaw-gateway

# Stop
docker compose -f docker/docker-compose.yml --profile gateway down
```

:::tip
By default the Gateway listens on `127.0.0.1`. Set `SUPRCLAW_GATEWAY_HOST=0.0.0.0` to expose it to the host when running in Docker.
:::

### Web Console (Docker)

```bash
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

Open http://localhost:18800. The launcher manages the gateway process automatically.

:::warning
The web console has no authentication. Do not expose it to the public internet.
:::

## Verify Installation

```bash
suprclaw --version
```
