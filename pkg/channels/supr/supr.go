package supr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/itsivag/suprclaw/pkg/bus"
	"github.com/itsivag/suprclaw/pkg/channels"
	"github.com/itsivag/suprclaw/pkg/config"
	"github.com/itsivag/suprclaw/pkg/identity"
	"github.com/itsivag/suprclaw/pkg/logger"
	"github.com/itsivag/suprclaw/pkg/media"
	"github.com/itsivag/suprclaw/pkg/utils"
)

// agentSummary is a compact agent descriptor sent to clients on connect.
type agentSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type sessionRouteMetadata struct {
	resolvedAgentID string
	routeMatchedBy  string
}

const (
	eventSchemaVersion = "1.0"

	runStatusInProgress = "in_progress"
	runStatusCompleted  = "completed"
	runStatusFailed     = "failed"
	runStatusUnknown    = "unknown"
)

const reasoningUsage = "off|low|medium|high|xhigh|adaptive"

type sessionRunState struct {
	latestRunID    string
	syntheticRunID string
	runs           map[string]string
	sequences      map[string]int
}

// suprConn represents a single WebSocket connection.
type suprConn struct {
	id        string
	conn      *websocket.Conn
	sessionID string
	writeMu   sync.Mutex
	closed    atomic.Bool
}

// writeJSON sends a JSON message to the connection with write locking.
func (pc *suprConn) writeJSON(v any) error {
	if pc.closed.Load() {
		return fmt.Errorf("connection closed")
	}
	pc.writeMu.Lock()
	defer pc.writeMu.Unlock()
	return pc.conn.WriteJSON(v)
}

// close closes the connection.
func (pc *suprConn) close() {
	if pc.closed.CompareAndSwap(false, true) {
		pc.conn.Close()
	}
}

// SuprChannel implements the native Supr WebSocket WebSocket channel.
// It serves as the reference implementation for all optional capability interfaces.
type SuprChannel struct {
	*channels.BaseChannel
	config       config.SuprConfig
	upgrader     websocket.Upgrader
	connections  sync.Map // connID → *suprConn
	connCount    atomic.Int32
	ctx          context.Context
	cancel       context.CancelFunc
	agents       []agentSummary
	defaultAgent string
	routeMeta    sync.Map // sessionID -> sessionRouteMetadata
	runStateMu   sync.RWMutex
	runState     map[string]sessionRunState // sessionID -> run state
}

// NewSuprChannel creates a new Supr WebSocket channel.
func NewSuprChannel(cfg config.SuprConfig, messageBus *bus.MessageBus, agents []agentSummary, defaultAgent string) (*SuprChannel, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("supr token is required")
	}

	base := channels.NewBaseChannel("supr", cfg, messageBus, cfg.AllowFrom)

	allowOrigins := cfg.AllowOrigins
	checkOrigin := func(r *http.Request) bool {
		if len(allowOrigins) == 0 {
			return true // allow all if not configured
		}
		origin := r.Header.Get("Origin")
		for _, allowed := range allowOrigins {
			if allowed == "*" || allowed == origin {
				return true
			}
		}
		return false
	}

	return &SuprChannel{
		BaseChannel:  base,
		config:       cfg,
		agents:       agents,
		defaultAgent: defaultAgent,
		runState:     make(map[string]sessionRunState),
		upgrader: websocket.Upgrader{
			CheckOrigin:     checkOrigin,
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}, nil
}

// Start implements Channel.
func (c *SuprChannel) Start(ctx context.Context) error {
	logger.InfoC("supr", "Starting Supr WebSocket channel")
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.SetRunning(true)
	logger.InfoC("supr", "Supr WebSocket channel started")
	return nil
}

