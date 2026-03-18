package heartbeat

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/state"
)

// HeartbeatExecutor is implemented by *agent.AgentLoop.
type HeartbeatExecutor interface {
	ProcessHeartbeat(
		ctx context.Context,
		agentID string,
		prompt string,
		deliverChannel, deliverChatID string,
		maxTokens int,
	) (string, error)

	PruneLastTurn(agentID, sessionKey string) error
}

// HeartbeatRunConfig holds per-run configuration derived from config.HeartbeatConfig.
type HeartbeatRunConfig struct {
	AgentID             string
	Workspace           string
	IntervalMinutes     int
	MaxIntervalMinutes  int
	IdleWindowMinutes   int
	MaxTokensPerRun     int
	SkipIfUnchanged     bool
	ShowOk              bool
	AckMaxChars         int
	AdaptiveBackoff     bool
	ScheduleCfg         HeartbeatScheduleConfig
}

// RunnerDeps collects the external dependencies for a single heartbeat run.
type RunnerDeps struct {
	Cfg       HeartbeatRunConfig
	State     *HeartbeatState
	AgentLoop HeartbeatExecutor
	Bus       *bus.MessageBus
	StateMgr  *state.Manager // for last-activity detection
}

// RunOnce performs one complete heartbeat cycle and returns the emitted event.
func RunOnce(ctx context.Context, deps RunnerDeps) HeartbeatEvent {
	start := time.Now()
	cfg := deps.Cfg

	// Helper to build and emit a skipped event.
	skip := func(reason string) HeartbeatEvent {
		evt := HeartbeatEvent{
			Ts:                   start.UnixMilli(),
			Status:               StatusSkipped,
			AgentID:              cfg.AgentID,
			DurationMs:           time.Since(start).Milliseconds(),
			SkipReason:           reason,
			ConsecutiveOk:        deps.State.ConsecutiveOk,
			EffectiveIntervalMin: effectiveInterval(cfg, deps.State.ConsecutiveOk),
		}
		EmitHeartbeatEvent(evt)
		return evt
	}

	// 1. Active hours check.
	scheduleCfg := cfg.ScheduleCfg
	if inWindow, _ := IsWithinActiveHours(scheduleCfg, start); !inWindow {
		logger.DebugCF("heartbeat", "Skipping heartbeat: outside active hours", nil)
		return skip("active_hours")
	}

	// 2. Idle window check — skip if user was recently active.
	if deps.StateMgr != nil && cfg.IdleWindowMinutes > 0 {
		lastActivity := deps.StateMgr.GetTimestamp()
		if IsIdleWindowActive(lastActivity, cfg.IdleWindowMinutes, start) {
			logger.DebugCF("heartbeat", "Skipping heartbeat: user recently active",
				map[string]any{"idle_window_min": cfg.IdleWindowMinutes})
			return skip("idle_window")
		}
	}

	// 3. Read and hash HEARTBEAT.md.
	heartbeatFile := filepath.Join(cfg.Workspace, "HEARTBEAT.md")
	fileContent, err := os.ReadFile(heartbeatFile)
	if err != nil {
		if os.IsNotExist(err) {
			logger.DebugCF("heartbeat", "HEARTBEAT.md not found, skipping", nil)
			return skip("no_file")
		}
		logger.ErrorCF("heartbeat", "Failed to read HEARTBEAT.md",
			map[string]any{"error": err.Error()})
		evt := HeartbeatEvent{
			Ts:                   start.UnixMilli(),
			Status:               StatusFailed,
			AgentID:              cfg.AgentID,
			DurationMs:           time.Since(start).Milliseconds(),
			ConsecutiveOk:        deps.State.ConsecutiveOk,
			EffectiveIntervalMin: effectiveInterval(cfg, deps.State.ConsecutiveOk),
		}
		EmitHeartbeatEvent(evt)
		return evt
	}

	hash := hashContent(fileContent)

	// 4. Skip if file unchanged (and feature enabled).
	if cfg.SkipIfUnchanged && hash == deps.State.LastFileHash {
		logger.DebugCF("heartbeat", "Skipping heartbeat: HEARTBEAT.md unchanged", nil)
		return skip("unchanged_file")
	}

	// 5. Build prompt.
	prompt := buildPrompt(string(fileContent), start)

	// 6. Determine delivery target.
	var deliverChannel, deliverChatID string
	if deps.StateMgr != nil {
		lastCh := deps.StateMgr.GetLastChannel()
		if lastCh != "" {
			parts := strings.SplitN(lastCh, ":", 2)
			if len(parts) == 2 {
				deliverChannel = parts[0]
				deliverChatID = parts[1]
			}
		}
	}

	// 7. Call agent loop.
	logger.InfoCF("heartbeat", "Running heartbeat",
		map[string]any{
			"agent_id":    cfg.AgentID,
			"max_tokens":  cfg.MaxTokensPerRun,
			"deliver_to":  deliverChannel + ":" + deliverChatID,
		})

	response, runErr := deps.AgentLoop.ProcessHeartbeat(
		ctx,
		cfg.AgentID,
		prompt,
		deliverChannel,
		deliverChatID,
		cfg.MaxTokensPerRun,
	)

	deps.State.LastFileHash = hash
	deps.State.LastRunAtMs = time.Now().UnixMilli()

	if runErr != nil {
		logger.ErrorCF("heartbeat", "Heartbeat agent run failed",
			map[string]any{"error": runErr.Error()})
		evt := HeartbeatEvent{
			Ts:                   start.UnixMilli(),
			Status:               StatusFailed,
			AgentID:              cfg.AgentID,
			DurationMs:           time.Since(start).Milliseconds(),
			ConsecutiveOk:        deps.State.ConsecutiveOk,
			EffectiveIntervalMin: effectiveInterval(cfg, deps.State.ConsecutiveOk),
		}
		EmitHeartbeatEvent(evt)
		return evt
	}

	// 8. Evaluate response via policy.
	ackMax := cfg.AckMaxChars
	stripped := StripHeartbeatToken(response, ackMax)

	var evt HeartbeatEvent

	if stripped.ShouldSkip || IsEffectivelyEmpty(stripped.Text) {
		// Nothing to report — prune and increment backoff counter.
		status := StatusOkToken
		if IsEffectivelyEmpty(response) {
			status = StatusOkEmpty
		}

		// Prune the idle turn from session to avoid accumulation.
		if err := pruneLastTurn(deps, cfg.AgentID); err != nil {
			logger.WarnCF("heartbeat", "Failed to prune last turn",
				map[string]any{"error": err.Error()})
		}

		deps.State.ConsecutiveOk++

		evt = HeartbeatEvent{
			Ts:                   start.UnixMilli(),
			Status:               status,
			AgentID:              cfg.AgentID,
			DurationMs:           time.Since(start).Milliseconds(),
			Preview:              truncate(stripped.Text, 80),
			ConsecutiveOk:        deps.State.ConsecutiveOk,
			EffectiveIntervalMin: effectiveInterval(cfg, deps.State.ConsecutiveOk),
		}

		if cfg.ShowOk && deliverChannel != "" && deliverChatID != "" {
			pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_ = deps.Bus.PublishOutbound(pubCtx, bus.OutboundMessage{
				Channel: deliverChannel,
				ChatID:  deliverChatID,
				Content: stripped.Text,
			})
		}
	} else {
		// Real content — deliver and reset backoff.
		deps.State.ConsecutiveOk = 0

		evt = HeartbeatEvent{
			Ts:                   start.UnixMilli(),
			Status:               StatusSent,
			AgentID:              cfg.AgentID,
			DurationMs:           time.Since(start).Milliseconds(),
			Preview:              truncate(stripped.Text, 80),
			ConsecutiveOk:        0,
			EffectiveIntervalMin: effectiveInterval(cfg, 0),
		}

		if deliverChannel != "" && deliverChatID != "" {
			pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if pubErr := deps.Bus.PublishOutbound(pubCtx, bus.OutboundMessage{
				Channel: deliverChannel,
				ChatID:  deliverChatID,
				Content: stripped.Text,
			}); pubErr != nil {
				logger.WarnCF("heartbeat", "Failed to deliver heartbeat response",
					map[string]any{"error": pubErr.Error()})
			} else {
				logger.InfoCF("heartbeat", "Heartbeat response delivered",
					map[string]any{
						"channel": deliverChannel,
						"chat_id": deliverChatID,
						"len":     len(stripped.Text),
					})
			}
		} else {
			logger.WarnCF("heartbeat", "No delivery target — response not sent",
				map[string]any{"response_preview": truncate(stripped.Text, 80)})
		}
	}

	EmitHeartbeatEvent(evt)
	return evt
}

