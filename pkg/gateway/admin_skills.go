package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/skills"
)

const adminSkillsSearchMaxResults = 20

// --- helpers ---

func (h *adminHandler) newSkillsInstaller() (*skills.SkillInstaller, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, err
	}
	return skills.NewSkillInstaller(
		cfg.WorkspacePath(),
		cfg.Tools.Skills.Github.Token,
		cfg.Tools.Skills.Github.Proxy,
	)
}

func (h *adminHandler) newSkillsLoader() (*skills.SkillsLoader, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, err
	}
	globalDir := filepath.Dir(h.configPath)
	globalSkillsDir := filepath.Join(globalDir, "skills")
	builtinSkillsDir := filepath.Join(globalDir, "suprclaw", "skills")
	return skills.NewSkillsLoader(cfg.WorkspacePath(), globalSkillsDir, builtinSkillsDir), nil
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

// --- GET /api/admin/skills ---

func (h *adminHandler) listSkills(w http.ResponseWriter, r *http.Request) {
	loader, err := h.newSkillsLoader()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	list := loader.ListSkills()
	if list == nil {
		list = []skills.SkillInfo{}
	}
	writeJSON(w, http.StatusOK, list)
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

// --- GET /api/admin/skills/{name} ---

func (h *adminHandler) showSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !reRelPath.MatchString(name) || strings.Contains(name, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid skill name"})
		return
	}
	loader, err := h.newSkillsLoader()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	content, ok := loader.LoadSkill(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "skill not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "content": content})
}

// --- POST /api/admin/skills/install ---

type skillInstallRequest struct {
	Repo     string `json:"repo"`     // e.g. "owner/repo/path" or full GitHub URL
	Registry string `json:"registry"` // optional: registry name (e.g. "clawhub")
	Slug     string `json:"slug"`     // required when registry is set
}

func (h *adminHandler) installSkill(w http.ResponseWriter, r *http.Request) {
	var req skillInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if req.Registry != "" {
		h.installSkillFromRegistry(w, r, req)
		return
	}

	if req.Repo == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo is required"})
		return
	}

	installer, err := h.newSkillsInstaller()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *adminHandler) installSkillFromRegistry(w http.ResponseWriter, r *http.Request, req skillInstallRequest) {
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

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	targetDir := filepath.Join(cfg.WorkspacePath(), "skills", req.Slug)
	if _, err := os.Stat(targetDir); err == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "skill '" + req.Slug + "' already installed"})
		return
	}

	if err := os.MkdirAll(filepath.Join(cfg.WorkspacePath(), "skills"), 0o755); err != nil {
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

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"version":       result.Version,
		"is_suspicious": result.IsSuspicious,
		"summary":       result.Summary,
	})
}

// --- POST /api/admin/skills/install-builtin ---

func (h *adminHandler) installBuiltinSkills(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	builtinDir := h.builtinSkillsDir()
	workspaceSkillsDir := filepath.Join(cfg.WorkspacePath(), "skills")

	entries, err := os.ReadDir(builtinDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "installed": []string{}})
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

	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"installed": installed,
		"errors":    errs,
	})
}

// --- DELETE /api/admin/skills/{name} ---

func (h *adminHandler) removeSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !reRelPath.MatchString(name) || strings.Contains(name, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid skill name"})
		return
	}

	installer, err := h.newSkillsInstaller()
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

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