// Stop implements Channel.
func (c *SuprChannel) Stop(ctx context.Context) error {
	logger.InfoC("supr", "Stopping Supr WebSocket channel")
	c.SetRunning(false)

	// Close all connections
	c.connections.Range(func(key, value any) bool {
		if pc, ok := value.(*suprConn); ok {
			pc.close()
		}
		c.connections.Delete(key)
		return true
	})

	if c.cancel != nil {
		c.cancel()
	}
	c.runStateMu.Lock()
	c.runState = make(map[string]sessionRunState)
	c.runStateMu.Unlock()

	logger.InfoC("supr", "Supr WebSocket channel stopped")
	return nil
}

// WebhookPath implements channels.WebhookHandler.
func (c *SuprChannel) WebhookPath() string { return "/supr/" }

// ServeHTTP implements http.Handler for the shared HTTP server.
func (c *SuprChannel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/supr")

	switch {
	case path == "/ws" || path == "/ws/":
		c.handleWebSocket(w, r)
	default:
		http.NotFound(w, r)
	}
}

// Send implements Channel — sends a message to the appropriate WebSocket connection.
func (c *SuprChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}
	c.rememberRouteMetadata(msg.ChatID, msg.ResolvedAgentID, msg.RouteMatchedBy)
	sessionID := strings.TrimPrefix(msg.ChatID, "supr:")
	if sessionID == "" {
		return fmt.Errorf("invalid supr chat id: %q", msg.ChatID)
	}
	runID := c.resolveOrCreateRunID(sessionID)

	// Emit canonical error envelope instead of legacy typed error frame.
	if msg.ErrorCode != "" {
		errData := map[string]any{
			"scope":     "run",
			"code":      msg.ErrorCode,
			"message":   msg.ErrorMessage,
			"retryable": false,
		}
		if msg.ResolvedAgentID != "" {
			errData["resolved_agent_id"] = msg.ResolvedAgentID
		}
		if msg.RouteMatchedBy != "" {
			errData["route_matched_by"] = msg.RouteMatchedBy
		}
		return c.broadcastRawToSession(msg.ChatID, c.newDerivedEventEnvelope(sessionID, runID, "error.raised", errData))
	}

	if strings.TrimSpace(msg.Content) == "" {
		return nil
	}

	payload := map[string]any{
		"message_id": "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		"text":       msg.Content,
		"format":     "markdown",
	}
	if msg.ModelUsed != "" {
		payload["model_used"] = msg.ModelUsed
	}
	if msg.ResolvedAgentID != "" {
		payload["resolved_agent_id"] = msg.ResolvedAgentID
	}
	if msg.RouteMatchedBy != "" {
		payload["route_matched_by"] = msg.RouteMatchedBy
	}
	return c.broadcastRawToSession(msg.ChatID, c.newDerivedEventEnvelope(sessionID, runID, "message.completed", payload))
}

// EditMessage implements channels.MessageEditor.
func (c *SuprChannel) EditMessage(ctx context.Context, chatID string, messageID string, content string) error {
	return nil
}

// StartTyping implements channels.TypingCapable.
func (c *SuprChannel) StartTyping(ctx context.Context, chatID string) (func(), error) {
	return func() {}, nil
}

// SendPlaceholder implements channels.PlaceholderCapable.
// It sends a placeholder message via the Supr WebSocket that will later be
// edited to the actual response via EditMessage (channels.MessageEditor).
func (c *SuprChannel) SendPlaceholder(ctx context.Context, chatID string) (string, error) {
	return "", nil
}

// BroadcastStatus implements channels.StatusBroadcaster.
// It sends a typing.status WebSocket event to all connected clients for the session,
// giving the browser instant feedback about the current agent operation.
func (c *SuprChannel) BroadcastStatus(ctx context.Context, chatID, text string) error {
	return nil
}

// BroadcastActivityEvent implements channels.ActivityEventBroadcaster.
// It sends the canonical event envelope directly (without legacy type/payload wrapper).
func (c *SuprChannel) BroadcastActivityEvent(ctx context.Context, chatID string, event bus.ActivityEventEnvelope) error {
	if event.SessionID == "" {
		event.SessionID = strings.TrimPrefix(chatID, "supr:")
	}
	c.rememberRunStatus(chatID, event)
	return c.broadcastRawToSession(chatID, event)
}

