package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/itsivag/suprclaw/pkg/agent"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/fileutil"
)

// Input validation regexes.
var (
	reAgentID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	reModel   = regexp.MustCompile(`^[a-zA-Z0-9._:/-]+$`)
	reRelPath = regexp.MustCompile(`^[a-zA-Z0-9._/-]+$`)
)

const adminReloadTimeout = 30 * time.Second

// baseDir returns the directory that holds config.json.
func (h *adminHandler) baseDir() string { return filepath.Dir(h.configPath) }

// workspaceDir returns the conventional workspace directory for an agent.
func (h *adminHandler) workspaceDir(agentID string) string {
	return filepath.Join(h.baseDir(), "workspace-"+agentID)
}

func validateAgentID(id string) error {
	if !reAgentID.MatchString(id) {
		return fmt.Errorf("agentId %q is invalid: must match ^[a-zA-Z0-9_-]+$", id)
	}
	return nil
}

// --- POST /api/admin/agents ---

type upsertAgentRequest struct {
	AgentID       string `json:"agentId"`
	WorkspacePath string `json:"workspacePath"`
	Model         string `json:"model"`
	DefaultAgent  bool   `json:"defaultAgent"`
}

func (h *adminHandler) upsertAgent(w http.ResponseWriter, r *http.Request) {
	var req upsertAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := validateAgentID(req.AgentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Model != "" && !reModel.MatchString(req.Model) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model contains invalid characters"})
		return
	}

	var result *config.AgentConfig
	if err := h.mutateCfg(func(cfg *config.Config) error {
		entry := config.AgentConfig{
			ID:        req.AgentID,
			Default:   req.DefaultAgent,
			Workspace: req.WorkspacePath,
		}
		if req.Model != "" {
			entry.Model = &config.AgentModelConfig{Primary: req.Model}
		}
		// If defaultAgent, clear default flag from all others.
		if req.DefaultAgent {
			for i := range cfg.Agents.List {
				cfg.Agents.List[i].Default = false
			}
		}
		for i, a := range cfg.Agents.List {
			if a.ID == req.AgentID {
				cfg.Agents.List[i] = entry
				result = &cfg.Agents.List[i]
				return nil
			}
		}
		cfg.Agents.List = append(cfg.Agents.List, entry)
		result = &cfg.Agents.List[len(cfg.Agents.List)-1]
		return nil
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// --- DELETE /api/admin/agents/{agentId} ---

func (h *adminHandler) deleteAgent(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentId")
	if err := validateAgentID(agentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	found := false
	if err := h.mutateCfg(func(cfg *config.Config) error {
		list := cfg.Agents.List[:0]
		for _, a := range cfg.Agents.List {
			if a.ID == agentID {
				found = true
				continue
			}
			list = append(list, a)
		}
		cfg.Agents.List = list
		return nil
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	if err := h.reloadAgentLoopFromConfig(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "agent removed from config but runtime sync failed: " + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- POST /api/admin/agents/{agentId}/wake ---

type wakeAgentRequest struct {
	SessionKey string `json:"sessionKey"`
	Message    string `json:"message"`
}

func (h *adminHandler) wakeAgent(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentId")
	if err := validateAgentID(agentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var req wakeAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.SessionKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionKey is required"})
		return
	}
	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}

	exe, err := os.Executable()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot locate binary: " + err.Error()})
		return
	}

	out, err := exec.Command(exe, "agent", "--session", req.SessionKey, "--message", req.Message).CombinedOutput()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":  err.Error(),
			"output": string(out),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": string(out)})
}

type sessionOpRequest struct {
	SessionKey string `json:"sessionKey"`
}

// --- POST /api/admin/agents/{agentId}/sessions/new ---
func (h *adminHandler) newSession(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentId")
	if err := validateAgentID(agentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if h.agentLoop == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "agent loop not initialized"})
		return
	}

	var req sessionOpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.SessionKey) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionKey is required"})
		return
	}

	if err := h.agentLoop.ResetSession(agentID, req.SessionKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"agentId":    agentID,
		"sessionKey": req.SessionKey,
	})
}

// --- POST /api/admin/agents/{agentId}/sessions/compact ---
func (h *adminHandler) compactSession(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentId")
	if err := validateAgentID(agentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if h.agentLoop == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "agent loop not initialized"})
		return
	}

	var req sessionOpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.SessionKey) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionKey is required"})
		return
	}

	if err := h.agentLoop.CompactSession(agentID, req.SessionKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"agentId":    agentID,
		"sessionKey": req.SessionKey,
	})
}

