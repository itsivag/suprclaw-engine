package gateway

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/cron"
)

type adminHandler struct {
	configPath  string
	cronService *cron.CronService
	secret      string
	mu          sync.Mutex // serialises all config mutations
}

func newAdminHandler(configPath string, cs *cron.CronService, secret string) *adminHandler {
	return &adminHandler{configPath: configPath, cronService: cs, secret: secret}
}

func (h *adminHandler) registerRoutes(mux *http.ServeMux) {
	// Cron
	mux.HandleFunc("GET /api/admin/cron/jobs", h.auth(h.listJobs))
	mux.HandleFunc("POST /api/admin/cron/jobs", h.auth(h.addJob))
	mux.HandleFunc("DELETE /api/admin/cron/jobs/{id}", h.auth(h.removeJob))
	mux.HandleFunc("PATCH /api/admin/cron/jobs/{id}", h.auth(h.patchJob))

	// Config
	mux.HandleFunc("GET /api/admin/config", h.auth(h.getConfig))
	mux.HandleFunc("PUT /api/admin/config", h.auth(h.putConfig))
	mux.HandleFunc("PATCH /api/admin/config", h.auth(h.patchConfig))

	// Agents
	mux.HandleFunc("POST /api/admin/agents", h.auth(h.upsertAgent))
	mux.HandleFunc("DELETE /api/admin/agents/{agentId}", h.auth(h.deleteAgent))
	mux.HandleFunc("POST /api/admin/agents/{agentId}/wake", h.auth(h.wakeAgent))

	// Runtime
	mux.HandleFunc("POST /api/admin/runtime/reload", h.auth(h.reloadRuntime))

	// Workspaces
	mux.HandleFunc("POST /api/admin/workspaces/bootstrap", h.auth(h.bootstrapWorkspace))
	mux.HandleFunc("DELETE /api/admin/workspaces/{agentId}", h.auth(h.deleteWorkspace))
	mux.HandleFunc("GET /api/admin/workspaces/{agentId}/files", h.auth(h.listWorkspaceFiles))
	mux.HandleFunc("GET /api/admin/workspaces/{agentId}/files/{fileName}", h.auth(h.getWorkspaceFile))

	// Marketplace
	mux.HandleFunc("POST /api/admin/marketplace/install", h.auth(h.marketplaceInstall))

	// MCP
	mux.HandleFunc("POST /api/admin/mcp/configure", h.auth(h.mcpConfigure))

	// Skills — specific paths before wildcard
	mux.HandleFunc("GET /api/admin/skills", h.auth(h.listSkills))
	mux.HandleFunc("GET /api/admin/skills/builtin", h.auth(h.listBuiltinSkills))
	mux.HandleFunc("GET /api/admin/skills/search", h.auth(h.searchSkills))
	mux.HandleFunc("POST /api/admin/skills/install", h.auth(h.installSkill))
	mux.HandleFunc("POST /api/admin/skills/install-builtin", h.auth(h.installBuiltinSkills))
	mux.HandleFunc("GET /api/admin/skills/{name}", h.auth(h.showSkill))
	mux.HandleFunc("DELETE /api/admin/skills/{name}", h.auth(h.removeSkill))
}

func (h *adminHandler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.secret == "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin API is disabled; set admin_secret to enable"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+h.secret {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

// --- Cron handlers ---

func (h *adminHandler) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs := h.cronService.ListJobs(true)
	if jobs == nil {
		jobs = []cron.CronJob{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

type addJobRequest struct {
	Name     string            `json:"name"`
	Message  string            `json:"message"`
	Deliver  bool              `json:"deliver"`
	Channel  string            `json:"channel"`
	To       string            `json:"to"`
	Schedule cron.CronSchedule `json:"schedule"`
}

func (h *adminHandler) addJob(w http.ResponseWriter, r *http.Request) {
	var req addJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	job, err := h.cronService.AddJob(req.Name, req.Schedule, req.Message, req.Deliver, req.Channel, req.To)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (h *adminHandler) removeJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !h.cronService.RemoveJob(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *adminHandler) patchJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	job := h.cronService.EnableJob(id, body.Enabled)
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// --- Config handlers ---

func (h *adminHandler) getConfig(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	cfg, err := config.LoadConfig(h.configPath)
	h.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (h *adminHandler) putConfig(w http.ResponseWriter, r *http.Request) {
	var newCfg config.Config
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	h.mu.Lock()
	err := config.SaveConfig(h.configPath, &newCfg)
	h.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *adminHandler) patchConfig(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if err := h.mutateCfg(func(cfg *config.Config) error {
		return applyMergePatch(cfg, patch)
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// mutateCfg loads config, calls fn to modify it, then saves atomically under the mutex.
func (h *adminHandler) mutateCfg(fn func(*config.Config) error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return config.SaveConfig(h.configPath, cfg)
}

// applyMergePatch applies a JSON Merge Patch to cfg in-place via round-trip.
func applyMergePatch(cfg *config.Config, patch map[string]any) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var dst map[string]any
	if err := json.Unmarshal(raw, &dst); err != nil {
		return err
	}
	mergeMap(dst, patch)
	merged, err := json.Marshal(dst)
	if err != nil {
		return err
	}
	return json.Unmarshal(merged, cfg)
}

// mergeMap recursively merges src into dst (JSON Merge Patch semantics).
func mergeMap(dst, src map[string]any) {
	for key, srcVal := range src {
		if srcVal == nil {
			delete(dst, key)
			continue
		}
		srcMap, srcIsMap := srcVal.(map[string]any)
		dstMap, dstIsMap := dst[key].(map[string]any)
		if srcIsMap && dstIsMap {
			mergeMap(dstMap, srcMap)
		} else {
			dst[key] = srcVal
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
