// SuprClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: Elastic License 2.0
//
// Copyright (c) 2026 SuprClaw contributors

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/mcp"
	"github.com/itsivag/suprclaw/pkg/tools"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpManagerRuntime interface {
	LoadFromMCPConfig(ctx context.Context, mcpCfg config.MCPConfig, workspacePath string) error
	GetServers() map[string]*mcp.ServerConnection
	CallTool(
		ctx context.Context,
		serverName, toolName string,
		arguments map[string]any,
	) (*sdkmcp.CallToolResult, error)
	Close() error
}

var newMCPManagerRuntime = func() mcpManagerRuntime {
	return mcp.NewManager()
}

type mcpRuntime struct {
	mu          sync.Mutex
	initialized bool
	managers    map[string]mcpManagerRuntime
}

func (r *mcpRuntime) takeManagers() map[string]mcpManagerRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	managers := r.managers
	r.managers = nil
	r.initialized = false
	return managers
}

func (r *mcpRuntime) swapState(managers map[string]mcpManagerRuntime, initialized bool) map[string]mcpManagerRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.managers
	r.managers = managers
	r.initialized = initialized
	return old
}

func (r *mcpRuntime) hasManager() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.managers) > 0
}

func (r *mcpRuntime) hasManagerForAgent(agentID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.managers) == 0 {
		return false
	}
	_, ok := r.managers[agentID]
	return ok
}

func (r *mcpRuntime) isInitialized() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.initialized
}

func hasEnabledMCPServers(mcpCfg config.MCPConfig) bool {
	if mcpCfg.Servers == nil || len(mcpCfg.Servers) == 0 {
		return false
	}
	for _, serverCfg := range mcpCfg.Servers {
		if serverCfg.Enabled {
			return true
		}
	}
	return false
}