// --- POST /api/admin/runtime/reload ---

func (h *adminHandler) reloadRuntime(w http.ResponseWriter, r *http.Request) {
	if err := h.reloadAgentLoopFromConfig(); err == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
		return
	}

	// Fallback: fire-and-forget supervisor restart when in-process reload fails.
	go func() {
		time.Sleep(500 * time.Millisecond)
		exec.Command("supervisorctl", "restart", "suprclaw-engine-gateway").Run() //nolint:errcheck
	}()
	writeJSON(
		w,
		http.StatusAccepted,
		map[string]string{"status": "restarting", "error": "in-process reload failed; falling back to supervisor restart"},
	)
}

func (h *adminHandler) reloadAgentLoopFromConfig() error {
	if h.agentLoop == nil {
		return fmt.Errorf("agent loop not initialized")
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	provider := h.agentLoop.CurrentProvider()
	if provider == nil {
		return fmt.Errorf("current provider unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), adminReloadTimeout)
	defer cancel()

	if err := h.agentLoop.ReloadProviderAndConfig(ctx, provider, cfg); err != nil {
		return err
	}
	if err := verifyAgentRegistrySync(h.agentLoop, cfg); err != nil {
		return err
	}
	return nil
}

func verifyAgentRegistrySync(loop *agent.AgentLoop, cfg *config.Config) error {
	if loop == nil || cfg == nil {
		return fmt.Errorf("invalid reload state")
	}
	registry := loop.GetRegistry()
	for _, a := range cfg.Agents.List {
		if _, ok := registry.GetAgent(a.ID); !ok {
			return fmt.Errorf("agent %q missing from runtime registry after reload", a.ID)
		}
	}
	return nil
}

// --- POST /api/admin/runtime/stop ---

func (h *adminHandler) stopRuntime(w http.ResponseWriter, r *http.Request) {
	go func() {
		time.Sleep(200 * time.Millisecond)
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			p.Signal(os.Interrupt) //nolint:errcheck
		}
	}()
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

// waitForPort blocks until addr is connectable or timeout elapses.
//
//nolint:unused
func waitForPort(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// --- POST /api/admin/workspaces/bootstrap ---

type skillFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type bootstrapWorkspaceRequest struct {
	AgentID  string      `json:"agentId"`
	AgentsMD string      `json:"agentsMd"`
	Skills   []skillFile `json:"skills"`
}

func (h *adminHandler) bootstrapWorkspace(w http.ResponseWriter, r *http.Request) {
	var req bootstrapWorkspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := validateAgentID(req.AgentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	wsDir := h.workspaceDir(req.AgentID)
	if err := os.MkdirAll(wsDir, 0o750); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir: " + err.Error()})
		return
	}

	if req.AgentsMD != "" {
		if err := fileutil.WriteFileAtomic(filepath.Join(wsDir, "AGENTS.md"), []byte(req.AgentsMD), 0o640); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write AGENTS.md: " + err.Error()})
			return
		}
	}

	for _, sf := range req.Skills {
		// Validate filename: no path separators, no .., relative only.
		if !reRelPath.MatchString(sf.Name) || strings.Contains(sf.Name, "..") || filepath.IsAbs(sf.Name) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid skill name: " + sf.Name})
			return
		}
		dest := filepath.Join(wsDir, sf.Name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir for skill: " + err.Error()})
			return
		}
		if err := fileutil.WriteFileAtomic(dest, []byte(sf.Content), 0o640); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write skill " + sf.Name + ": " + err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "workspace": wsDir})
}

// --- DELETE /api/admin/workspaces/{agentId} ---

func (h *adminHandler) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentId")
	if err := validateAgentID(agentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	wsDir := h.workspaceDir(agentID)
	if err := os.RemoveAll(wsDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- GET /api/admin/workspaces/{agentId}/files ---

// allowedWorkspaceExt is the set of extensions readable via the files API.
var allowedWorkspaceExt = map[string]bool{
	".md":   true,
	".txt":  true,
	".yaml": true,
	".yml":  true,
	".json": true,
}

func (h *adminHandler) listWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentId")
	if err := validateAgentID(agentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	wsDir := h.workspaceDir(agentID)
	var files []string
	err := filepath.WalkDir(wsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !allowedWorkspaceExt[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, _ := filepath.Rel(wsDir, path)
		files = append(files, rel)
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if files == nil {
		files = []string{}
	}
	writeJSON(w, http.StatusOK, files)
}

// --- GET /api/admin/workspaces/{agentId}/files/{fileName} ---

func (h *adminHandler) getWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentId")
	fileName := r.PathValue("fileName")
	if err := validateAgentID(agentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !reRelPath.MatchString(fileName) || strings.Contains(fileName, "..") || filepath.IsAbs(fileName) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file name"})
		return
	}
	if !allowedWorkspaceExt[strings.ToLower(filepath.Ext(fileName))] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "file type not allowed"})
		return
	}
	wsDir := h.workspaceDir(agentID)
	fullPath := filepath.Join(wsDir, fileName)
	// Ensure the resolved path is still inside the workspace.
	if rel, err := filepath.Rel(wsDir, fullPath); err != nil || strings.HasPrefix(rel, "..") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "path traversal not allowed"})
		return
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": fileName, "content": string(data)})
}

// --- POST /api/admin/marketplace/install ---

type marketplaceInstallRequest struct {
	RepoURL    string   `json:"repoUrl"`
	Paths      []string `json:"paths"` // sparse checkout paths
	AgentID    string   `json:"agentId"`
	DestSubdir string   `json:"destSubdir"` // relative subdir inside workspace (optional)
}

func (h *adminHandler) marketplaceInstall(w http.ResponseWriter, r *http.Request) {
	var req marketplaceInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.RepoURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repoUrl is required"})
		return
	}
	if err := validateAgentID(req.AgentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.DestSubdir != "" {
		if !reRelPath.MatchString(req.DestSubdir) || strings.Contains(req.DestSubdir, "..") || filepath.IsAbs(req.DestSubdir) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "destSubdir is invalid"})
			return
		}
	}
	for _, p := range req.Paths {
		if strings.Contains(p, "..") || filepath.IsAbs(p) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path " + p + " is invalid"})
			return
		}
	}

	tmpDir, err := os.MkdirTemp("", "suprclaw-mkt-*")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mktemp: " + err.Error()})
		return
	}
	defer os.RemoveAll(tmpDir)

	// Sparse clone.
	run := func(args ...string) error {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w\n%s", strings.Join(args, " "), err, out)
		}
		return nil
	}

	if err := run("git", "clone", "--no-checkout", "--filter=blob:none", "--sparse", req.RepoURL, "."); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if len(req.Paths) > 0 {
		sparseArgs := append([]string{"git", "sparse-checkout", "set"}, req.Paths...)
		if err := run(sparseArgs...); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	if err := run("git", "checkout"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	wsDir := h.workspaceDir(req.AgentID)
	destDir := wsDir
	if req.DestSubdir != "" {
		destDir = filepath.Join(wsDir, req.DestSubdir)
	}
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir dest: " + err.Error()})
		return
	}

	// Copy checked-out files into the workspace.
	if err := copyDir(tmpDir, destDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "copy: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "dest": destDir})
}

// copyDir recursively copies src into dst, skipping .git.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		// Skip .git directory.
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		if parts[0] == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return fileutil.WriteFileAtomic(target, data, 0o640)
	})
}

// --- POST /api/admin/mcp/configure ---

func (h *adminHandler) mcpConfigure(w http.ResponseWriter, r *http.Request) {
	var servers map[string]config.MCPServerConfig
	if err := json.NewDecoder(r.Body).Decode(&servers); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := h.mutateCfg(func(cfg *config.Config) error {
		cfg.Tools.MCP.Servers = servers
		return nil
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
