package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/logger"
)

// BridgeConn represents a connected Ollama bridge (e.g. a Mac app proxying to local Ollama).
type BridgeConn struct {
	ws       *websocket.Conn
	mu       sync.Mutex
	pending  map[string]chan []byte // request ID → response channel
	models   []OllamaModel
	modelsMu sync.RWMutex
}

// OllamaModel describes a model available on the bridge's Ollama instance.
type OllamaModel struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

// bridgeMessage is the wire format for bridge WebSocket messages.
type bridgeMessage struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Forward sends a CompletionRequest through the bridge and waits for the response.
func (bc *BridgeConn) Forward(req brain.CompletionRequest) ([]byte, error) {
	reqID := fmt.Sprintf("req-%d", time.Now().UnixNano())

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	ch := make(chan []byte, 1)
	bc.mu.Lock()
	bc.pending[reqID] = ch
	bc.mu.Unlock()

	defer func() {
		bc.mu.Lock()
		delete(bc.pending, reqID)
		bc.mu.Unlock()
	}()

	msg := bridgeMessage{
		Type:    "completion_request",
		ID:      reqID,
		Payload: payload,
	}
	data, _ := json.Marshal(msg)

	bc.mu.Lock()
	writeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	err = bc.ws.Write(writeCtx, websocket.MessageText, data)
	cancel()
	bc.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("bridge write: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(180 * time.Second):
		return nil, fmt.Errorf("bridge timeout (180s)")
	}
}

// Models returns the current list of models reported by the bridge.
func (bc *BridgeConn) Models() []OllamaModel {
	bc.modelsMu.RLock()
	defer bc.modelsMu.RUnlock()
	return bc.models
}

// handleBridge is the WebSocket endpoint for the Ollama bridge app.
// Auth via ?token= query param (like /ws) to avoid middleware wrapping the ResponseWriter.
// GET /api/workspaces/{slug}/bridge?token=...
func (s *Server) handleBridge(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	// Manual auth — WebSocket endpoints can't use authed middleware (breaks http.Hijacker)
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	claims, err := s.jwt.Validate(tokenStr)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	if claims.WorkspaceSlug != slug || claims.Role != "admin" {
		http.Error(w, "admin access required", http.StatusForbidden)
		return
	}

	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Msg("bridge websocket accept failed")
		return
	}
	wsConn.SetReadLimit(1 << 20) // 1MB

	bc := &BridgeConn{
		ws:      wsConn,
		pending: make(map[string]chan []byte),
	}

	s.bridges.Store(slug, bc)
	logger.WithCategory(logger.CatSystem).Info().Str("workspace", slug).Msg("bridge connected")

	// Broadcast bridge status
	s.broadcastBridgeStatus(slug, true, nil)

	defer func() {
		s.bridges.Delete(slug)
		wsConn.Close(websocket.StatusNormalClosure, "")
		logger.WithCategory(logger.CatSystem).Info().Str("workspace", slug).Msg("bridge disconnected")
		s.broadcastBridgeStatus(slug, false, nil)
	}()

	// Ping loop
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				msg, _ := json.Marshal(bridgeMessage{Type: "ping"})
				bc.mu.Lock()
				pingCtx, c := context.WithTimeout(ctx, 15*time.Second)
				bc.ws.Write(pingCtx, websocket.MessageText, msg)
				c()
				bc.mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Read loop
	for {
		_, data, err := wsConn.Read(ctx)
		if err != nil {
			return
		}

		var msg bridgeMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}

		switch msg.Type {
		case "completion_response":
			bc.mu.Lock()
			if ch, ok := bc.pending[msg.ID]; ok {
				ch <- []byte(msg.Payload)
			}
			bc.mu.Unlock()

		case "models":
			var models []OllamaModel
			if json.Unmarshal(msg.Payload, &models) == nil {
				bc.modelsMu.Lock()
				bc.models = models
				bc.modelsMu.Unlock()
				s.broadcastBridgeStatus(slug, true, models)
			}

		case "pong":
			// keepalive ack — nothing to do
		}
	}
}

// broadcastBridgeStatus sends bridge connection status to all workspace clients.
func (s *Server) broadcastBridgeStatus(slug string, connected bool, models []OllamaModel) {
	if models == nil {
		models = []OllamaModel{}
	}
	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeBridgeStatus, map[string]any{
		"connected": connected,
		"models":    models,
	}), "")
}

// handleBridgeStatus returns the current bridge status for a workspace.
// GET /api/workspaces/{slug}/bridge/status
func (s *Server) handleBridgeStatus(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	result := map[string]any{
		"connected": false,
		"models":    []OllamaModel{},
	}
	if v, ok := s.bridges.Load(slug); ok {
		bc := v.(*BridgeConn)
		result["connected"] = true
		result["models"] = bc.Models()
	}
	writeJSON(w, http.StatusOK, result)
}

// handleOllamaModels queries a direct (localhost) Ollama instance for available models.
// GET /api/workspaces/{slug}/ollama/models
func (s *Server) handleOllamaModels(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	ollamaURL := s.getBrainSetting(slug, "ollama_url", "http://localhost:11434")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ollamaURL+"/api/tags", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"models": []OllamaModel{}, "error": "Ollama not reachable"})
		return
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name       string `json:"name"`
			Size       int64  `json:"size"`
			ModifiedAt string `json:"modified_at"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) != nil {
		writeJSON(w, http.StatusOK, map[string]any{"models": []OllamaModel{}})
		return
	}

	models := make([]OllamaModel, len(result.Models))
	for i, m := range result.Models {
		models[i] = OllamaModel{Name: m.Name, Size: m.Size, ModifiedAt: m.ModifiedAt}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}
