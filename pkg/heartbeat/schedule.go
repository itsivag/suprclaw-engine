package heartbeat

import (
	"fmt"
	"time"
)

// HeartbeatScheduleConfig holds schedule-related configuration.
type HeartbeatScheduleConfig struct {
	ActiveHoursStart string // "08:00"
	ActiveHoursEnd   string // "22:00"
	Timezone         string // IANA e.g. "America/New_York"; empty = UTC
}

// IsWithinActiveHours checks whether now falls within [start, end) in the
// configured timezone.
//
// Returns (inWindow bool, nextWindowStart time.Time).
// When start/end are both empty, the function always returns (true, zero).
func IsWithinActiveHours(cfg HeartbeatScheduleConfig, now time.Time) (bool, time.Time) {
	if cfg.ActiveHoursStart == "" && cfg.ActiveHoursEnd == "" {
		return true, time.Time{}
	}

	loc, err := loadLocation(cfg.Timezone)
	if err != nil {
		// Fall back to UTC on bad timezone config.
		loc = time.UTC
	}

	localNow := now.In(loc)

	startH, startM, err := parseHHMM(cfg.ActiveHoursStart)
	if err != nil {
		return true, time.Time{}
	}
	endH, endM, err := parseHHMM(cfg.ActiveHoursEnd)
	if err != nil {
		return true, time.Time{}
	}

	windowStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), startH, startM, 0, 0, loc)
	windowEnd := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), endH, endM, 0, 0, loc)

	// Handle overnight windows (e.g. 22:00–06:00)
	if windowEnd.Before(windowStart) || windowEnd.Equal(windowStart) {
		// Window wraps midnight
		if localNow.Before(windowEnd) {
			// We're in the early-morning portion of the window.
			return true, time.Time{}
		}
		if !localNow.Before(windowStart) {
			// We're in the late-night portion.
			return true, time.Time{}
		}
		// Outside window — next start is today's windowStart.
		return false, windowStart
	}

	// Normal window (start < end, same day)
	if !localNow.Before(windowStart) && localNow.Before(windowEnd) {
		return true, time.Time{}
	}

	// Outside window.
	if localNow.Before(windowStart) {
		return false, windowStart
	}
	// Past end for today — next window starts tomorrow.
	nextStart := windowStart.Add(24 * time.Hour)
	return false, nextStart
}

// AdaptiveInterval computes the effective heartbeat interval given the count of
// consecutive HEARTBEAT_OK responses. The interval doubles with each consecutive
// ok, capped at maxMin minutes.
//
// AdaptiveInterval(30, 120, 0) = 30 min
// AdaptiveInterval(30, 120, 1) = 60 min
// AdaptiveInterval(30, 120, 2) = 120 min (capped)
func AdaptiveInterval(baseMin, maxMin, consecutiveOk int) time.Duration {
	if baseMin <= 0 {
		baseMin = 30
	}
	if maxMin <= 0 {
		maxMin = 120
	}
	if consecutiveOk < 0 {
		consecutiveOk = 0
	}

	result := baseMin
	for i := 0; i < consecutiveOk; i++ {
		result *= 2
		if result >= maxMin {
			result = maxMin
			break
		}
	}

	return time.Duration(result) * time.Minute
}

// IsIdleWindowActive returns true if the user was recently active (within
// idleWindowMin minutes of now) — i.e., heartbeat should skip.
//
// idleWindowMin ≤ 0 disables the idle window (always returns false).
func IsIdleWindowActive(lastActivityAt time.Time, idleWindowMin int, now time.Time) bool {
	if idleWindowMin <= 0 {
		return false
	}
	if lastActivityAt.IsZero() {
		return false
	}
	cutoff := now.Add(-time.Duration(idleWindowMin) * time.Minute)
	return lastActivityAt.After(cutoff)
}

// --- helpers ---

func loadLocation(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	return time.LoadLocation(tz)
}

func parseHHMM(s string) (hour, min int, err error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, fmt.Errorf("invalid HH:MM format: %q", s)
	}
	_, err = fmt.Sscanf(s, "%d:%d", &hour, &min)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid HH:MM format: %q: %w", s, err)
	}
	if hour < 0 || hour > 23 || min < 0 || min > 59 {
		return 0, 0, fmt.Errorf("out of range HH:MM: %q", s)
	}
	return hour, min, nil
}