func (c *SuprChannel) rememberRunStatus(chatID string, event bus.ActivityEventEnvelope) {
	status, ok := runStatusForEvent(event.EventType)
	if !ok {
		return
	}
	runID := strings.TrimSpace(event.RunID)
	if runID == "" {
		return
	}
	sessionID := strings.TrimSpace(event.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimPrefix(chatID, "supr:")
	}
	if sessionID == "" {
		return
	}

	c.runStateMu.Lock()
	defer c.runStateMu.Unlock()

	state := c.runState[sessionID]
	if state.runs == nil {
		state.runs = make(map[string]string)
	}
	if state.sequences == nil {
		state.sequences = make(map[string]int)
	}
	state.runs[runID] = status
	if event.Sequence > state.sequences[runID] {
		state.sequences[runID] = event.Sequence
	}
	state.latestRunID = runID
	c.runState[sessionID] = state
}

func runStatusForEvent(eventType string) (string, bool) {
	switch eventType {
	case "run.started":
		return runStatusInProgress, true
	case "run.completed":
		return runStatusCompleted, true
	case "run.failed":
		return runStatusFailed, true
	default:
		return "", false
	}
}

func (c *SuprChannel) resolveOrCreateRunID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}

	c.runStateMu.Lock()
	defer c.runStateMu.Unlock()

	state := c.runState[sessionID]
	if latest := strings.TrimSpace(state.latestRunID); latest != "" {
		c.runState[sessionID] = state
		return latest
	}
	if strings.TrimSpace(state.syntheticRunID) == "" {
		state.syntheticRunID = "run_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if state.runs == nil {
		state.runs = make(map[string]string)
	}
	if state.sequences == nil {
		state.sequences = make(map[string]int)
	}
	state.latestRunID = state.syntheticRunID
	if _, ok := state.runs[state.syntheticRunID]; !ok {
		state.runs[state.syntheticRunID] = runStatusInProgress
	}
	c.runState[sessionID] = state
	return state.syntheticRunID
}

func (c *SuprChannel) nextDerivedEventSequence(sessionID, runID string) int {
	sessionID = strings.TrimSpace(sessionID)
	runID = strings.TrimSpace(runID)
	if sessionID == "" || runID == "" {
		return 1
	}

	c.runStateMu.Lock()
	defer c.runStateMu.Unlock()

	state := c.runState[sessionID]
	if state.sequences == nil {
		state.sequences = make(map[string]int)
	}
	seq := state.sequences[runID] + 1
	if seq < 1 {
		seq = 1
	}
	state.sequences[runID] = seq
	c.runState[sessionID] = state
	return seq
}

func (c *SuprChannel) newDerivedEventEnvelope(sessionID, runID, eventType string, data map[string]any) bus.ActivityEventEnvelope {
	seq := c.nextDerivedEventSequence(sessionID, runID)
	return bus.ActivityEventEnvelope{
		V:              eventSchemaVersion,
		EventID:        "evt_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		EventType:      eventType,
		Timestamp:      time.Now().UTC().Format(time.RFC3339Nano),
		Sequence:       seq,
		SessionID:      sessionID,
		RunID:          runID,
		ParentRunID:    nil,
		IdempotencyKey: fmt.Sprintf("%s_%d", runID, seq),
		Replay:         false,
		Data:           data,
	}
}

