package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/utils"
)

const (
	MaxRegexPatternLength = 200
)

type RegexSearchTool struct {
	registry         *ToolRegistry
	maxSearchResults int
}

func NewRegexSearchTool(r *ToolRegistry, maxSearchResults int) *RegexSearchTool {
	return &RegexSearchTool{registry: r, maxSearchResults: maxSearchResults}
}

func (t *RegexSearchTool) Name() string {
	return "tool_search_tool_regex"
}

func (t *RegexSearchTool) Description() string {
	return "Search hidden tools on-demand using a regex pattern. Returns a strict JSON tool reference bundle."
}

func (t *RegexSearchTool) UsageContract() ToolUsageContract {
	return ToolUsageContract{
		UseWhen:      "you need to discover hidden tools by matching tool names or descriptions with a regex pattern.",
		DoNotUseWhen: "the needed tool is already available as a visible callable tool.",
		HardRequirements: []string{
			"pattern must be a non-empty string.",
			"pattern length must not exceed the regex maximum limit.",
			"Invalid regex syntax must be treated as an explicit error.",
		},
	}
}

func (t *RegexSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to match tool name or description",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *RegexSearchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	pattern, ok := args["pattern"].(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		// An empty string regex (?i) will match every hidden tool,
		// dumping massive payloads into the context and burning tokens.
		return ErrorResult("Missing or invalid 'pattern' argument. Must be a non-empty string.")
	}

	if len(pattern) > MaxRegexPatternLength {
		logger.WarnCF("discovery", "Regex pattern rejected (too long)", map[string]any{"len": len(pattern)})
		return ErrorResult(fmt.Sprintf("Pattern too long: max %d characters allowed", MaxRegexPatternLength))
	}

	logger.DebugCF("discovery", "Regex search", map[string]any{"pattern": pattern})

	res, err := t.registry.SearchRegex(pattern, t.maxSearchResults)
	if err != nil {
		logger.WarnCF("discovery", "Invalid regex pattern", map[string]any{"pattern": pattern, "error": err.Error()})
		return ErrorResult(fmt.Sprintf("Invalid regex pattern syntax: %v. Please fix your regex and try again.", err))
	}

	logger.InfoCF("discovery", "Regex search completed", map[string]any{"pattern": pattern, "results": len(res)})
	return formatDiscoveryResponse(res)
}

type BM25SearchTool struct {
	registry         *ToolRegistry
	maxSearchResults int

	// Cache: rebuilt only when the registry version changes.
	cacheMu      sync.Mutex
	cachedEngine *bm25CachedEngine
	cacheVersion uint64
}

func NewBM25SearchTool(r *ToolRegistry, maxSearchResults int) *BM25SearchTool {
	return &BM25SearchTool{registry: r, maxSearchResults: maxSearchResults}
}

func (t *BM25SearchTool) Name() string {
	return "tool_search_tool_bm25"
}

func (t *BM25SearchTool) Description() string {
	return "Search hidden tools on-demand with a natural language query. Returns a strict JSON tool reference bundle."
}

func (t *BM25SearchTool) UsageContract() ToolUsageContract {
	return ToolUsageContract{
		UseWhen:      "you need semantic hidden-tool discovery from a natural language query.",
		DoNotUseWhen: "you already know and can call the required visible tool directly.",
		HardRequirements: []string{
			"query must be a non-empty string.",
			"Search scope is hidden tools only; core tools are excluded.",
		},
	}
}

func (t *BM25SearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
		},
		"required": []string{"query"},
	}
}

func (t *BM25SearchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		// An empty string query will match every hidden tool,
		// dumping massive payloads into the context and burning tokens.
		return ErrorResult("Missing or invalid 'query' argument. Must be a non-empty string.")
	}

	logger.DebugCF("discovery", "BM25 search", map[string]any{"query": query})

	cached := t.getOrBuildEngine()
	if cached == nil {
		logger.DebugCF("discovery", "BM25 search: no hidden tools available", nil)
		return formatDiscoveryResponse(nil)
	}

	ranked := cached.engine.Search(query, t.maxSearchResults)
	if len(ranked) == 0 {
		logger.DebugCF("discovery", "BM25 search: no matches", map[string]any{"query": query})
		return formatDiscoveryResponse(nil)
	}

	results := make([]ToolSearchResult, len(ranked))
	for i, r := range ranked {
		results[i] = ToolSearchResult{
			Name:        r.Document.Name,
			Description: r.Document.Description,
		}
	}

	logger.InfoCF("discovery", "BM25 search completed", map[string]any{"query": query, "results": len(results)})
	return formatDiscoveryResponse(results)
}

// ToolSearchResult represents the result returned to the LLM.
// Parameters are omitted from discovery responses to save context tokens.
type ToolSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolReferenceBundle is the strict machine-readable envelope returned by
// tool discovery tools.
type ToolReferenceBundle struct {
	Type  string             `json:"type"`
	Tools []ToolSearchResult `json:"tools"`
}

