package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsivag/suprclaw/pkg/agent"
	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/heartbeat"
)

func TestAdminHeartbeatRoutes_Auth(t *testing.T) {
	h := &adminHandler{secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/heartbeat", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}

	h2 := &adminHandler{}
	mux2 := http.NewServeMux()
	h2.registerRoutes(mux2)
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/heartbeat", nil)
	rec2 := httptest.NewRecorder()
	mux2.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestAdminHeartbeatConfigAndJobsCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := minimalAdminConfig(tmpDir)
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &gatewayMockProvider{response: "ok"})
	h := &adminHandler{configPath: cfgPath, secret: "test-secret", agentLoop: loop}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	putBody := `{
	  "enabled": true,
	  "minimum_gap_minutes": 5,
	  "jobs": [{"agent_id":"main","interval_minutes":5}]
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/admin/heartbeat", bytes.NewBufferString(putBody))
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /heartbeat status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/admin/heartbeat", nil)
	getReq.Header.Set("Authorization", "Bearer test-secret")
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /heartbeat status = %d, want 200, body=%s", getRec.Code, getRec.Body.String())
	}

	postDupReq := httptest.NewRequest(http.MethodPost, "/api/admin/heartbeat/jobs", bytes.NewBufferString(`{"agent_id":"main","interval_minutes":5}`))
	postDupReq.Header.Set("Authorization", "Bearer test-secret")
	postDupRec := httptest.NewRecorder()
	mux.ServeHTTP(postDupRec, postDupReq)
	if postDupRec.Code != http.StatusBadRequest {
		t.Fatalf("POST duplicate job status = %d, want 400, body=%s", postDupRec.Code, postDupRec.Body.String())
	}

	putMismatchReq := httptest.NewRequest(http.MethodPut, "/api/admin/heartbeat/jobs/main", bytes.NewBufferString(`{"agent_id":"writer","interval_minutes":5}`))
	putMismatchReq.Header.Set("Authorization", "Bearer test-secret")
	putMismatchRec := httptest.NewRecorder()
	mux.ServeHTTP(putMismatchRec, putMismatchReq)
	if putMismatchRec.Code != http.StatusBadRequest {
		t.Fatalf("PUT job mismatch status = %d, want 400, body=%s", putMismatchRec.Code, putMismatchRec.Body.String())
	}

	putJobReq := httptest.NewRequest(http.MethodPut, "/api/admin/heartbeat/jobs/main", bytes.NewBufferString(`{"agent_id":"main","interval_minutes":10}`))
	putJobReq.Header.Set("Authorization", "Bearer test-secret")
	putJobRec := httptest.NewRecorder()
	mux.ServeHTTP(putJobRec, putJobReq)
	if putJobRec.Code != http.StatusOK {
		t.Fatalf("PUT job status = %d, want 200, body=%s", putJobRec.Code, putJobRec.Body.String())
	}

	postLegacyTimezoneReq := httptest.NewRequest(http.MethodPost, "/api/admin/heartbeat/jobs", bytes.NewBufferString(`{"agent_id":"writer","interval_minutes":5,"timezone":"UTC"}`))
	postLegacyTimezoneReq.Header.Set("Authorization", "Bearer test-secret")
	postLegacyTimezoneRec := httptest.NewRecorder()
	mux.ServeHTTP(postLegacyTimezoneRec, postLegacyTimezoneReq)
	if postLegacyTimezoneRec.Code != http.StatusBadRequest {
		t.Fatalf("POST legacy timezone status = %d, want 400, body=%s", postLegacyTimezoneRec.Code, postLegacyTimezoneRec.Body.String())
	}

	delMissingReq := httptest.NewRequest(http.MethodDelete, "/api/admin/heartbeat/jobs/missing", nil)
	delMissingReq.Header.Set("Authorization", "Bearer test-secret")
	delMissingRec := httptest.NewRecorder()
	mux.ServeHTTP(delMissingRec, delMissingReq)
	if delMissingRec.Code != http.StatusNotFound {
		t.Fatalf("DELETE missing job status = %d, want 404, body=%s", delMissingRec.Code, delMissingRec.Body.String())
	}
}

func TestAdminHeartbeatMutation_RuntimeSyncFailureReturns503ButPersists(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := minimalAdminConfig(tmpDir)
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := &adminHandler{configPath: cfgPath, secret: "test-secret"}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	putBody := `{
	  "enabled": true,
	  "minimum_gap_minutes": 5,
	  "jobs": [{"agent_id":"main","interval_minutes":5}]
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/admin/heartbeat", bytes.NewBufferString(putBody))
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "saved but runtime sync failed") {
		t.Fatalf("expected runtime sync failure message, got %s", rec.Body.String())
	}

	updated, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !updated.Heartbeat.Enabled || len(updated.Heartbeat.Jobs) != 1 {
		t.Fatalf("heartbeat config was not persisted: %+v", updated.Heartbeat)
	}
}

