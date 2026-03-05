package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// ensureBrainMember creates the Brain member in a workspace if it doesn't exist.
// Also ensures Brain has a system agent row in the agents table.
func (s *Server) ensureBrainMember(slug string) error {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return err
	}

	var exists int
	err = wdb.DB.QueryRow("SELECT COUNT(*) FROM members WHERE id = ?", brain.BrainMemberID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists == 0 {
		brainColor := assignMemberColor(wdb)
		_, err = wdb.DB.Exec(
			"INSERT INTO members (id, display_name, role, color) VALUES (?, ?, ?, ?)",
			brain.BrainMemberID, brain.BrainName, brain.BrainRole, brainColor,
		)
		if err != nil {
			return err
		}
	}

	// Ensure Brain system agent row exists
	var agentExists int
	err = wdb.DB.QueryRow("SELECT COUNT(*) FROM agents WHERE id = ?", brain.BrainMemberID).Scan(&agentExists)
	if err != nil {
		// Table might not exist yet (pre-v9 migration) — skip silently
		return nil
	}
	if agentExists == 0 {
		_, err = wdb.DB.Exec(`
			INSERT INTO agents (id, name, description, avatar, role, goal, backstory, instructions,
				model, temperature, max_tokens, tools, channels, knowledge_access, memory_access, can_delegate,
				max_iterations, constraints, escalation_prompt, trigger_type, is_system, is_active, created_by)
			VALUES (?, 'Brain', 'System AI agent with full workspace access', '🧠',
				'System Agent', 'Coordinate the workspace, answer questions, and manage other agents',
				'', '', '', 0.7, 2048, '["create_task","list_tasks","search_messages","create_document","search_knowledge","delegate_to_agent"]',
				'[]', 1, 1, 1, 10, '', '', 'mention', 1, 1, 'system')`,
			brain.BrainMemberID,
		)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("failed to seed system agent")
		}
	}

	return nil
}

// ensureBuiltinAgents seeds all built-in agents (except Brain) into a workspace.
// Called alongside ensureBrainMember on workspace open.
func (s *Server) ensureBuiltinAgents(slug string) error {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return err
	}

	for _, ba := range brain.BuiltinAgents {
		// Ensure member row
		var memberExists int
		err = wdb.DB.QueryRow("SELECT COUNT(*) FROM members WHERE id = ?", ba.MemberID).Scan(&memberExists)
		if err != nil {
			continue
		}
		if memberExists == 0 {
			agentColor := assignMemberColor(wdb)
			_, err = wdb.DB.Exec(
				"INSERT INTO members (id, display_name, role, color) VALUES (?, ?, ?, ?)",
				ba.MemberID, ba.Name, "agent", agentColor,
			)
			if err != nil {
				logger.WithCategory(logger.CatAgent).Warn().Str("workspace", slug).Str("agent", ba.ID).Err(err).Msg("failed to create member")
			}
		}

		// Ensure agent row
		var agentExists int
		err = wdb.DB.QueryRow("SELECT COUNT(*) FROM agents WHERE id = ?", ba.ID).Scan(&agentExists)
		if err != nil {
			continue
		}
		if agentExists > 0 {
			// Update model and tools if they changed
			if ba.Model != "" {
				_, _ = wdb.DB.Exec("UPDATE agents SET model = ? WHERE id = ? AND is_system = 1", ba.Model, ba.ID)
			}
			if len(ba.Tools) > 0 {
				if b, err := json.Marshal(ba.Tools); err == nil {
					_, _ = wdb.DB.Exec("UPDATE agents SET tools = ? WHERE id = ? AND is_system = 1", string(b), ba.ID)
				}
			}
		} else {
			toolsJSON := "[]"
			if len(ba.Tools) > 0 {
				if b, err := json.Marshal(ba.Tools); err == nil {
					toolsJSON = string(b)
				}
			}
			_, err = wdb.DB.Exec(`
				INSERT INTO agents (id, name, description, avatar, role, goal, backstory, instructions,
					model, temperature, max_tokens, tools, channels, knowledge_access, memory_access, can_delegate,
					max_iterations, constraints, escalation_prompt, trigger_type, is_system, is_active, created_by)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0.7, 4096, ?, '[]', ?, ?, 0,
					10, ?, '', 'mention', 1, 1, 'system')`,
				ba.ID, ba.Name, ba.Role, ba.Avatar, ba.Role, ba.Goal, ba.Backstory, ba.Instructions,
				ba.Model, toolsJSON, ba.KnowledgeAccess, ba.MemoryAccess, ba.Constraints,
			)
			if err != nil {
				logger.WithCategory(logger.CatAgent).Warn().Str("workspace", slug).Str("agent", ba.ID).Err(err).Msg("failed to seed agent")
			}
		}

		// Seed agent skills from embedded FS
		if err := brain.SeedAgentSkills(s.cfg.DataDir, slug, ba.ID, ba.SkillsFS, ba.SkillsDir); err != nil {
			logger.WithCategory(logger.CatAgent).Warn().Str("workspace", slug).Str("agent", ba.ID).Err(err).Msg("failed to seed skills")
		}

		// Also seed shared skills
		if err := brain.SeedSharedSkills(s.cfg.DataDir, slug, ba.ID); err != nil {
			logger.WithCategory(logger.CatAgent).Warn().Str("workspace", slug).Str("agent", ba.ID).Err(err).Msg("failed to seed shared skills")
		}
	}

	return nil
}

