package server

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/logger"
)

// imageGenEvent captures one Gemini image-generation call from the moment
// Brain (or an agent) invokes the tool through to the response. Written
// to the image_generations table by recordImageGen so the user can debug
// what Brain actually sent vs what was enriched vs what came back.
type imageGenEvent struct {
	channelID       string
	callerKind      string // "brain" | "agent"
	callerID        string // agent ID or "Brain"
	model           string
	aspectRatio     string
	rawPrompt       string
	enrichedPrompt  string
	enrichedByModel string
	status          string // "ok" | "error"
	errorMessage    string
	latency         time.Duration
	blobHash        string
}

// recordImageGen writes a single image-generation event to the workspace
// DB. Best-effort: errors are logged but don't fail the user-visible
// image gen. Caller passes the slug; we open the workspace DB just for
// the insert and release immediately.
func (s *Server) recordImageGen(slug string, ev imageGenEvent) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("recordImageGen: workspace open failed")
		return
	}
	_, err = wdb.DB.Exec(`
		INSERT INTO image_generations (
			channel_id, caller_kind, caller_id, model, aspect_ratio,
			raw_prompt, enriched_prompt, enriched_by_model,
			status, error_message, latency_ms, blob_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		ev.channelID, ev.callerKind, ev.callerID, ev.model, ev.aspectRatio,
		ev.rawPrompt, ev.enrichedPrompt, ev.enrichedByModel,
		ev.status, ev.errorMessage, ev.latency.Milliseconds(), ev.blobHash,
	)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("recordImageGen: insert failed")
	}
}

// handleListImageGenLog returns the most recent image-generation events
// for a workspace. Admin only — the prompts may contain sensitive
// product brief content.
func (s *Server) handleListImageGenLog(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)
	if claims == nil || (claims.Role != "admin" && claims.Role != "owner" && !claims.SuperAdmin) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	slug := r.PathValue("slug")
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n := parseInt(v); n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, err := wdb.DB.Query(`
		SELECT id, created_at, channel_id, caller_kind, caller_id, model,
		       aspect_ratio, raw_prompt, enriched_prompt, enriched_by_model,
		       status, error_message, latency_ms, blob_hash
		  FROM image_generations
		 ORDER BY created_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	type entry struct {
		ID              int    `json:"id"`
		CreatedAt       string `json:"created_at"`
		ChannelID       string `json:"channel_id"`
		CallerKind      string `json:"caller_kind"`
		CallerID        string `json:"caller_id"`
		Model           string `json:"model"`
		AspectRatio     string `json:"aspect_ratio"`
		RawPrompt       string `json:"raw_prompt"`
		EnrichedPrompt  string `json:"enriched_prompt"`
		EnrichedByModel string `json:"enriched_by_model"`
		Status          string `json:"status"`
		ErrorMessage    string `json:"error_message"`
		LatencyMs       int    `json:"latency_ms"`
		BlobHash        string `json:"blob_hash"`
	}
	out := []entry{}
	for rows.Next() {
		var e entry
		var rawPrompt, enrichedPrompt, errMsg sql.NullString
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.ChannelID, &e.CallerKind, &e.CallerID, &e.Model, &e.AspectRatio, &rawPrompt, &enrichedPrompt, &e.EnrichedByModel, &e.Status, &errMsg, &e.LatencyMs, &e.BlobHash); err != nil {
			continue
		}
		e.RawPrompt = rawPrompt.String
		e.EnrichedPrompt = enrichedPrompt.String
		e.ErrorMessage = errMsg.String
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "limit": limit})
}

// parseInt is defined in brain_memory.go — reused here.
