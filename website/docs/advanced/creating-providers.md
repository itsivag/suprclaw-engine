---
sidebar_position: 5
---

# Creating a New Provider

SuprClaw providers are implemented as Go packages under `pkg/providers/`. Adding a new provider requires no changes to the config system.

## Step-by-Step

### 1. Create the Provider File

```
pkg/providers/
└── your_provider.go
```

### 2. Implement the Provider Interface

```go
package providers

type YourProvider struct {
    apiKey  string
    apiBase string
}

func NewYourProvider(apiKey, apiBase, proxy string) *YourProvider {
    if apiBase == "" {
        apiBase = "https://api.your-provider.com/v1"
    }
    return &YourProvider{apiKey: apiKey, apiBase: apiBase}
}

func (p *YourProvider) Chat(ctx context.Context, messages []Message, tools []Tool, cb StreamCallback) error {
    // Implement streaming chat completion
}
```

### 3. Register in the Factory

Add to the protocol switch in `pkg/providers/factory.go`:

```go
case "your-provider":
    return NewYourProvider(sel.apiKey, sel.apiBase, sel.proxy), nil
```

### 4. Add Default Config (Optional)

Add a default entry in `pkg/config/defaults.go`:

```go
{
    ModelName: "your-model",
    Model:     "your-provider/model-name",
    APIKey:    "",
},
```

### 5. Add Auth Support (Optional)

If your provider requires OAuth, add a case to `cmd/suprclaw/cmd_auth.go`:

```go
case "your-provider":
    authLoginYourProvider()
```

## Configure in config.json

```json
{
  "model_list": [
    {
      "model_name": "your-model",
      "model": "your-provider/model-name",
      "api_key": "your-api-key",
      "api_base": "https://api.your-provider.com/v1"
    }
  ]
}
```

## Testing

```bash
# Authenticate
suprclaw auth login --provider your-provider

# Test a message
suprclaw agent -m "Hello" --model your-model

# Start the gateway
suprclaw gateway
```

## Source Files Reference

| File | Purpose |
|------|---------|
| `pkg/providers/factory.go` | Provider factory and protocol routing |
| `pkg/providers/types.go` | Provider interface definitions |
| `pkg/config/defaults.go` | Default model configurations |
| `cmd/suprclaw/cmd_auth.go` | Auth CLI commands |
| `pkg/auth/oauth.go` | OAuth flow implementation |
| `pkg/auth/store.go` | Auth credential storage |
