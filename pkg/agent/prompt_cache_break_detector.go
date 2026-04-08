package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/providers"
)

type PromptStateSnapshot struct {
	ProviderName           string
	SystemHash             string
	ToolsHash              string
	PerToolHashes          map[string]string
	Model                  string
	OptionsHash            string
	SystemChars            int
	ToolCount              int
	SkillStateEpoch        uint64
	CacheInvalidationEpoch uint64
}

type PromptMutators struct {
	DiscoveredToolsChanged bool
	SkillSetChanged        bool
	ConfigChanged          bool
	ExplicitInvalidation   bool
}

func (m PromptMutators) Any() bool {
	return m.DiscoveredToolsChanged || m.SkillSetChanged || m.ConfigChanged || m.ExplicitInvalidation
}

type PromptCacheReasonCode string

const (
	PromptCacheReasonUnexpectedBreak         PromptCacheReasonCode = "unexpected_cache_break"
	PromptCacheReasonExpectedDiscoveredTools PromptCacheReasonCode = "expected_discovered_tools_change"
	PromptCacheReasonExpectedSkillSet        PromptCacheReasonCode = "expected_skill_set_change"
	PromptCacheReasonExpectedConfig          PromptCacheReasonCode = "expected_config_change"
	PromptCacheReasonExpectedInvalidation    PromptCacheReasonCode = "expected_explicit_invalidation"
	PromptCacheReasonExpectedPromptShape     PromptCacheReasonCode = "expected_prompt_shape_change"
	PromptCacheReasonExpectedMultiMutation   PromptCacheReasonCode = "expected_multi_mutation"
)

type PromptCacheDiagnostic struct {
	Key               string                `json:"key"`
	ReasonCode        PromptCacheReasonCode `json:"reason_code"`
	MutationTags      []string              `json:"mutation_tags"`
	ChangedTools      []string              `json:"changed_tools"`
	PromptTokensPrev  int                   `json:"prompt_tokens_prev"`
	PromptTokensCurr  int                   `json:"prompt_tokens_curr"`
	PromptTokensDelta int                   `json:"prompt_tokens_delta"`
	Threshold         int                   `json:"threshold"`
	SystemChars       int                   `json:"system_chars"`
	ToolCount         int                   `json:"tool_count"`
}

type PromptUsageContractViolationError struct {
	Provider string
	Model    string
	Reason   string
}

func (e *PromptUsageContractViolationError) Error() string {
	return fmt.Sprintf(
		"prompt usage contract violation for provider=%q model=%q: %s",
		e.Provider,
		e.Model,
		e.Reason,
	)
}

type promptStateRecord struct {
	snapshot     PromptStateSnapshot
	promptTokens int
}

type PromptCacheBreakDetector struct {
	mu          sync.Mutex
	records     map[string]promptStateRecord
	diagnostics map[string]PromptCacheDiagnostic
}

func NewPromptCacheBreakDetector() *PromptCacheBreakDetector {
	return &PromptCacheBreakDetector{
		records:     make(map[string]promptStateRecord),
		diagnostics: make(map[string]PromptCacheDiagnostic),
	}
}

