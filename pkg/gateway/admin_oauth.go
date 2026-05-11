package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/itsivag/suprclaw/pkg/auth"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/providers"
)

const (
	oauthProviderOpenAI            = "openai"
	oauthProviderAnthropic         = "anthropic"
	oauthProviderGoogleAntigravity = "google-antigravity"
	oauthProviderNVIDIA            = "nvidia"
	oauthProviderOpenRouter        = "openrouter"

	oauthMethodBrowser    = "browser"
	oauthMethodDeviceCode = "device_code"
	oauthMethodToken      = "token"

	oauthFlowPending = "pending"
	oauthFlowSuccess = "success"
	oauthFlowError   = "error"
	oauthFlowExpired = "expired"
)

const (
	oauthBrowserFlowTTL    = 10 * time.Minute
	oauthDeviceCodeFlowTTL = 15 * time.Minute
	oauthTerminalFlowGC    = 30 * time.Minute
)

var oauthProviderOrder = []string{
	oauthProviderOpenAI,
	oauthProviderAnthropic,
	oauthProviderGoogleAntigravity,
	oauthProviderNVIDIA,
	oauthProviderOpenRouter,
}

var oauthProviderMethods = map[string][]string{
	oauthProviderOpenAI:            {oauthMethodBrowser, oauthMethodDeviceCode, oauthMethodToken},
	oauthProviderAnthropic:         {oauthMethodToken},
	oauthProviderGoogleAntigravity: {oauthMethodBrowser},
	oauthProviderNVIDIA:            {oauthMethodToken},
	oauthProviderOpenRouter:        {oauthMethodToken},
}

var oauthProviderLabels = map[string]string{
	oauthProviderOpenAI:            "OpenAI",
	oauthProviderAnthropic:         "Anthropic",
	oauthProviderGoogleAntigravity: "Google Antigravity",
	oauthProviderNVIDIA:            "NVIDIA",
	oauthProviderOpenRouter:        "OpenRouter",
}

var (
	oauthNow                      = time.Now
	oauthGeneratePKCE             = auth.GeneratePKCE
	oauthGenerateState            = auth.GenerateState
	oauthBuildAuthorizeURL        = auth.BuildAuthorizeURL
	oauthRequestDeviceCode        = auth.RequestDeviceCode
	oauthPollDeviceCodeOnce       = auth.PollDeviceCodeOnce
	oauthExchangeCodeForTokens    = auth.ExchangeCodeForTokens
	oauthGetCredential            = auth.GetCredential
	oauthSetCredential            = auth.SetCredential
	oauthDeleteCredential         = auth.DeleteCredential
	oauthFetchAntigravityProject  = providers.FetchAntigravityProjectID
	oauthFetchGoogleUserEmailFunc = fetchGoogleUserEmail
)

type oauthFlow struct {
	ID            string
	Provider      string
	Method        string
	SelectedModel string
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ExpiresAt     time.Time
	Error         string
	CodeVerifier  string
	OAuthState    string
	RedirectURI   string
	DeviceAuthID  string
	UserCode      string
	VerifyURL     string
	Interval      int
}

type oauthProviderStatus struct {
	Provider        string               `json:"provider"`
	DisplayName     string               `json:"display_name"`
	Methods         []string             `json:"methods"`
	AvailableModels []oauthProviderModel `json:"available_models"`
	LoggedIn        bool                 `json:"logged_in"`
	Status          string               `json:"status"`
	AuthMethod      string               `json:"auth_method,omitempty"`
	ExpiresAt       string               `json:"expires_at,omitempty"`
	AccountID       string               `json:"account_id,omitempty"`
	Email           string               `json:"email,omitempty"`
	ProjectID       string               `json:"project_id,omitempty"`
}

type oauthProviderModel struct {
	ModelName string `json:"model_name"`
	Model     string `json:"model"`
}