func TestAdminHeartbeatHistoryRoutes(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := minimalAdminConfig(tmpDir)
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	store := heartbeat.NewHistoryStore(tmpDir)
	if err := store.Start(); err != nil {
		t.Fatalf("history store Start() error = %v", err)
	}
	t.Cleanup(store.Stop)

	heartbeat.EmitHeartbeatEvent(heartbeat.HeartbeatEvent{
		Ts:      1000,
		Status:  heartbeat.StatusSent,
		AgentID: "main",
	})
	heartbeat.EmitHeartbeatEvent(heartbeat.HeartbeatEvent{
		Ts:      2000,
		Status:  heartbeat.StatusSkipped,
		AgentID: "writer",
	})
	heartbeat.EmitHeartbeatEvent(heartbeat.HeartbeatEvent{
		Ts:      3000,
		Status:  heartbeat.StatusSent,
		AgentID: "main",
	})

	h := &adminHandler{
		configPath:            cfgPath,
		secret:                "test-secret",
		heartbeatHistoryStore: store,
	}
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	listReq := httptest.NewRequest(http.MethodGet, "/api/admin/heartbeat/history?agent_id=main&status=sent&limit=1", nil)
	listReq.Header.Set("Authorization", "Bearer test-secret")
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET history status = %d, want 200, body=%s", listRec.Code, listRec.Body.String())
	}

	var records []heartbeat.HistoryRecord
	if err := json.Unmarshal(listRec.Body.Bytes(), &records); err != nil {
		t.Fatalf("decode list response error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].AgentID != "main" || records[0].Status != heartbeat.StatusSent {
		t.Fatalf("unexpected record filter result: %+v", records[0])
	}
	recordID := records[0].ID

	getReq := httptest.NewRequest(http.MethodGet, "/api/admin/heartbeat/history/"+recordID, nil)
	getReq.Header.Set("Authorization", "Bearer test-secret")
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET history/{id} status = %d, want 200, body=%s", getRec.Code, getRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/heartbeat/history/"+recordID, nil)
	deleteReq.Header.Set("Authorization", "Bearer test-secret")
	deleteRec := httptest.NewRecorder()
	mux.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("DELETE history/{id} status = %d, want 200, body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/api/admin/heartbeat/history/"+recordID, nil)
	missingReq.Header.Set("Authorization", "Bearer test-secret")
	missingRec := httptest.NewRecorder()
	mux.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("GET deleted history/{id} status = %d, want 404, body=%s", missingRec.Code, missingRec.Body.String())
	}

	clearReq := httptest.NewRequest(http.MethodDelete, "/api/admin/heartbeat/history", nil)
	clearReq.Header.Set("Authorization", "Bearer test-secret")
	clearRec := httptest.NewRecorder()
	mux.ServeHTTP(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("DELETE history status = %d, want 200, body=%s", clearRec.Code, clearRec.Body.String())
	}

	invalidReq := httptest.NewRequest(http.MethodGet, "/api/admin/heartbeat/history?limit=0", nil)
	invalidReq.Header.Set("Authorization", "Bearer test-secret")
	invalidRec := httptest.NewRecorder()
	mux.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("GET history invalid limit status = %d, want 400, body=%s", invalidRec.Code, invalidRec.Body.String())
	}
}

func minimalAdminConfig(workspace string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "main", Default: true}, {ID: "writer"}},
		},
		ModelList: []config.ModelConfig{
			{
				ModelName: "test-model",
				Model:     "openai/gpt-5.4",
				APIKey:    "x",
			},
		},
		Timezone: "UTC",
	}
}
