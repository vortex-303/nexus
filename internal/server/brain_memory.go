package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// messageCounter tracks messages per channel for triggering memory extraction.
var (
	msgCounters   = map[string]int{} // key: "slug:channelID"
	msgCountersMu sync.Mutex
)

const defaultExtractEveryN = 30 // Default extraction frequency

// trackMessageAndMaybeExtract runs rule extraction (if system memory enabled) and triggers LLM extraction at threshold.
func (s *Server) trackMessageAndMaybeExtract(slug, channelID, messageID, messageContent, senderName string) {
	// Rule extraction runs if system memory is enabled (no API key needed)
	if s.isSystemMemoryEnabled(slug) {
		if wdb, err := s.ws.Open(slug); err == nil {
			if n := brain.RunRuleExtraction(wdb.DB, channelID, messageID, messageContent, senderName); n > 0 {
				logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("count", n).Msg("rule-extracted memories")
			}
		}
	}

	// Check if LLM memory extraction is enabled
	enabled, freq, _ := s.getMemorySettings(slug)
	if !enabled {
		return
	}

	msgCountersMu.Lock()
	key := slug + ":" + channelID

	// Initialize counter from DB on first access (survives server restarts)
	if _, exists := msgCounters[key]; !exists {
		if wdb, err := s.ws.Open(slug); err == nil {
			var dbCount int
			_ = wdb.DB.QueryRow("SELECT COUNT(*) FROM messages WHERE channel_id = ? AND deleted = FALSE", channelID).Scan(&dbCount)
			msgCounters[key] = dbCount % freq
		}
	}

	msgCounters[key]++
	count := msgCounters[key]
	if count >= freq {
		msgCounters[key] = 0
	}
	msgCountersMu.Unlock()

	if count >= freq {
		s.enqueueTask(TaskExtractMemories, ExtractMemoriesPayload{Slug: slug, ChannelID: channelID})
		s.enqueueTask(TaskUpdateSummary, UpdateSummaryPayload{Slug: slug, ChannelID: channelID})
	}
}

// handleExtractNow runs memory extraction synchronously and returns the result.
func (s *Server) handleExtractNow(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	var req struct {
		ChannelID string `json:"channel_id"`
	}
	if err := readJSON(r, &req); err != nil || req.ChannelID == "" {
		writeError(w, http.StatusBadRequest, "channel_id required")
		return
	}

	enabled, _, _ := s.getMemorySettings(slug)
	if !enabled {
		writeError(w, http.StatusBadRequest, "LLM memory is disabled")
		return
	}

	count := s.extractMemories(slug, req.ChannelID)
	// Also update summary in background
	s.enqueueTask(TaskUpdateSummary, UpdateSummaryPayload{Slug: slug, ChannelID: req.ChannelID})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "extracted": count})
}

// isSystemMemoryEnabled checks if system (rule-based) memory extraction is enabled.
func (s *Server) isSystemMemoryEnabled(slug string) bool {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return true // default enabled
	}
	var val string
	if wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'system_memory_enabled'").Scan(&val) == nil {
		return val != "false"
	}
	return true
}

// getMemorySettings reads memory_enabled, extraction_frequency, and memory_model from brain_settings.
func (s *Server) getMemorySettings(slug string) (enabled bool, frequency int, memoryModel string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return true, defaultExtractEveryN, ""
	}

	enabled = true
	frequency = defaultExtractEveryN

	var val string
	if wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'memory_enabled'").Scan(&val) == nil {
		enabled = val != "false"
	}
	if wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'extraction_frequency'").Scan(&val) == nil {
		if n := parseInt(val); n > 0 {
			frequency = n
		}
	}
	if wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'memory_model'").Scan(&val) == nil {
		memoryModel = val
	}
	return
}

func parseInt(s string) int {
	var n int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return n
}

// getMemoryEngine reads the memory_engine setting for a workspace.
// Returns "openrouter" (default), "grok", "gemini", or "openai".
func (s *Server) getMemoryEngine(slug string) string {
	engine := s.getBrainSetting(slug, "memory_engine")
	if engine == "" {
		return "openrouter"
	}
	return engine
}

// getOpenAIKey reads the OpenAI API key from workspace settings.
func (s *Server) getOpenAIKey(slug string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}
	var key string
	_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'openai_api_key'").Scan(&key)
	return key
}

