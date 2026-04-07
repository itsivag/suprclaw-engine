package heartbeat

import (
	"testing"
	"time"

	"github.com/itsivag/suprclaw/pkg/config"
)

func TestSelectNextJob_TieBreaksByListOrder(t *testing.T) {
	svc := &HeartbeatService{}
	dueAt := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)

	jobs := []heartbeatJobRuntime{
		{
			cfg:       HeartbeatRunConfig{AgentID: "a", IntervalMinutes: 5},
			state:     &HeartbeatState{},
			nextDueAt: dueAt,
		},
		{
			cfg:       HeartbeatRunConfig{AgentID: "b", IntervalMinutes: 5},
			state:     &HeartbeatState{},
			nextDueAt: dueAt,
		},
	}

	idx, at := svc.selectNextJob(jobs, time.Time{})
	if idx != 0 {
		t.Fatalf("selected job index = %d, want 0", idx)
	}
	if !at.Equal(dueAt) {
		t.Fatalf("selected start = %s, want %s", at.Format(time.RFC3339), dueAt.Format(time.RFC3339))
	}
}

func TestSelectNextJob_RespectsGlobalMinimumGap(t *testing.T) {
	svc := &HeartbeatService{}
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)

	jobs := []heartbeatJobRuntime{
		{
			cfg:       HeartbeatRunConfig{AgentID: "a", IntervalMinutes: 5},
			state:     &HeartbeatState{},
			nextDueAt: now,
		},
	}

	nextAllowedStart := now.Add(2 * time.Minute)
	_, at := svc.selectNextJob(jobs, nextAllowedStart)
	if !at.Equal(nextAllowedStart) {
		t.Fatalf("selected start = %s, want nextAllowedStart %s", at.Format(time.RFC3339), nextAllowedStart.Format(time.RFC3339))
	}
}