// handleBrainMention is called when a message contains @Brain.
// It runs asynchronously — spawns a goroutine to generate and send the response.
func (s *Server) handleBrainMention(slug, channelID, senderName, content string) {
	go func() {
		apiKey, model := s.getBrainSettings(slug)
		if apiKey == "" {
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Msg("no API key configured, ignoring mention")
			return
		}

		// Broadcast thinking state
		s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "")
		defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "")

		brainDir := brain.BrainDir(s.cfg.DataDir, slug)
		systemPrompt, err := brain.BuildSystemPrompt(brainDir)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("failed to build system prompt")
			return
		}

		wdb, err := s.ws.Open(slug)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("workspace error")
			return
		}

		// Append active memories to system prompt
		memoryContext := brain.BuildMemoryContext(wdb.DB)
		if memoryContext != "" {
			systemPrompt += "\n\n---\n\n" + memoryContext
		}

		messages := s.getRecentMessages(wdb, channelID, 20)

		client := brain.NewClient(apiKey, model)
		response, err := client.Complete(systemPrompt, messages)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("LLM error")
			s.sendBrainMessage(slug, channelID, "Sorry, I encountered an error. Check that the API key is configured correctly.")
			return
		}

		response = strings.TrimSpace(response)
		if response == "" {
			return
		}

		s.sendBrainMessage(slug, channelID, response)
	}()
}

// sendBrainMessage saves a message from Brain and broadcasts it.
func (s *Server) sendBrainMessage(slug, channelID, content string, toolsUsed ...string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	msgID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	metadata := "{}"
	if len(toolsUsed) > 0 {
		metaJSON, _ := json.Marshal(map[string]any{"tools_used": toolsUsed})
		metadata = string(metaJSON)
	}

	_, err = wdb.DB.Exec(
		"INSERT INTO messages (id, channel_id, sender_id, content, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		msgID, channelID, brain.BrainMemberID, content, metadata, now,
	)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("failed to save message")
		return
	}

	h := s.hubs.Get(slug)
	h.Broadcast(channelID, hub.MakeEnvelope(hub.TypeMessageNew, hub.MessageNewPayload{
		ID:         msgID,
		ChannelID:  channelID,
		SenderID:   brain.BrainMemberID,
		SenderName: brain.BrainName,
		Content:    content,
		CreatedAt:  now,
		ToolsUsed:  toolsUsed,
	}), "")
}

// getRecentMessages fetches recent channel messages formatted for the LLM.
func (s *Server) getRecentMessages(wdb *db.WorkspaceDB, channelID string, limit int) []brain.Message {
	rows, err := wdb.DB.Query(`
		SELECT m.sender_id, COALESCE(mem.display_name, m.sender_id), m.content
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
		var senderID, senderName, content string
		if err := rows.Scan(&senderID, &senderName, &content); err != nil {
			continue
		}
		// Strip base64 image data and truncate long messages to prevent token overflow
		origLen := len(content)
		content = stripBase64Images(content)
		if len(content) > 4000 {
			content = content[:4000] + "\n[...truncated]"
		}
		if origLen > 10000 {
			logger.WithCategory(logger.CatBrain).Info().Str("sender", senderName).Int("origLen", origLen).Int("strippedLen", len(content)).Msg("truncated large message")
		}
		role := "user"
		if senderID == brain.BrainMemberID {
			role = "assistant"
		}
		msgs = append(msgs, brain.Message{
			Role:    role,
			Content: content,
			Name:    senderName,
		})
	}

	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs
}

// stripBase64Images removes base64-encoded image data from message content,
// replacing it with a short placeholder to prevent token overflow.
func stripBase64Images(content string) string {
	// Strip data:image/... base64 inline images
	for {
		idx := strings.Index(content, "data:image/")
		if idx < 0 {
			break
		}
		// Find the end of the base64 data (next whitespace, quote, or paren)
		end := idx + 11
		for end < len(content) {
			c := content[end]
			if c == ' ' || c == '\n' || c == '"' || c == ')' || c == '\'' || c == '`' || c == ']' {
				break
			}
			end++
		}
		content = content[:idx] + "[image]" + content[end:]
	}
	return content
}