// memoryComplete routes a memory extraction/summarization/consolidation request
// to the configured memory engine. Returns the LLM response text and usage.
func (s *Server) memoryComplete(slug, systemPrompt string, msgs []brain.Message) (string, *brain.CompletionUsage, error) {
	engine := s.getMemoryEngine(slug)
	_, _, memoryModel := s.getMemorySettings(slug)

	switch engine {
	case "gemini":
		geminiKey := s.getGeminiAPIKey(slug)
		if geminiKey == "" {
			return "", nil, fmt.Errorf("no Gemini API key configured")
		}
		if memoryModel == "" {
			memoryModel = "gemini-2.5-flash-lite"
		}
		return brain.GenerateTextGemini(geminiKey, memoryModel, systemPrompt, msgs)

	case "grok":
		xaiKey := s.getXAIKey(slug)
		if xaiKey == "" {
			return "", nil, fmt.Errorf("no xAI API key configured")
		}
		if memoryModel == "" {
			memoryModel = "grok-4.1-fast"
		}
		client := brain.NewXAIClient(xaiKey, memoryModel)
		return client.Complete(systemPrompt, msgs)

	case "openai":
		oaiKey := s.getOpenAIKey(slug)
		if oaiKey == "" {
			return "", nil, fmt.Errorf("no OpenAI API key configured")
		}
		if memoryModel == "" {
			memoryModel = "gpt-4o-mini"
		}
		client := brain.NewOpenAIClient(oaiKey, memoryModel)
		return client.Complete(systemPrompt, msgs)

	default: // "openrouter"
		apiKey, model := s.getBrainSettings(slug)
		if memoryModel != "" {
			model = memoryModel
		}
		resolvedModel, fallbacks := s.resolveFreeAuto(model, slug)
		if apiKey == "" && len(fallbacks) == 0 {
			// No API key — try free models
			resolvedModel, fallbacks = s.resolveFreeAuto(FreeAutoModelID, slug)
			if len(fallbacks) == 0 && resolvedModel == "" {
				return "", nil, fmt.Errorf("no OpenRouter API key and no free models available")
			}
		}
		client := s.makeBrainClient(slug, apiKey, resolvedModel, fallbacks)
		return client.Complete(systemPrompt, msgs)
	}
}

// extractMemories fetches recent messages and asks the LLM to extract facts. Returns count of saved memories.
func (s *Server) extractMemories(slug, channelID string) int {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return 0
	}

	// Get the last N messages from the channel
	_, freq, _ := s.getMemorySettings(slug)

	messages := s.getRecentMessagesRaw(wdb, channelID, freq)
	if len(messages) < 3 {
		return 0
	}

	// Build the extraction request
	var msgText string
	for _, m := range messages {
		msgText += m.Name + ": " + m.Content + "\n"
	}

	llmMessages := []brain.Message{
		{Role: "user", Content: "Extract memories from these messages:\n\n" + msgText},
	}

	result, usage, err := s.memoryComplete(slug, brain.ExtractionPrompt, llmMessages)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("memory extraction failed")
		return 0
	}
	s.trackUsage(slug, usage, "", "memory", channelID, "")

	// Parse extracted memories
	var extracted []struct {
		Type         string   `json:"type"`
		Content      string   `json:"content"`
		Summary      string   `json:"summary"`
		Confidence   float64  `json:"confidence"`
		Participants []string `json:"participants"`
	}
	if err := json.Unmarshal([]byte(result), &extracted); err != nil {
		// Try to find JSON array in response
		start := findChar(result, '[')
		end := findCharReverse(result, ']')
		if start >= 0 && end > start {
			json.Unmarshal([]byte(result[start:end+1]), &extracted)
		}
		if len(extracted) == 0 {
			logger.WithCategory(logger.CatBrain).Warn().Err(err).Str("workspace", slug).Msg("failed to parse extraction")
			return 0
		}
	}

	// Save each extracted memory (with confidence gate)
	saved := 0
	for _, m := range extracted {
		validTypes := map[string]bool{
			brain.MemoryTypeFact:       true,
			brain.MemoryTypeDecision:   true,
			brain.MemoryTypeCommitment: true,
			brain.MemoryTypePerson:     true,
			brain.MemoryTypePolicy:     true,
		}
		if !validTypes[m.Type] {
			m.Type = brain.MemoryTypeFact
		}
		if m.Content == "" {
			continue
		}
		// Confidence gate: discard low-confidence extractions
		if m.Confidence < 0.7 {
			continue
		}
		// Fuzzy dedup: skip if similar memory already exists
		if brain.MemorySimilarExists(wdb.DB, m.Type, m.Content) {
			continue
		}

		participants := strings.Join(m.Participants, ", ")
		if err := brain.SaveMemoryFull(wdb.DB, id.New(), m.Type, m.Content, "llm", channelID, "", 0, m.Summary, m.Confidence, participants); err != nil {
			logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("failed to save memory")
		} else {
			saved++
		}
	}

	if saved > 0 {
		logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("count", saved).Str("channel_id", channelID).Msg("extracted memories")
		brain.LogAction(wdb.DB, id.New(), brain.ActionExtraction, channelID,
			fmt.Sprintf("%d messages", len(messages)),
			fmt.Sprintf("Extracted %d memories (from %d candidates)", saved, len(extracted)),
			s.getMemoryEngine(slug), nil)
	}

	// Try consolidation after extraction
	s.maybeConsolidateMemories(slug, wdb)
	return saved
}

