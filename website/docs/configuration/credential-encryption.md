---
sidebar_position: 4
---

# Credential Encryption

SuprClaw supports encrypting `api_key` values in `model_list` using AES-256-GCM. Encrypted keys are stored as `enc://<base64>` strings and decrypted automatically at startup.

## Quick Start

**1. Set your passphrase**

```bash
export SUPRCLAW_KEY_PASSPHRASE="your-passphrase"
```

**2. Run onboard**

```bash
suprclaw onboard
```

This generates a dedicated SSH key (`~/.ssh/suprclaw_ed25519.key`) and re-encrypts any plaintext `api_key` values in config automatically.

**3. The config will contain encrypted keys:**

```json
{
  "model_list": [
    {
      "model_name": "gpt-4o",
      "api_key": "enc://AAAA...base64...",
      "base_url": "https://api.openai.com/v1"
    }
  ]
}
```

## Supported Key Formats

| Format | Example | Behavior |
|--------|---------|----------|
| Plaintext | `sk-abc123` | Used as-is |
| File reference | `file://openai.key` | Content read from config directory |
| Encrypted | `enc://<base64>` | Decrypted at startup using `SUPRCLAW_KEY_PASSPHRASE` |
| Empty | `""` | Passed through (used with `auth_method: oauth`) |

## Cryptographic Design

### Key Derivation

Uses **HKDF-SHA256** with an optional SSH private key as a second factor.

```
Without SSH key (passphrase only):
  ikm     = SHA256(passphrase)
  aes_key = HKDF-SHA256(ikm, salt, info="suprclaw-credential-v1", 32 bytes)

With SSH key (recommended):
  sshHash = SHA256(ssh_private_key_file_bytes)
  ikm     = HMAC-SHA256(key=sshHash, message=passphrase)
  aes_key = HKDF-SHA256(ikm, salt, info="suprclaw-credential-v1", 32 bytes)
```

### Encryption

```
AES-256-GCM(key=aes_key, nonce=random[12], plaintext=api_key)
```

### Wire Format

```
enc://<base64( salt[16] + nonce[12] + ciphertext )>
```

## Two-Factor Security with SSH Key

When an SSH private key is provided, breaking encryption requires **both**:

1. The **passphrase** (`SUPRCLAW_KEY_PASSPHRASE`)
2. The **SSH private key file**

| Attacker Has | Can Decrypt? |
|---|---|
| Config file only | No |
| SSH key only | No |
| Passphrase only | No |
| Config + SSH key + passphrase | Yes |

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `SUPRCLAW_KEY_PASSPHRASE` | Yes (for `enc://`) | Passphrase for key derivation |
| `SUPRCLAW_SSH_KEY_PATH` | No | Path to SSH private key |

SSH key is auto-detected at `~/.ssh/suprclaw_ed25519.key`. To use passphrase-only mode:

```bash
export SUPRCLAW_SSH_KEY_PATH=""
```

## Performance

| Operation | Time (ARM Cortex-A) |
|-----------|---------------------|
| Key derivation (HKDF) | < 1 ms |
| AES-256-GCM decrypt | < 1 ms |
| **Total startup overhead** | **< 2 ms per key** |

## Migration to a New Machine

1. Copy `~/.suprclaw/config.json` to the new machine
2. Set `SUPRCLAW_KEY_PASSPHRASE` to the same value
3. Copy `~/.ssh/suprclaw_ed25519.key` to the same path

No re-encryption needed.

:::warning
In passphrase-only mode (`SUPRCLAW_SSH_KEY_PATH=""`), use a strong passphrase (≥32 random characters). Without the SSH key, a weak passphrase can be brute-forced offline.
:::