func (r *ToolRegistry) SearchRegex(pattern string, maxSearchResults int) ([]ToolSearchResult, error) {
	if maxSearchResults <= 0 {
		return nil, nil
	}

	regex, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to compile regex pattern %q: %w", pattern, err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var results []ToolSearchResult

	// Iterate in sorted order for deterministic results across calls.
	for _, name := range r.sortedToolNames() {
		entry := r.tools[name]
		// Search only among the hidden tools (Core tools are already visible)
		if !entry.IsCore {
			// Directly call interface methods! No reflection/unmarshalling needed.
			desc := entry.Tool.Description()

			if regex.MatchString(name) || regex.MatchString(desc) {
				results = append(results, ToolSearchResult{
					Name:        name,
					Description: desc,
				})
				if len(results) >= maxSearchResults {
					break // Stop searching once we hit the max! Saves CPU.
				}
			}
		}
	}

	return results, nil
}

func formatDiscoveryResponse(results []ToolSearchResult) *ToolResult {
	if results == nil {
		results = []ToolSearchResult{}
	}
	bundle := ToolReferenceBundle{
		Type:  "tool_reference_bundle",
		Tools: results,
	}

	b, err := json.Marshal(bundle)
	if err != nil {
		return ErrorResult("Failed to format search results: " + err.Error())
	}
	return SilentResult(string(b))
}

// ParseToolReferenceBundle parses a strict tool discovery response.
func ParseToolReferenceBundle(raw string) (ToolReferenceBundle, error) {
	var bundle ToolReferenceBundle
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &bundle); err != nil {
		return ToolReferenceBundle{}, fmt.Errorf("invalid tool_reference_bundle JSON: %w", err)
	}
	if bundle.Type != "tool_reference_bundle" {
		return ToolReferenceBundle{}, fmt.Errorf("invalid bundle type: %q", bundle.Type)
	}
	return bundle, nil
}

// ExtractToolReferenceNames returns deduplicated discovered tool names from
// a tool_reference_bundle response.
func ExtractToolReferenceNames(raw string) ([]string, error) {
	bundle, err := ParseToolReferenceBundle(raw)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(bundle.Tools))
	out := make([]string, 0, len(bundle.Tools))
	for _, tool := range bundle.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

// Lightweight internal type used as corpus document for BM25.
type searchDoc struct {
	Name        string
	Description string
}

// bm25CachedEngine wraps a BM25Engine with its corpus snapshot.
type bm25CachedEngine struct {
	engine *utils.BM25Engine[searchDoc]
}

// snapshotToSearchDocs converts a HiddenToolSnapshot to BM25 searchDoc slice.
func snapshotToSearchDocs(snap HiddenToolSnapshot) []searchDoc {
	docs := make([]searchDoc, len(snap.Docs))
	for i, d := range snap.Docs {
		docs[i] = searchDoc{Name: d.Name, Description: d.Description}
	}
	return docs
}

// buildBM25Engine creates a BM25Engine from a slice of searchDocs.
func buildBM25Engine(docs []searchDoc) *utils.BM25Engine[searchDoc] {
	return utils.NewBM25Engine(
		docs,
		func(doc searchDoc) string {
			return doc.Name + " " + doc.Description
		},
	)
}

// getOrBuildEngine returns a cached BM25 engine, rebuilding it only when
// the registry version has changed (new tools registered).
func (t *BM25SearchTool) getOrBuildEngine() *bm25CachedEngine {
	// Fast path: optimistic check without locking.
	if t.cachedEngine != nil && t.cacheVersion == t.registry.Version() {
		return t.cachedEngine
	}

	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()

	// Snapshot + version are read under a single registry RLock,
	// guaranteeing consistency (no TOCTOU).
	snap := t.registry.SnapshotHiddenTools()

	// Re-check: another goroutine may have rebuilt while we waited for cacheMu.
	if t.cachedEngine != nil && t.cacheVersion == snap.Version {
		return t.cachedEngine
	}

	docs := snapshotToSearchDocs(snap)
	if len(docs) == 0 {
		t.cachedEngine = nil
		t.cacheVersion = snap.Version
		return nil
	}

	cached := &bm25CachedEngine{engine: buildBM25Engine(docs)}
	t.cachedEngine = cached
	t.cacheVersion = snap.Version
	logger.DebugCF("discovery", "BM25 engine rebuilt", map[string]any{"docs": len(docs), "version": snap.Version})
	return cached
}

// SearchBM25 ranks hidden tools against query using BM25 via utils.BM25Engine.
// This non-cached variant rebuilds the engine on every call. Used by tests
// and any code that doesn't hold a BM25SearchTool instance.
func (r *ToolRegistry) SearchBM25(query string, maxSearchResults int) []ToolSearchResult {
	snap := r.SnapshotHiddenTools()
	docs := snapshotToSearchDocs(snap)
	if len(docs) == 0 {
		return nil
	}

	ranked := buildBM25Engine(docs).Search(query, maxSearchResults)
	if len(ranked) == 0 {
		return nil
	}

	out := make([]ToolSearchResult, len(ranked))
	for i, r := range ranked {
		out[i] = ToolSearchResult{
			Name:        r.Document.Name,
			Description: r.Document.Description,
		}
	}
	return out
}
