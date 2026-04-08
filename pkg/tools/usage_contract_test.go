package tools

import (
	"strings"
	"testing"
)

func TestFormatToolDescription_DeterministicOutput(t *testing.T) {
	contract := ToolUsageContract{
		UseWhen:      "you need deterministic behavior.",
		DoNotUseWhen: "you need fallback behavior.",
		HardRequirements: []string{
			"first requirement",
			"second requirement",
		},
	}

	got := FormatToolDescription("Base tool description.", contract)
	want := strings.Join([]string{
		"Base tool description.",
		"",
		"LLM Usage Guidance:",
		"Use this tool when:",
		"- you need deterministic behavior.",
		"Do not use this tool when:",
		"- you need fallback behavior.",
		"Hard requirements:",
		"- first requirement",
		"- second requirement",
	}, "\n")

	if got != want {
		t.Fatalf("formatted description mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestValidateToolUsageContract_RejectsInvalidContracts(t *testing.T) {
	tests := []struct {
		name     string
		contract ToolUsageContract
		wantErr  string
	}{
		{
			name: "empty use when",
			contract: ToolUsageContract{
				UseWhen:          " ",
				DoNotUseWhen:     "valid",
				HardRequirements: []string{"req"},
			},
			wantErr: "UseWhen",
		},
		{
			name: "empty do not use when",
			contract: ToolUsageContract{
				UseWhen:          "valid",
				DoNotUseWhen:     " ",
				HardRequirements: []string{"req"},
			},
			wantErr: "DoNotUseWhen",
		},
		{
			name: "empty hard requirements",
			contract: ToolUsageContract{
				UseWhen:      "valid",
				DoNotUseWhen: "valid",
			},
			wantErr: "HardRequirements",
		},
		{
			name: "blank hard requirement entry",
			contract: ToolUsageContract{
				UseWhen:          "valid",
				DoNotUseWhen:     "valid",
				HardRequirements: []string{"req", " "},
			},
			wantErr: "HardRequirements[1]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateToolUsageContract(tt.contract)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error to contain %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateToolUsageContract_AcceptsValidContract(t *testing.T) {
	err := ValidateToolUsageContract(ToolUsageContract{
		UseWhen:      "valid",
		DoNotUseWhen: "valid",
		HardRequirements: []string{
			"req-1",
		},
	})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}
