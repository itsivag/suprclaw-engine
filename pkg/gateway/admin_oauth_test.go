package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsivag/suprclaw/pkg/auth"
	"github.com/itsivag/suprclaw/pkg/config"
)

func TestAdminOAuthRoutesRequireBearer(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/oauth/providers"},
		{method: http.MethodPost, path: "/api/oauth/login", body: `{"provider":"openai","method":"browser"}`},
		{method: http.MethodGet, path: "/api/oauth/flows/abc123"},
		{method: http.MethodPost, path: "/api/oauth/flows/abc123/poll"},
		{method: http.MethodPost, path: "/api/oauth/logout", body: `{"provider":"openai"}`},
	}
	for _, tc := range cases {
		var bodyReader *bytes.Reader
		if tc.body == "" {
			bodyReader = bytes.NewReader(nil)
		} else {
			bodyReader = bytes.NewReader([]byte(tc.body))
		}
		req := httptest.NewRequest(tc.method, tc.path, bodyReader)
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, rec.Code, http.StatusUnauthorized)
		}
	}

	callbackRec := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/oauth/callback?state=missing", nil)
	mux.ServeHTTP(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusBadRequest {
		t.Fatalf("callback status = %d, want %d", callbackRec.Code, http.StatusBadRequest)
	}
}

func TestAdminOAuthLoginRejectsUnsupportedMethod(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/login",
		strings.NewReader(`{"provider":"anthropic","method":"browser"}`),
	)
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestAdminOAuthBrowserFlowCreatedAndQueried(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	oauthGeneratePKCE = func() (auth.PKCECodes, error) {
		return auth.PKCECodes{CodeVerifier: "verifier-1", CodeChallenge: "challenge-1"}, nil
	}
	oauthGenerateState = func() (string, error) { return "state-1", nil }
	oauthBuildAuthorizeURL = func(cfg auth.OAuthProviderConfig, pkce auth.PKCECodes, state, redirectURI string) string {
		return "https://example.com/authorize?state=" + state
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/login",
		strings.NewReader(`{"provider":"openai","method":"browser"}`),
	)
	req.Host = "localhost:18800"
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var loginResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	flowID, _ := loginResp["flow_id"].(string)
	if flowID == "" {
		t.Fatalf("flow_id is empty: %v", loginResp)
	}
	if loginResp["auth_url"] != "https://example.com/authorize?state=state-1" {
		t.Fatalf("unexpected auth_url: %v", loginResp["auth_url"])
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/oauth/flows/"+flowID, nil)
	req2.Header.Set("Authorization", "Bearer test-secret")
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("flow status code = %d, want %d, body=%s", rec2.Code, http.StatusOK, rec2.Body.String())
	}

	var flowResp oauthFlowResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &flowResp); err != nil {
		t.Fatalf("unmarshal flow response: %v", err)
	}
	if flowResp.Status != oauthFlowPending {
		t.Fatalf("flow status = %q, want %q", flowResp.Status, oauthFlowPending)
	}
	if flowResp.Method != oauthMethodBrowser {
		t.Fatalf("flow method = %q, want %q", flowResp.Method, oauthMethodBrowser)
	}
}

func TestAdminOAuthLoginGoogleAliasCreatesGoogleAntigravityBrowserFlow(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	oauthGeneratePKCE = func() (auth.PKCECodes, error) {
		return auth.PKCECodes{CodeVerifier: "verifier-google", CodeChallenge: "challenge-google"}, nil
	}
	oauthGenerateState = func() (string, error) { return "state-google", nil }
	oauthBuildAuthorizeURL = func(cfg auth.OAuthProviderConfig, pkce auth.PKCECodes, state, redirectURI string) string {
		return "https://accounts.google.com/o/oauth2/v2/auth?state=" + state
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/login",
		strings.NewReader(`{"provider":"google","method":"browser"}`),
	)
	req.Host = "tenant.suprclaw.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var loginResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	if loginResp["provider"] != oauthProviderGoogleAntigravity {
		t.Fatalf("provider = %v, want %q", loginResp["provider"], oauthProviderGoogleAntigravity)
	}
	if loginResp["method"] != oauthMethodBrowser {
		t.Fatalf("method = %v, want %q", loginResp["method"], oauthMethodBrowser)
	}
}