func (c *SuprChannel) resolveRunStatus(sessionID, requestedRunID string) (string, string) {
	sessionID = strings.TrimSpace(sessionID)
	requestedRunID = strings.TrimSpace(requestedRunID)
	if sessionID == "" {
		return requestedRunID, runStatusUnknown
	}

	c.runStateMu.RLock()
	defer c.runStateMu.RUnlock()

	state, hasState := c.runState[sessionID]
	if requestedRunID != "" {
		if hasState {
			if status, ok := state.runs[requestedRunID]; ok {
				return requestedRunID, status
			}
		}
		return requestedRunID, runStatusUnknown
	}

	if !hasState || len(state.runs) == 0 {
		return "", runStatusUnknown
	}

	if latest := strings.TrimSpace(state.latestRunID); latest != "" {
		if status, ok := state.runs[latest]; ok {
			return latest, status
		}
	}

	for runID, status := range state.runs {
		return runID, status
	}

	return "", runStatusUnknown
}

func (c *SuprChannel) rememberRouteMetadata(chatID, resolvedAgentID, routeMatchedBy string) {
	sessionID := strings.TrimPrefix(chatID, "supr:")
	if sessionID == "" {
		return
	}
	if strings.TrimSpace(resolvedAgentID) == "" && strings.TrimSpace(routeMatchedBy) == "" {
		return
	}

	currentAny, _ := c.routeMeta.Load(sessionID)
	current, _ := currentAny.(sessionRouteMetadata)
	next := sessionRouteMetadata{
		resolvedAgentID: strings.TrimSpace(resolvedAgentID),
		routeMatchedBy:  strings.TrimSpace(routeMatchedBy),
	}
	if next.resolvedAgentID == "" {
		next.resolvedAgentID = current.resolvedAgentID
	}
	if next.routeMatchedBy == "" {
		next.routeMatchedBy = current.routeMatchedBy
	}
	c.routeMeta.Store(sessionID, next)
}

func (c *SuprChannel) routeMetadataPayload(chatID string) map[string]any {
	sessionID := strings.TrimPrefix(chatID, "supr:")
	if sessionID == "" {
		return nil
	}
	value, ok := c.routeMeta.Load(sessionID)
	if !ok {
		return nil
	}
	meta, ok := value.(sessionRouteMetadata)
	if !ok {
		return nil
	}

	payload := map[string]any{}
	if meta.resolvedAgentID != "" {
		payload["resolved_agent_id"] = meta.resolvedAgentID
	}
	if meta.routeMatchedBy != "" {
		payload["route_matched_by"] = meta.routeMatchedBy
	}
	if len(payload) == 0 {
		return nil
	}
	return payload
}

// broadcastToSession sends a message to all connections with a matching session.
func (c *SuprChannel) broadcastToSession(chatID string, msg SuprMessage) error {
	// chatID format: "supr:<sessionID>"
	sessionID := strings.TrimPrefix(chatID, "supr:")
	msg.SessionID = sessionID
	return c.broadcastRawToSession(chatID, msg)
}

// broadcastRawToSession sends a JSON payload as-is to all session connections.
func (c *SuprChannel) broadcastRawToSession(chatID string, payload any) error {
	// chatID format: "supr:<sessionID>"
	sessionID := strings.TrimPrefix(chatID, "supr:")
	var sent bool
	c.connections.Range(func(key, value any) bool {
		pc, ok := value.(*suprConn)
		if !ok {
			return true
		}
		if pc.sessionID == sessionID {
			if err := pc.writeJSON(payload); err != nil {
				logger.DebugCF("supr", "Write to connection failed", map[string]any{
					"conn_id": pc.id,
					"error":   err.Error(),
				})
			} else {
				sent = true
			}
		}
		return true
	})

	if !sent {
		return fmt.Errorf("no active connections for session %s: %w", sessionID, channels.ErrSendFailed)
	}
	return nil
}

func (c *SuprChannel) closeReplacedSessionConnection(pc *suprConn) {
	if _, loaded := c.connections.LoadAndDelete(pc.id); loaded {
		c.connCount.Add(-1)
	}
	pc.close()
	logger.InfoCF("supr", "WebSocket client replaced", map[string]any{
		"conn_id":    pc.id,
		"session_id": pc.sessionID,
	})
}

