package heartbeat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/state"
)

// HeartbeatService manages the heartbeat lifecycle and scheduling loop.
type HeartbeatService struct {
	cfg            config.HeartbeatConfig
	timezone       string
	stateWorkspace string
	agentDefaults  config.AgentDefaults
	agents         []config.AgentConfig
	agentLoop      HeartbeatExecutor
	msgBus         *bus.MessageBus
	stateMgr       *state.Manager

	mu       sync.Mutex
	running  bool
	stopChan chan struct{}
	wakeChan chan struct{}
	cancel   context.CancelFunc
}

type heartbeatJobRuntime struct {
	index     int
	cfg       HeartbeatRunConfig
	state     *HeartbeatState
	nextDueAt time.Time
}

// NewHeartbeatService creates a new service. Call Start() to begin scheduling.
func NewHeartbeatService(
	cfg config.HeartbeatConfig,
	timezone string,
	stateWorkspace string,
	agentDefaults config.AgentDefaults,
	agents []config.AgentConfig,
	agentLoop HeartbeatExecutor,
	msgBus *bus.MessageBus,
	stateMgr *state.Manager,
) *HeartbeatService {
	return &HeartbeatService{
		cfg:            cfg,
		timezone:       timezone,
		stateWorkspace: stateWorkspace,
		agentDefaults:  agentDefaults,
		agents:         append([]config.AgentConfig(nil), agents...),
		agentLoop:      agentLoop,
		msgBus:         msgBus,
		stateMgr:       stateMgr,
		wakeChan:       make(chan struct{}, 1),
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
	if err := validateConfig(s.cfg, s.timezone); err != nil {
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
	jobStates, err := LoadStates(s.stateWorkspace)
	if err != nil {
		logger.WarnCF("heartbeat", "Failed to load heartbeat state, starting fresh",
			map[string]any{"error": err.Error()})
		jobStates = map[string]*HeartbeatState{}
	}

	serviceStart := time.Now()
	jobs := s.buildRuntimeJobs(jobStates, serviceStart)
	if len(jobs) == 0 {
		logger.WarnCF("heartbeat", "Heartbeat enabled but no jobs available; orchestrator stopped", nil)
		return
	}

	minGap := time.Duration(s.cfg.MinimumGapMinutes) * time.Minute
	var nextAllowedStart time.Time

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		jobIndex, scheduledAt := s.selectNextJob(jobs, nextAllowedStart)
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

		job := &jobs[jobIndex]
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
		job.nextDueAt = runFinishedAt.Add(intervalDuration(job.cfg, job.state.ConsecutiveOk))
		nextAllowedStart = runFinishedAt.Add(minGap)

		if saveErr := SaveStates(s.stateWorkspace, jobStates); saveErr != nil {
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

func (s *HeartbeatService) buildRuntimeJobs(states map[string]*HeartbeatState, serviceStart time.Time) []heartbeatJobRuntime {
	jobs := make([]heartbeatJobRuntime, 0, len(s.cfg.Jobs))
	for i, jobCfg := range s.cfg.Jobs {
		jobState, ok := states[jobCfg.AgentID]
		if !ok || jobState == nil {
			jobState = &HeartbeatState{}
			states[jobCfg.AgentID] = jobState
		}
		workspace := config.ResolveAgentWorkspaceByID(jobCfg.AgentID, s.agents, s.agentDefaults)
		runtimeCfg := heartbeatRunConfigFromJob(jobCfg, workspace, s.timezone)
		job := heartbeatJobRuntime{
			index: i,
			cfg:   runtimeCfg,
			state: jobState,
		}
		job.nextDueAt = s.initialDueAt(job, serviceStart)
		jobs = append(jobs, job)
	}
	return jobs
}

func (s *HeartbeatService) selectNextJob(
	jobs []heartbeatJobRuntime,
	nextAllowedStart time.Time,
) (int, time.Time) {
	if len(jobs) == 0 {
		return -1, time.Time{}
	}

	selectedIndex := -1
	var selectedDue time.Time
	for i, job := range jobs {
		due := s.adjustToActiveHours(job.cfg.ScheduleCfg, job.nextDueAt)
		if nextAllowedStart.After(due) {
			due = nextAllowedStart
		}
		due = s.adjustToActiveHours(job.cfg.ScheduleCfg, due)
		if selectedIndex == -1 || due.Before(selectedDue) {
			selectedIndex = i
			selectedDue = due
		}
	}

	return selectedIndex, selectedDue
}

func (s *HeartbeatService) initialDueAt(job heartbeatJobRuntime, serviceStart time.Time) time.Time {
	if job.state.LastRunAtMs > 0 {
		return time.UnixMilli(job.state.LastRunAtMs).Add(intervalDuration(job.cfg, job.state.ConsecutiveOk))
	}
	return serviceStart.Add(intervalDuration(job.cfg, job.state.ConsecutiveOk))
}

func (s *HeartbeatService) adjustToActiveHours(cfg HeartbeatScheduleConfig, dueAt time.Time) time.Time {
	if inWindow, nextWindowStart := IsWithinActiveHours(cfg, dueAt); !inWindow && !nextWindowStart.IsZero() {
		return nextWindowStart
	}
	return dueAt
}

func intervalDuration(cfg HeartbeatRunConfig, consecutiveOk int) time.Duration {
	if cfg.AdaptiveBackoff {
		return AdaptiveInterval(cfg.IntervalMinutes, cfg.MaxIntervalMinutes, consecutiveOk)
	}
	return time.Duration(cfg.IntervalMinutes) * time.Minute
}

// Validate returns an error if the config is invalid.
func validateConfig(cfg config.HeartbeatConfig, timezone string) error {
	if strings.TrimSpace(timezone) == "" {
		return fmt.Errorf("heartbeat timezone is required")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return fmt.Errorf("heartbeat timezone %q is invalid: %w", timezone, err)
	}
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
