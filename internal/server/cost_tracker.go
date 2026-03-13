package server

import (
	"net/http"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/logger"
)

// trackUsage records an LLM usage entry in the workspace database.
// Silent on error — never blocks the LLM flow.
func (s *Server) trackUsage(slug string, usage *brain.CompletionUsage, model, actionType, channelID, memberName string) {
	if usage == nil {
		return
	}
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}
	_, err = wdb.DB.Exec(`
		INSERT INTO llm_usage (model, input_tokens, output_tokens, cost_usd, action_type, channel_id, member_name)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, model, usage.PromptTokens, usage.CompletionTokens, usage.Cost, actionType, channelID, memberName)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Warn().Err(err).Str("workspace", slug).Msg("failed to track LLM usage")
	}
}

// handleGetUsage returns LLM usage statistics for a workspace.
// GET /api/workspaces/{slug}/usage?period=day|week|month|all
func (s *Server) handleGetUsage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	period := r.URL.Query().Get("period")
	var since string
	now := time.Now().UTC()
	switch period {
	case "day":
		since = now.AddDate(0, 0, -1).Format("2006-01-02T15:04:05Z")
	case "week":
		since = now.AddDate(0, 0, -7).Format("2006-01-02T15:04:05Z")
	case "all":
		since = "2000-01-01T00:00:00Z"
	default: // month
		since = now.AddDate(0, -1, 0).Format("2006-01-02T15:04:05Z")
	}

	// Totals
	var totalCost float64
	var totalInput, totalOutput, callCount int
	err = wdb.DB.QueryRow(`
		SELECT COALESCE(SUM(cost_usd), 0), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COUNT(*)
		FROM llm_usage WHERE created_at >= ?
	`, since).Scan(&totalCost, &totalInput, &totalOutput, &callCount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query error")
		return
	}

	// By model
	type modelBreakdown struct {
		Model        string  `json:"model"`
		Cost         float64 `json:"cost"`
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		Calls        int     `json:"calls"`
	}
	rows, err := wdb.DB.Query(`
		SELECT model, COALESCE(SUM(cost_usd), 0), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COUNT(*)
		FROM llm_usage WHERE created_at >= ? GROUP BY model ORDER BY SUM(cost_usd) DESC
	`, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query error")
		return
	}
	defer rows.Close()
	var byModel []modelBreakdown
	for rows.Next() {
		var m modelBreakdown
		rows.Scan(&m.Model, &m.Cost, &m.InputTokens, &m.OutputTokens, &m.Calls)
		byModel = append(byModel, m)
	}
	if byModel == nil {
		byModel = []modelBreakdown{}
	}

	// By action
	type actionBreakdown struct {
		Action       string  `json:"action"`
		Cost         float64 `json:"cost"`
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		Calls        int     `json:"calls"`
	}
	rows2, err := wdb.DB.Query(`
		SELECT action_type, COALESCE(SUM(cost_usd), 0), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COUNT(*)
		FROM llm_usage WHERE created_at >= ? GROUP BY action_type ORDER BY SUM(cost_usd) DESC
	`, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query error")
		return
	}
	defer rows2.Close()
	var byAction []actionBreakdown
	for rows2.Next() {
		var a actionBreakdown
		rows2.Scan(&a.Action, &a.Cost, &a.InputTokens, &a.OutputTokens, &a.Calls)
		byAction = append(byAction, a)
	}
	if byAction == nil {
		byAction = []actionBreakdown{}
	}

	// Daily breakdown (last 30 days)
	type dailyBreakdown struct {
		Date  string  `json:"date"`
		Cost  float64 `json:"cost"`
		Calls int     `json:"calls"`
	}
	rows3, err := wdb.DB.Query(`
		SELECT DATE(created_at) as day, COALESCE(SUM(cost_usd), 0), COUNT(*)
		FROM llm_usage WHERE created_at >= ? GROUP BY DATE(created_at) ORDER BY day
	`, now.AddDate(0, 0, -30).Format("2006-01-02T15:04:05Z"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query error")
		return
	}
	defer rows3.Close()
	var daily []dailyBreakdown
	for rows3.Next() {
		var d dailyBreakdown
		rows3.Scan(&d.Date, &d.Cost, &d.Calls)
		daily = append(daily, d)
	}
	if daily == nil {
		daily = []dailyBreakdown{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_cost":          totalCost,
		"total_input_tokens":  totalInput,
		"total_output_tokens": totalOutput,
		"call_count":          callCount,
		"by_model":            byModel,
		"by_action":           byAction,
		"daily":               daily,
	})
}
