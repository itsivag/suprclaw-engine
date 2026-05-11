package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/skills"
)

const adminSkillsSearchMaxResults = 20

// --- workspace resolution ---

// resolveSkillsWorkspace resolves the target workspace for skill operations.
// agentId is the primary input: the agent's configured workspace is used.
// workspacePath is an override/escape-hatch when agentId is absent.
// Never falls back to the default workspace — callers must provide one or the other.
func (h *adminHandler) resolveSkillsWorkspace(agentID, workspacePath string) (ws string, err error) {
	if workspacePath != "" {
		// Direct override — still validate it's not doing path traversal.
		if strings.Contains(workspacePath, "..") {
			return "", fmt.Errorf("workspacePath must not contain '..'")
		}
		expanded := filepath.Clean(expandHomePath(workspacePath))
		// Backward-compatible normalization: some callers historically passed
		// "<workspace>/skills" instead of the workspace root. Normalize this to
		// avoid creating nested ".../skills/skills/<slug>" installs.
		if filepath.Base(expanded) == "skills" {
			parent := filepath.Dir(expanded)
			if parent != "." && parent != string(filepath.Separator) {
				return parent, nil
			}
		}
		return expanded, nil
	}
	if agentID == "" {
		return "", fmt.Errorf("agentId or workspacePath is required")
	}
	if err := validateAgentID(agentID); err != nil {
		return "", err
	}
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return "", err
	}
	for _, a := range cfg.Agents.List {
		if a.ID == agentID {
			if a.Workspace != "" {
				return expandHomePath(a.Workspace), nil
			}
			// Agent exists but has no explicit workspace — use the conventional dir.
			return h.workspaceDir(agentID), nil
		}
	}
	return "", fmt.Errorf("agent %q not found", agentID)
}

// expandHomePath expands a leading ~/ to the user's home directory.
func expandHomePath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// --- skills infrastructure helpers ---

func (h *adminHandler) newSkillsInstallerFor(workspace string) (*skills.SkillInstaller, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, err
	}
	return skills.NewSkillInstaller(workspace, cfg.Tools.Skills.Github.Token, cfg.Tools.Skills.Github.Proxy)
}

func (h *adminHandler) newSkillsLoaderFor(workspace string) (*skills.SkillsLoader, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, err
	}

	globalSkillsDir := config.ResolveGlobalSkillsDir(cfg.Tools.Skills.GlobalDir)
	builtinSkillsDir := h.builtinSkillsDir()
	return skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir), nil
}

func (h *adminHandler) newSkillsRegistryMgr() (*skills.RegistryManager, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, err
	}
	return skills.NewRegistryManagerFromConfig(skills.RegistryConfig{
		MaxConcurrentSearches: cfg.Tools.Skills.MaxConcurrentSearches,
		ClawHub:               skills.ClawHubConfig(cfg.Tools.Skills.Registries.ClawHub),
	}), nil
}

func (h *adminHandler) builtinSkillsDir() string {
	return filepath.Join(filepath.Dir(h.configPath), "suprclaw", "skills")
}

func (h *adminHandler) buildSkillScopeInventory(extraWorkspaceByAgent map[string]string) (*skills.SkillScopeInventory, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, err
	}

	workspaceByAgent := make(map[string]string)
	if len(cfg.Agents.List) == 0 {
		workspaceByAgent["main"] = config.ResolveAgentWorkspaceByID("main", cfg.Agents.List, cfg.Agents.Defaults)
	} else {
		for i := range cfg.Agents.List {
			agentID := strings.TrimSpace(cfg.Agents.List[i].ID)
			if agentID == "" {
				continue
			}
			workspaceByAgent[agentID] = config.ResolveAgentWorkspaceByID(agentID, cfg.Agents.List, cfg.Agents.Defaults)
		}
	}

	for agentID, workspace := range extraWorkspaceByAgent {
		if strings.TrimSpace(agentID) == "" || strings.TrimSpace(workspace) == "" {
			continue
		}
		workspaceByAgent[agentID] = workspace
	}

	globalSkillsDir := config.ResolveGlobalSkillsDir(cfg.Tools.Skills.GlobalDir)
	return skills.BuildSkillScopeInventory(globalSkillsDir, workspaceByAgent)
}