func TestAdminOAuthLoginDefaultOpenAIClientUsesLocalhostRedirect(t *testing.T) {
	t.Setenv("OPENAI_OAUTH_CLIENT_ID", "")
	t.Setenv("OPENAI_OAUTH_ORIGINATOR", "")

	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	oauthGeneratePKCE = func() (auth.PKCECodes, error) {
		return auth.PKCECodes{CodeVerifier: "verifier-1", CodeChallenge: "challenge-1"}, nil
	}
	oauthGenerateState = func() (string, error) { return "state-1", nil }
	var capturedRedirectURI string
	oauthBuildAuthorizeURL = func(cfg auth.OAuthProviderConfig, pkce auth.PKCECodes, state, redirectURI string) string {
		capturedRedirectURI = redirectURI
		return "https://example.com/authorize?state=" + state
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/login",
		strings.NewReader(`{"provider":"openai","method":"browser"}`),
	)
	req.Host = "tenant.suprclaw.com"
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if capturedRedirectURI != "http://localhost:1455/auth/callback" {
		t.Fatalf("redirect_uri = %q, want %q", capturedRedirectURI, "http://localhost:1455/auth/callback")
	}
}

func TestAdminOAuthLoginCustomOpenAIClientUsesTenantCallbackRedirect(t *testing.T) {
	t.Setenv("OPENAI_OAUTH_CLIENT_ID", "custom-client")
	t.Setenv("OPENAI_OAUTH_ORIGINATOR", "")

	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	oauthGeneratePKCE = func() (auth.PKCECodes, error) {
		return auth.PKCECodes{CodeVerifier: "verifier-1", CodeChallenge: "challenge-1"}, nil
	}
	oauthGenerateState = func() (string, error) { return "state-1", nil }
	var capturedRedirectURI string
	oauthBuildAuthorizeURL = func(cfg auth.OAuthProviderConfig, pkce auth.PKCECodes, state, redirectURI string) string {
		capturedRedirectURI = redirectURI
		return "https://example.com/authorize?state=" + state
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/login",
		strings.NewReader(`{"provider":"openai","method":"browser"}`),
	)
	req.Host = "tenant.suprclaw.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if capturedRedirectURI != "https://tenant.suprclaw.com/oauth/callback" {
		t.Fatalf("redirect_uri = %q, want %q", capturedRedirectURI, "https://tenant.suprclaw.com/oauth/callback")
	}
}

func TestAdminOAuthFlowExpiresWhenQueried(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:        "expired-flow",
		Provider:  oauthProviderOpenAI,
		Method:    oauthMethodBrowser,
		Status:    oauthFlowPending,
		CreatedAt: now.Add(-20 * time.Minute),
		UpdatedAt: now.Add(-20 * time.Minute),
		ExpiresAt: now.Add(-1 * time.Minute),
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/oauth/flows/expired-flow", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var flowResp oauthFlowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &flowResp); err != nil {
		t.Fatalf("unmarshal flow response: %v", err)
	}
	if flowResp.Status != oauthFlowExpired {
		t.Fatalf("flow status = %q, want %q", flowResp.Status, oauthFlowExpired)
	}
}

func TestAdminOAuthBrowserPollWithCodeCompletesFlow(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }
	oauthExchangeCodeForTokens = func(
		cfg auth.OAuthProviderConfig,
		code, codeVerifier, redirectURI string,
	) (*auth.AuthCredential, error) {
		if code != "auth-code-1" {
			t.Fatalf("code = %q, want %q", code, "auth-code-1")
		}
		if codeVerifier != "verifier-1" {
			t.Fatalf("code_verifier = %q, want %q", codeVerifier, "verifier-1")
		}
		if redirectURI != "http://localhost:1455/auth/callback" {
			t.Fatalf("redirect_uri = %q, want %q", redirectURI, "http://localhost:1455/auth/callback")
		}
		return &auth.AuthCredential{
			AccessToken: "access-token-1",
			Provider:    oauthProviderOpenAI,
		}, nil
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:           "flow-browser-code",
		Provider:     oauthProviderOpenAI,
		Method:       oauthMethodBrowser,
		Status:       oauthFlowPending,
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(oauthBrowserFlowTTL),
		CodeVerifier: "verifier-1",
		OAuthState:   "state-1",
		RedirectURI:  "http://localhost:1455/auth/callback",
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/flows/flow-browser-code/poll", strings.NewReader(`{"code":"auth-code-1"}`))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var flowResp oauthFlowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &flowResp); err != nil {
		t.Fatalf("unmarshal flow response: %v", err)
	}
	if flowResp.Status != oauthFlowSuccess {
		t.Fatalf("flow status = %q, want %q", flowResp.Status, oauthFlowSuccess)
	}
}

