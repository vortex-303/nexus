package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// handleIncomingWebhook is the public endpoint that receives webhook payloads.
// POST /w/{slug}/hook/{token}
func (s *Server) handleIncomingWebhook(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	token := r.PathValue("token")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Look up hook by token
	var hookID, channelID string
	err = wdb.DB.QueryRow(
		"SELECT id, channel_id FROM webhook_hooks WHERE token = ?", token,
	).Scan(&hookID, &channelID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Read body (limit 64KB)
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	payload := string(body)

	// Insert webhook event
	eventID := id.New()
	_, _ = wdb.DB.Exec(
		"INSERT INTO webhook_events (id, hook_id, remote_addr, payload, status) VALUES (?, ?, ?, ?, 'received')",
		eventID, hookID, r.RemoteAddr, payload,
	)

	// Respond immediately
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"status":"accepted","event_id":"%s"}`, eventID)

	// Process asynchronously
	go func() {
		autonomy := s.getBrainSetting(slug, "webhook_autonomy")
		if autonomy == "" {
			autonomy = "autonomous"
		}

		content := fmt.Sprintf("Webhook event received:\n\n```json\n%s\n```", payload)

		s.ingestExternalMessage(slug, channelID, "webhook", "Webhook", content, "webhook", autonomy, nil)

		// Update event status
		_, _ = wdb.DB.Exec("UPDATE webhook_events SET status = 'processed' WHERE id = ?", eventID)
	}()
}

// handleCreateWebhook creates a new webhook hook.
// POST /api/workspaces/{slug}/brain/webhooks
func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		ChannelID   string `json:"channel_id"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ChannelID == "" {
		writeError(w, http.StatusBadRequest, "channel_id required")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Generate secure token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	token := hex.EncodeToString(tokenBytes)

	hookID := id.New()
	_, err = wdb.DB.Exec(
		"INSERT INTO webhook_hooks (id, token, channel_id, description) VALUES (?, ?, ?, ?)",
		hookID, token, req.ChannelID, req.Description,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create webhook")
		return
	}

	// Build webhook URL
	scheme := "https"
	if s.cfg.Dev {
		scheme = "http"
	}
	host := r.Host
	url := fmt.Sprintf("%s://%s/w/%s/hook/%s", scheme, host, slug, token)

	writeJSON(w, http.StatusCreated, map[string]string{
		"id":    hookID,
		"token": token,
		"url":   url,
	})
}

// handleListWebhooks returns all webhook hooks for a workspace.
// GET /api/workspaces/{slug}/brain/webhooks
func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	rows, err := wdb.DB.Query(
		"SELECT id, token, channel_id, description, created_at FROM webhook_hooks ORDER BY created_at DESC",
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	var hooks []map[string]string
	for rows.Next() {
		var id, token, channelID, desc, createdAt string
		if rows.Scan(&id, &token, &channelID, &desc, &createdAt) == nil {
			scheme := "https"
			if s.cfg.Dev {
				scheme = "http"
			}
			url := fmt.Sprintf("%s://%s/w/%s/hook/%s", scheme, r.Host, slug, token)
			hooks = append(hooks, map[string]string{
				"id":          id,
				"token":       token,
				"channel_id":  channelID,
				"description": desc,
				"url":         url,
				"created_at":  createdAt,
			})
		}
	}

	if hooks == nil {
		hooks = []map[string]string{}
	}
	writeJSON(w, http.StatusOK, hooks)
}

// handleDeleteWebhook deletes a webhook hook and its events.
// DELETE /api/workspaces/{slug}/brain/webhooks/{id}
func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	hookID := r.PathValue("id")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Delete events first (foreign key)
	_, _ = wdb.DB.Exec("DELETE FROM webhook_events WHERE hook_id = ?", hookID)
	result, err := wdb.DB.Exec("DELETE FROM webhook_hooks WHERE id = ?", hookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}

	logger.WithCategory(logger.CatSystem).Info().Str("workspace", slug).Str("hook_id", hookID).Msg("deleted webhook")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListWebhookEvents returns recent events for a webhook hook.
// GET /api/workspaces/{slug}/brain/webhooks/{id}/events
func (s *Server) handleListWebhookEvents(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	hookID := r.PathValue("id")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	rows, err := wdb.DB.Query(
		"SELECT id, remote_addr, payload, status, error, created_at FROM webhook_events WHERE hook_id = ? ORDER BY created_at DESC LIMIT 50",
		hookID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	var events []map[string]string
	for rows.Next() {
		var eid, addr, payload, status, errStr, createdAt string
		if rows.Scan(&eid, &addr, &payload, &status, &errStr, &createdAt) == nil {
			events = append(events, map[string]string{
				"id":          eid,
				"remote_addr": addr,
				"payload":     payload,
				"status":      status,
				"error":       errStr,
				"created_at":  createdAt,
			})
		}
	}

	if events == nil {
		events = []map[string]string{}
	}
	writeJSON(w, http.StatusOK, events)
}
