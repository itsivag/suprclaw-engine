package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/itsivag/suprclaw/pkg/providers"
)

type toolSchemaCacheKey struct {
	SessionKey   string
	AgentID      string
	ProviderName string
	Model        string
	Fingerprint  string
}

type ToolSchemaCache struct {
	mu      sync.RWMutex
	entries map[toolSchemaCacheKey][]providers.ToolDefinition
}

func NewToolSchemaCache() *ToolSchemaCache {
	return &ToolSchemaCache{entries: make(map[toolSchemaCacheKey][]providers.ToolDefinition)}
}

func normalizeToolDefs(defs []providers.ToolDefinition) []providers.ToolDefinition {
	cloned := make([]providers.ToolDefinition, len(defs))
	copy(cloned, defs)
	sort.Slice(cloned, func(i, j int) bool {
		return cloned[i].Function.Name < cloned[j].Function.Name
	})
	return cloned
}

func computeToolsetFingerprint(defs []providers.ToolDefinition) string {
	normalized := normalizeToolDefs(defs)
	payload, _ := json.Marshal(normalized)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func cloneToolDefs(defs []providers.ToolDefinition) []providers.ToolDefinition {
	cloned := make([]providers.ToolDefinition, len(defs))
	copy(cloned, defs)
	return cloned
}

func (c *ToolSchemaCache) GetOrSet(
	sessionKey, agentID, providerName, model string,
	defs []providers.ToolDefinition,
) (cached []providers.ToolDefinition, fingerprint string, hit bool) {
	fingerprint = computeToolsetFingerprint(defs)
	key := toolSchemaCacheKey{
		SessionKey:   sessionKey,
		AgentID:      agentID,
		ProviderName: providerName,
		Model:        model,
		Fingerprint:  fingerprint,
	}

	c.mu.RLock()
	if existing, ok := c.entries[key]; ok {
		c.mu.RUnlock()
		return cloneToolDefs(existing), fingerprint, true
	}
	c.mu.RUnlock()

	normalized := normalizeToolDefs(defs)
	c.mu.Lock()
	if existing, ok := c.entries[key]; ok {
		c.mu.Unlock()
		return cloneToolDefs(existing), fingerprint, true
	}
	c.entries[key] = cloneToolDefs(normalized)
	c.mu.Unlock()

	return normalized, fingerprint, false
}

func (c *ToolSchemaCache) invalidateIf(predicate func(toolSchemaCacheKey) bool) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0
	for key := range c.entries {
		if !predicate(key) {
			continue
		}
		delete(c.entries, key)
		removed++
	}
	return removed
}

func (c *ToolSchemaCache) InvalidateSession(sessionKey string) int {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return 0
	}
	return c.invalidateIf(func(key toolSchemaCacheKey) bool {
		return key.SessionKey == sessionKey
	})
}

func (c *ToolSchemaCache) InvalidateAgentProviderModel(
	agentID, providerName, model string,
) int {
	agentID = strings.TrimSpace(agentID)
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	if agentID == "" || providerName == "" || model == "" {
		return 0
	}
	return c.invalidateIf(func(key toolSchemaCacheKey) bool {
		return key.AgentID == agentID &&
			key.ProviderName == providerName &&
			key.Model == model
	})
}

func (c *ToolSchemaCache) InvalidateAgent(agentID string) int {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return 0
	}
	return c.invalidateIf(func(key toolSchemaCacheKey) bool {
		return key.AgentID == agentID
	})
}

func (c *ToolSchemaCache) InvalidateAll() int {
	return c.invalidateIf(func(toolSchemaCacheKey) bool { return true })
}

func (c *ToolSchemaCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
