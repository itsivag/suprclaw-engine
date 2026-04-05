package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/heartbeat"
)

type adminBadRequestError struct {
	msg string
}

func (e *adminBadRequestError) Error() string { return e.msg }

type adminNotFoundError struct {
	msg string
}

func (e *adminNotFoundError) Error() string { return e.msg }

type heartbeatRuntimeSyncError struct {
	err error
}

func (e *heartbeatRuntimeSyncError) Error() string {
	return e.err.Error()
}

func (e *heartbeatRuntimeSyncError) Unwrap() error {
	return e.err
}

func (h *adminHandler) getHeartbeatConfig(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	cfg, err := config.LoadConfig(h.configPath)
	h.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cfg.Heartbeat)
}

func (h *adminHandler) putHeartbeatConfig(w http.ResponseWriter, r *http.Request) {
	var hb config.HeartbeatConfig
	if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	err := h.mutateHeartbeatConfigAndApply(func(cfg *config.Config) error {
		cfg.Heartbeat = hb
		return nil
	})
	if err != nil {
		h.writeHeartbeatMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hb)
}

func (h *adminHandler) createHeartbeatJob(w http.ResponseWriter, r *http.Request) {
	var job config.HeartbeatJobConfig
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	err := h.mutateHeartbeatConfigAndApply(func(cfg *config.Config) error {
		if strings.TrimSpace(job.AgentID) == "" {
			return &adminBadRequestError{msg: "agent_id is required"}
		}
		for _, existing := range cfg.Heartbeat.Jobs {
			if existing.AgentID == job.AgentID {
				return &adminBadRequestError{msg: fmt.Sprintf("heartbeat job for agent %q already exists", job.AgentID)}
			}
		}
		cfg.Heartbeat.Jobs = append(cfg.Heartbeat.Jobs, job)
		return nil
	})
	if err != nil {
		h.writeHeartbeatMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (h *adminHandler) updateHeartbeatJob(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(r.PathValue("agentId"))
	if err := validateAgentID(agentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	var job config.HeartbeatJobConfig
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	err := h.mutateHeartbeatConfigAndApply(func(cfg *config.Config) error {
		if strings.TrimSpace(job.AgentID) == "" {
			return &adminBadRequestError{msg: "agent_id is required"}
		}
		if job.AgentID != agentID {
			return &adminBadRequestError{msg: "path agentId must match body agent_id"}
		}
		for i, existing := range cfg.Heartbeat.Jobs {
			if existing.AgentID == agentID {
				cfg.Heartbeat.Jobs[i] = job
				return nil
			}
		}
		return &adminNotFoundError{msg: fmt.Sprintf("heartbeat job for agent %q not found", agentID)}
	})
	if err != nil {
		h.writeHeartbeatMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *adminHandler) deleteHeartbeatJob(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(r.PathValue("agentId"))
	if err := validateAgentID(agentID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	err := h.mutateHeartbeatConfigAndApply(func(cfg *config.Config) error {
		jobs := cfg.Heartbeat.Jobs
		idx := -1
		for i, job := range jobs {
			if job.AgentID == agentID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return &adminNotFoundError{msg: fmt.Sprintf("heartbeat job for agent %q not found", agentID)}
		}
		cfg.Heartbeat.Jobs = append(jobs[:idx], jobs[idx+1:]...)
		return nil
	})
	if err != nil {
		h.writeHeartbeatMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *adminHandler) listHeartbeatHistory(w http.ResponseWriter, r *http.Request) {
	store := h.heartbeatHistoryStore
	if store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "heartbeat history store is unavailable"})
		return
	}

	limit, err := heartbeat.ParseHeartbeatHistoryLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	beforeTs, err := heartbeat.ParseHeartbeatHistoryTimestamp(r.URL.Query().Get("before_ts"), "before_ts")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	afterTs, err := heartbeat.ParseHeartbeatHistoryTimestamp(r.URL.Query().Get("after_ts"), "after_ts")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	status, err := heartbeat.ParseHeartbeatStatus(r.URL.Query().Get("status"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	records := store.List(heartbeat.HistoryFilter{
		AgentID:  strings.TrimSpace(r.URL.Query().Get("agent_id")),
		Status:   status,
		BeforeTs: beforeTs,
		AfterTs:  afterTs,
		Limit:    limit,
	})
	writeJSON(w, http.StatusOK, records)
}

func (h *adminHandler) getHeartbeatHistory(w http.ResponseWriter, r *http.Request) {
	store := h.heartbeatHistoryStore
	if store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "heartbeat history store is unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
		return
	}
	record, ok := store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "heartbeat history record not found"})
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (h *adminHandler) deleteHeartbeatHistory(w http.ResponseWriter, r *http.Request) {
	store := h.heartbeatHistoryStore
	if store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "heartbeat history store is unavailable"})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
		return
	}
	deleted, err := store.Delete(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !deleted {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "heartbeat history record not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *adminHandler) clearHeartbeatHistory(w http.ResponseWriter, r *http.Request) {
	store := h.heartbeatHistoryStore
	if store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "heartbeat history store is unavailable"})
		return
	}
	if err := store.Clear(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *adminHandler) mutateHeartbeatConfigAndApply(mutator func(*config.Config) error) error {
	h.mu.Lock()
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	if err := mutator(cfg); err != nil {
		h.mu.Unlock()
		return err
	}
	if err := cfg.Validate(); err != nil {
		h.mu.Unlock()
		return &adminBadRequestError{msg: err.Error()}
	}
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		h.mu.Unlock()
		return err
	}
	h.mu.Unlock()

	if err := h.reloadAgentLoopFromConfig(); err != nil {
		return &heartbeatRuntimeSyncError{err: fmt.Errorf("heartbeat config saved but runtime sync failed: %w", err)}
	}
	return nil
}

func (h *adminHandler) writeHeartbeatMutationError(w http.ResponseWriter, err error) {
	var badReqErr *adminBadRequestError
	if errors.As(err, &badReqErr) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": badReqErr.Error()})
		return
	}
	var notFoundErr *adminNotFoundError
	if errors.As(err, &notFoundErr) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": notFoundErr.Error()})
		return
	}
	var syncErr *heartbeatRuntimeSyncError
	if errors.As(err, &syncErr) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": syncErr.Error()})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