func (c *SuprChannel) closeSessionDuplicates(sessionID string) {
	c.connections.Range(func(key, value any) bool {
		pc, ok := value.(*suprConn)
		if !ok {
			return true
		}
		if pc.sessionID == sessionID {
			c.closeReplacedSessionConnection(pc)
		}
		return true
	})
}

// handleWebSocket upgrades the HTTP connection and manages the WebSocket lifecycle.
func (c *SuprChannel) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !c.IsRunning() {
		http.Error(w, "channel not running", http.StatusServiceUnavailable)
		return
	}

	// Authenticate
	if !c.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Check connection limit
	maxConns := c.config.MaxConnections
	if maxConns <= 0 {
		maxConns = 100
	}
	if int(c.connCount.Load()) >= maxConns {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}

	// Echo the matched subprotocol back so the browser accepts the upgrade.
	var responseHeader http.Header
	if proto := c.matchedSubprotocol(r); proto != "" {
		responseHeader = http.Header{"Sec-WebSocket-Protocol": {proto}}
	}

	conn, err := c.upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		logger.ErrorCF("supr", "WebSocket upgrade failed", map[string]any{
			"error": err.Error(),
		})
		return
	}

	// Determine session ID from query param or generate one
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	pc := &suprConn{
		id:        uuid.New().String(),
		conn:      conn,
		sessionID: sessionID,
	}

	// Enforce one active websocket connection per Supr session_id.
	c.closeSessionDuplicates(sessionID)
	c.connections.Store(pc.id, pc)
	c.connCount.Add(1)

	logger.InfoCF("supr", "WebSocket client connected", map[string]any{
		"conn_id":    pc.id,
		"session_id": sessionID,
	})

	// Send agent list to every client immediately after connect.
	listMsg := newMessage(TypeAgentList, map[string]any{
		"agents":  c.agents,
		"default": c.defaultAgent,
	})
	pc.writeJSON(listMsg)

	go c.readLoop(pc)
}

// authenticate checks the request for a valid token:
//  1. Authorization: Bearer <token> header
//  2. Sec-WebSocket-Protocol "token.<value>" (for browsers that can't set headers)
//  3. Query parameter "token" (only when AllowTokenQuery is on)
func (c *SuprChannel) authenticate(r *http.Request) bool {
	token := c.config.Token
	if token == "" {
		return false
	}

	// Check Authorization header
	auth := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
		if after == token {
			return true
		}
	}

	// Check Sec-WebSocket-Protocol subprotocol ("token.<value>")
	if c.matchedSubprotocol(r) != "" {
		return true
	}

	// Check query parameter only when explicitly allowed
	if c.config.AllowTokenQuery {
		if r.URL.Query().Get("token") == token {
			return true
		}
	}

	return false
}

// matchedSubprotocol returns the "token.<value>" subprotocol that matches
// the configured token, or "" if none do.
func (c *SuprChannel) matchedSubprotocol(r *http.Request) string {
	token := c.config.Token
	for _, proto := range websocket.Subprotocols(r) {
		if after, ok := strings.CutPrefix(proto, "token."); ok && after == token {
			return proto
		}
	}
	return ""
}

