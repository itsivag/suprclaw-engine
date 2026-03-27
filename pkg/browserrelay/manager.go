package browserrelay

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/itsivag/suprclaw/pkg/logger"
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
	defaultMaxClients                   = 16
	defaultIdleTimeoutSec               = 60
	defaultEngineMode                   = EngineModeHybrid
	defaultAgentBrowserBinary           = "agent-browser"
	defaultAgentBrowserMaxSessions      = 8
	defaultAgentBrowserIdleTimeout      = 300
	defaultAgentBrowserBatchWindowMS    = 25
	defaultAgentBrowserBatchMaxSteps    = 24
	defaultAgentBrowserRuntimeTimeoutMS = 30000
	defaultAgentBrowserQueueDepth       = 64
	defaultSnapshotMode                 = "compact"
	defaultSnapshotMaxPayloadBytes      = 98304
	defaultSnapshotMaxNodes             = 120
	defaultSnapshotMaxTextChars         = 120
	defaultSnapshotMaxDepth             = 6
	defaultSnapshotRefTTLSec            = 600
	defaultSnapshotMaxGenerations       = 4
	defaultTargetQueueDepth             = 32
	loopGuardFailureThreshold           = 3
	loopGuardWindow                     = 45 * time.Second
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

type commandTask struct {
	ctx             context.Context
	targetID        string
	method          string
	params          any
	requireAttached bool
	fingerprint     string
	responseCh      chan commandResponse
}

type targetCommandQueue struct {
	targetID  string
	ownerID   string
	tasks     chan *commandTask
	closed    chan struct{}
	closeOnce sync.Once
	cancelErr error
}

func (q *targetCommandQueue) stop(err error) {
	q.closeOnce.Do(func() {
		if err == nil {
			err = ErrRelayQueueCanceled
		}
		q.cancelErr = err
		close(q.closed)
	})
}

type loopGuardRecord struct {
	count       int
	lastFailure time.Time
}

type targetEvent struct {
	TargetID string
	Method   string
	Params   json.RawMessage
	At       time.Time
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
	queues       map[string]*targetCommandQueue
	queueDepth   int
	loopGuard    map[string]loopGuardRecord
	eventSubs    map[string]map[string]chan targetEvent

	requestCounter uint64
	eventCounter   uint64
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
		queues:       make(map[string]*targetCommandQueue),
		queueDepth:   defaultTargetQueueDepth,
		loopGuard:    make(map[string]loopGuardRecord),
		eventSubs:    make(map[string]map[string]chan targetEvent),
		stopCh:       make(chan struct{}),
	}
	go m.cleanupLoop()
	return m
}

