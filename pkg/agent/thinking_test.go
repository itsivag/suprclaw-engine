package agent

import "testing"

func TestParseThinkingLevel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ThinkingLevel
	}{
		{"off", "off", ThinkingOff},
		{"empty", "", ThinkingOff},
		{"low", "low", ThinkingLow},
		{"medium", "medium", ThinkingMedium},
		{"high", "high", ThinkingHigh},
		{"xhigh", "xhigh", ThinkingXHigh},
		{"adaptive", "adaptive", ThinkingAdaptive},
		{"unknown", "unknown", ThinkingOff},
		// Case-insensitive and whitespace-tolerant
		{"upper_Medium", "Medium", ThinkingMedium},
		{"upper_HIGH", "HIGH", ThinkingHigh},
		{"mixed_Adaptive", "Adaptive", ThinkingAdaptive},
		{"leading_space", " high", ThinkingHigh},
		{"trailing_space", "low ", ThinkingLow},
		{"both_spaces", " medium ", ThinkingMedium},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseThinkingLevel(tt.input); got != tt.want {
				t.Errorf("parseThinkingLevel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseThinkingLevelStrict(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ThinkingLevel
		ok    bool
	}{
		{"off", "off", ThinkingOff, true},
		{"low", "low", ThinkingLow, true},
		{"medium", "medium", ThinkingMedium, true},
		{"high", "high", ThinkingHigh, true},
		{"xhigh", "xhigh", ThinkingXHigh, true},
		{"adaptive", "adaptive", ThinkingAdaptive, true},
		{"upper_with_spaces", " HIGH ", ThinkingHigh, true},
		{"empty", "", ThinkingOff, false},
		{"unknown", "fast", ThinkingOff, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseThinkingLevelStrict(tt.input)
			if ok != tt.ok {
				t.Fatalf("parseThinkingLevelStrict(%q) ok=%v, want %v", tt.input, ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("parseThinkingLevelStrict(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
