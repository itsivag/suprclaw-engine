package browserrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrTargetNotFound       = errors.New("target not found")
	ErrTargetNotAttached    = errors.New("target not attached")
	ErrTargetOwned          = errors.New("target already controlled by another extension session")
	ErrExtensionNotFound    = errors.New("extension session not found")
	ErrMaxClientsReached    = errors.New("max relay clients reached")
	ErrInvalidMessage       = errors.New("invalid extension message")
	ErrRequestTimeout       = errors.New("relay request timed out")
	ErrRequestCanceled      = errors.New("relay request canceled")
	ErrNoExtensionForTarget = errors.New("no extension session available for target")
)

const (
	defaultMaxClients              = 16
	defaultIdleTimeoutSec          = 60
	defaultEngineMode              = EngineModeHybrid
	defaultAgentBrowserBinary      = "agent-browser"
	defaultAgentBrowserMaxSessions = 8
	defaultAgentBrowserIdleTimeout = 300
)

type extensionSession struct {
	id       string
	conn     *safeConn
	lastSeen time.Time
	targets  map[string]Target
	attached map[string]struct{}
}

type cdpClient struct {
	id       string
	targetID string
	conn     *safeConn
}

type pendingRequest struct {
	extensionID string
	responseCh  chan commandResponse
}

type commandResponse struct {
	result json.RawMessage
	err    error
}

type safeConn struct {
	inner JSONConn
	mu    sync.Mutex
}

func newSafeConn(inner JSONConn) *safeConn {
	return &safeConn{inner: inner}
}

func (s *safeConn) WriteJSON(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.WriteJSON(v)
}

func (s *safeConn) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.Close()
}

type requestEnvelope struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	TargetID  string `json:"targetId,omitempty"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
}

type responseEnvelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"requestId"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type eventEnvelope struct {
	Type     string          `json:"type"`
	TargetID string          `json:"targetId,omitempty"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
}

type attachResultEnvelope struct {
	Type     string `json:"type"`
	TargetID string `json:"targetId"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
}

type extensionMessage struct {
	Type      string          `json:"type"`
	RequestID string          `json:"requestId,omitempty"`
	TargetID  string          `json:"targetId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	Targets   []Target        `json:"targets,omitempty"`
}

// Manager coordinates browser relay sessions, target ownership, and command routing.
type Manager struct {
	mu sync.RWMutex

	cfg Config
	now func() time.Time

	extensions   map[string]*extensionSession
	targetOwners map[string]string // targetID -> extension session ID
	cdpClients   map[string]*cdpClient
	pending      map[string]*pendingRequest

	requestCounter uint64
	stopCh         chan struct{}
	closeOnce      sync.Once
}

func NewManager(cfg Config) *Manager {
	cfg = normalizeConfig(cfg)
	m := &Manager{
		cfg:          cfg,
		now:          time.Now,
		extensions:   make(map[string]*extensionSession),
		targetOwners: make(map[string]string),
		cdpClients:   make(map[string]*cdpClient),
		pending:      make(map[string]*pendingRequest),
		stopCh:       make(chan struct{}),
	}
	go m.cleanupLoop()
	return m
}

func normalizeConfig(cfg Config) Config {
	if cfg.MaxClients <= 0 {
		cfg.MaxClients = defaultMaxClients
	}
	if cfg.IdleTimeoutSec <= 0 {
		cfg.IdleTimeoutSec = defaultIdleTimeoutSec
	}
	switch strings.ToLower(strings.TrimSpace(cfg.EngineMode)) {
	case "", EngineModeHybrid:
		cfg.EngineMode = EngineModeHybrid
	case EngineModeExtension:
		cfg.EngineMode = EngineModeExtension
	case EngineModeAgentBrowser:
		cfg.EngineMode = EngineModeAgentBrowser
	default:
		cfg.EngineMode = defaultEngineMode
	}
	if strings.TrimSpace(cfg.AgentBrowserBinary) == "" {
		cfg.AgentBrowserBinary = defaultAgentBrowserBinary
	}
	if cfg.AgentBrowserMaxSessions <= 0 {
		cfg.AgentBrowserMaxSessions = defaultAgentBrowserMaxSessions
	}
	if cfg.AgentBrowserIdleTimeoutSec <= 0 {
		cfg.AgentBrowserIdleTimeoutSec = defaultAgentBrowserIdleTimeout
	}
	return cfg
}

func (m *Manager) ApplyConfig(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = normalizeConfig(cfg)
}