func normalizeConfig(cfg Config) Config {
	rawSnapshotDefaultsUnset := strings.TrimSpace(cfg.SnapshotDefaultMode) == "" &&
		cfg.SnapshotMaxPayloadBytes == 0 &&
		cfg.SnapshotMaxNodes == 0 &&
		cfg.SnapshotMaxTextChars == 0 &&
		cfg.SnapshotMaxDepth == 0 &&
		cfg.SnapshotRefTTLSec == 0 &&
		cfg.SnapshotMaxGenerations == 0
	rawAgentBrowserRuntimeDefaultsUnset := cfg.AgentBrowserBatchWindowMS == 0 &&
		cfg.AgentBrowserBatchMaxSteps == 0 &&
		cfg.AgentBrowserRuntimeCommandTimeoutMS == 0 &&
		cfg.AgentBrowserStreamPort == 0 &&
		!cfg.AgentBrowserStreamEnabled

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
	if cfg.AgentBrowserBatchWindowMS <= 0 {
		cfg.AgentBrowserBatchWindowMS = defaultAgentBrowserBatchWindowMS
	}
	if cfg.AgentBrowserBatchMaxSteps <= 0 {
		cfg.AgentBrowserBatchMaxSteps = defaultAgentBrowserBatchMaxSteps
	}
	if cfg.AgentBrowserRuntimeCommandTimeoutMS <= 0 {
		cfg.AgentBrowserRuntimeCommandTimeoutMS = defaultAgentBrowserRuntimeTimeoutMS
	}
	if cfg.AgentBrowserStreamPort < 0 {
		cfg.AgentBrowserStreamPort = 0
	}
	if rawAgentBrowserRuntimeDefaultsUnset && !cfg.AgentBrowserStreamEnabled {
		cfg.AgentBrowserStreamEnabled = true
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SnapshotDefaultMode)) {
	case "", "compact":
		cfg.SnapshotDefaultMode = defaultSnapshotMode
	case "full":
		cfg.SnapshotDefaultMode = "full"
	default:
		cfg.SnapshotDefaultMode = defaultSnapshotMode
	}
	if cfg.SnapshotMaxPayloadBytes <= 0 {
		cfg.SnapshotMaxPayloadBytes = defaultSnapshotMaxPayloadBytes
	}
	if cfg.SnapshotMaxNodes <= 0 {
		cfg.SnapshotMaxNodes = defaultSnapshotMaxNodes
	}
	if cfg.SnapshotMaxTextChars <= 0 {
		cfg.SnapshotMaxTextChars = defaultSnapshotMaxTextChars
	}
	if cfg.SnapshotMaxDepth <= 0 {
		cfg.SnapshotMaxDepth = defaultSnapshotMaxDepth
	}
	if cfg.SnapshotRefTTLSec <= 0 {
		cfg.SnapshotRefTTLSec = defaultSnapshotRefTTLSec
	}
	if cfg.SnapshotMaxGenerations <= 0 {
		cfg.SnapshotMaxGenerations = defaultSnapshotMaxGenerations
	}
	if rawSnapshotDefaultsUnset && !cfg.SnapshotInteractiveOnly {
		cfg.SnapshotInteractiveOnly = true
	}
	if rawSnapshotDefaultsUnset && !cfg.SnapshotAllowFullTree {
		cfg.SnapshotAllowFullTree = true
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

func (m *Manager) QueueDepth(targetID string) int {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	queue, ok := m.queues[targetID]
	if !ok {
		return 0
	}
	return len(queue.tasks)
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
		for targetID, queue := range m.queues {
			delete(m.queues, targetID)
			queue.stop(ErrRelayQueueCanceled)
		}
		for id, pending := range m.pending {
			delete(m.pending, id)
			pending.responseCh <- commandResponse{err: ErrRequestCanceled}
		}
		for targetID, subs := range m.eventSubs {
			for subID, ch := range subs {
				close(ch)
				delete(subs, subID)
			}
			delete(m.eventSubs, targetID)
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
	targetID = strings.TrimSpace(targetID)
	fingerprint := commandFingerprint(method, params)

	// Attached-target commands are serialized through a per-target queue to avoid
	// interleaving and to provide deterministic cancelation on detach.
	if requireAttached {
		m.mu.Lock()
		ext, err := m.findExtensionForTargetLocked(targetID, true)
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		if loopErr := m.loopGuardPrecheckLocked(targetID, fingerprint); loopErr != nil {
			m.mu.Unlock()
			return nil, loopErr
		}

		queue := m.ensureQueueLocked(targetID, ext.id)
		task := &commandTask{
			ctx:             ctx,
			targetID:        targetID,
			method:          method,
			params:          params,
			requireAttached: true,
			fingerprint:     fingerprint,
			responseCh:      make(chan commandResponse, 1),
		}

		select {
		case queue.tasks <- task:
			m.mu.Unlock()
		default:
			m.mu.Unlock()
			return nil, ErrRelayQueueFull
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, ErrRequestTimeout
			}
			return nil, ErrRequestCanceled
		case resp := <-task.responseCh:
			return resp.result, resp.err
		}
	}

	m.mu.Lock()
	if loopErr := m.loopGuardPrecheckLocked(targetID, fingerprint); loopErr != nil {
		m.mu.Unlock()
		return nil, loopErr
	}
	m.mu.Unlock()

	result, err := m.sendCommandDirect(ctx, targetID, method, params, requireAttached)
	if err != nil {
		m.recordLoopGuardFailure(targetID, fingerprint)
		return nil, err
	}
	m.clearLoopGuard(targetID, fingerprint)
	return result, nil
}

func (m *Manager) sendCommandDirect(
	ctx context.Context,
	targetID, method string,
	params any,
	requireAttached bool,
) (json.RawMessage, error) {
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

func (m *Manager) ensureQueueLocked(targetID, ownerID string) *targetCommandQueue {
	if queue, ok := m.queues[targetID]; ok {
		if queue.ownerID == ownerID {
			return queue
		}
		m.stopQueueLocked(targetID, ErrRelayQueueCanceled)
	}

	queue := &targetCommandQueue{
		targetID: targetID,
		ownerID:  ownerID,
		tasks:    make(chan *commandTask, m.queueDepth),
		closed:   make(chan struct{}),
	}
	m.queues[targetID] = queue
	go m.runTargetQueue(queue)
	return queue
}

func (m *Manager) stopQueueLocked(targetID string, err error) {
	queue, ok := m.queues[targetID]
	if !ok {
		return
	}
	delete(m.queues, targetID)
	queue.stop(err)
}

func (m *Manager) runTargetQueue(queue *targetCommandQueue) {
	for {
		select {
		case <-queue.closed:
			m.drainQueue(queue, queue.cancelErr)
			return
		case task := <-queue.tasks:
			if task == nil {
				continue
			}
			select {
			case <-queue.closed:
				task.responseCh <- commandResponse{err: queue.cancelErr}
				continue
			default:
			}
			result, err := m.sendCommandDirect(task.ctx, task.targetID, task.method, task.params, task.requireAttached)
			if err != nil {
				m.recordLoopGuardFailure(task.targetID, task.fingerprint)
				task.responseCh <- commandResponse{err: err}
				continue
			}
			m.clearLoopGuard(task.targetID, task.fingerprint)
			task.responseCh <- commandResponse{result: result}
		}
	}
}

func (m *Manager) drainQueue(queue *targetCommandQueue, err error) {
	if err == nil {
		err = ErrRelayQueueCanceled
	}
	for {
		select {
		case task := <-queue.tasks:
			if task != nil {
				task.responseCh <- commandResponse{err: err}
			}
		default:
			return
		}
	}
}

func commandFingerprint(method string, params any) string {
	method = strings.TrimSpace(method)
	paramsJSON := stableJSON(params)
	sum := sha1.Sum([]byte(method + "|" + paramsJSON))
	return fmt.Sprintf("%x", sum[:8])
}

func stableJSON(v any) string {
	if v == nil {
		return "null"
	}
	normalized := normalizeJSONValue(v)
	data, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

func normalizeJSONValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(val))
		for _, k := range keys {
			out[k] = normalizeJSONValue(val[k])
		}
		return out
	case []any:
		out := make([]any, 0, len(val))
		for _, item := range val {
			out = append(out, normalizeJSONValue(item))
		}
		return out
	default:
		return v
	}
}

