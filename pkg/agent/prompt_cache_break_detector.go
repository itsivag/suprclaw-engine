package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"

	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/providers"
)

type PromptStateSnapshot struct {
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

type promptStateRecord struct {
	snapshot     PromptStateSnapshot
	promptTokens int
}

type PromptCacheBreakDetector struct {
	mu                           sync.Mutex
	records                      map[string]promptStateRecord
	minUnexpectedPromptTokenJump int
}

func NewPromptCacheBreakDetector() *PromptCacheBreakDetector {
	return &PromptCacheBreakDetector{
		records:                      make(map[string]promptStateRecord),
		minUnexpectedPromptTokenJump: 2000,
	}
}

func hashValue(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func NewPromptStateSnapshot(
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

func (d *PromptCacheBreakDetector) Analyze(
	key string,
	snapshot PromptStateSnapshot,
	usage *providers.UsageInfo,
	mutators PromptMutators,
) {
	if usage == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	prev, ok := d.records[key]
	if !ok {
		d.records[key] = promptStateRecord{snapshot: snapshot, promptTokens: usage.PromptTokens}
		return
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
	if tokenJump >= d.minUnexpectedPromptTokenJump {
		changedSchemas := changedToolSchemas(prev.snapshot.PerToolHashes, snapshot.PerToolHashes)
		if !systemPromptChanged && !toolSchemasChanged && !modelChanged && !optionsChanged && !classified.Any() {
			logger.WarnCF("agent", "Prompt cache break detected",
				map[string]any{
					"key":                 key,
					"prompt_tokens_prev":  prev.promptTokens,
					"prompt_tokens_curr":  usage.PromptTokens,
					"prompt_tokens_delta": tokenJump,
					"system_chars":        snapshot.SystemChars,
					"tool_count":          snapshot.ToolCount,
				})
		} else {
			logger.DebugCF("agent", "Prompt cache shift classified",
				map[string]any{
					"key":                         key,
					"prompt_tokens_prev":          prev.promptTokens,
					"prompt_tokens_curr":          usage.PromptTokens,
					"prompt_tokens_delta":         tokenJump,
					"system_prompt_changed":       systemPromptChanged,
					"tool_schemas_changed":        toolSchemasChanged,
					"changed_tool_schemas":        changedSchemas,
					"model_changed":               modelChanged,
					"options_changed":             optionsChanged,
					"classified_discovered_tools": classified.DiscoveredToolsChanged,
					"classified_skill_set":        classified.SkillSetChanged,
					"classified_config":           classified.ConfigChanged,
					"classified_invalidation":     classified.ExplicitInvalidation,
				})
		}
	}

	d.records[key] = promptStateRecord{snapshot: snapshot, promptTokens: usage.PromptTokens}
}