func (m *Manager) Config() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		close(m.stopCh)
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, ext := range m.extensions {
			_ = ext.conn.Close()
		}
		for _, client := range m.cdpClients {
			_ = client.conn.Close()
		}
		for id, pending := range m.pending {
			delete(m.pending, id)
			pending.responseCh <- commandResponse{err: ErrRequestCanceled}
		}
	})
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Status{
		Enabled:             m.cfg.Enabled,
		ConnectedExtensions: len(m.extensions),
		ConnectedClients:    len(m.cdpClients),
		AttachedTargets:     len(m.targetOwners),
		MaxClients:          m.cfg.MaxClients,
		IdleTimeoutSec:      m.cfg.IdleTimeoutSec,
	}
}

func (m *Manager) Targets() []Target {
	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[string]Target)
	for sessionID, sess := range m.extensions {
		for id, target := range sess.targets {
			t := target
			t.SessionID = sessionID
			if ownerID, ok := m.targetOwners[id]; ok {
				t.Attached = true
				t.OwnerID = ownerID
			} else {
				t.Attached = false
				t.OwnerID = ""
			}
			if t.Type == "" {
				t.Type = "page"
			}
			if existing, ok := seen[id]; !ok || t.LastSeen.After(existing.LastSeen) {
				seen[id] = t
			}
		}
	}

	out := make([]Target, 0, len(seen))
	for _, target := range seen {
		out = append(out, target)
	}
	return out
}

func (m *Manager) AttachExtension(sessionID string, conn JSONConn) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.extensions[sessionID]; ok {
		m.detachExtensionLocked(old)
	}

	m.extensions[sessionID] = &extensionSession{
		id:       sessionID,
		conn:     newSafeConn(conn),
		lastSeen: m.now(),
		targets:  make(map[string]Target),
		attached: make(map[string]struct{}),
	}
}

func (m *Manager) DetachExtension(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.extensions[sessionID]; ok {
		m.detachExtensionLocked(sess)
	}
}

func (m *Manager) RegisterCDPClient(clientID, targetID string, conn JSONConn) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg.MaxClients > 0 && len(m.cdpClients) >= m.cfg.MaxClients {
		return ErrMaxClientsReached
	}
	if _, ok := m.targetOwners[targetID]; !ok {
		return ErrTargetNotAttached
	}

	m.cdpClients[clientID] = &cdpClient{
		id:       clientID,
		targetID: targetID,
		conn:     newSafeConn(conn),
	}
	return nil
}

func (m *Manager) UnregisterCDPClient(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if client, ok := m.cdpClients[clientID]; ok {
		_ = client.conn.Close()
		delete(m.cdpClients, clientID)
	}
}

func (m *Manager) HandleExtensionMessage(sessionID string, payload []byte) error {
	var msg extensionMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}

	m.mu.Lock()
	sess, ok := m.extensions[sessionID]
	if !ok {
		m.mu.Unlock()
		return ErrExtensionNotFound
	}
	sess.lastSeen = m.now()
	m.mu.Unlock()

	switch msg.Type {
	case "hello", "heartbeat":
		if len(msg.Targets) > 0 {
			m.updateTargets(sessionID, msg.Targets)
		}
		return nil
	case "targets":
		m.updateTargets(sessionID, msg.Targets)
		return nil
	case "attach", "attached":
		return m.handleAttach(sessionID, msg.TargetID)
	case "detach", "detached":
		m.handleDetach(sessionID, msg.TargetID)
		return nil
	case "response":
		m.handleResponse(sessionID, msg)
		return nil
	case "event":
		m.handleEvent(msg)
		return nil
	default:
		return fmt.Errorf("%w: unknown message type %q", ErrInvalidMessage, msg.Type)
	}
}

func (m *Manager) SendCommand(ctx context.Context, targetID, method string, params any, requireAttached bool) (json.RawMessage, error) {
	m.mu.Lock()
	ext, err := m.findExtensionForTargetLocked(targetID, requireAttached)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}

	requestID := m.nextRequestIDLocked()
	pending := &pendingRequest{
		extensionID: ext.id,
		responseCh:  make(chan commandResponse, 1),
	}
	m.pending[requestID] = pending

	req := requestEnvelope{
		Type:      "request",
		RequestID: requestID,
		TargetID:  targetID,
		Method:    method,
		Params:    params,
	}
	m.mu.Unlock()

	if err = ext.conn.WriteJSON(req); err != nil {
		m.mu.Lock()
		delete(m.pending, requestID)
		m.mu.Unlock()
		return nil, fmt.Errorf("send command: %w", err)
	}

	select {
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.pending, requestID)
		m.mu.Unlock()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrRequestTimeout
		}
		return nil, ErrRequestCanceled
	case resp := <-pending.responseCh:
		if resp.err != nil {
			return nil, resp.err
		}
		return resp.result, nil
	}
}

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.EvictStaleSessions()
		}
	}
}

func (m *Manager) EvictStaleSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()

	idle := time.Duration(m.cfg.IdleTimeoutSec) * time.Second
	now := m.now()
	for _, sess := range m.extensions {
		if now.Sub(sess.lastSeen) > idle {
			m.detachExtensionLocked(sess)
		}
	}
}