func TestAdminOAuthBrowserPollWithRedirectURLCompletesFlow(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }
	oauthExchangeCodeForTokens = func(
		cfg auth.OAuthProviderConfig,
		code, codeVerifier, redirectURI string,
	) (*auth.AuthCredential, error) {
		if code != "auth-code-2" {
			t.Fatalf("code = %q, want %q", code, "auth-code-2")
		}
		return &auth.AuthCredential{
			AccessToken: "access-token-2",
			Provider:    oauthProviderOpenAI,
		}, nil
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:           "flow-browser-redirect",
		Provider:     oauthProviderOpenAI,
		Method:       oauthMethodBrowser,
		Status:       oauthFlowPending,
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(oauthBrowserFlowTTL),
		CodeVerifier: "verifier-2",
		OAuthState:   "state-2",
		RedirectURI:  "http://localhost:1455/auth/callback",
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/flows/flow-browser-redirect/poll",
		strings.NewReader(`{"redirect_url":"http://localhost:1455/auth/callback?code=auth-code-2&state=state-2"}`),
	)
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var flowResp oauthFlowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &flowResp); err != nil {
		t.Fatalf("unmarshal flow response: %v", err)
	}
	if flowResp.Status != oauthFlowSuccess {
		t.Fatalf("flow status = %q, want %q", flowResp.Status, oauthFlowSuccess)
	}
}

func TestAdminOAuthBrowserPollRejectsMissingPayload(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:        "flow-browser-missing-payload",
		Provider:  oauthProviderOpenAI,
		Method:    oauthMethodBrowser,
		Status:    oauthFlowPending,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(oauthBrowserFlowTTL),
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/flows/flow-browser-missing-payload/poll", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "exactly one of code or redirect_url") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestAdminOAuthBrowserPollRejectsUnsupportedPayloadShape(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:        "flow-browser-invalid-shape",
		Provider:  oauthProviderOpenAI,
		Method:    oauthMethodBrowser,
		Status:    oauthFlowPending,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(oauthBrowserFlowTTL),
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/flows/flow-browser-invalid-shape/poll",
		strings.NewReader(`{"code":"c1","redirect_url":"http://localhost:1455/auth/callback?code=c2"}`),
	)
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unsupported payload shape") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestAdminOAuthBrowserPollRejectsMalformedRedirectURL(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:         "flow-browser-malformed-redirect",
		Provider:   oauthProviderOpenAI,
		Method:     oauthMethodBrowser,
		Status:     oauthFlowPending,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(oauthBrowserFlowTTL),
		OAuthState: "state-1",
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/flows/flow-browser-malformed-redirect/poll",
		strings.NewReader(`{"redirect_url":"not-a-url"}`),
	)
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var flowResp oauthFlowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &flowResp); err != nil {
		t.Fatalf("unmarshal flow response: %v", err)
	}
	if flowResp.Status != oauthFlowError {
		t.Fatalf("flow status = %q, want %q", flowResp.Status, oauthFlowError)
	}
	if !strings.Contains(flowResp.Error, "malformed redirect_url") {
		t.Fatalf("unexpected flow error: %q", flowResp.Error)
	}
}

func TestAdminOAuthBrowserPollRejectsMissingCodeInRedirectURL(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:         "flow-browser-missing-code",
		Provider:   oauthProviderOpenAI,
		Method:     oauthMethodBrowser,
		Status:     oauthFlowPending,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(oauthBrowserFlowTTL),
		OAuthState: "state-1",
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/flows/flow-browser-missing-code/poll",
		strings.NewReader(`{"redirect_url":"http://localhost:1455/auth/callback?state=state-1"}`),
	)
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var flowResp oauthFlowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &flowResp); err != nil {
		t.Fatalf("unmarshal flow response: %v", err)
	}
	if flowResp.Status != oauthFlowError {
		t.Fatalf("flow status = %q, want %q", flowResp.Status, oauthFlowError)
	}
	if !strings.Contains(flowResp.Error, "missing authorization code") {
		t.Fatalf("unexpected flow error: %q", flowResp.Error)
	}
}