func TestAdjustToActiveHours_RespectsActiveHours(t *testing.T) {
	svc := &HeartbeatService{}
	due := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)

	got := svc.adjustToActiveHours(HeartbeatScheduleConfig{
		ActiveHoursStart: "08:00",
		ActiveHoursEnd:   "22:00",
		Timezone:         "UTC",
	}, due)
	want := time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("adjusted due = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestInitialDueAt_FirstRunAfterInterval(t *testing.T) {
	serviceStart := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	svc := &HeartbeatService{}
	job := heartbeatJobRuntime{
		cfg: HeartbeatRunConfig{
			AgentID:         "main",
			IntervalMinutes: 30,
		},
		state: &HeartbeatState{},
	}

	got := svc.initialDueAt(job, serviceStart)
	want := serviceStart.Add(30 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("initial due = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestValidateConfigRejectsDuplicateAgents(t *testing.T) {
	cfg := config.HeartbeatConfig{
		Enabled:           true,
		MinimumGapMinutes: 5,
		Jobs: []config.HeartbeatJobConfig{
			{AgentID: "main", IntervalMinutes: 5},
			{AgentID: "main", IntervalMinutes: 5},
		},
	}
	if err := validateConfig(cfg, "UTC"); err == nil {
		t.Fatal("expected validateConfig() to fail for duplicate agent_id")
	}
}

func TestValidateConfigRejectsInvalidTimezone(t *testing.T) {
	cfg := config.HeartbeatConfig{
		Enabled:           true,
		MinimumGapMinutes: 5,
		Jobs: []config.HeartbeatJobConfig{
			{AgentID: "main", IntervalMinutes: 5},
		},
	}
	if err := validateConfig(cfg, "Invalid/Timezone"); err == nil {
		t.Fatal("expected validateConfig() to fail for invalid timezone")
	}
}

func TestBuildRuntimeJobs_UsesGlobalTimezone(t *testing.T) {
	svc := &HeartbeatService{
		timezone:       "Asia/Kolkata",
		stateWorkspace: "/tmp",
		agentDefaults: config.AgentDefaults{
			Workspace: "/tmp/workspace",
		},
		cfg: config.HeartbeatConfig{
			Enabled:           true,
			MinimumGapMinutes: 5,
			Jobs: []config.HeartbeatJobConfig{
				{
					AgentID:          "main",
					IntervalMinutes:  5,
					ActiveHoursStart: "08:00",
					ActiveHoursEnd:   "22:00",
				},
			},
		},
	}

	jobStates := map[string]*HeartbeatState{
		"main": {LastRunAtMs: time.Date(2026, 1, 2, 0, 50, 0, 0, time.UTC).UnixMilli()},
	}
	jobs := svc.buildRuntimeJobs(jobStates, time.Date(2026, 1, 2, 1, 0, 0, 0, time.UTC))
	if len(jobs) != 1 {
		t.Fatalf("jobs len = %d, want 1", len(jobs))
	}
	if jobs[0].cfg.ScheduleCfg.Timezone != "Asia/Kolkata" {
		t.Fatalf("runtime timezone = %q, want Asia/Kolkata", jobs[0].cfg.ScheduleCfg.Timezone)
	}

	due := svc.adjustToActiveHours(jobs[0].cfg.ScheduleCfg, jobs[0].nextDueAt)
	want := time.Date(2026, 1, 2, 2, 30, 0, 0, time.UTC) // 08:00 in Asia/Kolkata
	if !due.Equal(want) {
		t.Fatalf("job due = %s, want %s", due.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if jobs[0].cfg.Workspace != "/tmp/workspace" {
		t.Fatalf("workspace = %q, want /tmp/workspace", jobs[0].cfg.Workspace)
	}
}

func TestBuildRuntimeJobs_UsesPerAgentWorkspace(t *testing.T) {
	svc := &HeartbeatService{
		timezone: "UTC",
		agentDefaults: config.AgentDefaults{
			Workspace: "/root/.suprclaw/workspace",
		},
		agents: []config.AgentConfig{
			{ID: "main"},
			{ID: "content-writer", Workspace: "/root/.suprclaw/workspace-content-writer"},
			{ID: "seo"},
		},
		cfg: config.HeartbeatConfig{
			Enabled:           true,
			MinimumGapMinutes: 5,
			Jobs: []config.HeartbeatJobConfig{
				{AgentID: "main", IntervalMinutes: 30},
				{AgentID: "content-writer", IntervalMinutes: 30},
				{AgentID: "seo", IntervalMinutes: 30},
			},
		},
	}

	jobs := svc.buildRuntimeJobs(map[string]*HeartbeatState{}, time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC))
	if len(jobs) != 3 {
		t.Fatalf("jobs len = %d, want 3", len(jobs))
	}
	if jobs[0].cfg.Workspace != "/root/.suprclaw/workspace" {
		t.Fatalf("main workspace = %q", jobs[0].cfg.Workspace)
	}
	if jobs[1].cfg.Workspace != "/root/.suprclaw/workspace-content-writer" {
		t.Fatalf("content-writer workspace = %q", jobs[1].cfg.Workspace)
	}
	if jobs[2].cfg.Workspace != "/root/.suprclaw/workspace-seo" {
		t.Fatalf("seo workspace = %q", jobs[2].cfg.Workspace)
	}
}

func TestSelectNextJob_NoStarvationForEqualIntervals(t *testing.T) {
	serviceStart := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	svc := &HeartbeatService{
		cfg: config.HeartbeatConfig{
			Enabled:           true,
			MinimumGapMinutes: 5,
			Jobs: []config.HeartbeatJobConfig{
				{AgentID: "main", IntervalMinutes: 30},
				{AgentID: "content-writer", IntervalMinutes: 30},
				{AgentID: "seo", IntervalMinutes: 30},
			},
		},
		timezone: "UTC",
		agentDefaults: config.AgentDefaults{
			Workspace: "/tmp/workspace",
		},
		agents: []config.AgentConfig{
			{ID: "main"},
			{ID: "content-writer"},
			{ID: "seo"},
		},
	}

	jobs := svc.buildRuntimeJobs(map[string]*HeartbeatState{}, serviceStart)
	nextAllowedStart := time.Time{}
	gotOrder := make([]string, 0, 6)
	gotStarts := make([]time.Time, 0, 6)

	for i := 0; i < 6; i++ {
		idx, startAt := svc.selectNextJob(jobs, nextAllowedStart)
		gotOrder = append(gotOrder, jobs[idx].cfg.AgentID)
		gotStarts = append(gotStarts, startAt)

		jobs[idx].nextDueAt = startAt.Add(intervalDuration(jobs[idx].cfg, jobs[idx].state.ConsecutiveOk))
		nextAllowedStart = startAt.Add(time.Duration(svc.cfg.MinimumGapMinutes) * time.Minute)
	}

	wantOrder := []string{"main", "content-writer", "seo", "main", "content-writer", "seo"}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("order[%d] = %q, want %q (full=%v)", i, gotOrder[i], wantOrder[i], gotOrder)
		}
	}

	for i := 1; i < len(gotStarts); i++ {
		gap := gotStarts[i].Sub(gotStarts[i-1])
		if gap < 5*time.Minute {
			t.Fatalf("run gap[%d] = %s, want >= 5m", i, gap)
		}
	}
}