func (m *Manager) updateTargets(sessionID string, targets []Target) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.extensions[sessionID]
	if !ok {
		return
	}

	next := make(map[string]Target, len(targets))
	now := m.now()
	for _, target := range targets {
		if target.ID == "" {
			continue
		}
		t := target
		if t.Type == "" {
			t.Type = "page"
		}
		t.LastSeen = now
		next[t.ID] = t
	}
	sess.targets = next
}

func (m *Manager) handleAttach(sessionID, targetID string) error {
	if targetID == "" {
		return fmt.Errorf("%w: empty targetId", ErrInvalidMessage)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.extensions[sessionID]
	if !ok {
		return ErrExtensionNotFound
	}

	if ownerID, exists := m.targetOwners[targetID]; exists && ownerID != sessionID {
		_ = sess.conn.WriteJSON(attachResultEnvelope{
			Type:     "attach_result",
			TargetID: targetID,
			OK:       false,
			Error:    ErrTargetOwned.Error(),
		})
		return ErrTargetOwned
	}

	m.targetOwners[targetID] = sessionID
	sess.attached[targetID] = struct{}{}
	_ = sess.conn.WriteJSON(attachResultEnvelope{Type: "attach_result", TargetID: targetID, OK: true})
	return nil
}

func (m *Manager) handleDetach(sessionID, targetID string) {
	if targetID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.extensions[sessionID]
	if !ok {
		return
	}
	if ownerID, owned := m.targetOwners[targetID]; owned && ownerID == sessionID {
		delete(m.targetOwners, targetID)
		delete(sess.attached, targetID)
	}
}

func (m *Manager) handleResponse(sessionID string, msg extensionMessage) {
	m.mu.Lock()
	pending, ok := m.pending[msg.RequestID]
	if ok {
		delete(m.pending, msg.RequestID)
	}
	m.mu.Unlock()

	if !ok {
		return
	}
	if pending.extensionID != sessionID {
		pending.responseCh <- commandResponse{err: fmt.Errorf("response session mismatch")}
		return
	}
	if msg.Error != "" {
		pending.responseCh <- commandResponse{err: errors.New(msg.Error)}
		return
	}
	pending.responseCh <- commandResponse{result: msg.Result}
}

func (m *Manager) handleEvent(msg extensionMessage) {
	if msg.Method == "" {
		return
	}

	var params any
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			params = map[string]any{"raw": string(msg.Params)}
		}
	}

	payload := eventEnvelope{
		Type:     "event",
		TargetID: msg.TargetID,
		Method:   msg.Method,
		Params:   msg.Params,
	}
	_ = payload // keep explicit envelope for extension protocol parity

	broadcast := map[string]any{"method": msg.Method}
	if params != nil {
		broadcast["params"] = params
	}

	clients := m.clientsForTarget(msg.TargetID)
	for _, client := range clients {
		_ = client.conn.WriteJSON(broadcast)
	}
}

func (m *Manager) clientsForTarget(targetID string) []*cdpClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*cdpClient, 0)
	for _, client := range m.cdpClients {
		if client.targetID == targetID {
			out = append(out, client)
		}
	}
	return out
}

func (m *Manager) findExtensionForTargetLocked(targetID string, requireAttached bool) (*extensionSession, error) {
	if requireAttached {
		sessionID, ok := m.targetOwners[targetID]
		if !ok {
			if targetID == "" {
				return nil, ErrTargetNotFound
			}
			return nil, ErrTargetNotAttached
		}
		ext, ok := m.extensions[sessionID]
		if !ok {
			return nil, ErrExtensionNotFound
		}
		return ext, nil
	}

	if sessionID, ok := m.targetOwners[targetID]; ok {
		if ext, exists := m.extensions[sessionID]; exists {
			return ext, nil
		}
	}
	for _, ext := range m.extensions {
		if _, exists := ext.targets[targetID]; exists {
			return ext, nil
		}
	}
	return nil, ErrNoExtensionForTarget
}

func (m *Manager) nextRequestIDLocked() string {
	next := atomic.AddUint64(&m.requestCounter, 1)
	return fmt.Sprintf("req-%d", next)
}

func (m *Manager) detachExtensionLocked(sess *extensionSession) {
	delete(m.extensions, sess.id)
	for targetID, ownerID := range m.targetOwners {
		if ownerID == sess.id {
			delete(m.targetOwners, targetID)
		}
	}
	for requestID, pending := range m.pending {
		if pending.extensionID == sess.id {
			delete(m.pending, requestID)
			pending.responseCh <- commandResponse{err: ErrExtensionNotFound}
		}
	}
	_ = sess.conn.Close()
}