// readLoop reads messages from a WebSocket connection.
func (c *SuprChannel) readLoop(pc *suprConn) {
	defer func() {
		pc.close()
		if _, loaded := c.connections.LoadAndDelete(pc.id); loaded {
			c.connCount.Add(-1)
		}
		logger.InfoCF("supr", "WebSocket client disconnected", map[string]any{
			"conn_id":    pc.id,
			"session_id": pc.sessionID,
		})
	}()

	readTimeout := time.Duration(c.config.ReadTimeout) * time.Second
	if readTimeout <= 0 {
		readTimeout = 60 * time.Second
	}

	_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))
	pc.conn.SetPongHandler(func(appData string) error {
		_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))
		return nil
	})

	// Start ping ticker
	pingInterval := time.Duration(c.config.PingInterval) * time.Second
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	go c.pingLoop(pc, pingInterval)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		_, rawMsg, err := pc.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				logger.DebugCF("supr", "WebSocket read error", map[string]any{
					"conn_id": pc.id,
					"error":   err.Error(),
				})
			}
			return
		}

		_ = pc.conn.SetReadDeadline(time.Now().Add(readTimeout))

		var msg SuprMessage
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			errMsg := newError("invalid_message", "failed to parse message")
			pc.writeJSON(errMsg)
			continue
		}

		c.handleMessage(pc, msg)
	}
}

// pingLoop sends periodic ping frames to keep the connection alive.
func (c *SuprChannel) pingLoop(pc *suprConn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if pc.closed.Load() {
				return
			}
			pc.writeMu.Lock()
			err := pc.conn.WriteMessage(websocket.PingMessage, nil)
			pc.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// handleMessage processes an inbound Supr WebSocket message.
func (c *SuprChannel) handleMessage(pc *suprConn, msg SuprMessage) {
	switch msg.Type {
	case TypePing:
		pong := newMessage(TypePong, nil)
		pong.ID = msg.ID
		pc.writeJSON(pong)

	case TypeMessageSend:
		c.handleMessageSend(pc, msg)

	case TypeMediaSend:
		c.handleMediaSend(pc, msg)

	default:
		errMsg := newError("unknown_type", fmt.Sprintf("unknown message type: %s", msg.Type))
		pc.writeJSON(errMsg)
	}
}

// handleMessageSend processes an inbound message.send from a client.
func (c *SuprChannel) handleMessageSend(pc *suprConn, msg SuprMessage) {
	content, _ := msg.Payload["content"].(string)
	if strings.TrimSpace(content) == "" {
		errMsg := newError("empty_content", "message content is empty")
		pc.writeJSON(errMsg)
		return
	}
	reasoningOverride, err := parseReasoningOverride(msg.Payload)
	if err != nil {
		pc.writeJSON(newError("invalid_reasoning", err.Error()))
		return
	}

	sessionID := msg.SessionID
	if sessionID == "" {
		sessionID = pc.sessionID
	}

	chatID := "supr:" + sessionID
	senderID := "supr-user"

	peer := bus.Peer{Kind: "direct", ID: "supr:" + sessionID}

	metadata := map[string]string{
		"platform":   "supr",
		"session_id": sessionID,
		"conn_id":    pc.id,
	}

	if agentID, _ := msg.Payload["agent_id"].(string); agentID != "" {
		metadata["requested_agent_id"] = agentID
	}

	if model, _ := msg.Payload["model"].(string); model != "" {
		metadata["model_override"] = model
	}
	if reasoningOverride != "" {
		metadata["reasoning_override"] = reasoningOverride
	}

	logger.DebugCF("supr", "Received message", map[string]any{
		"session_id": sessionID,
		"preview":    truncate(content, 50),
	})

	sender := bus.SenderInfo{
		Platform:    "supr",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("supr", senderID),
	}

	if !c.IsAllowedSender(sender) {
		return
	}

	c.HandleMessage(c.ctx, peer, msg.ID, senderID, chatID, content, nil, metadata, sender)
}

// maxMediaBytes is the maximum decoded media size (25 MB).
const maxMediaBytes = 25 * 1024 * 1024

// mediaItem is a single attachment parsed from a media.send payload.
type mediaItem struct {
	data        string // base64-encoded bytes (mutually exclusive with url)
	url         string // remote URL to download (mutually exclusive with data)
	filename    string
	contentType string
	caption     string
}

// resolveMediaItem writes the item's content to a temp file and returns its local path.
// The caller is responsible for removing the file on error.
func resolveMediaItem(item mediaItem) (string, error) {
	if item.filename == "" {
		item.filename = "upload"
	}

	if item.url != "" {
		localPath := utils.DownloadFile(item.url, item.filename, utils.DownloadOptions{
			LoggerPrefix: "supr",
		})
		if localPath == "" {
			return "", fmt.Errorf("failed to download media from url")
		}
		return localPath, nil
	}

	// Guard against excessively large payloads before decoding.
	if len(item.data) > (maxMediaBytes/3)*4+4 {
		return "", fmt.Errorf("media payload exceeds maximum allowed size")
	}

	rawBytes, err := base64.StdEncoding.DecodeString(item.data)
	if err != nil {
		rawBytes, err = base64.URLEncoding.DecodeString(item.data)
		if err != nil {
			return "", fmt.Errorf("failed to decode base64 data")
		}
	}

	if len(rawBytes) > maxMediaBytes {
		return "", fmt.Errorf("media payload exceeds maximum allowed size")
	}

	mediaDir := media.TempDir()
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create media directory")
	}
	safeName := utils.SanitizeFilename(item.filename)
	localPath := filepath.Join(mediaDir, uuid.New().String()+"_"+safeName)

	if err := os.WriteFile(localPath, rawBytes, 0o600); err != nil {
		return "", fmt.Errorf("failed to write media file")
	}
	return localPath, nil
}