type oauthFlowResponse struct {
	FlowID    string `json:"flow_id"`
	Provider  string `json:"provider"`
	Method    string `json:"method"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Error     string `json:"error,omitempty"`
	UserCode  string `json:"user_code,omitempty"`
	VerifyURL string `json:"verify_url,omitempty"`
	Interval  int    `json:"interval,omitempty"`
}

type oauthBrowserPollRequest struct {
	Code        string
	RedirectURL string
}

type oauthRedirectCompletion struct {
	Code          string
	State         string
	ProviderError string
}

type oauthLoginRequest struct {
	Provider      string
	Method        string
	Token         string
	Model         string
	ModelProvided bool
}

type oauthModelUpdateRequest struct {
	Provider string
	Model    string
}

type oauthModelValidationError struct {
	message string
}

func (e *oauthModelValidationError) Error() string {
	return e.message
}

func (h *adminHandler) handleListOAuthProviders(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	cfg, err := config.LoadConfig(h.configPath)
	h.mu.Unlock()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	providersResp := make([]oauthProviderStatus, 0, len(oauthProviderOrder))

	for _, provider := range oauthProviderOrder {
		cred, err := oauthGetCredential(provider)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to load credentials: %v", err), http.StatusInternalServerError)
			return
		}

		item := oauthProviderStatus{
			Provider:        provider,
			DisplayName:     oauthProviderLabels[provider],
			Methods:         oauthProviderMethods[provider],
			AvailableModels: availableOAuthProviderModels(cfg, provider),
			Status:          "not_logged_in",
		}
		if cred != nil {
			item.LoggedIn = true
			item.AuthMethod = cred.AuthMethod
			item.AccountID = cred.AccountID
			item.Email = cred.Email
			item.ProjectID = cred.ProjectID
			if !cred.ExpiresAt.IsZero() {
				item.ExpiresAt = cred.ExpiresAt.Format(time.RFC3339)
			}
			switch {
			case cred.IsExpired():
				item.Status = "expired"
			case cred.NeedsRefresh():
				item.Status = "needs_refresh"
			default:
				item.Status = "connected"
			}
		}

		providersResp = append(providersResp, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"providers": providersResp,
	})
}

func availableOAuthProviderModels(cfg *config.Config, provider string) []oauthProviderModel {
	if cfg == nil {
		return []oauthProviderModel{}
	}
	result := make([]oauthProviderModel, 0)
	seenModelNames := make(map[string]struct{})
	for _, modelCfg := range cfg.ModelList {
		if !modelBelongsToProvider(provider, modelCfg.Model) {
			continue
		}
		modelName := strings.TrimSpace(modelCfg.ModelName)
		modelID := strings.TrimSpace(modelCfg.Model)
		if modelName == "" || modelID == "" {
			continue
		}
		if _, exists := seenModelNames[modelName]; exists {
			continue
		}
		seenModelNames[modelName] = struct{}{}
		result = append(result, oauthProviderModel{
			ModelName: modelName,
			Model:     modelID,
		})
	}
	return result
}

func (h *adminHandler) syncStoredOAuthCredentialsToConfig() error {
	for _, provider := range oauthProviderOrder {
		cred, err := oauthGetCredential(provider)
		if err != nil {
			return fmt.Errorf("load credential for %q: %w", provider, err)
		}
		if cred == nil {
			continue
		}

		authMethod := strings.ToLower(strings.TrimSpace(cred.AuthMethod))
		if authMethod == "" {
			return fmt.Errorf("stored credential for provider %q is missing auth_method", provider)
		}
		if !isStoredCredentialAuthMethodSupported(provider, authMethod) {
			return fmt.Errorf("stored credential for provider %q has unsupported auth_method %q", provider, authMethod)
		}

		if err := h.syncProviderAuthMethod(provider, authMethod, ""); err != nil {
			return fmt.Errorf("sync provider %q auth_method %q: %w", provider, authMethod, err)
		}
	}
	return nil
}

func isStoredCredentialAuthMethodSupported(provider, authMethod string) bool {
	switch authMethod {
	case "oauth":
		return provider == oauthProviderOpenAI ||
			provider == oauthProviderAnthropic ||
			provider == oauthProviderGoogleAntigravity
	default:
		return isOAuthMethodSupported(provider, authMethod)
	}
}

func (h *adminHandler) handleOAuthLogin(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	req, err := parseOAuthLoginRequest(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	provider, err := normalizeOAuthProvider(req.Provider)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	method := strings.ToLower(strings.TrimSpace(req.Method))
	if !isOAuthMethodSupported(provider, method) {
		http.Error(
			w,
			fmt.Sprintf("unsupported login method %q for provider %q", method, provider),
			http.StatusBadRequest,
		)
		return
	}

	if req.ModelProvided {
		if err := h.validateOAuthLoginSelectedModel(provider, req.Model); err != nil {
			var validationErr *oauthModelValidationError
			if errors.As(err, &validationErr) {
				http.Error(w, validationErr.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, fmt.Sprintf("failed to validate model: %v", err), http.StatusInternalServerError)
			return
		}
	}

	switch method {
	case oauthMethodToken:
		token := strings.TrimSpace(req.Token)
		if token == "" {
			http.Error(w, "token is required", http.StatusBadRequest)
			return
		}

		cred := &auth.AuthCredential{
			AccessToken: token,
			Provider:    provider,
			AuthMethod:  oauthMethodToken,
		}
		if err := h.persistCredentialAndConfig(provider, oauthMethodToken, cred, req.Model); err != nil {
			http.Error(w, fmt.Sprintf("token login failed: %v", err), http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"provider": provider,
			"method":   method,
		})
		return

	case oauthMethodDeviceCode:
		cfg := auth.OpenAIOAuthConfig()
		info, err := oauthRequestDeviceCode(cfg)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to request device code: %v", err), http.StatusInternalServerError)
			return
		}

		now := oauthNow()
		flow := &oauthFlow{
			ID:            newOAuthFlowID(),
			Provider:      provider,
			Method:        method,
			SelectedModel: req.Model,
			Status:        oauthFlowPending,
			CreatedAt:     now,
			UpdatedAt:     now,
			ExpiresAt:     now.Add(oauthDeviceCodeFlowTTL),
			DeviceAuthID:  info.DeviceAuthID,
			UserCode:      info.UserCode,
			VerifyURL:     info.VerifyURL,
			Interval:      info.Interval,
		}
		h.storeOAuthFlow(flow)

		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"provider":   provider,
			"method":     method,
			"flow_id":    flow.ID,
			"user_code":  flow.UserCode,
			"verify_url": flow.VerifyURL,
			"interval":   flow.Interval,
			"expires_at": flow.ExpiresAt.Format(time.RFC3339),
		})
		return

	case oauthMethodBrowser:
		cfg, err := oauthConfigForProvider(provider)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		pkce, err := oauthGeneratePKCE()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to generate PKCE: %v", err), http.StatusInternalServerError)
			return
		}
		state, err := oauthGenerateState()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to generate state: %v", err), http.StatusInternalServerError)
			return
		}

		redirectURI := resolveOAuthBrowserRedirectURI(provider, cfg, r)
		authURL := oauthBuildAuthorizeURL(cfg, pkce, state, redirectURI)

		now := oauthNow()
		flow := &oauthFlow{
			ID:            newOAuthFlowID(),
			Provider:      provider,
			Method:        method,
			SelectedModel: req.Model,
			Status:        oauthFlowPending,
			CreatedAt:     now,
			UpdatedAt:     now,
			ExpiresAt:     now.Add(oauthBrowserFlowTTL),
			CodeVerifier:  pkce.CodeVerifier,
			OAuthState:    state,
			RedirectURI:   redirectURI,
		}
		h.storeOAuthFlow(flow)

		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"provider":   provider,
			"method":     method,
			"flow_id":    flow.ID,
			"auth_url":   authURL,
			"expires_at": flow.ExpiresAt.Format(time.RFC3339),
		})
		return
	default:
		http.Error(w, "unsupported login method", http.StatusBadRequest)
	}
}

func (h *adminHandler) handleOAuthUpdateModel(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	req, err := parseOAuthModelUpdateRequest(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	provider, err := normalizeOAuthProvider(req.Provider)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cred, err := oauthGetCredential(provider)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to load credential: %v", err), http.StatusInternalServerError)
		return
	}
	if cred == nil {
		http.Error(w, fmt.Sprintf("provider %q is not connected", provider), http.StatusBadRequest)
		return
	}
	if cred.IsExpired() || cred.NeedsRefresh() {
		http.Error(w, fmt.Sprintf("provider %q credential is not connected", provider), http.StatusBadRequest)
		return
	}

	authMethod := strings.ToLower(strings.TrimSpace(cred.AuthMethod))
	if authMethod == "" {
		http.Error(w, fmt.Sprintf("provider %q credential is missing auth method", provider), http.StatusInternalServerError)
		return
	}
	if !isStoredCredentialAuthMethodSupported(provider, authMethod) {
		http.Error(
			w,
			fmt.Sprintf("provider %q credential has unsupported auth method %q", provider, authMethod),
			http.StatusBadRequest,
		)
		return
	}

	if err := h.validateOAuthLoginSelectedModel(provider, req.Model); err != nil {
		var validationErr *oauthModelValidationError
		if errors.As(err, &validationErr) {
			http.Error(w, validationErr.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, fmt.Sprintf("failed to validate model: %v", err), http.StatusInternalServerError)
		return
	}

	if err := h.syncProviderAuthMethod(provider, authMethod, req.Model); err != nil {
		var validationErr *oauthModelValidationError
		if errors.As(err, &validationErr) {
			http.Error(w, validationErr.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, fmt.Sprintf("failed to update oauth model: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"provider": provider,
		"model":    req.Model,
	})
}

func (h *adminHandler) handleGetOAuthFlow(w http.ResponseWriter, r *http.Request) {
	flowID := strings.TrimSpace(r.PathValue("id"))
	if flowID == "" {
		http.Error(w, "missing flow id", http.StatusBadRequest)
		return
	}

	flow, ok := h.getOAuthFlow(flowID)
	if !ok {
		http.Error(w, "flow not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, flowToResponse(flow))
}

func (h *adminHandler) handlePollOAuthFlow(w http.ResponseWriter, r *http.Request) {
	flowID := strings.TrimSpace(r.PathValue("id"))
	if flowID == "" {
		http.Error(w, "missing flow id", http.StatusBadRequest)
		return
	}

	flow, ok := h.getOAuthFlow(flowID)
	if !ok {
		http.Error(w, "flow not found", http.StatusNotFound)
		return
	}

	if flow.Status != oauthFlowPending {
		writeJSON(w, http.StatusOK, flowToResponse(flow))
		return
	}

	switch flow.Method {
	case oauthMethodDeviceCode:
		cfg := auth.OpenAIOAuthConfig()
		cred, err := oauthPollDeviceCodeOnce(cfg, flow.DeviceAuthID, flow.UserCode)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "pending") {
				updated, _ := h.getOAuthFlow(flowID)
				writeJSON(w, http.StatusOK, flowToResponse(updated))
				return
			}
			h.setOAuthFlowError(flowID, fmt.Sprintf("device code poll failed: %v", err))
			updated, _ := h.getOAuthFlow(flowID)
			writeJSON(w, http.StatusOK, flowToResponse(updated))
			return
		}
		if cred == nil {
			updated, _ := h.getOAuthFlow(flowID)
			writeJSON(w, http.StatusOK, flowToResponse(updated))
			return
		}

		if err := h.persistCredentialAndConfig(flow.Provider, oauthMethodTokenOrOAuth(flow.Method), cred, flow.SelectedModel); err != nil {
			h.setOAuthFlowError(flowID, fmt.Sprintf("failed to save credential: %v", err))
			updated, _ := h.getOAuthFlow(flowID)
			writeJSON(w, http.StatusOK, flowToResponse(updated))
			return
		}

		h.setOAuthFlowSuccess(flowID)
		updated, _ := h.getOAuthFlow(flowID)
		writeJSON(w, http.StatusOK, flowToResponse(updated))
		return

	case oauthMethodBrowser:
		completion, err := parseOAuthBrowserPollRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		code := completion.Code
		if completion.RedirectURL != "" {
			redirect, parseErr := parseOAuthRedirectCompletion(completion.RedirectURL)
			if parseErr != nil {
				h.setOAuthFlowError(flowID, parseErr.Error())
				updated, _ := h.getOAuthFlow(flowID)
				writeJSON(w, http.StatusBadRequest, flowToResponse(updated))
				return
			}
			if redirect.ProviderError != "" {
				h.setOAuthFlowError(flowID, "authorization failed: "+redirect.ProviderError)
				updated, _ := h.getOAuthFlow(flowID)
				writeJSON(w, http.StatusBadRequest, flowToResponse(updated))
				return
			}
			if redirect.State != "" && redirect.State != flow.OAuthState {
				h.setOAuthFlowError(flowID, "state mismatch")
				updated, _ := h.getOAuthFlow(flowID)
				writeJSON(w, http.StatusBadRequest, flowToResponse(updated))
				return
			}
			code = redirect.Code
		}

		if err := h.completeBrowserOAuthFlow(flow, code); err != nil {
			h.setOAuthFlowError(flowID, err.Error())
			updated, _ := h.getOAuthFlow(flowID)
			writeJSON(w, http.StatusOK, flowToResponse(updated))
			return
		}

		h.setOAuthFlowSuccess(flowID)
		updated, _ := h.getOAuthFlow(flowID)
		writeJSON(w, http.StatusOK, flowToResponse(updated))
		return

	default:
		http.Error(w, "flow does not support polling", http.StatusBadRequest)
	}
}

func (h *adminHandler) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		renderOAuthCallbackPage(w, "", oauthFlowError, "Missing state", "missing_state")
		return
	}

	flow, ok := h.getOAuthFlowByState(state)
	if !ok {
		renderOAuthCallbackPage(w, "", oauthFlowError, "OAuth flow not found", "flow_not_found")
		return
	}

	if flow.Status != oauthFlowPending {
		renderOAuthCallbackPage(w, flow.ID, flow.Status, "Flow already completed", flow.Error)
		return
	}

	if errMsg := strings.TrimSpace(r.URL.Query().Get("error")); errMsg != "" {
		if desc := strings.TrimSpace(r.URL.Query().Get("error_description")); desc != "" {
			errMsg += ": " + desc
		}
		h.setOAuthFlowError(flow.ID, errMsg)
		renderOAuthCallbackPage(w, flow.ID, oauthFlowError, "Authorization failed", errMsg)
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		h.setOAuthFlowError(flow.ID, "missing authorization code")
		renderOAuthCallbackPage(w, flow.ID, oauthFlowError, "Missing authorization code", "missing_code")
		return
	}

	if err := h.completeBrowserOAuthFlow(flow, code); err != nil {
		h.setOAuthFlowError(flow.ID, err.Error())
		renderOAuthCallbackPage(w, flow.ID, oauthFlowError, "Authentication failed", err.Error())
		return
	}

	h.setOAuthFlowSuccess(flow.ID)
	renderOAuthCallbackPage(w, flow.ID, oauthFlowSuccess, "Authentication successful", "")
}

func (h *adminHandler) handleOAuthLogout(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req struct {
		Provider string `json:"provider"`
	}
	if err = json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	provider, err := normalizeOAuthProvider(req.Provider)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := oauthDeleteCredential(provider); err != nil {
		http.Error(w, fmt.Sprintf("failed to delete credential: %v", err), http.StatusInternalServerError)
		return
	}
	if err := h.syncProviderAuthMethod(provider, "", ""); err != nil {
		http.Error(w, fmt.Sprintf("failed to update config: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"provider": provider,
	})
}

func renderOAuthCallbackPage(w http.ResponseWriter, flowID, status, title, errMsg string) {
	payload := map[string]string{
		"type":   "suprclaw-oauth-result",
		"flowId": flowID,
		"status": status,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	payloadJSON, _ := json.Marshal(payload)

	message := title
	if errMsg != "" {
		message = fmt.Sprintf("%s: %s", title, errMsg)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status == oauthFlowSuccess {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusBadRequest)
	}

	_, _ = fmt.Fprintf(
		w,
		"<!doctype html><html><head><meta charset=\"utf-8\"><title>SuprClaw OAuth</title></head><body><script>(function(){var payload=%s;var hasOpener=false;try{if(window.opener&&!window.opener.closed){window.opener.postMessage(payload,window.location.origin);hasOpener=true}}catch(e){}var target='/credentials?oauth_flow_id='+encodeURIComponent(payload.flowId||'')+'&oauth_status='+encodeURIComponent(payload.status||'');setTimeout(function(){if(hasOpener){window.close();return}window.location.replace(target)},800)})();</script><div style=\"font-family:Inter,system-ui,sans-serif;padding:24px\"><h2>%s</h2><p>%s</p><p>You can close this window.</p></div></body></html>",
		string(payloadJSON),
		html.EscapeString(title),
		html.EscapeString(message),
	)
}

func normalizeOAuthProvider(raw string) (string, error) {
	provider := strings.ToLower(strings.TrimSpace(raw))
	switch provider {
	case "antigravity", "google":
		return oauthProviderGoogleAntigravity, nil
	case oauthProviderOpenAI, oauthProviderAnthropic, oauthProviderGoogleAntigravity, oauthProviderNVIDIA, oauthProviderOpenRouter:
		return provider, nil
	default:
		return "", fmt.Errorf("unsupported provider %q", raw)
	}
}

func isOAuthMethodSupported(provider, method string) bool {
	methods := oauthProviderMethods[provider]
	for _, m := range methods {
		if m == method {
			return true
		}
	}
	return false
}

func oauthConfigForProvider(provider string) (auth.OAuthProviderConfig, error) {
	switch provider {
	case oauthProviderOpenAI:
		return auth.OpenAIOAuthConfig(), nil
	case oauthProviderGoogleAntigravity:
		return auth.GoogleAntigravityOAuthConfig(), nil
	default:
		return auth.OAuthProviderConfig{}, fmt.Errorf("provider %q does not support browser oauth", provider)
	}
}

func oauthMethodTokenOrOAuth(method string) string {
	if method == oauthMethodToken {
		return oauthMethodToken
	}
	return "oauth"
}

func parseOAuthBrowserPollRequest(r *http.Request) (oauthBrowserPollRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return oauthBrowserPollRequest{}, fmt.Errorf("failed to read request body")
	}
	defer r.Body.Close()

	if strings.TrimSpace(string(body)) == "" {
		return oauthBrowserPollRequest{}, fmt.Errorf("browser flow poll requires exactly one of code or redirect_url")
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return oauthBrowserPollRequest{}, fmt.Errorf("invalid JSON: %v", err)
	}
	if len(payload) != 1 {
		return oauthBrowserPollRequest{}, fmt.Errorf("unsupported payload shape: provide exactly one of code or redirect_url")
	}

	if rawCode, ok := payload["code"]; ok {
		var code string
		if err := json.Unmarshal(rawCode, &code); err != nil {
			return oauthBrowserPollRequest{}, fmt.Errorf("code must be a string")
		}
		code = strings.TrimSpace(code)
		if code == "" {
			return oauthBrowserPollRequest{}, fmt.Errorf("code is required")
		}
		return oauthBrowserPollRequest{Code: code}, nil
	}

	if rawRedirectURL, ok := payload["redirect_url"]; ok {
		var redirectURL string
		if err := json.Unmarshal(rawRedirectURL, &redirectURL); err != nil {
			return oauthBrowserPollRequest{}, fmt.Errorf("redirect_url must be a string")
		}
		redirectURL = strings.TrimSpace(redirectURL)
		if redirectURL == "" {
			return oauthBrowserPollRequest{}, fmt.Errorf("redirect_url is required")
		}
		return oauthBrowserPollRequest{RedirectURL: redirectURL}, nil
	}

	return oauthBrowserPollRequest{}, fmt.Errorf("unsupported payload shape: provide exactly one of code or redirect_url")
}

func parseOAuthRedirectCompletion(redirectURL string) (oauthRedirectCompletion, error) {
	parsedURL, err := url.Parse(redirectURL)
	if err != nil {
		return oauthRedirectCompletion{}, fmt.Errorf("malformed redirect_url: %v", err)
	}
	if strings.TrimSpace(parsedURL.Scheme) == "" || strings.TrimSpace(parsedURL.Host) == "" {
		return oauthRedirectCompletion{}, fmt.Errorf("malformed redirect_url: missing scheme or host")
	}

	query := parsedURL.Query()
	result := oauthRedirectCompletion{
		State: strings.TrimSpace(query.Get("state")),
	}

	providerErr := strings.TrimSpace(query.Get("error"))
	if providerErr != "" {
		if desc := strings.TrimSpace(query.Get("error_description")); desc != "" {
			providerErr = providerErr + ": " + desc
		}
		result.ProviderError = providerErr
		return result, nil
	}

	result.Code = strings.TrimSpace(query.Get("code"))
	if result.Code == "" {
		return oauthRedirectCompletion{}, fmt.Errorf("redirect_url is missing authorization code")
	}
	return result, nil
}

func buildOAuthRedirectURI(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = strings.Split(forwarded, ",")[0]
	}
	return fmt.Sprintf("%s://%s/oauth/callback", scheme, r.Host)
}

func resolveOAuthBrowserRedirectURI(provider string, cfg auth.OAuthProviderConfig, r *http.Request) string {
	if provider == oauthProviderOpenAI && auth.IsDefaultOpenAIOAuthClientID(cfg.ClientID) {
		return fmt.Sprintf("http://localhost:%d/auth/callback", cfg.Port)
	}
	return buildOAuthRedirectURI(r)
}

func (h *adminHandler) completeBrowserOAuthFlow(flow *oauthFlow, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return fmt.Errorf("missing authorization code")
	}

	cfg, err := oauthConfigForProvider(flow.Provider)
	if err != nil {
		return err
	}

	cred, err := oauthExchangeCodeForTokens(cfg, code, flow.CodeVerifier, flow.RedirectURI)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	if err := h.persistCredentialAndConfig(flow.Provider, oauthMethodTokenOrOAuth(flow.Method), cred, flow.SelectedModel); err != nil {
		return fmt.Errorf("failed to save credential: %w", err)
	}

	return nil
}

func flowToResponse(flow *oauthFlow) oauthFlowResponse {
	resp := oauthFlowResponse{
		FlowID:   flow.ID,
		Provider: flow.Provider,
		Method:   flow.Method,
		Status:   flow.Status,
		Error:    flow.Error,
	}
	if !flow.ExpiresAt.IsZero() {
		resp.ExpiresAt = flow.ExpiresAt.Format(time.RFC3339)
	}
	if flow.Method == oauthMethodDeviceCode {
		resp.UserCode = flow.UserCode
		resp.VerifyURL = flow.VerifyURL
		resp.Interval = flow.Interval
	}
	return resp
}

func newOAuthFlowID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("oauth_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (h *adminHandler) ensureOAuthStateLocked() {
	if h.oauthFlows == nil {
		h.oauthFlows = make(map[string]*oauthFlow)
	}
	if h.oauthState == nil {
		h.oauthState = make(map[string]string)
	}
}

func (h *adminHandler) storeOAuthFlow(flow *oauthFlow) {
	now := oauthNow()
	h.oauthMu.Lock()
	defer h.oauthMu.Unlock()
	h.ensureOAuthStateLocked()

	h.gcOAuthFlowsLocked(now)
	h.oauthFlows[flow.ID] = flow
	if flow.OAuthState != "" {
		h.oauthState[flow.OAuthState] = flow.ID
	}
}

func (h *adminHandler) getOAuthFlow(flowID string) (*oauthFlow, bool) {
	now := oauthNow()
	h.oauthMu.Lock()
	defer h.oauthMu.Unlock()
	h.ensureOAuthStateLocked()

	h.gcOAuthFlowsLocked(now)
	flow, ok := h.oauthFlows[flowID]
	if !ok {
		return nil, false
	}
	cp := *flow
	return &cp, true
}

func (h *adminHandler) getOAuthFlowByState(state string) (*oauthFlow, bool) {
	now := oauthNow()
	h.oauthMu.Lock()
	defer h.oauthMu.Unlock()
	h.ensureOAuthStateLocked()

	h.gcOAuthFlowsLocked(now)
	flowID, ok := h.oauthState[state]
	if !ok {
		return nil, false
	}
	flow, ok := h.oauthFlows[flowID]
	if !ok {
		delete(h.oauthState, state)
		return nil, false
	}
	cp := *flow
	return &cp, true
}

func (h *adminHandler) setOAuthFlowSuccess(flowID string) {
	now := oauthNow()
	h.oauthMu.Lock()
	defer h.oauthMu.Unlock()
	h.ensureOAuthStateLocked()

	flow, ok := h.oauthFlows[flowID]
	if !ok {
		return
	}
	flow.Status = oauthFlowSuccess
	flow.Error = ""
	flow.UpdatedAt = now
	if flow.OAuthState != "" {
		delete(h.oauthState, flow.OAuthState)
	}
}

func (h *adminHandler) setOAuthFlowError(flowID, errMsg string) {
	now := oauthNow()
	h.oauthMu.Lock()
	defer h.oauthMu.Unlock()
	h.ensureOAuthStateLocked()

	flow, ok := h.oauthFlows[flowID]
	if !ok {
		return
	}
	flow.Status = oauthFlowError
	flow.Error = errMsg
	flow.UpdatedAt = now
	if flow.OAuthState != "" {
		delete(h.oauthState, flow.OAuthState)
	}
}

func (h *adminHandler) gcOAuthFlowsLocked(now time.Time) {
	for id, flow := range h.oauthFlows {
		if flow.Status == oauthFlowPending && !flow.ExpiresAt.IsZero() && now.After(flow.ExpiresAt) {
			flow.Status = oauthFlowExpired
			flow.Error = "flow expired"
			flow.UpdatedAt = now
			if flow.OAuthState != "" {
				delete(h.oauthState, flow.OAuthState)
			}
		}

		if flow.Status != oauthFlowPending && now.Sub(flow.UpdatedAt) > oauthTerminalFlowGC {
			if flow.OAuthState != "" {
				delete(h.oauthState, flow.OAuthState)
			}
			delete(h.oauthFlows, id)
		}
	}
}

func (h *adminHandler) persistCredentialAndConfig(provider, authMethod string, cred *auth.AuthCredential, selectedModelName string) error {
	if cred == nil {
		return fmt.Errorf("empty credential")
	}

	cp := *cred
	cp.Provider = provider
	if cp.AuthMethod == "" {
		cp.AuthMethod = authMethod
	}

	if provider == oauthProviderGoogleAntigravity {
		if cp.Email == "" {
			email, err := oauthFetchGoogleUserEmailFunc(cp.AccessToken)
			if err != nil {
				log.Printf("oauth warning: could not fetch google email: %v", err)
			} else {
				cp.Email = email
			}
		}
		if cp.ProjectID == "" {
			projectID, err := oauthFetchAntigravityProject(cp.AccessToken)
			if err != nil {
				log.Printf("oauth warning: could not fetch antigravity project id: %v", err)
			} else {
				cp.ProjectID = projectID
			}
		}
	}

	if err := oauthSetCredential(provider, &cp); err != nil {
		return fmt.Errorf("saving credential: %w", err)
	}
	if err := h.syncProviderAuthMethod(provider, authMethod, selectedModelName); err != nil {
		return fmt.Errorf("syncing provider auth config: %w", err)
	}
	return nil
}

func (h *adminHandler) syncProviderAuthMethod(provider, authMethod, selectedModelName string) error {
	selectedModelName = strings.TrimSpace(selectedModelName)
	return h.mutateCfgIfChanged(func(cfg *config.Config) (bool, error) {
		changed := false
		switch provider {
		case oauthProviderOpenAI:
			if cfg.Providers.OpenAI.AuthMethod != authMethod {
				cfg.Providers.OpenAI.AuthMethod = authMethod
				changed = true
			}
		case oauthProviderAnthropic:
			if cfg.Providers.Anthropic.AuthMethod != authMethod {
				cfg.Providers.Anthropic.AuthMethod = authMethod
				changed = true
			}
		case oauthProviderGoogleAntigravity:
			if cfg.Providers.Antigravity.AuthMethod != authMethod {
				cfg.Providers.Antigravity.AuthMethod = authMethod
				changed = true
			}
		case oauthProviderNVIDIA:
			if cfg.Providers.Nvidia.AuthMethod != authMethod {
				cfg.Providers.Nvidia.AuthMethod = authMethod
				changed = true
			}
		case oauthProviderOpenRouter:
			if cfg.Providers.OpenRouter.AuthMethod != authMethod {
				cfg.Providers.OpenRouter.AuthMethod = authMethod
				changed = true
			}
		default:
			return false, fmt.Errorf("unsupported provider %q", provider)
		}

		targetModel := config.ModelConfig{}
		if authMethod != "" {
			if selectedModelName != "" {
				selectedModelCfg, err := resolveOAuthModelConfigForProvider(cfg, provider, selectedModelName)
				if err != nil {
					return false, err
				}
				selectedModelCfg.AuthMethod = authMethod
				targetModel = selectedModelCfg
			} else {
				targetModel = defaultModelConfigForProvider(provider, authMethod)
			}
		}

		found := false
		for i := range cfg.ModelList {
			if modelBelongsToProvider(provider, cfg.ModelList[i].Model) {
				if cfg.ModelList[i].AuthMethod != authMethod {
					cfg.ModelList[i].AuthMethod = authMethod
					changed = true
				}
				if authMethod != "" &&
					cfg.ModelList[i].ModelName == targetModel.ModelName &&
					cfg.ModelList[i].Model == targetModel.Model {
					found = true
				}
			}
		}

		if !found && authMethod != "" {
			cfg.ModelList = append(cfg.ModelList, targetModel)
			changed = true
		}

		if authMethod != "" {
			if h.syncAllAgentModels(cfg, targetModel.ModelName) {
				changed = true
			}
		}
		return changed, nil
	})
}

func (h *adminHandler) validateOAuthLoginSelectedModel(provider, modelName string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return err
	}
	_, err = resolveOAuthModelConfigForProvider(cfg, provider, modelName)
	return err
}

func resolveOAuthModelConfigForProvider(cfg *config.Config, provider, modelName string) (config.ModelConfig, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return config.ModelConfig{}, &oauthModelValidationError{message: "model is required when provided"}
	}
	var nameExists bool
	for _, modelCfg := range cfg.ModelList {
		if modelCfg.ModelName != modelName {
			continue
		}
		nameExists = true
		if modelBelongsToProvider(provider, modelCfg.Model) {
			return modelCfg, nil
		}
	}
	if !nameExists {
		return config.ModelConfig{}, &oauthModelValidationError{
			message: fmt.Sprintf("unsupported model %q for provider %q", modelName, provider),
		}
	}
	return config.ModelConfig{}, &oauthModelValidationError{
		message: fmt.Sprintf("model %q does not belong to provider %q", modelName, provider),
	}
}

func (h *adminHandler) syncAllAgentModels(cfg *config.Config, targetModelName string) bool {
	changed := false
	targetModelName = strings.TrimSpace(targetModelName)
	if targetModelName == "" {
		return false
	}

	if cfg.Agents.Defaults.ModelName != targetModelName || cfg.Agents.Defaults.Model != "" {
		cfg.Agents.Defaults.ModelName = targetModelName
		cfg.Agents.Defaults.Model = ""
		changed = true
	}

	for i := range cfg.Agents.List {
		agent := &cfg.Agents.List[i]
		if agent.Model == nil {
			agent.Model = &config.AgentModelConfig{}
			changed = true
		}
		if agent.Model.Primary != targetModelName {
			agent.Model.Primary = targetModelName
			changed = true
		}
		if len(agent.Model.Fallbacks) > 0 {
			agent.Model.Fallbacks = nil
			changed = true
		}
	}
	return changed
}

func modelBelongsToProvider(provider, model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	switch provider {
	case oauthProviderOpenAI:
		return lower == "openai" || strings.HasPrefix(lower, "openai/")
	case oauthProviderAnthropic:
		return lower == "anthropic" || strings.HasPrefix(lower, "anthropic/")
	case oauthProviderGoogleAntigravity:
		return lower == "antigravity" ||
			lower == "google-antigravity" ||
			strings.HasPrefix(lower, "antigravity/") ||
			strings.HasPrefix(lower, "google-antigravity/")
	case oauthProviderNVIDIA:
		return lower == "nvidia" || strings.HasPrefix(lower, "nvidia/")
	case oauthProviderOpenRouter:
		return lower == "openrouter" || strings.HasPrefix(lower, "openrouter/")
	default:
		return false
	}
}

func defaultModelConfigForProvider(provider, authMethod string) config.ModelConfig {
	switch provider {
	case oauthProviderOpenAI:
		return config.ModelConfig{
			ModelName:  "gpt-5.4",
			Model:      "openai/gpt-5.4",
			AuthMethod: authMethod,
		}
	case oauthProviderAnthropic:
		return config.ModelConfig{
			ModelName:  "claude-sonnet-4.6",
			Model:      "anthropic/claude-sonnet-4.6",
			AuthMethod: authMethod,
		}
	case oauthProviderGoogleAntigravity:
		return config.ModelConfig{
			ModelName:  "gemini-flash",
			Model:      "antigravity/gemini-3-flash",
			AuthMethod: authMethod,
		}
	case oauthProviderNVIDIA:
		return config.ModelConfig{
			ModelName:  "nvidia/deepseek-ai/deepseek-v4-pro",
			Model:      "nvidia/deepseek-ai/deepseek-v4-pro",
			AuthMethod: authMethod,
		}
	case oauthProviderOpenRouter:
		return config.ModelConfig{
			ModelName:  "openrouter/auto",
			Model:      "openrouter/auto",
			AuthMethod: authMethod,
		}
	default:
		return config.ModelConfig{}
	}
}

func parseOAuthLoginRequest(body []byte) (oauthLoginRequest, error) {
	var req oauthLoginRequest

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return oauthLoginRequest{}, fmt.Errorf("invalid JSON: %v", err)
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return oauthLoginRequest{}, fmt.Errorf("invalid JSON: %v", err)
	}

	rawModel, modelProvided := payload["model"]
	req.ModelProvided = modelProvided
	if modelProvided {
		var model string
		if err := json.Unmarshal(rawModel, &model); err != nil {
			return oauthLoginRequest{}, fmt.Errorf("model must be a string")
		}
		model = strings.TrimSpace(model)
		if model == "" {
			return oauthLoginRequest{}, fmt.Errorf("model is required when provided")
		}
		req.Model = model
	}
	return req, nil
}

func parseOAuthModelUpdateRequest(body []byte) (oauthModelUpdateRequest, error) {
	var req oauthModelUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return oauthModelUpdateRequest{}, fmt.Errorf("invalid JSON: %v", err)
	}
	req.Provider = strings.TrimSpace(req.Provider)
	req.Model = strings.TrimSpace(req.Model)
	if req.Provider == "" {
		return oauthModelUpdateRequest{}, fmt.Errorf("provider is required")
	}
	if req.Model == "" {
		return oauthModelUpdateRequest{}, fmt.Errorf("model is required")
	}
	return req, nil
}

func fetchGoogleUserEmail(accessToken string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("userinfo request failed: %s", string(body))
	}

	var userInfo struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return "", err
	}
	if userInfo.Email == "" {
		return "", fmt.Errorf("empty email in userinfo response")
	}
	return userInfo.Email, nil
}