// updateChannelSummary generates/updates a rolling summary for the channel.
func (s *Server) updateChannelSummary(slug, channelID string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	// Get messages since last summary
	newMsgs, err := brain.GetMessagesSinceSummary(wdb.DB, channelID, 50)
	if err != nil || len(newMsgs) < 5 {
		return // Not enough new messages to warrant a summary update
	}

	// Build the message text
	var msgText string
	for _, m := range newMsgs {
		msgText += m.Name + ": " + m.Content + "\n"
	}

	// Get existing summary for incremental merge
	existing, _ := brain.GetChannelSummary(wdb.DB, channelID)

	userContent := "Summarize these recent messages:\n\n" + msgText
	if existing != nil && existing.Summary != "" {
		userContent = fmt.Sprintf("Previous summary (%d messages):\n%s\n\nNew messages to merge in:\n%s",
			existing.MessageCount, existing.Summary, msgText)
	}

	result, usage, err := s.memoryComplete(slug, brain.SummarizationPrompt, []brain.Message{
		{Role: "user", Content: userContent},
	})
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("channel summary failed")
		return
	}
	s.trackUsage(slug, usage, "", "memory", channelID, "")

	// Calculate total message count
	totalCount := len(newMsgs)
	if existing != nil {
		totalCount += existing.MessageCount
	}

	// Save with the last message ID
	lastMsgID := ""
	if len(newMsgs) > 0 {
		lastMsgID = newMsgs[len(newMsgs)-1].ID
	}

	if err := brain.SaveChannelSummary(wdb.DB, channelID, result, lastMsgID, totalCount); err != nil {
		logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("failed to save channel summary")
		return
	}

	logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("channel_id", channelID).Int("new_msgs", len(newMsgs)).Int("total", totalCount).Msg("updated channel summary")
}

