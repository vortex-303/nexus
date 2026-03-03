package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/id"
)

// messageCounter tracks messages per channel for triggering memory extraction.
var (
	msgCounters   = map[string]int{} // key: "slug:channelID"
	msgCountersMu sync.Mutex
)

const defaultExtractEveryN = 15 // Default extraction frequency

// trackMessageAndMaybeExtract increments the counter and triggers extraction if threshold hit.
func (s *Server) trackMessageAndMaybeExtract(slug, channelID string) {
	// Check if memory is enabled
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
		go s.extractMemories(slug, channelID)
		go s.updateChannelSummary(slug, channelID)
	}
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

// extractMemories fetches recent messages and asks the LLM to extract facts.
func (s *Server) extractMemories(slug, channelID string) {
	apiKey, model := s.getBrainSettings(slug)
	if apiKey == "" {
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	// Get the last N messages from the channel
	_, freq, memoryModel := s.getMemorySettings(slug)
	if memoryModel != "" {
		model = memoryModel
	}
	messages := s.getRecentMessagesRaw(wdb, channelID, freq)
	if len(messages) == 0 {
		return
	}

	// Build the extraction request
	var msgText string
	for _, m := range messages {
		msgText += m.Name + ": " + m.Content + "\n"
	}

	client := brain.NewClient(apiKey, model)
	llmMessages := []brain.Message{
		{Role: "user", Content: "Extract memories from these messages:\n\n" + msgText},
	}

	result, err := client.Complete(brain.ExtractionPrompt, llmMessages)
	if err != nil {
		log.Printf("[brain:%s] memory extraction failed: %v", slug, err)
		return
	}

	// Parse extracted memories
	var extracted []struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(result), &extracted); err != nil {
		// Try to find JSON array in response
		start := findChar(result, '[')
		end := findCharReverse(result, ']')
		if start >= 0 && end > start {
			json.Unmarshal([]byte(result[start:end+1]), &extracted)
		}
		if len(extracted) == 0 {
			log.Printf("[brain:%s] failed to parse extraction: %v", slug, err)
			return
		}
	}

	// Save each extracted memory
	for _, m := range extracted {
		validTypes := map[string]bool{
			brain.MemoryTypeFact:       true,
			brain.MemoryTypeDecision:   true,
			brain.MemoryTypeCommitment: true,
			brain.MemoryTypePerson:     true,
		}
		if !validTypes[m.Type] {
			m.Type = brain.MemoryTypeFact
		}
		if m.Content == "" {
			continue
		}

		if err := brain.SaveMemory(wdb.DB, id.New(), m.Type, m.Content, channelID, ""); err != nil {
			log.Printf("[brain:%s] failed to save memory: %v", slug, err)
		}
	}

	if len(extracted) > 0 {
		log.Printf("[brain:%s] extracted %d memories from #%s", slug, len(extracted), channelID)
		brain.LogAction(wdb.DB, id.New(), brain.ActionExtraction, channelID,
			fmt.Sprintf("%d messages", len(messages)),
			fmt.Sprintf("Extracted %d memories", len(extracted)),
			model, nil)
	}
}

// updateChannelSummary generates/updates a rolling summary for the channel.
func (s *Server) updateChannelSummary(slug, channelID string) {
	apiKey, model := s.getBrainSettings(slug)
	if apiKey == "" {
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	// Use memory_model if configured (cheaper)
	_, _, memoryModel := s.getMemorySettings(slug)
	if memoryModel != "" {
		model = memoryModel
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

	prompt := brain.SummarizationPrompt
	userContent := "Summarize these recent messages:\n\n" + msgText
	if existing != nil && existing.Summary != "" {
		userContent = fmt.Sprintf("Previous summary (%d messages):\n%s\n\nNew messages to merge in:\n%s",
			existing.MessageCount, existing.Summary, msgText)
	}

	client := brain.NewClient(apiKey, model)
	result, err := client.Complete(prompt, []brain.Message{
		{Role: "user", Content: userContent},
	})
	if err != nil {
		log.Printf("[brain:%s] channel summary failed: %v", slug, err)
		return
	}

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
		log.Printf("[brain:%s] failed to save channel summary: %v", slug, err)
		return
	}

	log.Printf("[brain:%s] updated channel summary for #%s (%d new msgs, %d total)", slug, channelID, len(newMsgs), totalCount)
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

	counts, _ := brain.CountMemories(wdb.DB)

	writeJSON(w, http.StatusOK, map[string]any{
		"memories": memories,
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
