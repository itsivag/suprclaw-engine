package cron

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// WebhookConfig holds configuration for the cron result webhook.
type WebhookConfig struct {
	Enabled  bool
	Endpoint string
	Secret   string // Bearer token
	UserID   string // optional
}

// WebhookEvent is the payload sent to the webhook endpoint after a successful cron job execution.
type WebhookEvent struct {
	Type      string `json:"type"`
	EventID   string `json:"eventId"`
	MessageID string `json:"messageId"`
	ThreadKey string `json:"threadKey"`
	CreatedAt string `json:"createdAt"` // RFC3339
	Source    string `json:"source"`    // "cron"
	AgentID   string `json:"agentId"`
	AgentName string `json:"agentName"`
	JobID     string `json:"jobId"`
	JobName   string `json:"jobName"`
	Content   string `json:"content"`
	UserID    string `json:"userId,omitempty"`
}

// CronEventID returns a stable 32-char hex event ID for a given job execution.
// Formula: sha256(jobID + ":" + executionMS)[:16] → 32-char hex string.
func CronEventID(jobID string, executionMS int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", jobID, executionMS)))
	return fmt.Sprintf("%x", h[:16])
}

// SendWebhook marshals the event and delivers it to the configured endpoint,
// retrying up to 3 times on transient errors (network, 5xx, 429).
func SendWebhook(ctx context.Context, cfg WebhookConfig, event WebhookEvent) {
	body, err := json.Marshal(event)
	if err != nil {
		log.Printf("[webhook] failed to marshal event %s: %v", event.EventID, err)
		return
	}

	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
		if err != nil {
			log.Printf("[webhook] failed to create request (attempt %d): %v", attempt, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+cfg.Secret)
		req.Header.Set("Idempotency-Key", event.EventID)

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[webhook] network error (attempt %d/%d): %v", attempt, 3, err)
		} else {
			statusCode := resp.StatusCode
			resp.Body.Close()
			if statusCode >= 200 && statusCode < 300 {
				log.Printf("[webhook] delivered event %s (status %d)", event.EventID, statusCode)
				return
			}
			if statusCode >= 400 && statusCode != 429 {
				log.Printf("[webhook] non-retryable error for event %s (status %d)", event.EventID, statusCode)
				return
			}
			log.Printf("[webhook] retryable status %d for event %s (attempt %d/%d)", statusCode, event.EventID, attempt, 3)
		}

		if attempt < 3 {
			select {
			case <-ctx.Done():
				log.Printf("[webhook] context cancelled, stopping retries for event %s", event.EventID)
				return
			case <-time.After(delays[attempt-1]):
			}
		}
	}

	log.Printf("[webhook] all 3 attempts failed for event %s", event.EventID)
}
