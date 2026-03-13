package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
)

type SocialPulse struct {
	ID                   string          `json:"id"`
	Topic                string          `json:"topic"`
	Query                string          `json:"query"`
	RawText              string          `json:"raw_text,omitempty"`
	Citations            json.RawMessage `json:"citations"`
	Summary              string          `json:"summary"`
	SentimentScore       int             `json:"sentiment_score"`
	Themes               json.RawMessage `json:"themes"`
	KeyPosts             json.RawMessage `json:"key_posts"`
	Recommendations      string          `json:"recommendations"`
	Predictions          json.RawMessage `json:"predictions"`
	Risks                json.RawMessage `json:"risks"`
	CompetitiveMentions  json.RawMessage `json:"competitive_mentions"`
	AudienceBreakdown    json.RawMessage `json:"audience_breakdown"`
	SourceBreakdown      json.RawMessage `json:"source_breakdown"`
	Status               string          `json:"status"`
	CreatedBy            string          `json:"created_by"`
	CreatedAt            string          `json:"created_at"`
	UpdatedAt            string          `json:"updated_at"`
}

const pulseSelectCols = `id, topic, query, raw_text, citations, summary, sentiment_score, themes, key_posts, recommendations, predictions, risks, competitive_mentions, audience_breakdown, source_breakdown, status, created_by, created_at, updated_at`