func (m *Manager) loopGuardPrecheckLocked(targetID, fingerprint string) error {
	key := targetID + "|" + fingerprint
	record, ok := m.loopGuard[key]
	if !ok {
		return nil
	}
	if m.now().Sub(record.lastFailure) > loopGuardWindow {
		delete(m.loopGuard, key)
		return nil
	}
	if record.count >= loopGuardFailureThreshold {
		logger.WarnCF("browser-relay", "loop guard triggered", map[string]any{
			"target_id":   targetID,
			"fingerprint": fingerprint,
			"fail_count":  record.count,
			"window_secs": int(loopGuardWindow.Seconds()),
		})
		return fmt.Errorf("%w: target=%s fingerprint=%s", ErrRelayLoopGuardTriggered, targetID, fingerprint)
	}
	return nil
}

func (m *Manager) recordLoopGuardFailure(targetID, fingerprint string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := targetID + "|" + fingerprint
	record := m.loopGuard[key]
	now := m.now()
	if now.Sub(record.lastFailure) > loopGuardWindow {
		record.count = 1
	} else {
		record.count++
	}
	record.lastFailure = now
	m.loopGuard[key] = record
}

func (m *Manager) clearLoopGuard(targetID, fingerprint string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.loopGuard, targetID+"|"+fingerprint)
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
	m.ensureQueueLocked(targetID, sessionID)
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
		m.stopQueueLocked(targetID, ErrRelayQueueCanceled)
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

	event := targetEvent{
		TargetID: msg.TargetID,
		Method:   msg.Method,
		Params:   msg.Params,
		At:       m.now(),
	}
	m.publishTargetEvent(event)
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

func (m *Manager) SubscribeTargetEvents(targetID string) (string, <-chan targetEvent, func()) {
	targetID = strings.TrimSpace(targetID)
	ch := make(chan targetEvent, 32)
	if targetID == "" {
		close(ch)
		return "", ch, func() {}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	subID := fmt.Sprintf("sub-%d", atomic.AddUint64(&m.eventCounter, 1))
	if _, ok := m.eventSubs[targetID]; !ok {
		m.eventSubs[targetID] = make(map[string]chan targetEvent)
	}
	m.eventSubs[targetID][subID] = ch
	cancel := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		subs, ok := m.eventSubs[targetID]
		if !ok {
			return
		}
		if subCh, exists := subs[subID]; exists {
			delete(subs, subID)
			close(subCh)
		}
		if len(subs) == 0 {
			delete(m.eventSubs, targetID)
		}
	}
	return subID, ch, cancel
}

func (m *Manager) publishTargetEvent(event targetEvent) {
	m.mu.RLock()
	subs := m.eventSubs[event.TargetID]
	local := make([]chan targetEvent, 0, len(subs))
	for _, ch := range subs {
		local = append(local, ch)
	}
	m.mu.RUnlock()

	for _, ch := range local {
		select {
		case ch <- event:
		default:
		}
	}
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
			m.stopQueueLocked(targetID, ErrRelayQueueCanceled)
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
