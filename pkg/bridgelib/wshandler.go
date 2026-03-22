package bridgelib

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// WSHandler handles the ai-agent-bridge WebSocket JSON protocol on behalf of
// an existing HTTP/WebSocket server. It is safe for concurrent use.
//
// The protocol is the same JSON format understood by the useBridgeSession
// React hook in packages/bridge-client-node/react/useBridgeSession.ts.
type WSHandler struct {
	bridge *Bridge

	mu sync.Mutex
	// streams tracks active stream goroutines per connection:
	//   connID → subscriberID → cancelFunc
	streams map[string]map[string]context.CancelFunc
}

// NewWSHandler creates a WSHandler backed by the given Bridge.
func NewWSHandler(b *Bridge) *WSHandler {
	return &WSHandler{
		bridge:  b,
		streams: make(map[string]map[string]context.CancelFunc),
	}
}

// HandleMessage processes one client message.
//   - connID uniquely identifies the WebSocket connection (used for stream tracking).
//   - ctx should be cancelled when the connection closes.
//   - sendFn delivers response bytes back to that specific client.
//
// Messages with an unrecognised or missing "type" field are silently ignored,
// so file-event broadcasts from the myrndr server pass through harmlessly.
func (h *WSHandler) HandleMessage(ctx context.Context, connID string, msgBytes []byte, sendFn func([]byte)) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(msgBytes, &probe); err != nil || probe.Type == "" {
		return
	}

	switch probe.Type {
	case "start_session":
		h.handleStartSession(ctx, connID, msgBytes, sendFn)
	case "send_input":
		h.handleSendInput(msgBytes, sendFn)
	case "stop_session":
		h.handleStopSession(msgBytes, sendFn)
	case "stream_events":
		h.handleStreamEvents(ctx, connID, msgBytes, sendFn)
	case "list_sessions":
		h.handleListSessions(msgBytes, sendFn)
	case "get_session":
		h.handleGetSession(msgBytes, sendFn)
	case "health":
		h.handleHealth(ctx, sendFn)
	case "list_providers":
		h.handleListProviders(sendFn)
	}
}

// ConnClosed cancels all active streams for connID and cleans up resources.
// Call this when a WebSocket connection closes.
func (h *WSHandler) ConnClosed(connID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cancels, ok := h.streams[connID]; ok {
		for _, cancel := range cancels {
			cancel()
		}
		delete(h.streams, connID)
	}
}

// --- per-message handler structs ---

type startSessionMsg struct {
	ProjectID string            `json:"projectId"`
	SessionID string            `json:"sessionId"`
	RepoPath  string            `json:"repoPath"`
	Provider  string            `json:"provider"`
	AgentOpts map[string]string `json:"agentOpts"`
}

type sendInputMsg struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}

type stopSessionMsg struct {
	SessionID string `json:"sessionId"`
	Force     bool   `json:"force"`
}

type streamEventsMsg struct {
	SessionID    string `json:"sessionId"`
	AfterSeq     uint64 `json:"afterSeq"`
	SubscriberID string `json:"subscriberId"`
}

type listSessionsMsg struct {
	ProjectID string `json:"projectId"`
}

type getSessionMsg struct {
	SessionID string `json:"sessionId"`
}

// --- handlers ---

func (h *WSHandler) handleStartSession(ctx context.Context, connID string, msg []byte, sendFn func([]byte)) {
	var req startSessionMsg
	if err := json.Unmarshal(msg, &req); err != nil {
		sendError(sendFn, "invalid_request", err.Error())
		return
	}
	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("%s-%d", connID, time.Now().UnixNano())
	}
	if req.Provider == "" {
		req.Provider = "claude"
	}

	info, err := h.bridge.StartSession(ctx, req.ProjectID, req.SessionID, req.RepoPath, req.Provider, req.AgentOpts)
	if err != nil {
		sendError(sendFn, "start_failed", err.Error())
		return
	}
	sendJSON(sendFn, map[string]any{
		"type":      "session_started",
		"sessionId": info.SessionID,
		"status":    stateToString(info.State),
		"provider":  info.Provider,
		"createdAt": info.CreatedAt.Format(time.RFC3339),
	})
}

func (h *WSHandler) handleSendInput(msg []byte, sendFn func([]byte)) {
	var req sendInputMsg
	if err := json.Unmarshal(msg, &req); err != nil {
		sendError(sendFn, "invalid_request", err.Error())
		return
	}
	seq, err := h.bridge.Send(req.SessionID, req.Text)
	if err != nil {
		sendError(sendFn, "send_failed", err.Error())
		return
	}
	sendJSON(sendFn, map[string]any{
		"type":      "input_accepted",
		"sessionId": req.SessionID,
		"accepted":  true,
		"seq":       seq,
	})
}

