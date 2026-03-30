package agent

import "strings"

// ThinkingLevel controls how the provider sends thinking parameters.
//
//   - "adaptive": sends {thinking: {type: "adaptive"}} + output_config.effort (Claude 4.6+)
//   - "low"/"medium"/"high"/"xhigh": sends {thinking: {type: "enabled", budget_tokens: N}} (all models)
//   - "off": disables thinking
type ThinkingLevel string

const (
	ThinkingOff      ThinkingLevel = "off"
	ThinkingLow      ThinkingLevel = "low"
	ThinkingMedium   ThinkingLevel = "medium"
	ThinkingHigh     ThinkingLevel = "high"
	ThinkingXHigh    ThinkingLevel = "xhigh"
	ThinkingAdaptive ThinkingLevel = "adaptive"
)

// parseThinkingLevel normalizes a config string to a ThinkingLevel.
// Case-insensitive and whitespace-tolerant for user-facing config values.
// Returns ThinkingOff for unknown or empty values.
func parseThinkingLevel(level string) ThinkingLevel {
	if parsed, ok := parseThinkingLevelStrict(level); ok {
		return parsed
	}
	return ThinkingOff
}

// parseThinkingLevelStrict normalizes a config string to a ThinkingLevel and
// reports whether the input is a known value.
func parseThinkingLevelStrict(level string) (ThinkingLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off":
		return ThinkingOff, true
	case "adaptive":
		return ThinkingAdaptive, true
	case "low":
		return ThinkingLow, true
	case "medium":
		return ThinkingMedium, true
	case "high":
		return ThinkingHigh, true
	case "xhigh":
		return ThinkingXHigh, true
	default:
		return ThinkingOff, false
	}
}