// mediaAnnotation returns a content annotation for a MIME type and filename.
func mediaAnnotation(contentType, filename string) string {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "[image: " + filename + "]"
	case strings.HasPrefix(contentType, "audio/"):
		return "[audio: " + filename + "]"
	case strings.HasPrefix(contentType, "video/"):
		return "[video: " + filename + "]"
	default:
		return "[file: " + filename + "]"
	}
}

// parseMediaItems extracts one or more mediaItems from a SuprMessage payload.
// Supports both an "attachments" array and scalar "data"/"url" fields.
func parseMediaItems(payload map[string]any) ([]mediaItem, error) {
	if rawList, ok := payload["attachments"]; ok {
		list, ok := rawList.([]any)
		if !ok {
			return nil, fmt.Errorf("attachments must be an array")
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("attachments array is empty")
		}
		items := make([]mediaItem, 0, len(list))
		for i, entry := range list {
			m, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("attachment %d is not an object", i)
			}
			item := mediaItem{
				data:        stringField(m, "data"),
				url:         stringField(m, "url"),
				filename:    stringField(m, "filename"),
				contentType: stringField(m, "content_type"),
				caption:     stringField(m, "caption"),
			}
			if item.data == "" && item.url == "" {
				return nil, fmt.Errorf("attachment %d: either data or url is required", i)
			}
			items = append(items, item)
		}
		return items, nil
	}

	// Scalar fallback.
	item := mediaItem{
		data:        stringField(payload, "data"),
		url:         stringField(payload, "url"),
		filename:    stringField(payload, "filename"),
		contentType: stringField(payload, "content_type"),
		caption:     stringField(payload, "caption"),
	}
	if item.data == "" && item.url == "" {
		return nil, fmt.Errorf("either data or url field is required")
	}
	return []mediaItem{item}, nil
}