// getBrainSettings reads the API key and model from workspace settings.
func (s *Server) getBrainSettings(slug string) (apiKey, model string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "", ""
	}

	model = "anthropic/claude-sonnet-4" // default
	_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'api_key'").Scan(&apiKey)
	_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'model'").Scan(&model)
	return apiKey, model
}

// getGeminiAPIKey reads the Gemini API key from workspace settings.
func (s *Server) getGeminiAPIKey(slug string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}
	var key string
	_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'gemini_api_key'").Scan(&key)
	return key
}

// getImageModel reads the image generation model from workspace settings.
func (s *Server) getImageModel(slug string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return brain.DefaultGeminiImageModel
	}
	var model string
	if wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'image_model'").Scan(&model) != nil || model == "" {
		return brain.DefaultGeminiImageModel
	}
	return model
}

// handleGetBrainSettings returns brain settings (admin only).
func (s *Server) handleGetBrainSettings(w http.ResponseWriter, r *http.Request) {
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

	settings := map[string]string{
		"model":                "anthropic/claude-sonnet-4",
		"image_model":          brain.DefaultGeminiImageModel,
		"memory_model":         "openai/gpt-4o-mini",
		"memory_enabled":       "true",
		"extraction_frequency": "15",
	}

	rows, err := wdb.DB.Query("SELECT key, value FROM brain_settings")
	if err != nil {
		writeJSON(w, http.StatusOK, settings)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			if k == "api_key" {
				// Mask API key
				if len(v) > 8 {
					settings["api_key_masked"] = v[:4] + "..." + v[len(v)-4:]
				}
				settings["api_key_set"] = "true"
			} else if k == "gemini_api_key" {
				if len(v) > 8 {
					settings["gemini_api_key_masked"] = v[:4] + "..." + v[len(v)-4:]
				}
				settings["gemini_api_key_set"] = "true"
			} else {
				settings[k] = v
			}
		}
	}

	writeJSON(w, http.StatusOK, settings)
}

// handleUpdateBrainSettings updates brain settings (admin only).
func (s *Server) handleUpdateBrainSettings(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req map[string]string
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	allowedKeys := map[string]bool{
		"api_key": true, "gemini_api_key": true, "model": true, "image_model": true,
		"memory_enabled": true, "extraction_frequency": true, "memory_model": true,
		// Integrations
		"webhook_autonomy": true,
		"email_enabled": true, "email_autonomy": true, "email_reply_scope": true,
		"email_outbound_host": true, "email_outbound_port": true,
		"email_outbound_user": true, "email_outbound_pass": true,
		"telegram_bot_token": true, "telegram_autonomy": true,
	}
	// Allow built-in agent toggles
	for _, ba := range brain.BuiltinAgents {
		allowedKeys["builtin_agent_"+ba.ID+"_enabled"] = true
	}

	for k, v := range req {
		if !allowedKeys[k] {
			continue
		}
		_, err = wdb.DB.Exec(
			"INSERT INTO brain_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?",
			k, v, v,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save settings")
			return
		}

		// Sync built-in agent active state when toggle changes
		for _, ba := range brain.BuiltinAgents {
			if k == "builtin_agent_"+ba.ID+"_enabled" {
				active := 1
				if v == "false" {
					active = 0
				}
				_, _ = wdb.DB.Exec("UPDATE agents SET is_active = ? WHERE id = ? AND is_system = 1", active, ba.ID)
			}
		}

		// Auto-register Telegram webhook when bot token is saved
		if k == "telegram_bot_token" && v != "" {
			s.onTelegramBotTokenSaved(slug, v)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListActions returns recent brain action log entries (admin only).
func (s *Server) handleListActions(w http.ResponseWriter, r *http.Request) {
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

	actions, err := brain.ListActions(wdb.DB, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if actions == nil {
		actions = []brain.ActionLog{}
	}

	total := brain.CountActions(wdb.DB)
	writeJSON(w, http.StatusOK, map[string]any{
		"actions": actions,
		"total":   total,
	})
}

// handleGetBrainDefinition returns a brain definition file.
func (s *Server) handleGetBrainDefinition(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileName := r.PathValue("file")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	allowedFiles := map[string]bool{
		"SOUL.md": true, "INSTRUCTIONS.md": true,
		"TEAM.md": true, "MEMORY.md": true, "HEARTBEAT.md": true,
	}
	if !allowedFiles[fileName] {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	path := filepath.Join(brainDir, fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"file": fileName, "content": string(data)})
}

// handleUpdateBrainDefinition updates a brain definition file (admin only).
func (s *Server) handleUpdateBrainDefinition(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileName := r.PathValue("file")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	allowedFiles := map[string]bool{
		"SOUL.md": true, "INSTRUCTIONS.md": true,
		"TEAM.md": true, "MEMORY.md": true, "HEARTBEAT.md": true,
	}
	if !allowedFiles[fileName] {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	path := filepath.Join(brainDir, fileName)
	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save file")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
