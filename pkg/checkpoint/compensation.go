package checkpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"text/template"
)

// CompensationRule maps a tool to its inverse in config.
type CompensationRule struct {
	ToolName    string `json:"tool_name"`
	InverseTool string `json:"inverse_tool"`
	// InverseArgs is a Go text/template that produces a JSON object.
	// Template data: {Args map[string]any, Result map[string]any, ResultRaw string}
	InverseArgs string `json:"inverse_args"`
}

// CompensationPlan is the resolved inverse call, stored in ActionEntry.
type CompensationPlan struct {
	InverseTool string         `json:"inverse_tool"`
	InverseArgs map[string]any `json:"inverse_args"`
}

// CompensationResult is the outcome of one compensation execution during rollback.
type CompensationResult struct {
	Seq      int64          `json:"seq"`
	ToolName string         `json:"tool_name"`
	CompTool string         `json:"comp_tool"`
	CompArgs map[string]any `json:"comp_args"`
	Result   string         `json:"result,omitempty"`
	Error    string         `json:"error,omitempty"`
	Skipped  bool           `json:"skipped,omitempty"`
}

// ToolExecutorFunc executes a named tool with the given args.
// Used during rollback to fire compensation calls.
type ToolExecutorFunc func(ctx context.Context, toolName string, args map[string]any) (string, error)

type compensationTemplateData struct {
	Args      map[string]any
	Result    map[string]any // nil if result is not valid JSON
	ResultRaw string
}

// ResolveCompensation finds a matching rule and executes the InverseArgs template.
// Returns nil if no rule matches or if template execution/JSON parse fails.
func ResolveCompensation(rules []CompensationRule, toolName string, callArgs map[string]any, resultRaw string) *CompensationPlan {
	for _, rule := range rules {
		if rule.ToolName != toolName {
			continue
		}
		var resultMap map[string]any
		_ = json.Unmarshal([]byte(resultRaw), &resultMap)

		tmpl, err := template.New("").Parse(rule.InverseArgs)
		if err != nil {
			return nil
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, compensationTemplateData{
			Args: callArgs, Result: resultMap, ResultRaw: resultRaw,
		}); err != nil {
			return nil
		}
		var inverseArgs map[string]any
		if err := json.Unmarshal(buf.Bytes(), &inverseArgs); err != nil {
			return nil
		}
		return &CompensationPlan{InverseTool: rule.InverseTool, InverseArgs: inverseArgs}
	}
	return nil
}

// ExecuteCompensations runs compensation plans for entries in reverse seq order.
// entries must be oldest-first (ascending Seq) — reversed internally for undo order.
// Returns results for all entries that had a CompensationPlan, in execution order.
func ExecuteCompensations(ctx context.Context, entries []ActionEntry, executor ToolExecutorFunc) []CompensationResult {
	var results []CompensationResult
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Compensation == nil {
			continue
		}
		res := CompensationResult{
			Seq: e.Seq, ToolName: e.ToolName,
			CompTool: e.Compensation.InverseTool, CompArgs: e.Compensation.InverseArgs,
		}
		if executor == nil {
			res.Skipped = true
		} else {
			out, err := executor(ctx, e.Compensation.InverseTool, e.Compensation.InverseArgs)
			if err != nil {
				res.Error = err.Error()
			} else {
				res.Result = out
			}
		}
		results = append(results, res)
	}
	return results
}
