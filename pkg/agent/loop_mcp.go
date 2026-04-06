// SuprClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: Elastic License 2.0
//
// Copyright (c) 2026 SuprClaw contributors

package agent

import (
	"context"
	"fmt"
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
	mu      sync.Mutex
	manager mcpManagerRuntime
}

func (r *mcpRuntime) takeManager() mcpManagerRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	manager := r.manager
	r.manager = nil
	return manager
}

func (r *mcpRuntime) swapManager(manager mcpManagerRuntime) mcpManagerRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.manager
	r.manager = manager
	return old
}

func (r *mcpRuntime) hasManager() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manager != nil
}

func hasEnabledMCPServers(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	if !cfg.Tools.IsToolEnabled("mcp") {
		return false
	}
	mcpCfg := cfg.Tools.MCP
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

func (al *AgentLoop) buildAndRegisterMCPManager(
	ctx context.Context,
	cfg *config.Config,
	registry *AgentRegistry,
) (mcpManagerRuntime, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if registry == nil {
		return nil, fmt.Errorf("registry cannot be nil")
	}

	if !cfg.Tools.IsToolEnabled("mcp") {
		return nil, nil
	}

	mcpCfg := cfg.Tools.MCP
	if mcpCfg.Servers == nil || len(mcpCfg.Servers) == 0 {
		logger.WarnCF("agent", "MCP is enabled but no servers are configured, skipping MCP initialization", nil)
		return nil, nil
	}

	if !hasEnabledMCPServers(cfg) {
		logger.WarnCF("agent", "MCP is enabled but no valid servers are configured, skipping MCP initialization", nil)
		return nil, nil
	}

	mcpManager := newMCPManagerRuntime()
	defaultAgent := registry.GetDefaultAgent()
	workspacePath := cfg.WorkspacePath()
	if defaultAgent != nil && defaultAgent.Workspace != "" {
		workspacePath = defaultAgent.Workspace
	}

	if err := mcpManager.LoadFromMCPConfig(ctx, mcpCfg, workspacePath); err != nil {
		logger.WarnCF("agent", "Failed to load MCP servers, MCP tools will not be available",
			map[string]any{
				"error": err.Error(),
			})
		if closeErr := mcpManager.Close(); closeErr != nil {
			logger.ErrorCF("agent", "Failed to close MCP manager",
				map[string]any{
					"error": closeErr.Error(),
				})
		}
		return nil, fmt.Errorf("failed to load MCP servers: %w", err)
	}

	// Register MCP tools for all agents
	servers := mcpManager.GetServers()
	uniqueTools := 0
	totalRegistrations := 0
	agentIDs := registry.ListAgentIDs()
	agentCount := len(agentIDs)

	for serverName, conn := range servers {
		uniqueTools += len(conn.Tools)
		for _, tool := range conn.Tools {
			for _, agentID := range agentIDs {
				agent, ok := registry.GetAgent(agentID)
				if !ok {
					continue
				}

				mcpTool := tools.NewMCPTool(mcpManager, serverName, tool)

				if cfg.Tools.MCP.Discovery.Enabled {
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
	}
	logger.InfoCF("agent", "MCP tools registered successfully",
		map[string]any{
			"server_count":        len(servers),
			"unique_tools":        uniqueTools,
			"total_registrations": totalRegistrations,
			"agent_count":         agentCount,
		})

	// Initializes Discovery Tools only if enabled by configuration.
	if cfg.Tools.MCP.Enabled && cfg.Tools.MCP.Discovery.Enabled {
		useBM25 := cfg.Tools.MCP.Discovery.UseBM25
		useRegex := cfg.Tools.MCP.Discovery.UseRegex

		// Fail fast: If discovery is enabled but no search method is turned on.
		if !useBM25 && !useRegex {
			if closeErr := mcpManager.Close(); closeErr != nil {
				logger.ErrorCF("agent", "Failed to close MCP manager",
					map[string]any{
						"error": closeErr.Error(),
					})
			}
			return nil, fmt.Errorf(
				"tool discovery is enabled but neither 'use_bm25' nor 'use_regex' is set to true in the configuration",
			)
		}

		ttl := cfg.Tools.MCP.Discovery.TTL
		if ttl <= 0 {
			ttl = 5 // Default value.
		}

		maxSearchResults := cfg.Tools.MCP.Discovery.MaxSearchResults
		if maxSearchResults <= 0 {
			maxSearchResults = 5 // Default value.
		}

		logger.InfoCF("agent", "Initializing tool discovery", map[string]any{
			"bm25": useBM25, "regex": useRegex, "ttl": ttl, "max_results": maxSearchResults,
		})

		for _, agentID := range agentIDs {
			agent, ok := registry.GetAgent(agentID)
			if !ok {
				continue
			}

			if useRegex {
				agent.Tools.Register(tools.NewRegexSearchTool(agent.Tools, ttl, maxSearchResults))
			}
			if useBM25 {
				agent.Tools.Register(tools.NewBM25SearchTool(agent.Tools, ttl, maxSearchResults))
			}
		}
	}

	return mcpManager, nil
}

// ensureMCPInitialized loads MCP servers/tools for the current config and
// registry if MCP has not been initialized for the active runtime state yet.
func (al *AgentLoop) ensureMCPInitialized(ctx context.Context) error {
	cfg := al.GetConfig()
	if !cfg.Tools.IsToolEnabled("mcp") {
		return nil
	}
	registry := al.GetRegistry()

	al.mcp.mu.Lock()
	defer al.mcp.mu.Unlock()
	if al.mcp.manager != nil {
		return nil
	}

	manager, err := al.buildAndRegisterMCPManager(ctx, cfg, registry)
	if err != nil {
		return err
	}
	al.mcp.manager = manager
	return nil
}
