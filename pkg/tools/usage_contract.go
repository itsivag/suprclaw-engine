package tools

import (
	"fmt"
	"strings"
)

// ToolUsageContract defines strict LLM usage guidance for a tool.
// Every field is required and validated at registration time.
type ToolUsageContract struct {
	UseWhen          string
	DoNotUseWhen     string
	HardRequirements []string
}

// ValidateToolUsageContract enforces strict guidance contract requirements.
func ValidateToolUsageContract(contract ToolUsageContract) error {
	if strings.TrimSpace(contract.UseWhen) == "" {
		return fmt.Errorf("usage contract UseWhen must be non-empty")
	}
	if strings.TrimSpace(contract.DoNotUseWhen) == "" {
		return fmt.Errorf("usage contract DoNotUseWhen must be non-empty")
	}
	if len(contract.HardRequirements) == 0 {
		return fmt.Errorf("usage contract HardRequirements must contain at least one requirement")
	}
	for i, requirement := range contract.HardRequirements {
		if strings.TrimSpace(requirement) == "" {
			return fmt.Errorf("usage contract HardRequirements[%d] must be non-empty", i)
		}
	}
	return nil
}

// ValidateToolContract validates full tool registration contract requirements.
func ValidateToolContract(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("tool must not be nil")
	}
	if strings.TrimSpace(tool.Name()) == "" {
		return fmt.Errorf("tool name must be non-empty")
	}
	if err := ValidateToolUsageContract(tool.UsageContract()); err != nil {
		return fmt.Errorf("tool %q has invalid usage contract: %w", tool.Name(), err)
	}
	return nil
}

// FormatToolDescription deterministically composes the base description and the
// strict usage contract into one provider-facing description string.
func FormatToolDescription(description string, contract ToolUsageContract) string {
	description = strings.TrimSpace(description)

	requirements := make([]string, len(contract.HardRequirements))
	for i := range contract.HardRequirements {
		requirements[i] = strings.TrimSpace(contract.HardRequirements[i])
	}

	var b strings.Builder
	if description != "" {
		b.WriteString(description)
		b.WriteString("\n\n")
	}
	b.WriteString("LLM Usage Guidance:\n")
	b.WriteString("Use this tool when:\n")
	b.WriteString("- ")
	b.WriteString(strings.TrimSpace(contract.UseWhen))
	b.WriteString("\n")
	b.WriteString("Do not use this tool when:\n")
	b.WriteString("- ")
	b.WriteString(strings.TrimSpace(contract.DoNotUseWhen))
	b.WriteString("\n")
	b.WriteString("Hard requirements:\n")
	for i := range requirements {
		b.WriteString("- ")
		b.WriteString(requirements[i])
		if i < len(requirements)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
