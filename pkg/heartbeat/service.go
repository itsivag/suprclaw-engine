package heartbeat

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/state"
)

// HeartbeatService manages the heartbeat lifecycle and scheduling loop.
type HeartbeatService struct {
	cfg       config.HeartbeatConfig
	workspace string
	agentLoop HeartbeatExecutor
	msgBus    *bus.MessageBus
	stateMgr  *state.Manager

	mu       sync.Mutex
	running  bool
	stopChan chan struct{}
	wakeChan chan struct{}
	cancel   context.CancelFunc
}

// NewHeartbeatService creates a new service. Call Start() to begin scheduling.
func NewHeartbeatService(
	cfg config.HeartbeatConfig,
	workspace string,
	agentLoop HeartbeatExecutor,
	msgBus *bus.MessageBus,
	stateMgr *state.Manager,
) *HeartbeatService {
	return &HeartbeatService{
		cfg:       cfg,
		workspace: workspace,
		agentLoop: agentLoop,
		msgBus:    msgBus,
		stateMgr:  stateMgr,
		wakeChan:  make(chan struct{}, 1),
	}
}

// Start begins the heartbeat scheduling loop.
func (s *HeartbeatService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	if !s.cfg.Enabled {
		return nil
	}

	s.stopChan = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true

	go s.runLoop(ctx, s.stopChan)

	logger.InfoCF("heartbeat", "Heartbeat service started",
		map[string]any{
			"interval_min": s.cfg.IntervalMinutes,
			"agent_id":     s.cfg.AgentID,
		})

	return nil
}

// Stop halts the heartbeat scheduling loop, canceling any in-flight run.
func (s *HeartbeatService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	s.running = false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.stopChan != nil {
		close(s.stopChan)
		s.stopChan = nil
	}

	logger.InfoCF("heartbeat", "Heartbeat service stopped", nil)
}

// IsRunning returns whether the service is currently active.
func (s *HeartbeatService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Wake signals the service to run a heartbeat tick immediately (best-effort).
func (s *HeartbeatService) Wake() {
	select {
	case s.wakeChan <- struct{}{}:
	default:
	}
}

func (s *HeartbeatService) runLoop(ctx context.Context, stopChan chan struct{}) {
	runCfg := heartbeatRunConfigFromCfg(s.cfg, s.workspace)

	// Load persisted state.
	hbState, err := LoadState(s.workspace)
	if err != nil {
		logger.WarnCF("heartbeat", "Failed to load heartbeat state, starting fresh",
			map[string]any{"error": err.Error()})
		hbState = &HeartbeatState{}
	}

	timer := time.NewTimer(s.initialDelay(runCfg, hbState))
	defer timer.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ctx.Done():
			return
		case <-s.wakeChan:
			// Early wake — drain timer if it hasn't fired and run now.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			// Scheduled tick.
		}

		// Run one heartbeat cycle.
		deps := RunnerDeps{
			Cfg:       runCfg,
			State:     hbState,
			AgentLoop: s.agentLoop,
			Bus:       s.msgBus,
			StateMgr:  s.stateMgr,
		}

		evt := RunOnce(ctx, deps)

		// Persist updated state.
		if saveErr := SaveState(s.workspace, hbState); saveErr != nil {
			logger.WarnCF("heartbeat", "Failed to save heartbeat state",
				map[string]any{"error": saveErr.Error()})
		}

		// Compute next tick.
		nextTick := s.nextTickDuration(runCfg, hbState, evt)

		logger.DebugCF("heartbeat", "Next heartbeat scheduled",
			map[string]any{
				"next_in":        nextTick.String(),
				"consecutive_ok": hbState.ConsecutiveOk,
			})

		timer.Reset(nextTick)
	}
}

// initialDelay computes the first delay before the first heartbeat run.
// If the service was recently run (within the configured interval), it waits
// for the remainder of the interval rather than running immediately.
func (s *HeartbeatService) initialDelay(cfg HeartbeatRunConfig, st *HeartbeatState) time.Duration {
	interval := AdaptiveInterval(cfg.IntervalMinutes, cfg.MaxIntervalMinutes, st.ConsecutiveOk)
	if st.LastRunAtMs == 0 {
		return interval
	}
	elapsed := time.Since(time.UnixMilli(st.LastRunAtMs))
	if elapsed >= interval {
		return 0
	}
	return interval - elapsed
}

// nextTickDuration computes the wait before the next heartbeat.
// If outside active hours, returns the time until the next window opens.
func (s *HeartbeatService) nextTickDuration(cfg HeartbeatRunConfig, st *HeartbeatState, evt HeartbeatEvent) time.Duration {
	now := time.Now()

	// If the last event was skipped due to active hours, sleep until window opens.
	if evt.SkipReason == "active_hours" {
		_, nextWindowStart := IsWithinActiveHours(cfg.ScheduleCfg, now)
		if !nextWindowStart.IsZero() {
			d := nextWindowStart.Sub(now)
			if d > 0 {
				return d
			}
		}
	}

	if cfg.AdaptiveBackoff {
		return AdaptiveInterval(cfg.IntervalMinutes, cfg.MaxIntervalMinutes, st.ConsecutiveOk)
	}
	return time.Duration(cfg.IntervalMinutes) * time.Minute
}

// Validate returns an error if the config is invalid.
func validateConfig(cfg config.HeartbeatConfig) error {
	if cfg.IntervalMinutes < 5 {
		return fmt.Errorf("heartbeat interval_minutes must be at least 5 (got %d)", cfg.IntervalMinutes)
	}
	return nil
}