func (al *AgentLoop) buildAndRegisterMCPManagers(
	ctx context.Context,
	cfg *config.Config,
	registry *AgentRegistry,
) (map[string]mcpManagerRuntime, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if registry == nil {
		return nil, fmt.Errorf("registry cannot be nil")
	}

	if !cfg.Tools.IsToolEnabled("mcp") && !cfg.Tools.IsToolEnabled("agent_browser") {
		return nil, nil
	}

	managers := make(map[string]mcpManagerRuntime)
	agentIDs := registry.ListAgentIDs()

	if cfg.Tools.IsToolEnabled("mcp") {
		mcpCfg := cfg.Tools.MCP
		if mcpCfg.Servers == nil || len(mcpCfg.Servers) == 0 {
			logger.WarnCF("agent", "MCP is enabled but no servers are configured, skipping MCP initialization", nil)
		} else if !hasEnabledMCPServers(mcpCfg) {
			logger.WarnCF("agent", "MCP is enabled but no valid servers are configured, skipping MCP initialization", nil)
		} else {
			uniqueTools := 0
			totalRegistrations := 0
			connectedManagers := 0

			for _, agentID := range agentIDs {
				agent, ok := registry.GetAgent(agentID)
				if !ok {
					continue
				}
				agentCfg, err := resolveAgentConfigForRuntime(cfg, agentID)
				if err != nil {
					return nil, err
				}
				resolvedMCPConfig, err := cfg.ResolvedMCPConfigForAgent(agentCfg)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve MCP config for agent %q: %w", agentID, err)
				}
				manager := newMCPManagerRuntime()
				if err := manager.LoadFromMCPConfig(ctx, resolvedMCPConfig, agent.Workspace); err != nil {
					logger.WarnCF("agent", "Failed to load MCP servers for agent",
						map[string]any{
							"agent_id": agentID,
							"error":    err.Error(),
						})
					if closeErr := manager.Close(); closeErr != nil {
						logger.ErrorCF("agent", "Failed to close MCP manager",
							map[string]any{
								"agent_id": agentID,
								"error":    closeErr.Error(),
							})
					}
					return nil, fmt.Errorf("failed to load MCP servers for agent %q: %w", agentID, err)
				}
				managers[agentID] = manager
				connectedManagers++

				servers := manager.GetServers()
				for serverName, conn := range servers {
					uniqueTools += len(conn.Tools)
					for _, tool := range conn.Tools {
						mcpTool := tools.NewMCPTool(manager, serverName, tool)
						if resolvedMCPConfig.Discovery.Enabled {
							agent.Tools.RegisterHidden(mcpTool)
						} else {
							agent.Tools.Register(mcpTool)
						}
						totalRegistrations++
						logger.DebugCF("agent", "Registered MCP tool",
							map[string]any{
								"agent_id": agentID,
								"server":   serverName,
								"tool":     tool.Name,
								"name":     mcpTool.Name(),
							})
					}
				}

				if resolvedMCPConfig.Enabled && resolvedMCPConfig.Discovery.Enabled {
					useBM25 := resolvedMCPConfig.Discovery.UseBM25
					useRegex := resolvedMCPConfig.Discovery.UseRegex
					if !useBM25 && !useRegex {
						if closeErr := manager.Close(); closeErr != nil {
							logger.ErrorCF("agent", "Failed to close MCP manager",
								map[string]any{
									"agent_id": agentID,
									"error":    closeErr.Error(),
								})
						}
						return nil, fmt.Errorf(
							"tool discovery is enabled but neither 'use_bm25' nor 'use_regex' is set to true in the configuration",
						)
					}
					maxSearchResults := resolvedMCPConfig.Discovery.MaxSearchResults
					if maxSearchResults <= 0 {
						maxSearchResults = 5
					}
					if useRegex {
						agent.Tools.Register(tools.NewRegexSearchTool(agent.Tools, maxSearchResults))
					}
					if useBM25 {
						agent.Tools.Register(tools.NewBM25SearchTool(agent.Tools, maxSearchResults))
					}
				}
			}

			logger.InfoCF("agent", "MCP tools registered successfully",
				map[string]any{
					"manager_count":       connectedManagers,
					"unique_tools":        uniqueTools,
					"total_registrations": totalRegistrations,
					"agent_count":         len(agentIDs),
				})
		}
	}

	if cfg.Tools.IsToolEnabled("agent_browser") {
		totalTools := 0
		for _, agentID := range agentIDs {
			agent, ok := registry.GetAgent(agentID)
			if !ok {
				continue
			}
			agentCfg, err := resolveAgentConfigForRuntime(cfg, agentID)
			if err != nil {
				return nil, err
			}
			agentBrowserTools, err := tools.NewAgentBrowserMCPTools(cfg, agentCfg)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize agent browser tools for agent %q: %w", agentID, err)
			}
			for _, agentBrowserTool := range agentBrowserTools {
				agent.Tools.Register(agentBrowserTool)
				totalTools++
			}
		}
		if totalTools > 0 {
			logger.InfoCF("agent", "Agent Browser MCP tools registered successfully",
				map[string]any{
					"tool_registrations": totalTools,
					"agent_count":        len(agentIDs),
				})
		}
	}

	return managers, nil
}

// ensureMCPInitialized loads MCP servers/tools for the current config and
// registry if MCP has not been initialized for the active runtime state yet.
func (al *AgentLoop) ensureMCPInitialized(ctx context.Context) error {
	cfg := al.GetConfig()
	if !cfg.Tools.IsToolEnabled("mcp") && !cfg.Tools.IsToolEnabled("agent_browser") {
		return nil
	}
	registry := al.GetRegistry()

	al.mcp.mu.Lock()
	defer al.mcp.mu.Unlock()
	if al.mcp.initialized {
		return nil
	}

	managers, err := al.buildAndRegisterMCPManagers(ctx, cfg, registry)
	if err != nil {
		return err
	}
	al.mcp.managers = managers
	al.mcp.initialized = true
	return nil
}

func resolveAgentConfigForRuntime(cfg *config.Config, agentID string) (config.AgentConfig, error) {
	if cfg == nil {
		return config.AgentConfig{}, fmt.Errorf("config cannot be nil")
	}
	if agentCfg, ok := cfg.FindAgentConfig(agentID); ok {
		return *agentCfg, nil
	}
	if strings.TrimSpace(agentID) == "" {
		return config.AgentConfig{}, fmt.Errorf("agent id is required")
	}
	return config.AgentConfig{ID: agentID}, nil
}