func expectedInstallNamesFromRequest(req skillInstallRequest) []string {
	candidates := make([]string, 0)
	if strings.TrimSpace(req.Path) != "" {
		candidates = append(candidates, filepath.Base(req.Path))
	}
	if strings.TrimSpace(req.Slug) != "" {
		candidates = append(candidates, strings.TrimSpace(req.Slug))
	}
	if strings.TrimSpace(req.Repo) != "" {
		repoName := strings.TrimSpace(req.Repo)
		repoName = strings.TrimSuffix(repoName, "/")
		repoName = strings.TrimSuffix(repoName, ".git")
		repoName = filepath.Base(repoName)
		if repoName != "" && repoName != "." {
			candidates = append(candidates, repoName)
		}
	}

	dedup := make(map[string]struct{})
	names := make([]string, 0, len(candidates))
	for _, name := range candidates {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := dedup[name]; ok {
			continue
		}
		dedup[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func findPreflightScopeCollisions(
	inv *skills.SkillScopeInventory,
	agentID string,
	expectedNames []string,
	targetScope skills.SkillScope,
) *skills.SkillScopeCollisionError {
	if inv == nil || len(expectedNames) == 0 {
		return nil
	}

	collisions := make([]skills.SkillScopeCollision, 0)
	switch targetScope {
	case skills.SkillScopeGlobal:
		workspaceByName := make(map[string][]skills.SkillScopeRef)
		for _, refs := range inv.WorkspaceSkillsByAgent {
			for _, ref := range refs {
				workspaceByName[ref.Name] = append(workspaceByName[ref.Name], ref)
			}
		}
		for _, name := range expectedNames {
			conflicts := workspaceByName[name]
			if len(conflicts) == 0 {
				continue
			}
			for _, conflict := range conflicts {
				collisions = append(collisions, skills.SkillScopeCollision{
					Name: name,
					Winner: skills.SkillScopeRef{
						Name:   name,
						Source: string(skills.SkillScopeGlobal),
						Scope:  skills.SkillScopeGlobal,
						Path:   filepath.Join("<pending-install-global>", name, "SKILL.md"),
					},
					Conflict: conflict,
				})
			}
		}
	default:
		if len(inv.GlobalSkills) == 0 {
			return nil
		}
		globalByName := make(map[string][]skills.SkillScopeRef)
		for _, g := range inv.GlobalSkills {
			globalByName[g.Name] = append(globalByName[g.Name], g)
		}
		for _, name := range expectedNames {
			globals := globalByName[name]
			if len(globals) == 0 {
				continue
			}
			for _, g := range globals {
				collisions = append(collisions, skills.SkillScopeCollision{
					Name: name,
					Winner: skills.SkillScopeRef{
						Name:    name,
						Source:  string(skills.SkillScopeWorkspace),
						Scope:   skills.SkillScopeWorkspace,
						Path:    filepath.Join("<pending-install>", name, "SKILL.md"),
						AgentID: agentID,
					},
					Conflict: g,
				})
			}
		}
	}

	return skills.NewSkillScopeCollisionError(collisions)
}

func (h *adminHandler) validateCollisionPreflightAndPost(
	extraWorkspaceByAgent map[string]string,
	pendingAgentID string,
	expectedInstallNames []string,
	targetScope skills.SkillScope,
	stage string,
) error {
	inv, err := h.buildSkillScopeInventory(extraWorkspaceByAgent)
	if err != nil {
		return fmt.Errorf("build skill scope inventory (%s): %w", stage, err)
	}

	if stage == "preflight" {
		if preflightErr := findPreflightScopeCollisions(inv, pendingAgentID, expectedInstallNames, targetScope); preflightErr != nil {
			logger.WarnCF("gateway",
				"event=skill_scope_collision_preflight_reject skill mutation rejected by preflight",
				map[string]any{
					"code":            preflightErr.Code,
					"collision_count": len(preflightErr.Collisions),
					"agent_id":        pendingAgentID,
				},
			)
			return preflightErr
		}
	}

	if collisionErr := inv.CollisionError(); collisionErr != nil {
		logger.WarnCF("gateway",
			"event=skill_scope_collision_preflight_reject skill mutation rejected due to existing collision",
			map[string]any{
				"code":            collisionErr.Code,
				"collision_count": len(collisionErr.Collisions),
				"agent_id":        pendingAgentID,
			},
		)
		return collisionErr
	}
	return nil
}

func (h *adminHandler) resolveSkillInstallTargetScope(resolvedWorkspace string) (skills.SkillScope, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return skills.SkillScopeWorkspace, err
	}
	globalSkillsRoot := filepath.Clean(filepath.Dir(config.ResolveGlobalSkillsDir(cfg.Tools.Skills.GlobalDir)))
	workspaceRoot := filepath.Clean(resolvedWorkspace)
	if workspaceRoot == globalSkillsRoot {
		return skills.SkillScopeGlobal, nil
	}
	return skills.SkillScopeWorkspace, nil
}

func preflightWorkspaceContext(agentID, resolvedWorkspace string, targetScope skills.SkillScope) (string, map[string]string) {
	normalizedAgentID := strings.TrimSpace(agentID)
	if normalizedAgentID != "" {
		return normalizedAgentID, nil
	}
	if targetScope == skills.SkillScopeGlobal {
		return "global_override", nil
	}
	return "workspace_override", map[string]string{
		"workspace_override": resolvedWorkspace,
	}
}

// --- GET /api/admin/skills/inventory ---

func (h *adminHandler) skillsInventory(w http.ResponseWriter, r *http.Request) {
	inv, err := h.buildSkillScopeInventory(nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":                    "ok",
		"global_skills":             inv.GlobalSkills,
		"workspace_skills_by_agent": inv.WorkspaceSkillsByAgent,
		"collisions":                inv.Collisions,
	})
}

// --- GET /api/admin/skills?agentId=<id>&workspacePath=<path> ---

func (h *adminHandler) listSkills(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agentId")
	workspacePath := r.URL.Query().Get("workspacePath")

	ws, err := h.resolveSkillsWorkspace(agentID, workspacePath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	loader, err := h.newSkillsLoaderFor(ws)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	list := loader.ListSkills()
	if list == nil {
		list = []skills.SkillInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agentId":       agentID,
		"workspacePath": ws,
		"skills":        list,
	})
}

// --- GET /api/admin/skills/builtin ---

type builtinSkillEntry struct {
	Name string `json:"name"`
}

func (h *adminHandler) listBuiltinSkills(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(h.builtinSkillsDir())
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []builtinSkillEntry{})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	result := make([]builtinSkillEntry, 0)
	for _, e := range entries {
		if e.IsDir() {
			result = append(result, builtinSkillEntry{Name: e.Name()})
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// --- GET /api/admin/skills/search?q=<query> ---

func (h *adminHandler) searchSkills(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	rmgr, err := h.newSkillsRegistryMgr()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	results, err := rmgr.SearchAll(ctx, query, adminSkillsSearchMaxResults)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if results == nil {
		results = []skills.SearchResult{}
	}
	writeJSON(w, http.StatusOK, results)
}

// --- GET /api/admin/skills/{name}?agentId=<id>&workspacePath=<path> ---

func (h *adminHandler) showSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !reRelPath.MatchString(name) || strings.Contains(name, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid skill name"})
		return
	}

	agentID := r.URL.Query().Get("agentId")
	workspacePath := r.URL.Query().Get("workspacePath")

	ws, err := h.resolveSkillsWorkspace(agentID, workspacePath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	loader, err := h.newSkillsLoaderFor(ws)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	content, ok := loader.LoadSkill(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "skill not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"name":          name,
		"agentId":       agentID,
		"workspacePath": ws,
		"content":       content,
	})
}

// --- POST /api/admin/skills/install ---

type skillInstallRequest struct {
	AgentID       string `json:"agentId"`
	WorkspacePath string `json:"workspacePath"`
	Repo          string `json:"repo"`     // e.g. "owner/repo" or full GitHub URL
	Path          string `json:"path"`     // explicit repo subpath → sparse checkout
	Registry      string `json:"registry"` // optional: registry name (e.g. "clawhub")
	Slug          string `json:"slug"`     // required when registry is set
}

func (h *adminHandler) installSkill(w http.ResponseWriter, r *http.Request) {
	var req skillInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	// Validate mutually exclusive / dependent fields before workspace resolution.
	if req.Repo != "" && req.Registry != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo and registry are mutually exclusive"})
		return
	}
	if req.Path != "" && req.Repo == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path requires repo"})
		return
	}
	if req.Path != "" {
		cleaned := path.Clean(req.Path)
		if cleaned == "." || path.IsAbs(cleaned) || strings.Contains(cleaned, "..") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be relative and must not contain '..'"})
			return
		}
		req.Path = cleaned
	}

	ws, err := h.resolveSkillsWorkspace(req.AgentID, req.WorkspacePath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	targetScope, err := h.resolveSkillInstallTargetScope(ws)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	preflightAgentID, extraWorkspaces := preflightWorkspaceContext(req.AgentID, ws, targetScope)
	expectedNames := expectedInstallNamesFromRequest(req)
	if err := h.validateCollisionPreflightAndPost(extraWorkspaces, preflightAgentID, expectedNames, targetScope, "preflight"); err != nil {
		if writeSkillCollisionHTTPError(w, err) {
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if req.Registry != "" {
		h.installSkillFromRegistry(w, r, req, ws, targetScope, preflightAgentID, extraWorkspaces)
		return
	}

	if req.Repo == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo is required (or provide registry + slug)"})
		return
	}

	installer, err := h.newSkillsInstallerFor(ws)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if req.Path != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		if err := installer.InstallFromGitHubPath(ctx, req.Repo, req.Path); err != nil {
			if strings.Contains(err.Error(), "already exists") {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := h.validateCollisionPreflightAndPost(extraWorkspaces, preflightAgentID, nil, targetScope, "post-state"); err != nil {
			if writeSkillCollisionHTTPError(w, err) {
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if h.agentLoop != nil {
			if err := h.agentLoop.RefreshSkillCollisionState(); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "refresh collision state: " + err.Error()})
				return
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":         "ok",
			"installedSkill": filepath.Base(req.Path),
			"agentId":        req.AgentID,
			"workspacePath":  ws,
			"source": map[string]string{
				"repo": req.Repo,
				"path": req.Path,
			},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := installer.InstallFromGitHub(ctx, req.Repo); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := h.validateCollisionPreflightAndPost(extraWorkspaces, preflightAgentID, nil, targetScope, "post-state"); err != nil {
		if writeSkillCollisionHTTPError(w, err) {
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.agentLoop != nil {
		if err := h.agentLoop.RefreshSkillCollisionState(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "refresh collision state: " + err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"installedSkill": filepath.Base(req.Repo),
		"agentId":        req.AgentID,
		"workspacePath":  ws,
		"source": map[string]string{
			"repo": req.Repo,
			"path": "",
		},
	})
}

func (h *adminHandler) installSkillFromRegistry(
	w http.ResponseWriter,
	r *http.Request,
	req skillInstallRequest,
	ws string,
	targetScope skills.SkillScope,
	preflightAgentID string,
	extraWorkspaces map[string]string,
) {
	if req.Slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slug is required for registry installs"})
		return
	}

	rmgr, err := h.newSkillsRegistryMgr()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	registry := rmgr.GetRegistry(req.Registry)
	if registry == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "registry '" + req.Registry + "' not found or not enabled"})
		return
	}

	targetDir := filepath.Join(ws, "skills", req.Slug)
	if _, err := os.Stat(targetDir); err == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "skill '" + req.Slug + "' already installed"})
		return
	}

	if err := os.MkdirAll(filepath.Join(ws, "skills"), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	result, err := registry.DownloadAndInstall(ctx, req.Slug, "", targetDir)
	if err != nil {
		os.RemoveAll(targetDir) //nolint:errcheck
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if result.IsMalwareBlocked {
		os.RemoveAll(targetDir) //nolint:errcheck
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "skill '" + req.Slug + "' is flagged as malicious"})
		return
	}
	if err := h.validateCollisionPreflightAndPost(extraWorkspaces, preflightAgentID, nil, targetScope, "post-state"); err != nil {
		if writeSkillCollisionHTTPError(w, err) {
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.agentLoop != nil {
		if err := h.agentLoop.RefreshSkillCollisionState(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "refresh collision state: " + err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"installedSkill": req.Slug,
		"version":        result.Version,
		"is_suspicious":  result.IsSuspicious,
		"summary":        result.Summary,
		"agentId":        req.AgentID,
		"workspacePath":  ws,
	})
}

// --- POST /api/admin/skills/install-builtin ---

type installBuiltinRequest struct {
	AgentID       string `json:"agentId"`
	WorkspacePath string `json:"workspacePath"`
}

func (h *adminHandler) installBuiltinSkills(w http.ResponseWriter, r *http.Request) {
	var req installBuiltinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	ws, err := h.resolveSkillsWorkspace(req.AgentID, req.WorkspacePath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	targetScope, err := h.resolveSkillInstallTargetScope(ws)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	preflightAgentID, extraWorkspaces := preflightWorkspaceContext(req.AgentID, ws, targetScope)

	builtinDir := h.builtinSkillsDir()
	workspaceSkillsDir := filepath.Join(ws, "skills")

	entries, err := os.ReadDir(builtinDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"status":        "ok",
				"installed":     []string{},
				"agentId":       req.AgentID,
				"workspacePath": ws,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	expectedBuiltinNames := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			expectedBuiltinNames = append(expectedBuiltinNames, entry.Name())
		}
	}
	sort.Strings(expectedBuiltinNames)
	if err := h.validateCollisionPreflightAndPost(extraWorkspaces, preflightAgentID, expectedBuiltinNames, targetScope, "preflight"); err != nil {
		if writeSkillCollisionHTTPError(w, err) {
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	installed := make([]string, 0)
	errs := make([]string, 0)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		src := filepath.Join(builtinDir, entry.Name())
		dst := filepath.Join(workspaceSkillsDir, entry.Name())
		if err := os.MkdirAll(dst, 0o755); err != nil {
			errs = append(errs, entry.Name()+": "+err.Error())
			continue
		}
		if err := copyDir(src, dst); err != nil {
			errs = append(errs, entry.Name()+": "+err.Error())
			continue
		}
		installed = append(installed, entry.Name())
	}
	if err := h.validateCollisionPreflightAndPost(extraWorkspaces, preflightAgentID, nil, targetScope, "post-state"); err != nil {
		if writeSkillCollisionHTTPError(w, err) {
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.agentLoop != nil {
		if err := h.agentLoop.RefreshSkillCollisionState(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "refresh collision state: " + err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"installed":     installed,
		"errors":        errs,
		"agentId":       req.AgentID,
		"workspacePath": ws,
	})
}

// --- DELETE /api/admin/skills/{name}?agentId=<id>&workspacePath=<path> ---

func (h *adminHandler) removeSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !reRelPath.MatchString(name) || strings.Contains(name, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid skill name"})
		return
	}

	agentID := r.URL.Query().Get("agentId")
	workspacePath := r.URL.Query().Get("workspacePath")

	ws, err := h.resolveSkillsWorkspace(agentID, workspacePath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	installer, err := h.newSkillsInstallerFor(ws)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if err := installer.Uninstall(name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.agentLoop != nil {
		if err := h.agentLoop.RefreshSkillCollisionState(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "refresh collision state: " + err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"removedSkill":  name,
		"agentId":       agentID,
		"workspacePath": ws,
	})
}