// getRecentMessagesRaw fetches recent messages without role mapping.
func (s *Server) getRecentMessagesRaw(wdb *db.WorkspaceDB, channelID string, limit int) []brain.Message {
	rows, err := wdb.DB.Query(`
		SELECT COALESCE(mem.display_name, m.sender_id), m.content
		FROM messages m
		LEFT JOIN members mem ON mem.id = m.sender_id
		WHERE m.channel_id = ? AND m.deleted = FALSE
		ORDER BY m.created_at DESC
		LIMIT ?
	`, channelID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var msgs []brain.Message
	for rows.Next() {
		var name, content string
		if err := rows.Scan(&name, &content); err != nil {
			continue
		}
		msgs = append(msgs, brain.Message{Name: name, Content: content, Role: "user"})
	}

	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs
}

func findChar(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func findCharReverse(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// --- API Handlers ---

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
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

	memType := r.URL.Query().Get("type")
	memories, err := brain.ListMemories(wdb.DB, memType, 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if memories == nil {
		memories = []brain.Memory{}
	}

	// Collect unique source_channel IDs and resolve names
	channelNames := map[string]string{}
	{
		uniqueIDs := map[string]bool{}
		for _, m := range memories {
			if m.SourceChannel != "" {
				uniqueIDs[m.SourceChannel] = true
			}
		}
		if len(uniqueIDs) > 0 {
			ids := make([]string, 0, len(uniqueIDs))
			for id := range uniqueIDs {
				ids = append(ids, id)
			}
			placeholders := strings.Repeat("?,", len(ids))
			placeholders = placeholders[:len(placeholders)-1]
			args := make([]any, len(ids))
			for i, v := range ids {
				args[i] = v
			}
			rows, err := wdb.DB.Query("SELECT id, name FROM channels WHERE id IN ("+placeholders+")", args...)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var cid, cname string
					if rows.Scan(&cid, &cname) == nil {
						channelNames[cid] = cname
					}
				}
			}
		}
	}

	// Build enriched response
	type memoryWithChannel struct {
		brain.Memory
		ChannelName string `json:"channel_name,omitempty"`
	}
	enriched := make([]memoryWithChannel, len(memories))
	for i, m := range memories {
		enriched[i] = memoryWithChannel{Memory: m, ChannelName: channelNames[m.SourceChannel]}
	}

	counts, _ := brain.CountMemories(wdb.DB)

	writeJSON(w, http.StatusOK, map[string]any{
		"memories": enriched,
		"counts":   counts,
	})
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	memID := r.PathValue("memoryID")
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

	if err := brain.DeleteMemory(wdb.DB, memID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleClearMemories(w http.ResponseWriter, r *http.Request) {
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

	if err := brain.ClearMemories(wdb.DB); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (s *Server) handlePinMemory(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	if !s.isSystemMemoryEnabled(slug) {
		writeError(w, http.StatusBadRequest, "system memory is disabled for this workspace")
		return
	}

	var body struct {
		MessageID string `json:"message_id"`
		ChannelID string `json:"channel_id"`
		Type      string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MessageID == "" || body.ChannelID == "" {
		writeError(w, http.StatusBadRequest, "message_id and channel_id required")
		return
	}
	if body.Type == "" {
		body.Type = brain.MemoryTypeFact
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Fetch message content and sender
	var content, senderName string
	err = wdb.DB.QueryRow(`
		SELECT m.content, COALESCE(mem.display_name, m.sender_id)
		FROM messages m
		LEFT JOIN members mem ON mem.id = m.sender_id
		WHERE m.id = ?
	`, body.MessageID).Scan(&content, &senderName)
	if err != nil {
		writeError(w, http.StatusNotFound, "message not found")
		return
	}

	pinContent := senderName + ": " + content
	if len(pinContent) > 500 {
		pinContent = pinContent[:497] + "..."
	}

	if brain.MemoryExists(wdb.DB, pinContent) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already_exists"})
		return
	}

	if err := brain.SaveMemory(wdb.DB, id.New(), body.Type, pinContent, "pin", body.ChannelID, body.MessageID, 0); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "pinned"})
}

// maybeConsolidateMemories runs consolidation at most once every 6 hours.
func (s *Server) maybeConsolidateMemories(slug string, wdb *db.WorkspaceDB) {
	// Check last consolidation time
	var lastConsolidation string
	_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'last_consolidation'").Scan(&lastConsolidation)
	if lastConsolidation != "" {
		if t, err := time.Parse(time.RFC3339, lastConsolidation); err == nil {
			if time.Since(t) < 6*time.Hour {
				return
			}
		}
	}

	// Get recent memories for consolidation
	memories, err := brain.RecentMemories(wdb.DB, 7, 50)
	if err != nil || len(memories) < 3 {
		return
	}

	// Build memory list for consolidation
	var memText strings.Builder
	for _, m := range memories {
		memText.WriteString(fmt.Sprintf("[%s] id=%s type=%s confidence=%.1f participants=%s\n%s\n\n",
			m.CreatedAt, m.ID, m.Type, m.Confidence, m.Participants, m.Content))
	}

	result, usage, err := s.memoryComplete(slug, brain.ConsolidationPrompt, []brain.Message{
		{Role: "user", Content: "Consolidate these memories:\n\n" + memText.String()},
	})
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("consolidation failed")
		return
	}
	s.trackUsage(slug, usage, "", "memory", "", "")

	// Parse consolidation result
	var consolidation struct {
		Supersede []struct {
			OldID string `json:"old_id"`
			NewID string `json:"new_id"`
		} `json:"supersede"`
		Insights []struct {
			Content string `json:"content"`
			Summary string `json:"summary"`
		} `json:"insights"`
		UpgradeConfidence []struct {
			ID            string  `json:"id"`
			NewConfidence float64 `json:"new_confidence"`
		} `json:"upgrade_confidence"`
	}

	if err := json.Unmarshal([]byte(result), &consolidation); err != nil {
		start := findChar(result, '{')
		end := findCharReverse(result, '}')
		if start >= 0 && end > start {
			json.Unmarshal([]byte(result[start:end+1]), &consolidation)
		}
	}

	changes := 0

	// Apply supersessions
	for _, s := range consolidation.Supersede {
		if s.OldID != "" && s.NewID != "" {
			if brain.SupersedeMemory(wdb.DB, s.OldID, s.NewID) == nil {
				changes++
			}
		}
	}

	// Save insights
	for _, insight := range consolidation.Insights {
		if insight.Content != "" && !brain.MemoryExists(wdb.DB, insight.Content) {
			if brain.SaveMemoryFull(wdb.DB, id.New(), brain.MemoryTypeInsight, insight.Content, "consolidation", "", "", 0.7, insight.Summary, 0.8, "") == nil {
				changes++
			}
		}
	}

	// Upgrade confidence
	for _, uc := range consolidation.UpgradeConfidence {
		if uc.ID != "" && uc.NewConfidence > 0 && uc.NewConfidence <= 1.0 {
			if brain.UpdateConfidence(wdb.DB, uc.ID, uc.NewConfidence) == nil {
				changes++
			}
		}
	}

	// Record consolidation timestamp
	now := time.Now().UTC().Format(time.RFC3339)
	wdb.DB.Exec(`INSERT INTO brain_settings (key, value) VALUES ('last_consolidation', ?)
		ON CONFLICT(key) DO UPDATE SET value = ?`, now, now)

	if changes > 0 {
		logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("changes", changes).Msg("consolidated memories")
	}
}