// scanPulse scans a row into a SocialPulse, handling JSON columns stored as TEXT in SQLite.
func scanPulse(scanner interface{ Scan(dest ...any) error }) (SocialPulse, error) {
	var p SocialPulse
	var citations, themes, keyPosts, predictions, risks, competitive, audience, source string
	err := scanner.Scan(&p.ID, &p.Topic, &p.Query, &p.RawText, &citations, &p.Summary,
		&p.SentimentScore, &themes, &keyPosts, &p.Recommendations,
		&predictions, &risks, &competitive, &audience, &source,
		&p.Status, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	p.Citations = json.RawMessage(citations)
	p.Themes = json.RawMessage(themes)
	p.KeyPosts = json.RawMessage(keyPosts)
	p.Predictions = json.RawMessage(predictions)
	p.Risks = json.RawMessage(risks)
	p.CompetitiveMentions = json.RawMessage(competitive)
	p.AudienceBreakdown = json.RawMessage(audience)
	p.SourceBreakdown = json.RawMessage(source)
	return p, nil
}

func (s *Server) handleCreateSocialPulse(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		Topic string `json:"topic"`
		Query string `json:"query"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Topic == "" {
		writeError(w, http.StatusBadRequest, "topic is required")
		return
	}
	if req.Query == "" {
		req.Query = req.Topic
	}

	xaiKey := s.getXAIKey(slug)
	if xaiKey == "" {
		writeError(w, http.StatusBadRequest, "xAI API key not configured — add it in Brain settings")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	pulseID := id.New()

	_, err = wdb.DB.Exec(`INSERT INTO social_pulses (id, topic, query, status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, 'pending', ?, ?, ?)`,
		pulseID, req.Topic, req.Query, claims.UserID, now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create pulse")
		return
	}

	pulse := SocialPulse{
		ID:                  pulseID,
		Topic:               req.Topic,
		Query:               req.Query,
		Status:              "pending",
		CreatedBy:           claims.UserID,
		Citations:           json.RawMessage("[]"),
		Themes:              json.RawMessage("[]"),
		KeyPosts:            json.RawMessage("[]"),
		Predictions:         json.RawMessage("[]"),
		Risks:               json.RawMessage("[]"),
		CompetitiveMentions: json.RawMessage("[]"),
		AudienceBreakdown:   json.RawMessage("{}"),
		SourceBreakdown:     json.RawMessage("{}"),
		SentimentScore:      50,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeSocialPulseCreated, pulse), "")

	go s.runSocialPulseAnalysis(slug, pulseID, req.Query)

	writeJSON(w, http.StatusCreated, pulse)
}

func (s *Server) handleListSocialPulses(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	query := "SELECT " + pulseSelectCols + " FROM social_pulses"
	var args []any

	if topic := r.URL.Query().Get("topic"); topic != "" {
		query += " WHERE topic = ?"
		args = append(args, topic)
	}

	query += " ORDER BY created_at DESC LIMIT 50"

	rows, err := wdb.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	pulses := []SocialPulse{}
	for rows.Next() {
		p, err := scanPulse(rows)
		if err != nil {
			continue
		}
		pulses = append(pulses, p)
	}

	writeJSON(w, http.StatusOK, map[string]any{"pulses": pulses})
}

func (s *Server) handleGetSocialPulse(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	pulseID := r.PathValue("pulseID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	p, err := scanPulse(wdb.DB.QueryRow("SELECT "+pulseSelectCols+" FROM social_pulses WHERE id = ?", pulseID))
	if err != nil {
		writeError(w, http.StatusNotFound, "pulse not found")
		return
	}

	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteSocialPulse(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	pulseID := r.PathValue("pulseID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	res, err := wdb.DB.Exec("DELETE FROM social_pulses WHERE id = ?", pulseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "pulse not found")
		return
	}

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeSocialPulseDeleted, map[string]string{"id": pulseID}), "")

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) runSocialPulseAnalysis(slug, pulseID, query string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[social-pulse] panic in analysis goroutine: %v", r)
		}
	}()

	log.Printf("[social-pulse] starting analysis for pulse=%s query=%q", pulseID, query)

	wdb, err := s.ws.Open(slug)
	if err != nil {
		log.Printf("[social-pulse] workspace error: %v", err)
		return
	}
	h := s.hubs.Get(slug)

	updateStatus := func(status string) {
		log.Printf("[social-pulse] pulse=%s status→%s", pulseID, status)
		now := time.Now().UTC().Format(time.RFC3339)
		wdb.DB.Exec("UPDATE social_pulses SET status = ?, updated_at = ? WHERE id = ?", status, now, pulseID)
		h.BroadcastAll(hub.MakeEnvelope(hub.TypeSocialPulseUpdated, map[string]any{
			"id": pulseID, "status": status, "updated_at": now,
		}), "")
	}

	setFailed := func(errMsg string) {
		log.Printf("[social-pulse] pulse=%s FAILED: %s", pulseID, errMsg)
		now := time.Now().UTC().Format(time.RFC3339)
		wdb.DB.Exec("UPDATE social_pulses SET status = 'failed', summary = ?, updated_at = ? WHERE id = ?",
			errMsg, now, pulseID)
		h.BroadcastAll(hub.MakeEnvelope(hub.TypeSocialPulseUpdated, map[string]any{
			"id": pulseID, "status": "failed", "summary": errMsg, "updated_at": now,
		}), "")
	}

	xaiKey := s.getXAIKey(slug)
	if xaiKey == "" {
		setFailed("xAI API key not configured")
		return
	}

	// Step 1: Search X
	updateStatus("searching")

	client := brain.NewXAIClient(xaiKey, "grok-4")
	sevenDaysAgo := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	xTools := []brain.ResponsesTool{
		{Type: "x_search", FromDate: sevenDaysAgo},
	}

	log.Printf("[social-pulse] pulse=%s calling CompleteXSearch (x_search)...", pulseID)
	rawX, citationsX, err := client.CompleteXSearch(query, xTools...)
	if err != nil {
		setFailed(fmt.Sprintf("X search failed: %v", err))
		return
	}
	log.Printf("[social-pulse] pulse=%s x_search returned %d chars, %d citations", pulseID, len(rawX), len(citationsX))

	// Step 2: Web Search
	updateStatus("searching_web")

	webClient := brain.NewXAIClient(xaiKey, "grok-4")
	webTools := []brain.ResponsesTool{
		{Type: "web_search"},
	}

	log.Printf("[social-pulse] pulse=%s calling CompleteXSearch (web_search)...", pulseID)
	rawWeb, citationsWeb, err := webClient.CompleteXSearch(query+" sentiment analysis news", webTools...)
	if err != nil {
		// Web search failure is non-fatal — continue with X results only
		log.Printf("[social-pulse] pulse=%s web_search failed (non-fatal): %v", pulseID, err)
		rawWeb = ""
	}
	log.Printf("[social-pulse] pulse=%s web_search returned %d chars, %d citations", pulseID, len(rawWeb), len(citationsWeb))

	// Merge citations and raw text
	allCitations := append(citationsX, citationsWeb...)
	combinedRaw := rawX
	if rawWeb != "" {
		combinedRaw += "\n\n--- Web Results ---\n\n" + rawWeb
	}

	// Save raw results
	citationsJSON, _ := json.Marshal(allCitations)
	now := time.Now().UTC().Format(time.RFC3339)
	wdb.DB.Exec("UPDATE social_pulses SET raw_text = ?, citations = ?, updated_at = ? WHERE id = ?",
		combinedRaw, string(citationsJSON), now, pulseID)

	// Step 3: Enriched Analysis with Grok
	updateStatus("analyzing")

	analysisPrompt := `You are a senior market intelligence analyst. Analyze the following social media and web data about the given topic. Return ONLY valid JSON with no markdown formatting, no code fences, just raw JSON:
{
  "sentiment_score": <0-100 where 0=very negative, 50=neutral, 100=very positive>,
  "summary": "<executive summary, 3-4 sentences>",
  "themes": [{"name": "<theme>", "count": <approximate mentions>, "sentiment": "positive|negative|neutral|mixed", "description": "<1-sentence context>"}],
  "key_posts": [{"text": "<excerpt>", "author": "<handle or source>", "sentiment": "positive|negative|neutral", "source_type": "x|news|blog|forum|reddit"}],
  "predictions": [{"prediction": "<what may happen next>", "confidence": "high|medium|low", "timeframe": "<when>", "basis": "<what data supports this>"}],
  "risks": [{"risk": "<potential risk or negative trend>", "severity": "high|medium|low", "evidence": "<supporting data>"}],
  "competitive_mentions": [{"competitor": "<name>", "sentiment": "positive|negative|neutral", "context": "<how they were mentioned>"}],
  "audience_breakdown": {"advocates": "<who is positive and why>", "critics": "<who is negative and why>", "neutral": "<who is undecided>"},
  "source_breakdown": {"x_posts": <count>, "news_articles": <count>, "blogs": <count>, "forums": <count>, "other": <count>},
  "recommendations": "<actionable strategic recommendations, 3-5 bullet points>"
}`

	log.Printf("[social-pulse] pulse=%s calling Grok analysis...", pulseID)
	analysisClient := brain.NewXAIClient(xaiKey, "grok-4")
	analysisResult, pulseUsage, err := analysisClient.Complete(analysisPrompt, []brain.Message{
		{Role: "user", Content: fmt.Sprintf("Topic: %s\n\nX/Twitter Results:\n%s\n\nWeb Results:\n%s", query, rawX, rawWeb)},
	})
	if err != nil {
		setFailed(fmt.Sprintf("Analysis failed: %v", err))
		return
	}
	s.trackUsage(slug, pulseUsage, "grok-4", "social_pulse", "", "")
	log.Printf("[social-pulse] pulse=%s analysis returned %d chars", pulseID, len(analysisResult))

	// Parse the JSON response - strip markdown code fences if present
	cleaned := strings.TrimSpace(analysisResult)
	if strings.HasPrefix(cleaned, "```") {
		if idx := strings.Index(cleaned[3:], "\n"); idx >= 0 {
			cleaned = cleaned[3+idx+1:]
		}
		if strings.HasSuffix(cleaned, "```") {
			cleaned = cleaned[:len(cleaned)-3]
		}
		cleaned = strings.TrimSpace(cleaned)
	}

	var analysis struct {
		SentimentScore      int             `json:"sentiment_score"`
		Summary             string          `json:"summary"`
		Themes              json.RawMessage `json:"themes"`
		KeyPosts            json.RawMessage `json:"key_posts"`
		Recommendations     string          `json:"recommendations"`
		Predictions         json.RawMessage `json:"predictions"`
		Risks               json.RawMessage `json:"risks"`
		CompetitiveMentions json.RawMessage `json:"competitive_mentions"`
		AudienceBreakdown   json.RawMessage `json:"audience_breakdown"`
		SourceBreakdown     json.RawMessage `json:"source_breakdown"`
	}
	if err := json.Unmarshal([]byte(cleaned), &analysis); err != nil {
		log.Printf("[social-pulse] pulse=%s parse error, raw response: %s", pulseID, cleaned[:min(500, len(cleaned))])
		setFailed(fmt.Sprintf("Failed to parse analysis: %v", err))
		return
	}

	// Default empty JSON for optional fields
	if analysis.Predictions == nil {
		analysis.Predictions = json.RawMessage("[]")
	}
	if analysis.Risks == nil {
		analysis.Risks = json.RawMessage("[]")
	}
	if analysis.CompetitiveMentions == nil {
		analysis.CompetitiveMentions = json.RawMessage("[]")
	}
	if analysis.AudienceBreakdown == nil {
		analysis.AudienceBreakdown = json.RawMessage("{}")
	}
	if analysis.SourceBreakdown == nil {
		analysis.SourceBreakdown = json.RawMessage("{}")
	}

	// Save final results
	now = time.Now().UTC().Format(time.RFC3339)
	_, err = wdb.DB.Exec(`UPDATE social_pulses SET
		summary = ?, sentiment_score = ?, themes = ?, key_posts = ?,
		recommendations = ?, predictions = ?, risks = ?, competitive_mentions = ?,
		audience_breakdown = ?, source_breakdown = ?, status = 'ready', updated_at = ?
		WHERE id = ?`,
		analysis.Summary, analysis.SentimentScore, string(analysis.Themes),
		string(analysis.KeyPosts), analysis.Recommendations,
		string(analysis.Predictions), string(analysis.Risks), string(analysis.CompetitiveMentions),
		string(analysis.AudienceBreakdown), string(analysis.SourceBreakdown),
		now, pulseID)
	if err != nil {
		setFailed(fmt.Sprintf("Failed to save results: %v", err))
		return
	}

	// Broadcast final update with full data
	finalPulse, err := scanPulse(wdb.DB.QueryRow("SELECT "+pulseSelectCols+" FROM social_pulses WHERE id = ?", pulseID))
	if err != nil {
		log.Printf("[social-pulse] pulse=%s failed to read final state: %v", pulseID, err)
		return
	}

	log.Printf("[social-pulse] pulse=%s READY score=%d", pulseID, finalPulse.SentimentScore)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeSocialPulseUpdated, finalPulse), "")
}