// stringField safely extracts a string value from a map.
func stringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// handleMediaSend processes an inbound media.send frame from a client.
func (c *SuprChannel) handleMediaSend(pc *suprConn, msg SuprMessage) {
	store := c.GetMediaStore()
	if store == nil {
		pc.writeJSON(newError("media_store_unavailable", "media store is not configured"))
		return
	}
	reasoningOverride, err := parseReasoningOverride(msg.Payload)
	if err != nil {
		pc.writeJSON(newError("invalid_reasoning", err.Error()))
		return
	}

	items, err := parseMediaItems(msg.Payload)
	if err != nil {
		pc.writeJSON(newError("invalid_media_data", err.Error()))
		return
	}

	sessionID := msg.SessionID
	if sessionID == "" {
		sessionID = pc.sessionID
	}
	chatID := "supr:" + sessionID
	messageID := msg.ID
	if messageID == "" {
		messageID = uuid.New().String()
	}
	scope := channels.BuildMediaScope("supr", chatID, messageID)

	var refs []string
	var annotations []string

	for _, item := range items {
		if item.filename == "" {
			item.filename = "upload"
		}

		localPath, err := resolveMediaItem(item)
		if err != nil {
			// Clean up any files written so far.
			for _, ref := range refs {
				if path, resolveErr := store.Resolve(ref); resolveErr == nil {
					os.Remove(path)
				}
			}
			pc.writeJSON(newError("media_write_failed", err.Error()))
			return
		}

		ref, err := store.Store(localPath, media.MediaMeta{
			Filename:    item.filename,
			ContentType: item.contentType,
			Source:      "supr",
		}, scope)
		if err != nil {
			os.Remove(localPath)
			for _, r := range refs {
				if path, resolveErr := store.Resolve(r); resolveErr == nil {
					os.Remove(path)
				}
			}
			pc.writeJSON(newError("media_write_failed", "failed to register media in store"))
			return
		}

		refs = append(refs, ref)

		ann := mediaAnnotation(item.contentType, item.filename)
		if item.caption != "" {
			ann = item.caption + "\n" + ann
		}
		annotations = append(annotations, ann)
	}

	// Top-level caption (scalar mode or batch-level caption).
	topCaption, _ := msg.Payload["caption"].(string)
	content := strings.Join(annotations, "\n")
	if topCaption != "" && len(items) > 1 {
		content = topCaption + "\n" + content
	}

	senderID := "supr-user"
	peer := bus.Peer{Kind: "direct", ID: "supr:" + sessionID}
	metadata := map[string]string{
		"platform":   "supr",
		"session_id": sessionID,
		"conn_id":    pc.id,
	}

	if agentID, _ := msg.Payload["agent_id"].(string); agentID != "" {
		metadata["requested_agent_id"] = agentID
	}

	if model, _ := msg.Payload["model"].(string); model != "" {
		metadata["model_override"] = model
	}
	if reasoningOverride != "" {
		metadata["reasoning_override"] = reasoningOverride
	}

	sender := bus.SenderInfo{
		Platform:    "supr",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("supr", senderID),
	}

	if !c.IsAllowedSender(sender) {
		return
	}

	logger.DebugCF("supr", "Received media", map[string]any{
		"session_id": sessionID,
		"count":      len(items),
	})

	c.HandleMessage(c.ctx, peer, messageID, senderID, chatID, content, refs, metadata, sender)
}

func parseReasoningOverride(payload map[string]any) (string, error) {
	if payload == nil {
		return "", nil
	}
	raw, exists := payload["reasoning"]
	if !exists {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("reasoning must be a string (%s)", reasoningUsage)
	}
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "off", "low", "medium", "high", "xhigh", "adaptive":
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid reasoning %q. Allowed: %s", value, reasoningUsage)
	}
}

// SendMedia implements channels.MediaSender — sends media.create frames to clients.
func (c *SuprChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}

	store := c.GetMediaStore()
	if store == nil {
		return fmt.Errorf("supr: media store is not configured")
	}

	var firstErr error
	for _, part := range msg.Parts {
		localPath, err := store.Resolve(part.Ref)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("supr: resolve %s: %w", part.Ref, err)
			}
			continue
		}

		data, err := os.ReadFile(localPath)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("supr: read %s: %w", localPath, err)
			}
			continue
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		outMsg := newMessage(TypeMediaCreate, map[string]any{
			"type":         part.Type,
			"data":         encoded,
			"filename":     part.Filename,
			"content_type": part.ContentType,
			"caption":      part.Caption,
		})

		if err := c.broadcastToSession(msg.ChatID, outMsg); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// truncate truncates a string to maxLen runes.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
