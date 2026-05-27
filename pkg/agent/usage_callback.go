package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/providers"
)

const hostedUsageCallbackTimeout = 10 * time.Second

type hostedUsageCallbackPayload struct {
	CallType         string                      `json:"call_type"`
	Model            string                      `json:"model"`
	PromptTokens     int                         `json:"prompt_tokens"`
	CompletionTokens int                         `json:"completion_tokens"`
	TotalTokens      int                         `json:"total_tokens"`
	Metadata         hostedUsageCallbackMetadata `json:"metadata"`
}

type hostedUsageCallbackMetadata struct {
	UserAPIKeyUserID   string                  `json:"user_api_key_user_id"`
	UserAPIKeyMetadata hostedUsageCallbackTier `json:"user_api_key_metadata"`
}

type hostedUsageCallbackTier struct {
	Tier string `json:"tier,omitempty"`
}

func reportHostedUsageAsync(parent context.Context, model string, usage *providers.UsageInfo) {
	if usage == nil || usage.PromptTokens <= 0 || usage.TotalTokens <= 0 {
		return
	}

	callbackURL := strings.TrimSpace(os.Getenv("SUPRCLAW_USAGE_CALLBACK_URL"))
	userID := strings.TrimSpace(os.Getenv("SUPRCLAW_USAGE_CALLBACK_USER_ID"))
	if callbackURL == "" || userID == "" {
		return
	}

	secret := strings.TrimSpace(os.Getenv("SUPRCLAW_USAGE_CALLBACK_SECRET"))
	tier := strings.TrimSpace(os.Getenv("SUPRCLAW_USAGE_CALLBACK_TIER"))
	modelName := strings.TrimSpace(model)
	if modelName == "" {
		modelName = "unknown"
	}

	payload := hostedUsageCallbackPayload{
		CallType:         "completion",
		Model:            modelName,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		Metadata: hostedUsageCallbackMetadata{
			UserAPIKeyUserID: userID,
			UserAPIKeyMetadata: hostedUsageCallbackTier{
				Tier: tier,
			},
		},
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), hostedUsageCallbackTimeout)
		defer cancel()

		body, err := json.Marshal(payload)
		if err != nil {
			logger.ErrorCF("agent", "Failed to encode hosted usage callback payload", map[string]any{
				"error": err.Error(),
			})
			return
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
		if err != nil {
			logger.ErrorCF("agent", "Failed to create hosted usage callback request", map[string]any{
				"error": err.Error(),
			})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			logger.ErrorCF("agent", "Hosted usage callback request failed", map[string]any{
				"error": err.Error(),
				"model": modelName,
			})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			logger.ErrorCF("agent", "Hosted usage callback rejected", map[string]any{
				"status": resp.StatusCode,
				"model":  modelName,
			})
			return
		}

		logger.DebugCF("agent", "Hosted usage callback delivered", map[string]any{
			"model":             modelName,
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
		})
	}()
}