func TestAdminOAuthBrowserPollRejectsStateMismatch(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:         "flow-browser-state-mismatch",
		Provider:   oauthProviderOpenAI,
		Method:     oauthMethodBrowser,
		Status:     oauthFlowPending,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(oauthBrowserFlowTTL),
		OAuthState: "state-expected",
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/flows/flow-browser-state-mismatch/poll",
		strings.NewReader(`{"redirect_url":"http://localhost:1455/auth/callback?code=auth-code-1&state=state-actual"}`),
	)
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	var flowResp oauthFlowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &flowResp); err != nil {
		t.Fatalf("unmarshal flow response: %v", err)
	}
	if flowResp.Status != oauthFlowError {
		t.Fatalf("flow status = %q, want %q", flowResp.Status, oauthFlowError)
	}
	if flowResp.Error != "state mismatch" {
		t.Fatalf("flow error = %q, want %q", flowResp.Error, "state mismatch")
	}
}

func TestAdminOAuthDeviceCodePollUnchanged(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }
	oauthPollDeviceCodeOnce = func(
		cfg auth.OAuthProviderConfig,
		deviceAuthID, userCode string,
	) (*auth.AuthCredential, error) {
		if deviceAuthID != "device-auth-1" {
			t.Fatalf("device_auth_id = %q, want %q", deviceAuthID, "device-auth-1")
		}
		if userCode != "user-code-1" {
			t.Fatalf("user_code = %q, want %q", userCode, "user-code-1")
		}
		return &auth.AuthCredential{
			AccessToken: "access-token-device",
			Provider:    oauthProviderOpenAI,
		}, nil
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:           "flow-device-1",
		Provider:     oauthProviderOpenAI,
		Method:       oauthMethodDeviceCode,
		Status:       oauthFlowPending,
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(oauthDeviceCodeFlowTTL),
		DeviceAuthID: "device-auth-1",
		UserCode:     "user-code-1",
		VerifyURL:    "https://auth.openai.com/codex/device",
		Interval:     5,
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/flows/flow-device-1/poll", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer test-secret")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var flowResp oauthFlowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &flowResp); err != nil {
		t.Fatalf("unmarshal flow response: %v", err)
	}
	if flowResp.Status != oauthFlowSuccess {
		t.Fatalf("flow status = %q, want %q", flowResp.Status, oauthFlowSuccess)
	}
	if flowResp.Method != oauthMethodDeviceCode {
		t.Fatalf("flow method = %q, want %q", flowResp.Method, oauthMethodDeviceCode)
	}
}