func pruneLastTurn(deps RunnerDeps, agentID string) error {
	if deps.AgentLoop == nil {
		return nil
	}
	sessionKey := fmt.Sprintf("agent:%s:main", agentID)
	return deps.AgentLoop.PruneLastTurn(agentID, sessionKey)
}

func buildPrompt(fileContent string, now time.Time) string {
	return fmt.Sprintf("[Heartbeat check at %s]\n\n%s",
		now.UTC().Format(time.RFC3339),
		fileContent,
	)
}

func hashContent(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func effectiveInterval(cfg HeartbeatRunConfig, consecutiveOk int) int {
	if !cfg.AdaptiveBackoff {
		return cfg.IntervalMinutes
	}
	d := AdaptiveInterval(cfg.IntervalMinutes, cfg.MaxIntervalMinutes, consecutiveOk)
	return int(d.Minutes())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// heartbeatRunConfigFromCfg builds a HeartbeatRunConfig from config.HeartbeatConfig.
func heartbeatRunConfigFromCfg(cfg config.HeartbeatConfig, workspace string) HeartbeatRunConfig {
	return HeartbeatRunConfig{
		AgentID:            cfg.AgentID,
		Workspace:          workspace,
		IntervalMinutes:    cfg.IntervalMinutes,
		MaxIntervalMinutes: cfg.MaxIntervalMinutes,
		IdleWindowMinutes:  cfg.IdleWindowMinutes,
		MaxTokensPerRun:    cfg.MaxTokensPerRun,
		SkipIfUnchanged:    cfg.SkipIfUnchanged,
		ShowOk:             cfg.ShowOk,
		AckMaxChars:        cfg.AckMaxChars,
		AdaptiveBackoff:    cfg.AdaptiveBackoff,
		ScheduleCfg: HeartbeatScheduleConfig{
			ActiveHoursStart: cfg.ActiveHoursStart,
			ActiveHoursEnd:   cfg.ActiveHoursEnd,
			Timezone:         cfg.Timezone,
		},
	}
}
