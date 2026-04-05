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

type heartbeatJobRuntime struct {
	index int
	cfg   HeartbeatRunConfig
	state *HeartbeatState
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
	if err := validateConfig(s.cfg); err != nil {
		return err
	}

	s.stopChan = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true

	go s.runLoop(ctx, s.stopChan)

	logger.InfoCF("heartbeat", "Heartbeat service started",
		map[string]any{
			"jobs":            len(s.cfg.Jobs),
			"minimum_gap_min": s.cfg.MinimumGapMinutes,
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
	jobStates, err := LoadStates(s.workspace)
	if err != nil {
		logger.WarnCF("heartbeat", "Failed to load heartbeat state, starting fresh",
			map[string]any{"error": err.Error()})
		jobStates = map[string]*HeartbeatState{}
	}

	jobs := s.buildRuntimeJobs(jobStates)
	if len(jobs) == 0 {
		logger.WarnCF("heartbeat", "Heartbeat enabled but no jobs available; orchestrator stopped", nil)
		return
	}

	minGap := time.Duration(s.cfg.MinimumGapMinutes) * time.Minute
	var nextAllowedStart time.Time

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		jobIndex, scheduledAt := s.selectNextJob(jobs, time.Now(), nextAllowedStart)
		if jobIndex < 0 {
			return
		}
		wait := time.Until(scheduledAt)
		if wait < 0 {
			wait = 0
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)

		select {
		case <-stopChan:
			return
		case <-ctx.Done():
			return
		case <-s.wakeChan:
			// Re-evaluate scheduling immediately.
			continue
		case <-timer.C:
			// Run the selected due job.
		}

		job := jobs[jobIndex]
		runStart := time.Now()
		deps := RunnerDeps{
			Cfg:       job.cfg,
			State:     job.state,
			AgentLoop: s.agentLoop,
			Bus:       s.msgBus,
			StateMgr:  s.stateMgr,
		}
		evt := RunOnce(ctx, deps)
		runFinishedAt := time.Now()
		nextAllowedStart = runFinishedAt.Add(minGap)

		if saveErr := SaveStates(s.workspace, jobStates); saveErr != nil {
			logger.WarnCF("heartbeat", "Failed to save heartbeat state",
				map[string]any{"error": saveErr.Error()})
		}

		logger.DebugCF("heartbeat", "Heartbeat job completed",
			map[string]any{
				"agent_id":           job.cfg.AgentID,
				"status":             evt.Status,
				"duration_ms":        time.Since(runStart).Milliseconds(),
				"next_allowed_start": nextAllowedStart.Format(time.RFC3339),
			})
	}
}

func (s *HeartbeatService) buildRuntimeJobs(states map[string]*HeartbeatState) []heartbeatJobRuntime {
	jobs := make([]heartbeatJobRuntime, 0, len(s.cfg.Jobs))
	for i, jobCfg := range s.cfg.Jobs {
		jobState, ok := states[jobCfg.AgentID]
		if !ok || jobState == nil {
			jobState = &HeartbeatState{}
			states[jobCfg.AgentID] = jobState
		}
		jobs = append(jobs, heartbeatJobRuntime{
			index: i,
			cfg:   heartbeatRunConfigFromJob(jobCfg, s.workspace),
			state: jobState,
		})
	}
	return jobs
}

func (s *HeartbeatService) selectNextJob(
	jobs []heartbeatJobRuntime,
	now time.Time,
	nextAllowedStart time.Time,
) (int, time.Time) {
	if len(jobs) == 0 {
		return -1, now
	}

	selectedIndex := -1
	var selectedDue time.Time
	for i, job := range jobs {
		due := s.jobDueAt(job, now)
		if selectedIndex == -1 || due.Before(selectedDue) {
			selectedIndex = i
			selectedDue = due
		}
	}

	if nextAllowedStart.After(selectedDue) {
		selectedDue = nextAllowedStart
	}
	return selectedIndex, selectedDue
}

func (s *HeartbeatService) jobDueAt(job heartbeatJobRuntime, now time.Time) time.Time {
	interval := time.Duration(job.cfg.IntervalMinutes) * time.Minute
	if job.cfg.AdaptiveBackoff {
		interval = AdaptiveInterval(job.cfg.IntervalMinutes, job.cfg.MaxIntervalMinutes, job.state.ConsecutiveOk)
	}

	var dueAt time.Time
	if job.state.LastRunAtMs == 0 {
		dueAt = now.Add(interval)
	} else {
		dueAt = time.UnixMilli(job.state.LastRunAtMs).Add(interval)
		if dueAt.Before(now) {
			dueAt = now
		}
	}

	if inWindow, nextWindowStart := IsWithinActiveHours(job.cfg.ScheduleCfg, dueAt); !inWindow && !nextWindowStart.IsZero() {
		return nextWindowStart
	}
	return dueAt
}

// Validate returns an error if the config is invalid.
func validateConfig(cfg config.HeartbeatConfig) error {
	if cfg.MinimumGapMinutes <= 0 {
		return fmt.Errorf("heartbeat minimum_gap_minutes must be > 0 (got %d)", cfg.MinimumGapMinutes)
	}
	if cfg.Enabled && len(cfg.Jobs) == 0 {
		return fmt.Errorf("heartbeat jobs must be non-empty when enabled")
	}

	seenAgents := make(map[string]struct{}, len(cfg.Jobs))
	for i, job := range cfg.Jobs {
		if job.AgentID == "" {
			return fmt.Errorf("heartbeat jobs[%d].agent_id is required", i)
		}
		if _, exists := seenAgents[job.AgentID]; exists {
			return fmt.Errorf("heartbeat jobs[%d].agent_id %q is duplicated", i, job.AgentID)
		}
		seenAgents[job.AgentID] = struct{}{}
		if job.IntervalMinutes < 5 {
			return fmt.Errorf("heartbeat jobs[%d].interval_minutes must be at least 5 (got %d)", i, job.IntervalMinutes)
		}
	}
	return nil
}
