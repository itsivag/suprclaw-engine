package gateway

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/itsivag/suprclaw/pkg/checkpoint"
	"github.com/itsivag/suprclaw/pkg/providers"
	"github.com/itsivag/suprclaw/pkg/session"
)

// checkpointEnabled returns true if the checkpoint service is configured.
// Writes a 503 response and returns false otherwise.
func (h *adminHandler) checkpointEnabled(w http.ResponseWriter) bool {
	if h.checkpointSvc == nil || !h.checkpointSvc.IsEnabled() {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"error": "checkpoint service is not enabled"})
		return false
	}
	return true
}

// resolveAgentAssets returns the workspace path and session store for agentID.
// Falls back to the default agent if agentID is not found.
func (h *adminHandler) resolveAgentAssets(w http.ResponseWriter, agentID string) (workspace string, store session.SessionStore, ok bool) {
	if h.agentLoop == nil {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"error": "agent loop unavailable"})
		return "", nil, false
	}
	registry := h.agentLoop.GetRegistry()
	inst, found := registry.GetAgent(agentID)
	if !found {
		inst = registry.GetDefaultAgent()
	}
	if inst == nil {
		writeJSON(w, http.StatusNotFound,
			map[string]string{"error": "agent not found: " + agentID})
		return "", nil, false
	}
	return inst.Workspace, inst.Sessions, true
}

// --- GET /api/admin/checkpoints?agentId=&sessionKey= ---

func (h *adminHandler) listCheckpoints(w http.ResponseWriter, r *http.Request) {
	if !h.checkpointEnabled(w) {
		return
	}
	agentID := r.URL.Query().Get("agentId")
	if agentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agentId is required"})
		return
	}
	sessionKey := r.URL.Query().Get("sessionKey")
	commits, err := h.checkpointSvc.ListCommits(agentID, sessionKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if commits == nil {
		commits = []*checkpoint.CommitManifest{}
	}
	writeJSON(w, http.StatusOK, commits)
}

// --- POST /api/admin/checkpoints ---

func (h *adminHandler) createCheckpoint(w http.ResponseWriter, r *http.Request) {
	if !h.checkpointEnabled(w) {
		return
	}
	var req struct {
		AgentID    string `json:"agentId"`
		SessionKey string `json:"sessionKey"`
		Label      string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.AgentID == "" || req.SessionKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agentId and sessionKey are required"})
		return
	}

	workspace, store, ok := h.resolveAgentAssets(w, req.AgentID)
	if !ok {
		return
	}

	var msgs []providers.Message
	if store != nil {
		msgs = store.GetHistory(req.SessionKey)
	}

	commit, err := h.checkpointSvc.CreateCommit(
		r.Context(), req.AgentID, req.SessionKey, workspace, msgs,
		req.Label, "manual", "",
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, commit)
}

// --- POST /api/admin/checkpoints/{commitId}/rollback ---

func (h *adminHandler) rollbackCheckpoint(w http.ResponseWriter, r *http.Request) {
	if !h.checkpointEnabled(w) {
		return
	}
	commitID := r.PathValue("commitId")
	if commitID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "commitId is required"})
		return
	}

	var req struct {
		AgentID string `json:"agentId"`
		Scope   string `json:"scope"` // "session", "workspace", or "all"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agentId is required"})
		return
	}
	scope := req.Scope
	if scope == "" {
		scope = "all"
	}
	if scope != "session" && scope != "workspace" && scope != "all" {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "scope must be 'session', 'workspace', or 'all'"})
		return
	}

	workspace, store, ok := h.resolveAgentAssets(w, req.AgentID)
	if !ok {
		return
	}

	var setHistory checkpoint.SetHistoryFunc
	if store != nil {
		setHistory = func(key string, history []providers.Message) {
			store.SetHistory(key, history)
		}
	}

	result, err := h.checkpointSvc.Rollback(req.AgentID, commitID, scope, workspace, setHistory)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// --- GET /api/admin/audit/actions?agentId=&limit= ---

func (h *adminHandler) listAuditActions(w http.ResponseWriter, r *http.Request) {
	if !h.checkpointEnabled(w) {
		return
	}
	agentID := r.URL.Query().Get("agentId")
	if agentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agentId is required"})
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	entries, err := h.checkpointSvc.QueryActionLog(agentID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []checkpoint.ActionEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// --- POST /api/admin/commits/{commitId}/revoke ---

func (h *adminHandler) revokeCommit(w http.ResponseWriter, r *http.Request) {
	if !h.checkpointEnabled(w) {
		return
	}
	commitID := r.PathValue("commitId")
	if commitID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "commitId is required"})
		return
	}

	var req struct {
		AgentID string `json:"agentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agentId is required"})
		return
	}

	m, err := h.checkpointSvc.RevokeCommit(req.AgentID, commitID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, m)
}