func (h *WSHandler) handleStopSession(msg []byte, sendFn func([]byte)) {
	var req stopSessionMsg
	if err := json.Unmarshal(msg, &req); err != nil {
		sendError(sendFn, "invalid_request", err.Error())
		return
	}
	if err := h.bridge.Stop(req.SessionID, req.Force); err != nil {
		sendError(sendFn, "stop_failed", err.Error())
		return
	}
	sendJSON(sendFn, map[string]any{
		"type":      "session_stopped",
		"sessionId": req.SessionID,
		"status":    "stopped",
	})
}

func (h *WSHandler) handleStreamEvents(ctx context.Context, connID string, msg []byte, sendFn func([]byte)) {
	var req streamEventsMsg
	if err := json.Unmarshal(msg, &req); err != nil {
		sendError(sendFn, "invalid_request", err.Error())
		return
	}
	if req.SubscriberID == "" {
		req.SubscriberID = fmt.Sprintf("%s-%s", connID, req.SessionID)
	}

	streamCtx, cancel := context.WithCancel(ctx)

	h.mu.Lock()
	if h.streams[connID] == nil {
		h.streams[connID] = make(map[string]context.CancelFunc)
	}
	// Replace any existing stream for this subscriber.
	if prev, ok := h.streams[connID][req.SubscriberID]; ok {
		prev()
	}
	h.streams[connID][req.SubscriberID] = cancel
	h.mu.Unlock()

	events, err := h.bridge.StreamEvents(streamCtx, req.SessionID, req.SubscriberID, req.AfterSeq)
	if err != nil {
		cancel()
		sendError(sendFn, "stream_failed", err.Error())
		return
	}

	go func() {
		defer cancel()
		for se := range events {
			sendJSON(sendFn, map[string]any{
				"type":      "event",
				"seq":       se.Seq,
				"sessionId": se.SessionID,
				"eventType": se.TypeName,
				"stream":    se.Stream,
				"text":      se.Text,
				"done":      se.Done,
				"error":     se.Error,
			})
		}
	}()
}

func (h *WSHandler) handleListSessions(msg []byte, sendFn func([]byte)) {
	var req listSessionsMsg
	if err := json.Unmarshal(msg, &req); err != nil {
		sendError(sendFn, "invalid_request", err.Error())
		return
	}
	sessions := h.bridge.List(req.ProjectID)
	list := make([]map[string]any, len(sessions))
	for i := range sessions {
		list[i] = sessionInfoToMap(&sessions[i])
	}
	sendJSON(sendFn, map[string]any{
		"type":     "sessions_list",
		"sessions": list,
	})
}

func (h *WSHandler) handleGetSession(msg []byte, sendFn func([]byte)) {
	var req getSessionMsg
	if err := json.Unmarshal(msg, &req); err != nil {
		sendError(sendFn, "invalid_request", err.Error())
		return
	}
	info, err := h.bridge.Get(req.SessionID)
	if err != nil {
		sendError(sendFn, "not_found", err.Error())
		return
	}
	sendJSON(sendFn, map[string]any{
		"type":    "session_info",
		"session": sessionInfoToMap(info),
	})
}

func (h *WSHandler) handleHealth(ctx context.Context, sendFn func([]byte)) {
	results := h.bridge.Health(ctx)
	providers := make(map[string]any, len(results))
	healthy := true
	for id, err := range results {
		if err != nil {
			healthy = false
			providers[id] = map[string]any{"healthy": false, "error": err.Error()}
		} else {
			providers[id] = map[string]any{"healthy": true}
		}
	}
	sendJSON(sendFn, map[string]any{
		"type":      "health_response",
		"healthy":   healthy,
		"providers": providers,
	})
}

func (h *WSHandler) handleListProviders(sendFn func([]byte)) {
	ids := h.bridge.ListProviders()
	providers := make([]map[string]any, len(ids))
	for i, id := range ids {
		providers[i] = map[string]any{"id": id}
	}
	sendJSON(sendFn, map[string]any{
		"type":      "providers_list",
		"providers": providers,
	})
}

// --- helpers ---

func sendJSON(sendFn func([]byte), v map[string]any) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("bridgelib wshandler: marshal response", "error", err)
		return
	}
	sendFn(b)
}

func sendError(sendFn func([]byte), code, message string) {
	sendJSON(sendFn, map[string]any{
		"type":    "error",
		"code":    code,
		"message": message,
	})
}

func stateToString(state int) string {
	switch state {
	case 1:
		return "starting"
	case 2:
		return "running"
	case 3:
		return "stopping"
	case 4:
		return "stopped"
	case 5:
		return "failed"
	default:
		return "unspecified"
	}
}

func sessionInfoToMap(s *SessionInfo) map[string]any {
	m := map[string]any{
		"sessionId": s.SessionID,
		"projectId": s.ProjectID,
		"provider":  s.Provider,
		"status":    stateToString(s.State),
		"createdAt": s.CreatedAt.Format(time.RFC3339),
	}
	if !s.StoppedAt.IsZero() {
		m["stoppedAt"] = s.StoppedAt.Format(time.RFC3339)
	}
	if s.Error != "" {
		m["error"] = s.Error
	}
	return m
}