func hashValue(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func NewPromptStateSnapshot(
	providerName string,
	systemPrompt string,
	defs []providers.ToolDefinition,
	model string,
	options map[string]any,
	skillStateEpoch uint64,
	cacheInvalidationEpoch uint64,
) PromptStateSnapshot {
	perTool := make(map[string]string, len(defs))
	normalized := normalizeToolDefs(defs)
	for _, def := range normalized {
		perTool[def.Function.Name] = hashValue(def)
	}

	return PromptStateSnapshot{
		ProviderName:           strings.TrimSpace(providerName),
		SystemHash:             hashValue(systemPrompt),
		ToolsHash:              hashValue(normalized),
		PerToolHashes:          perTool,
		Model:                  model,
		OptionsHash:            hashValue(options),
		SystemChars:            len(systemPrompt),
		ToolCount:              len(defs),
		SkillStateEpoch:        skillStateEpoch,
		CacheInvalidationEpoch: cacheInvalidationEpoch,
	}
}

func changedToolSchemas(prev, curr map[string]string) []string {
	if len(prev) == 0 && len(curr) == 0 {
		return nil
	}
	changed := make([]string, 0)
	seen := make(map[string]struct{}, len(prev)+len(curr))
	for name := range prev {
		seen[name] = struct{}{}
	}
	for name := range curr {
		seen[name] = struct{}{}
	}
	for name := range seen {
		if prev[name] != curr[name] {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

func usageViolation(snapshot PromptStateSnapshot, reason string) error {
	return &PromptUsageContractViolationError{
		Provider: snapshot.ProviderName,
		Model:    snapshot.Model,
		Reason:   reason,
	}
}

func validateUsageContract(snapshot PromptStateSnapshot, usage *providers.UsageInfo) error {
	if usage == nil {
		return usageViolation(snapshot, "usage payload is required")
	}
	if usage.PromptTokens <= 0 {
		return usageViolation(snapshot, fmt.Sprintf("prompt_tokens must be > 0, got %d", usage.PromptTokens))
	}
	if usage.CompletionTokens < 0 {
		return usageViolation(snapshot, fmt.Sprintf("completion_tokens must be >= 0, got %d", usage.CompletionTokens))
	}
	if usage.TotalTokens <= 0 {
		return usageViolation(snapshot, fmt.Sprintf("total_tokens must be > 0, got %d", usage.TotalTokens))
	}
	expectedTotal := usage.PromptTokens + usage.CompletionTokens
	if usage.TotalTokens != expectedTotal {
		return usageViolation(snapshot, fmt.Sprintf(
			"total_tokens must equal prompt_tokens + completion_tokens (%d), got %d",
			expectedTotal,
			usage.TotalTokens,
		))
	}
	return nil
}

func modelFamily(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(m, "gpt-5"):
		return "gpt5"
	case strings.Contains(m, "gpt-4"):
		return "gpt4"
	case strings.Contains(m, "claude"):
		return "claude"
	case strings.Contains(m, "gemini"):
		return "gemini"
	case strings.Contains(m, "codex"):
		return "codex"
	default:
		return "default"
	}
}

func promptJumpThreshold(providerName, model string) int {
	provider := strings.ToLower(strings.TrimSpace(providerName))
	family := modelFamily(model)

	switch {
	case strings.Contains(provider, "anthropic"):
		if family == "claude" {
			return 2800
		}
		return 2400
	case strings.Contains(provider, "openai"), strings.Contains(provider, "azure"):
		if family == "gpt5" || family == "codex" {
			return 1600
		}
		return 1800
	case strings.Contains(provider, "google"), strings.Contains(provider, "gemini"), strings.Contains(provider, "antigravity"):
		return 2500
	default:
		return 2000
	}
}

func classifyExpectedReason(
	classified PromptMutators,
	systemPromptChanged bool,
	toolSchemasChanged bool,
	modelChanged bool,
	optionsChanged bool,
) (PromptCacheReasonCode, []string) {
	tags := make([]string, 0, 8)
	if classified.DiscoveredToolsChanged {
		tags = append(tags, "discovered_tools")
	}
	if classified.SkillSetChanged {
		tags = append(tags, "skill_set")
	}
	if classified.ConfigChanged {
		tags = append(tags, "config")
	}
	if classified.ExplicitInvalidation {
		tags = append(tags, "explicit_invalidation")
	}
	if systemPromptChanged {
		tags = append(tags, "system_prompt")
	}
	if toolSchemasChanged {
		tags = append(tags, "tool_schemas")
	}
	if modelChanged {
		tags = append(tags, "model")
	}
	if optionsChanged {
		tags = append(tags, "options")
	}
	sort.Strings(tags)

	switch {
	case len(tags) > 1:
		return PromptCacheReasonExpectedMultiMutation, tags
	case classified.DiscoveredToolsChanged:
		return PromptCacheReasonExpectedDiscoveredTools, tags
	case classified.SkillSetChanged:
		return PromptCacheReasonExpectedSkillSet, tags
	case classified.ConfigChanged:
		return PromptCacheReasonExpectedConfig, tags
	case classified.ExplicitInvalidation:
		return PromptCacheReasonExpectedInvalidation, tags
	default:
		return PromptCacheReasonExpectedPromptShape, tags
	}
}

func (d *PromptCacheBreakDetector) Analyze(
	key string,
	snapshot PromptStateSnapshot,
	usage *providers.UsageInfo,
	mutators PromptMutators,
) (*PromptCacheDiagnostic, error) {
	if err := validateUsageContract(snapshot, usage); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	prev, ok := d.records[key]
	if !ok {
		d.records[key] = promptStateRecord{snapshot: snapshot, promptTokens: usage.PromptTokens}
		return nil, nil
	}

	toolSchemasChanged := prev.snapshot.ToolsHash != snapshot.ToolsHash
	systemPromptChanged := prev.snapshot.SystemHash != snapshot.SystemHash
	modelChanged := prev.snapshot.Model != snapshot.Model
	optionsChanged := prev.snapshot.OptionsHash != snapshot.OptionsHash
	skillEpochChanged := prev.snapshot.SkillStateEpoch != snapshot.SkillStateEpoch
	cacheEpochChanged := prev.snapshot.CacheInvalidationEpoch != snapshot.CacheInvalidationEpoch

	classified := PromptMutators{
		DiscoveredToolsChanged: mutators.DiscoveredToolsChanged || toolSchemasChanged,
		SkillSetChanged:        mutators.SkillSetChanged || skillEpochChanged,
		ConfigChanged:          mutators.ConfigChanged || modelChanged || optionsChanged,
		ExplicitInvalidation:   mutators.ExplicitInvalidation || cacheEpochChanged,
	}

	tokenJump := usage.PromptTokens - prev.promptTokens
	threshold := promptJumpThreshold(snapshot.ProviderName, snapshot.Model)

	var diagnostic *PromptCacheDiagnostic
	if tokenJump >= threshold {
		changedSchemas := changedToolSchemas(prev.snapshot.PerToolHashes, snapshot.PerToolHashes)
		expected := classified.Any() || systemPromptChanged || toolSchemasChanged || modelChanged || optionsChanged

		reason := PromptCacheReasonUnexpectedBreak
		tags := []string{"unexpected"}
		if expected {
			reason, tags = classifyExpectedReason(
				classified,
				systemPromptChanged,
				toolSchemasChanged,
				modelChanged,
				optionsChanged,
			)
		}

		diag := PromptCacheDiagnostic{
			Key:               key,
			ReasonCode:        reason,
			MutationTags:      tags,
			ChangedTools:      changedSchemas,
			PromptTokensPrev:  prev.promptTokens,
			PromptTokensCurr:  usage.PromptTokens,
			PromptTokensDelta: tokenJump,
			Threshold:         threshold,
			SystemChars:       snapshot.SystemChars,
			ToolCount:         snapshot.ToolCount,
		}
		d.diagnostics[key] = diag
		diagnostic = &diag

		logFields := map[string]any{
			"key":                  diag.Key,
			"reason_code":          string(diag.ReasonCode),
			"mutation_tags":        diag.MutationTags,
			"changed_tool_schemas": diag.ChangedTools,
			"prompt_tokens_prev":   diag.PromptTokensPrev,
			"prompt_tokens_curr":   diag.PromptTokensCurr,
			"prompt_tokens_delta":  diag.PromptTokensDelta,
			"threshold":            diag.Threshold,
			"system_chars":         diag.SystemChars,
			"tool_count":           diag.ToolCount,
			"provider":             snapshot.ProviderName,
			"model":                snapshot.Model,
		}
		if reason == PromptCacheReasonUnexpectedBreak {
			logger.WarnCF("agent", "Prompt cache break detected", logFields)
		} else {
			logger.DebugCF("agent", "Prompt cache break classified", logFields)
		}
	}

	d.records[key] = promptStateRecord{snapshot: snapshot, promptTokens: usage.PromptTokens}
	return diagnostic, nil
}

func (d *PromptCacheBreakDetector) SnapshotDiagnostics() map[string]PromptCacheDiagnostic {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make(map[string]PromptCacheDiagnostic, len(d.diagnostics))
	for k, v := range d.diagnostics {
		out[k] = v
	}
	return out
}