func TestAdminOAuthCallbackUnknownState(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?state=unknown&code=abc", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "OAuth flow not found") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestAdminOAuthCallbackSuccessPersistsCredentialAndConfig(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }
	oauthExchangeCodeForTokens = func(
		cfg auth.OAuthProviderConfig,
		code, codeVerifier, redirectURI string,
	) (*auth.AuthCredential, error) {
		return &auth.AuthCredential{
			AccessToken:  "access-token-1",
			RefreshToken: "refresh-token-1",
			Provider:     oauthProviderOpenAI,
		}, nil
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:           "flow-1",
		Provider:     oauthProviderOpenAI,
		Method:       oauthMethodBrowser,
		Status:       oauthFlowPending,
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(oauthBrowserFlowTTL),
		CodeVerifier: "verifier-1",
		OAuthState:   "state-1",
		RedirectURI:  "https://api.suprclaw.com/oauth/callback",
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?state=state-1&code=auth-code-1", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	flow, ok := h.getOAuthFlow("flow-1")
	if !ok {
		t.Fatalf("expected oauth flow to exist")
	}
	if flow.Status != oauthFlowSuccess {
		t.Fatalf("flow status = %q, want %q", flow.Status, oauthFlowSuccess)
	}

	cred, err := auth.GetCredential(oauthProviderOpenAI)
	if err != nil {
		t.Fatalf("GetCredential error: %v", err)
	}
	if cred == nil {
		t.Fatalf("expected stored credential")
	}
	if cred.AccessToken != "access-token-1" {
		t.Fatalf("access_token = %q, want %q", cred.AccessToken, "access-token-1")
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	hasOAuthModel := false
	for _, modelCfg := range updated.ModelList {
		if strings.HasPrefix(modelCfg.Model, "openai/") && modelCfg.AuthMethod == "oauth" {
			hasOAuthModel = true
		}
	}
	if !hasOAuthModel {
		t.Fatalf("expected at least one openai model to have auth_method oauth")
	}
}

func TestAdminOAuthCallbackSuccessSwitchesManagedLeadModelToOpenAI(t *testing.T) {
	configPath, cleanup := setupAdminOAuthManagedRuntimeTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	oauthNow = func() time.Time { return now }
	oauthExchangeCodeForTokens = func(
		cfg auth.OAuthProviderConfig,
		code, codeVerifier, redirectURI string,
	) (*auth.AuthCredential, error) {
		return &auth.AuthCredential{
			AccessToken:  "access-token-2",
			RefreshToken: "refresh-token-2",
			Provider:     oauthProviderOpenAI,
		}, nil
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	h.storeOAuthFlow(&oauthFlow{
		ID:           "flow-managed-1",
		Provider:     oauthProviderOpenAI,
		Method:       oauthMethodBrowser,
		Status:       oauthFlowPending,
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(oauthBrowserFlowTTL),
		CodeVerifier: "verifier-2",
		OAuthState:   "state-managed-1",
		RedirectURI:  "https://api.suprclaw.com/oauth/callback",
	})

	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?state=state-managed-1&code=auth-code-2", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if got := updated.Agents.Defaults.GetModelName(); got != "gpt-5.4" {
		t.Fatalf("agents.defaults.model_name = %q, want %q", got, "gpt-5.4")
	}
	if len(updated.Agents.List) == 0 || updated.Agents.List[0].Model == nil {
		t.Fatalf("expected main agent model to be set")
	}
	if got := updated.Agents.List[0].Model.Primary; got != "gpt-5.4" {
		t.Fatalf("main agent model primary = %q, want %q", got, "gpt-5.4")
	}

	hasOAuthModel := false
	for _, modelCfg := range updated.ModelList {
		if modelCfg.Model == "openai/gpt-5.4" && modelCfg.AuthMethod == "oauth" {
			hasOAuthModel = true
		}
	}
	if !hasOAuthModel {
		t.Fatalf("expected openai/gpt-5.4 oauth model in config")
	}
}

func TestAdminOAuthProvidersSyncStoredCredentialConfigForManagedModel(t *testing.T) {
	configPath, cleanup := setupAdminOAuthManagedRuntimeTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	if err := auth.SetCredential(oauthProviderOpenAI, &auth.AuthCredential{
		AccessToken: "access-token-sync-1",
		Provider:    oauthProviderOpenAI,
		AuthMethod:  "oauth",
	}); err != nil {
		t.Fatalf("SetCredential error: %v", err)
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/oauth/providers", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if got := updated.Agents.Defaults.GetModelName(); got != "gpt-5.4" {
		t.Fatalf("agents.defaults.model_name = %q, want %q", got, "gpt-5.4")
	}
	if len(updated.Agents.List) == 0 || updated.Agents.List[0].Model == nil {
		t.Fatalf("expected main agent model to be set")
	}
	if got := updated.Agents.List[0].Model.Primary; got != "gpt-5.4" {
		t.Fatalf("main agent model primary = %q, want %q", got, "gpt-5.4")
	}

	hasOAuthModel := false
	for _, modelCfg := range updated.ModelList {
		if modelCfg.Model == "openai/gpt-5.4" && modelCfg.AuthMethod == "oauth" {
			hasOAuthModel = true
		}
	}
	if !hasOAuthModel {
		t.Fatalf("expected openai/gpt-5.4 oauth model in config")
	}
}

func TestAdminOAuthLogoutClearsCredentialAndConfig(t *testing.T) {
	configPath, cleanup := setupAdminOAuthTestEnv(t)
	defer cleanup()
	resetAdminOAuthHooks(t)

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	cfg.Providers.OpenAI.AuthMethod = "oauth"
	cfg.ModelList = append(cfg.ModelList, config.ModelConfig{
		ModelName:  "gpt-5.4",
		Model:      "openai/gpt-5.4",
		AuthMethod: "oauth",
	})
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}
	if err = auth.SetCredential(oauthProviderOpenAI, &auth.AuthCredential{
		AccessToken: "token-before-logout",
		Provider:    oauthProviderOpenAI,
		AuthMethod:  "oauth",
	}); err != nil {
		t.Fatalf("SetCredential error: %v", err)
	}

	h := newAdminHandler(configPath, nil, "test-secret", nil, nil)
	mux := http.NewServeMux()
	h.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/logout", bytes.NewBufferString(`{"provider":"openai"}`))
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cred, err := auth.GetCredential(oauthProviderOpenAI)
	if err != nil {
		t.Fatalf("GetCredential error: %v", err)
	}
	if cred != nil {
		t.Fatalf("expected credential deleted, got %#v", cred)
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if updated.Providers.OpenAI.AuthMethod != "" {
		t.Fatalf("providers.openai.auth_method = %q, want empty", updated.Providers.OpenAI.AuthMethod)
	}
	for _, modelCfg := range updated.ModelList {
		if strings.HasPrefix(modelCfg.Model, "openai/") && modelCfg.AuthMethod != "" {
			t.Fatalf("openai model auth_method = %q, want empty", modelCfg.AuthMethod)
		}
	}
}

func setupAdminOAuthTestEnv(t *testing.T) (string, func()) {
	t.Helper()

	tmp := t.TempDir()
	oldHome := os.Getenv("HOME")
	oldSuprHome := os.Getenv("SUPRCLAW_HOME")

	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if err := os.Setenv("SUPRCLAW_HOME", filepath.Join(tmp, ".suprclaw")); err != nil {
		t.Fatalf("set SUPRCLAW_HOME: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.ModelList = []config.ModelConfig{{
		ModelName: "custom-default",
		Model:     "openai/gpt-4o",
		APIKey:    "sk-default",
	}}
	cfg.Agents.Defaults.ModelName = "custom-default"

	configPath := filepath.Join(tmp, "config.json")
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}

	cleanup := func() {
		_ = os.Setenv("HOME", oldHome)
		if oldSuprHome == "" {
			_ = os.Unsetenv("SUPRCLAW_HOME")
		} else {
			_ = os.Setenv("SUPRCLAW_HOME", oldSuprHome)
		}
	}
	return configPath, cleanup
}

func setupAdminOAuthManagedRuntimeTestEnv(t *testing.T) (string, func()) {
	t.Helper()

	tmp := t.TempDir()
	oldHome := os.Getenv("HOME")
	oldSuprHome := os.Getenv("SUPRCLAW_HOME")

	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if err := os.Setenv("SUPRCLAW_HOME", filepath.Join(tmp, ".suprclaw")); err != nil {
		t.Fatalf("set SUPRCLAW_HOME: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.ModelList = []config.ModelConfig{
		{
			ModelName: "suprclaw-default",
			Model:     "litellm/suprclaw-default",
			APIBase:   "http://127.0.0.1:4000/v1",
			APIKey:    "litellm-secret",
		},
		{
			ModelName: "suprclaw-fast",
			Model:     "litellm/suprclaw-fast",
			APIBase:   "http://127.0.0.1:4000/v1",
			APIKey:    "litellm-secret",
		},
	}
	cfg.Agents.Defaults.ModelName = "suprclaw-default"
	cfg.Agents.Defaults.Model = ""
	cfg.Agents.List = []config.AgentConfig{
		{
			ID:      "main",
			Default: true,
			Model: &config.AgentModelConfig{
				Primary: "suprclaw-fast",
			},
		},
	}

	configPath := filepath.Join(tmp, "config.json")
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}

	cleanup := func() {
		_ = os.Setenv("HOME", oldHome)
		if oldSuprHome == "" {
			_ = os.Unsetenv("SUPRCLAW_HOME")
		} else {
			_ = os.Setenv("SUPRCLAW_HOME", oldSuprHome)
		}
	}
	return configPath, cleanup
}

func resetAdminOAuthHooks(t *testing.T) {
	t.Helper()

	origNow := oauthNow
	origGeneratePKCE := oauthGeneratePKCE
	origGenerateState := oauthGenerateState
	origBuildAuthorizeURL := oauthBuildAuthorizeURL
	origRequestDeviceCode := oauthRequestDeviceCode
	origPollDeviceCodeOnce := oauthPollDeviceCodeOnce
	origExchangeCodeForTokens := oauthExchangeCodeForTokens
	origGetCredential := oauthGetCredential
	origSetCredential := oauthSetCredential
	origDeleteCredential := oauthDeleteCredential
	origFetchProject := oauthFetchAntigravityProject
	origFetchGoogleEmail := oauthFetchGoogleUserEmailFunc

	t.Cleanup(func() {
		oauthNow = origNow
		oauthGeneratePKCE = origGeneratePKCE
		oauthGenerateState = origGenerateState
		oauthBuildAuthorizeURL = origBuildAuthorizeURL
		oauthRequestDeviceCode = origRequestDeviceCode
		oauthPollDeviceCodeOnce = origPollDeviceCodeOnce
		oauthExchangeCodeForTokens = origExchangeCodeForTokens
		oauthGetCredential = origGetCredential
		oauthSetCredential = origSetCredential
		oauthDeleteCredential = origDeleteCredential
		oauthFetchAntigravityProject = origFetchProject
		oauthFetchGoogleUserEmailFunc = origFetchGoogleEmail
	})
}
