package heartbeat

import (
	"testing"
	"time"

	"github.com/itsivag/suprclaw/pkg/config"
)

func TestSelectNextJob_TieBreaksByListOrder(t *testing.T) {
	svc := &HeartbeatService{}
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)

	jobs := []heartbeatJobRuntime{
		{
			cfg: HeartbeatRunConfig{
				AgentID:         "a",
				IntervalMinutes: 5,
			},
			state: &HeartbeatState{LastRunAtMs: now.Add(-10 * time.Minute).UnixMilli()},
		},
		{
			cfg: HeartbeatRunConfig{
				AgentID:         "b",
				IntervalMinutes: 5,
			},
			state: &HeartbeatState{LastRunAtMs: now.Add(-10 * time.Minute).UnixMilli()},
		},
	}

	idx, at := svc.selectNextJob(jobs, now, time.Time{})
	if idx != 0 {
		t.Fatalf("selected job index = %d, want 0", idx)
	}
	if !at.Equal(now) {
		t.Fatalf("selected start = %s, want %s", at.Format(time.RFC3339), now.Format(time.RFC3339))
	}
}

func TestSelectNextJob_RespectsGlobalMinimumGap(t *testing.T) {
	svc := &HeartbeatService{}
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)

	jobs := []heartbeatJobRuntime{
		{
			cfg: HeartbeatRunConfig{
				AgentID:         "a",
				IntervalMinutes: 5,
			},
			state: &HeartbeatState{LastRunAtMs: now.Add(-10 * time.Minute).UnixMilli()},
		},
	}

	nextAllowedStart := now.Add(2 * time.Minute)
	_, at := svc.selectNextJob(jobs, now, nextAllowedStart)
	if !at.Equal(nextAllowedStart) {
		t.Fatalf("selected start = %s, want nextAllowedStart %s", at.Format(time.RFC3339), nextAllowedStart.Format(time.RFC3339))
	}
}

func TestJobDueAt_RespectsActiveHours(t *testing.T) {
	svc := &HeartbeatService{}
	now := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)

	job := heartbeatJobRuntime{
		cfg: HeartbeatRunConfig{
			AgentID:         "a",
			IntervalMinutes: 5,
			ScheduleCfg: HeartbeatScheduleConfig{
				ActiveHoursStart: "08:00",
				ActiveHoursEnd:   "22:00",
				Timezone:         "UTC",
			},
		},
		state: &HeartbeatState{LastRunAtMs: now.Add(-10 * time.Minute).UnixMilli()},
	}

	got := svc.jobDueAt(job, now)
	want := time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("job due = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
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
		timezone:  "Asia/Kolkata",
		workspace: "/tmp",
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
	jobs := svc.buildRuntimeJobs(jobStates)
	if len(jobs) != 1 {
		t.Fatalf("jobs len = %d, want 1", len(jobs))
	}
	if jobs[0].cfg.ScheduleCfg.Timezone != "Asia/Kolkata" {
		t.Fatalf("runtime timezone = %q, want Asia/Kolkata", jobs[0].cfg.ScheduleCfg.Timezone)
	}

	now := time.Date(2026, 1, 2, 1, 0, 0, 0, time.UTC) // 06:30 in Asia/Kolkata
	due := svc.jobDueAt(jobs[0], now)
	want := time.Date(2026, 1, 2, 2, 30, 0, 0, time.UTC) // 08:00 in Asia/Kolkata
	if !due.Equal(want) {
		t.Fatalf("job due = %s, want %s", due.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}
