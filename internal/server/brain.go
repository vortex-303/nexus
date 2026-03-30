package server

import (
	"encoding/json"
	"fmt"
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
	} else {
		// Fix Brain's role if it was corrupted (e.g., by migration v34 simplify_roles)
		_, _ = wdb.DB.Exec("UPDATE members SET role = ? WHERE id = ? AND role != ?",
			brain.BrainRole, brain.BrainMemberID, brain.BrainRole)
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
				'', '', '', 0.7, 2048, '["create_task","list_tasks","search_workspace","create_document","search_knowledge","delegate_to_agent"]',
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
		ollamaEnabled := s.getBrainSetting(slug, "ollama_enabled") == "true"
		if apiKey == "" && s.getXAIKey(slug) == "" && !ollamaEnabled {
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

		// Inject North Star context
		if nsCtx := s.buildNorthStarContext(slug); nsCtx != "" {
			systemPrompt += "\n\n---\n\n" + nsCtx
		}

		// Tell Brain who it's talking to
		systemPrompt += fmt.Sprintf("\n\n## Current Conversation\nYou are talking to: **%s**. Address them by this name.", senderName)

		// Append active memories to system prompt
		memoryContext := brain.BuildMemoryContext(wdb.DB, content)
		if memoryContext != "" {
			systemPrompt += "\n\n---\n\n" + memoryContext
		}

		messages := s.getRecentMessages(wdb, channelID, 20)

		resolvedModel, fallbacks := s.resolveFreeAuto(model, slug)
		client := s.makeBrainClient(slug, apiKey, resolvedModel, fallbacks)
		response, usage, err := client.Complete(systemPrompt, messages)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("LLM error")
			s.sendBrainMessage(slug, channelID, "", "Sorry, I encountered an error. Check that the API key is configured correctly.")
			return
		}
		s.trackUsage(slug, usage, resolvedModel, "mention", channelID, senderName)

		response = strings.TrimSpace(response)
		if response == "" {
			return
		}

		s.sendBrainMessage(slug, channelID, "", response)
	}()
}

// sendBrainMessage saves a message from Brain and broadcasts it. Returns the message ID.
func (s *Server) sendBrainMessage(slug, channelID, parentID, content string, toolsUsed ...string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}

	msgID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	metadata := "{}"
	if len(toolsUsed) > 0 {
		metaJSON, _ := json.Marshal(map[string]any{"tools_used": toolsUsed})
		metadata = string(metaJSON)
	}

	if parentID != "" {
		_, err = wdb.DB.Exec(
			"INSERT INTO messages (id, channel_id, sender_id, content, metadata, parent_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			msgID, channelID, brain.BrainMemberID, content, metadata, parentID, now,
		)
	} else {
		_, err = wdb.DB.Exec(
			"INSERT INTO messages (id, channel_id, sender_id, content, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			msgID, channelID, brain.BrainMemberID, content, metadata, now,
		)
	}
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("failed to save message")
		return ""
	}

	if parentID != "" {
		wdb.DB.Exec("UPDATE messages SET reply_count = reply_count + 1, latest_reply_at = ? WHERE id = ?", now, parentID)
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
		ParentID:   parentID,
	}), "")
	return msgID
}

// ensureThreadContext creates or updates the thread_context row for a thread.
// Called before Brain/agent responds in a thread. No LLM call — just uses root message.
func (s *Server) ensureThreadContext(wdb *db.WorkspaceDB, channelID, parentID string) {
	if parentID == "" {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Check if row exists
	var exists int
	_ = wdb.DB.QueryRow("SELECT 1 FROM thread_context WHERE parent_id = ?", parentID).Scan(&exists)

	if exists == 0 {
		// Generate topic from root message (first sentence, max 120 chars)
		var senderName, rootContent string
		_ = wdb.DB.QueryRow(`
			SELECT COALESCE(mem.display_name, m.sender_id), m.content
			FROM messages m
			LEFT JOIN members mem ON mem.id = m.sender_id
			WHERE m.id = ?`, parentID).Scan(&senderName, &rootContent)

		topic := generateThreadTopic(senderName, rootContent)

		// Count participants and messages
		var participantCount, messageCount int
		_ = wdb.DB.QueryRow(
			"SELECT COUNT(DISTINCT sender_id) FROM messages WHERE (id = ? OR parent_id = ?) AND deleted = FALSE",
			parentID, parentID,
		).Scan(&participantCount)
		_ = wdb.DB.QueryRow(
			"SELECT COUNT(*) FROM messages WHERE (id = ? OR parent_id = ?) AND deleted = FALSE",
			parentID, parentID,
		).Scan(&messageCount)

		_, _ = wdb.DB.Exec(`
			INSERT INTO thread_context (parent_id, channel_id, topic, participant_count, message_count, last_activity_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			parentID, channelID, topic, participantCount, messageCount, now, now,
		)
	} else {
		// Update activity and counts
		s.updateThreadActivity(wdb, channelID, parentID)
	}
}

// generateThreadTopic creates a topic line from the root message content.
// Uses "sender: first sentence" format, truncated to 120 chars.
func generateThreadTopic(senderName, content string) string {
	// Strip @mentions for cleaner topic
	content = strings.TrimSpace(content)
	if content == "" {
		return senderName + ": (empty message)"
	}

	// Take first sentence (up to first period, newline, or 120 chars)
	firstLine := content
	if idx := strings.IndexAny(content, ".\n"); idx > 0 && idx < 120 {
		firstLine = content[:idx+1]
	}

	topic := senderName + ": " + firstLine
	if len(topic) > 120 {
		topic = topic[:117] + "..."
	}
	return topic
}

// updateThreadActivity updates last_activity_at and counts for a thread.
// Lightweight — called on every thread reply.
func (s *Server) updateThreadActivity(wdb *db.WorkspaceDB, channelID, parentID string) {
	if parentID == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)

	var participantCount, messageCount int
	_ = wdb.DB.QueryRow(
		"SELECT COUNT(DISTINCT sender_id) FROM messages WHERE (id = ? OR parent_id = ?) AND deleted = FALSE",
		parentID, parentID,
	).Scan(&participantCount)
	_ = wdb.DB.QueryRow(
		"SELECT COUNT(*) FROM messages WHERE (id = ? OR parent_id = ?) AND deleted = FALSE",
		parentID, parentID,
	).Scan(&messageCount)

	_, _ = wdb.DB.Exec(`
		UPDATE thread_context SET last_activity_at = ?, participant_count = ?, message_count = ?
		WHERE parent_id = ?`,
		now, participantCount, messageCount, parentID,
	)
}

// handleBrainWelcome sends a canned welcome DM from Brain to the requesting user.
// POST /api/workspaces/{slug}/brain/welcome
func (s *Server) handleBrainWelcome(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Find or create Brain DM channel for this user
	userID := claims.UserID
	ids := []string{brain.BrainMemberID, userID}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	dmName := "dm-" + ids[0] + "-" + ids[1]

	var channelID string
	err = wdb.DB.QueryRow("SELECT id FROM channels WHERE name = ? AND type = 'dm'", dmName).Scan(&channelID)
	if err != nil {
		// Create the DM channel
		channelID = id.New()
		_, err = wdb.DB.Exec(
			"INSERT INTO channels (id, name, type, created_by) VALUES (?, ?, 'dm', ?)",
			channelID, dmName, userID,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create DM channel")
			return
		}
	}

	welcome := "Hey! I'm **Brain**, your AI assistant for this workspace.\n\n" +
		"Here are some things I can help with:\n" +
		"- **@Brain** me in any channel to ask questions or get help\n" +
		"- Create and manage **tasks** and **documents**\n" +
		"- Search the web and **fetch URLs**\n" +
		"- Use **tools** from connected MCP servers\n" +
		"- Generate **images** with a simple prompt\n\n" +
		"Try typing something below to get started!"

	s.sendBrainMessage(slug, channelID, "", welcome)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "channel_id": channelID})
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

// getThreadOrChannelMessages fetches thread messages if parentID is set, otherwise channel messages.
func (s *Server) getThreadOrChannelMessages(wdb *db.WorkspaceDB, channelID, parentID string, limit int) []brain.Message {
	if parentID == "" {
		return s.getRecentMessages(wdb, channelID, limit)
	}
	// Fetch the parent message + all thread replies
	rows, err := wdb.DB.Query(`
		SELECT m.sender_id, COALESCE(mem.display_name, m.sender_id), m.content
		FROM messages m
		LEFT JOIN members mem ON mem.id = m.sender_id
		WHERE (m.id = ? OR m.parent_id = ?) AND m.deleted = FALSE
		ORDER BY m.created_at ASC
		LIMIT ?
	`, parentID, parentID, limit)
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
		content = stripBase64Images(content)
		if len(content) > 4000 {
			content = content[:4000] + "\n[...truncated]"
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

	model = FreeAutoModelID // default
	_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'api_key'").Scan(&apiKey)
	_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'model'").Scan(&model)
	return apiKey, model
}

// buildNorthStarContext reads North Star settings and returns a formatted markdown section.
// Returns empty string if no north_star is configured.
func (s *Server) buildNorthStarContext(slug string) string {
	goal := s.getBrainSetting(slug, "north_star")
	if goal == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("## North Star\n")
	b.WriteString("**Goal:** " + goal + "\n")

	if why := s.getBrainSetting(slug, "north_star_why"); why != "" {
		b.WriteString("**Why:** " + why + "\n")
	}
	if success := s.getBrainSetting(slug, "north_star_success"); success != "" {
		b.WriteString("**Success looks like:** " + success + "\n")
	}

	if themesJSON := s.getBrainSetting(slug, "strategic_themes"); themesJSON != "" {
		var themes []string
		if json.Unmarshal([]byte(themesJSON), &themes) == nil && len(themes) > 0 {
			b.WriteString("\n**Strategic Themes:**\n")
			for _, t := range themes {
				b.WriteString("- " + t + "\n")
			}
		}
	}

	b.WriteString("\nUse this direction to inform your reasoning. When suggesting tasks, answering strategic questions, or evaluating priorities, consider alignment with these themes.")
	return b.String()
}

// getXAIKey returns the xAI API key for a workspace, if configured.
func (s *Server) getXAIKey(slug string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}
	var key string
	_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = 'xai_api_key'").Scan(&key)
	return key
}

// brainCompleter is the interface that both brain.Client and brain.BridgeClient satisfy.
type brainCompleter interface {
	Complete(systemPrompt string, messages []brain.Message) (string, *brain.CompletionUsage, error)
	CompleteWithTools(systemPrompt string, messages []brain.Message, tools []brain.ToolDef) (string, []brain.ToolCall, *brain.CompletionUsage, error)
}

// makeBrainClient creates the appropriate LLM client for the resolved model.
// Priority: Ollama (bridge or direct) → xAI → OpenRouter.
func (s *Server) makeBrainClient(slug, apiKey, resolvedModel string, fallbacks []string) brainCompleter {
	// Check for Ollama mode first
	if s.getBrainSetting(slug, "ollama_enabled") == "true" {
		ollamaModel := s.getBrainSetting(slug, "ollama_model")
		if ollamaModel == "" {
			ollamaModel = "llama3.2"
		}
		// Check if bridge is connected (cloud-hosted)
		if v, ok := s.bridges.Load(slug); ok {
			bc := v.(*BridgeConn)
			return brain.NewBridgeClientFromConn(ollamaModel, bc.Forward)
		}
		// Direct mode (self-hosted, Ollama on same machine)
		ollamaURL := s.getBrainSetting(slug, "ollama_url", "http://localhost:11434")
		return brain.NewOllamaClient(ollamaURL, ollamaModel)
	}

	// If Grok engine is enabled (or key exists) with its own model, use xAI directly
	xaiKey := s.getXAIKey(slug)
	if xaiKey != "" {
		enabled := s.getBrainSetting(slug, "xai_enabled")
		if enabled == "true" || enabled == "" { // auto-enable if key exists
			if xaiModel := s.getBrainSetting(slug, "xai_model"); xaiModel != "" {
				return brain.NewXAIClient(xaiKey, xaiModel)
			}
		}
	}
	// Also route grok-* models selected in OpenRouter picker to xAI if key exists
	if brain.IsGrokModel(resolvedModel) {
		if xaiKey := s.getXAIKey(slug); xaiKey != "" {
			return brain.NewXAIClient(xaiKey, resolvedModel)
		}
	}
	client := brain.NewClient(apiKey, resolvedModel)
	client.FreeModelFallbacks = fallbacks
	return client
}

// resolveFreeAuto resolves the virtual free-auto model ID to a primary model and fallback list.
// If the model is not free-auto, returns the model unchanged with no fallbacks.
// Checks workspace-specific free models first, then falls back to global/defaults.
func (s *Server) resolveFreeAuto(model, slug string) (primaryModel string, fallbacks []string) {
	if model != FreeAutoModelID {
		return model, nil
	}
	freeModels := s.getWorkspaceFreeModels(slug)
	if len(freeModels) == 0 {
		return DefaultFreeModels[0].ID, nil
	}
	primary := freeModels[0].ID
	for _, m := range freeModels[1:] {
		fallbacks = append(fallbacks, m.ID)
	}
	return primary, fallbacks
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

// handleGetBrainSettings returns brain settings.
// All workspace members can read settings (needed for @Brain to work).
// Sensitive keys (api_key, gemini_api_key) are redacted for non-admins.
func (s *Server) handleGetBrainSettings(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	settings := map[string]string{
		"model":                 FreeAutoModelID,
		"image_model":           brain.DefaultGeminiImageModel,
		"memory_model":          "openai/gpt-4o-mini",
		"memory_enabled":        "true",
		"system_memory_enabled": "true",
		"extraction_frequency":  "15",
		"standard_chat_enabled": "true",
		"llm_enabled":           "true",
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
			} else if k == "xai_api_key" {
				if len(v) > 8 {
					settings["xai_api_key_masked"] = v[:4] + "..." + v[len(v)-4:]
				}
				settings["xai_api_key_set"] = "true"
			} else if k == "openai_api_key" {
				if len(v) > 8 {
					settings["openai_api_key_masked"] = v[:4] + "..." + v[len(v)-4:]
				}
				settings["openai_api_key_set"] = "true"
			} else if k == "brave_api_key" {
				if len(v) > 8 {
					settings["brave_api_key_masked"] = v[:4] + "..." + v[len(v)-4:]
				}
				settings["brave_api_key_set"] = "true"
			} else {
				settings[k] = v
			}
		}
	}

	// Include memory stats as extra keys in the flat response
	counts, _ := brain.CountMemories(wdb.DB)
	totalMemories := 0
	for _, c := range counts {
		totalMemories += c
	}
	settings["memory_total"] = fmt.Sprintf("%d", totalMemories)

	var lastExtraction string
	if wdb.DB.QueryRow("SELECT created_at FROM brain_action_log WHERE action_type = 'extraction' ORDER BY created_at DESC LIMIT 1").Scan(&lastExtraction) == nil {
		settings["last_extraction"] = lastExtraction
	}

	// Include bridge status
	if v, ok := s.bridges.Load(slug); ok {
		bc := v.(*BridgeConn)
		settings["bridge_connected"] = "true"
		if models := bc.Models(); len(models) > 0 {
			if b, err := json.Marshal(models); err == nil {
				settings["bridge_models"] = string(b)
			}
		}
	} else {
		settings["bridge_connected"] = "false"
	}

	// Non-admins: strip API key info entirely
	if claims.Role != "admin" {
		for _, k := range []string{"api_key_masked", "api_key_set", "gemini_api_key_masked", "gemini_api_key_set", "openai_api_key_masked", "openai_api_key_set", "brave_api_key_masked", "brave_api_key_set"} {
			delete(settings, k)
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
		"api_key": true, "gemini_api_key": true, "xai_api_key": true, "openai_api_key": true, "xai_model": true, "xai_enabled": true,
		"model": true, "image_model": true,
		"memory_enabled": true, "system_memory_enabled": true, "extraction_frequency": true, "memory_model": true, "memory_engine": true,
		"standard_chat_enabled": true, "llm_enabled": true,
		"webllm_enabled": true, "webllm_model": true, "webllm_system_prompt": true,
		"ollama_enabled": true, "ollama_model": true, "ollama_url": true,
		"north_star": true, "north_star_why": true, "north_star_success": true, "strategic_themes": true,
		"reflection_enabled": true, "reflection_time": true,
		// Integrations
		"webhook_autonomy": true,
		"email_enabled": true, "email_autonomy": true, "email_reply_scope": true,
		"email_outbound_host": true, "email_outbound_port": true,
		"email_outbound_user": true, "email_outbound_pass": true,
		"telegram_bot_token": true, "telegram_autonomy": true,
		"brave_api_key": true,
	}
	// Allow built-in agent toggles
	for _, ba := range brain.BuiltinAgents {
		allowedKeys["builtin_agent_"+ba.ID+"_enabled"] = true
	}

	// Sensitive keys — log "set"/"changed"/"cleared" instead of raw values
	sensitiveKeys := map[string]bool{
		"api_key": true, "gemini_api_key": true, "xai_api_key": true, "openai_api_key": true, "telegram_bot_token": true,
		"email_outbound_pass": true, "brave_api_key": true,
	}
	// Keys that also get logged to brain_action_log (visible in Activity tab)
	activityKeys := map[string]bool{
		"model": true, "image_model": true, "memory_model": true, "memory_enabled": true,
		"system_memory_enabled": true, "standard_chat_enabled": true, "llm_enabled": true,
	}

	for k, v := range req {
		if !allowedKeys[k] {
			continue
		}

		// Read old value before writing
		var oldValue string
		_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = ?", k).Scan(&oldValue)

		if oldValue == v {
			continue // no actual change
		}

		_, err = wdb.DB.Exec(
			"INSERT INTO brain_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?",
			k, v, v,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save settings")
			return
		}

		// Log to brain_settings_log
		logOld, logNew := oldValue, v
		if sensitiveKeys[k] {
			if oldValue == "" {
				logOld = ""
			} else {
				logOld = "(set)"
			}
			if v == "" {
				logNew = "(cleared)"
			} else {
				logNew = "(set)"
			}
		}
		_, _ = wdb.DB.Exec(
			"INSERT INTO brain_settings_log (id, key, old_value, new_value, changed_by) VALUES (?, ?, ?, ?, ?)",
			id.New(), k, logOld, logNew, claims.AccountID,
		)

		// Log model/memory changes to brain_action_log (shows in Activity tab)
		if activityKeys[k] {
			triggerText := k + ": " + logOld + " → " + logNew
			brain.LogAction(wdb.DB, id.New(), brain.ActionConfigChange, "", triggerText, "setting updated", "", nil)
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
		"TEAM.md": true, "MEMORY.md": true, "REFLECTIONS.md": true, "HEARTBEAT.md": true,
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
		"TEAM.md": true, "MEMORY.md": true, "REFLECTIONS.md": true, "HEARTBEAT.md": true,
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

// handleGetBrainPrompt returns the full system prompt for client-side LLM use.
// Accepts optional query params: ?channel_id=...&query=... for richer context.
func (s *Server) handleGetBrainPrompt(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	channelID := r.URL.Query().Get("channel_id")
	query := r.URL.Query().Get("query")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	systemPrompt, err := brain.BuildSystemPrompt(brainDir)
	if err != nil {
		systemPrompt = "You are Brain, a helpful AI assistant."
	}

	// Inject North Star context
	if nsCtx := s.buildNorthStarContext(slug); nsCtx != "" {
		systemPrompt += "\n\n---\n\n" + nsCtx
	}

	// Workspace context
	if wsContext := brain.BuildWorkspaceContext(wdb.DB); wsContext != "" {
		systemPrompt += "\n\n---\n\n" + wsContext
	}

	// Memories
	if memCtx := brain.BuildMemoryContext(wdb.DB, query); memCtx != "" {
		systemPrompt += "\n\n---\n\n" + memCtx
	}

	// Skills context
	skills := brain.LoadSkills(brainDir)
	s.applySkillEnabledState(slug, skills)
	skills = filterEnabledSkills(skills)
	if skillCtx := brain.BuildSkillContext(skills); skillCtx != "" {
		systemPrompt += "\n\n---\n\n" + skillCtx
	}

	// Knowledge base
	apiKey, _ := s.getBrainSettings(slug)
	if kbCtx := brain.BuildKnowledgeContext(wdb.DB, query, brain.SemanticOpts{
		VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
	}); kbCtx != "" {
		systemPrompt += "\n\n---\n\n" + kbCtx
	}

	// Channel history
	if channelID != "" {
		if chSummary := brain.BuildSingleChannelContext(wdb.DB, channelID); chSummary != "" {
			systemPrompt += "\n\n---\n\n" + chSummary
		}
		if crossCtx := brain.BuildCrossChannelContext(wdb.DB, channelID); crossCtx != "" {
			systemPrompt += "\n\n---\n\n" + crossCtx
		}
	}

	// Hard cap
	if len(systemPrompt) > 100000 {
		systemPrompt = systemPrompt[:100000]
	}

	writeJSON(w, http.StatusOK, map[string]string{"prompt": systemPrompt})
}

// handleSaveBrainMessage saves a message from Brain (sent by client-side LLM) and broadcasts it.
func (s *Server) handleSaveBrainMessage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	channelID := r.PathValue("channelId")

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Content == "" {
		writeError(w, http.StatusBadRequest, "content required")
		return
	}

	s.sendBrainMessage(slug, channelID, "", req.Content)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleWebLLMContext builds a pre-fetched context prompt for client-side WebLLM.
// Instead of multi-round tool calling, the server fetches all needed data based on
// client-detected intents and returns a single system prompt the LLM can complete against.
// POST /api/workspaces/{slug}/brain/webllm-context
func (s *Server) handleWebLLMContext(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var req struct {
		Message  string   `json:"message"`
		Intents  []string `json:"intents"`
		Channel  string   `json:"channel_id"`
		MaxChars int      `json:"max_chars"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		writeError(w, http.StatusBadRequest, "message required")
		return
	}
	if req.MaxChars <= 0 {
		req.MaxChars = 2500 // ~800 tokens, leaves room for messages + response in 4K context
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	var sections []string

	// 1. Identity: use custom WebLLM prompt if set, otherwise a dedicated default
	const defaultWebLLMPrompt = "You are Brain, the AI assistant for this Nexus workspace. You run locally in the user's browser.\n\n" +
		"## Your capabilities\n" +
		"You receive pre-fetched workspace data below: members, channels, tasks, documents, calendar events, memories, and search results. " +
		"Use this data to answer questions accurately. Cite specific items (task names, member names, dates) from the data.\n\n" +
		"## Response guidelines\n" +
		"- Be concise and direct (2-4 sentences for simple questions)\n" +
		"- When asked about tasks: reference status, assignee, due dates from the data\n" +
		"- When asked about people: reference their role and activity from the data\n" +
		"- When asked about events: reference dates, times, descriptions from the data\n" +
		"- When asked about documents: summarize relevant content from the data\n" +
		"- For search results: synthesize the key findings, cite sources\n" +
		"- If the data doesn't contain the answer, say so clearly — don't guess\n" +
		"- Use markdown formatting: **bold** for emphasis, bullet lists for multiple items\n" +
		"- Never mention that you're a local model or have limited context"
	if customPrompt := s.getBrainSetting(slug, "webllm_system_prompt"); customPrompt != "" {
		sections = append(sections, customPrompt)
	} else {
		sections = append(sections, defaultWebLLMPrompt)
	}

	// 1b. North Star context
	if nsCtx := s.buildNorthStarContext(slug); nsCtx != "" {
		sections = append(sections, nsCtx)
	}

	// 2. Compact workspace snapshot (channels, members, tasks, docs, events)
	if ws := brain.BuildWorkspaceContext(wdb.DB); ws != "" {
		if len(ws) > 1500 {
			ws = ws[:1500] + "\n[...]"
		}
		sections = append(sections, ws)
	}

	// 3. Intent-driven data fetching
	for _, intent := range req.Intents {
		var data string
		switch intent {
		case "web_search":
			data = s.toolWebSearch(slug, fmt.Sprintf(`{"query":%q,"num_results":5}`, req.Message))
		case "tasks":
			data = s.toolListTasks(slug, "{}")
		case "calendar":
			// 14 days ahead
			end := time.Now().Add(14 * 24 * time.Hour).UTC().Format(time.RFC3339)
			data = s.toolListCalendarEvents(slug, fmt.Sprintf(`{"end":%q}`, end))
		case "documents":
			data = s.toolSearchKnowledge(slug, fmt.Sprintf(`{"query":%q}`, req.Message))
		case "messages":
			data = s.toolSearchMessages(slug, req.Channel, fmt.Sprintf(`{"query":%q}`, req.Message))
		case "general":
			// For general queries, add recent channel history for conversational context
			if req.Channel != "" {
				if chCtx := brain.BuildSingleChannelContext(wdb.DB, req.Channel); chCtx != "" {
					if len(chCtx) > 600 {
						chCtx = chCtx[:600] + "\n[...]"
					}
					sections = append(sections, chCtx)
				}
			}
			continue
		default:
			continue
		}
		if data != "" {
			sections = append(sections, fmt.Sprintf("## %s results\n%s", intent, data))
		}
	}

	// 4. Top 10 memories (compact)
	if memories := brain.BuildMemoryContext(wdb.DB, req.Message); memories != "" {
		// Take top 10 lines max
		lines := strings.Split(memories, "\n")
		if len(lines) > 15 {
			lines = lines[:15]
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}

	// 5. Assemble and budget-truncate
	prompt := strings.Join(sections, "\n\n---\n\n")
	if len(prompt) > req.MaxChars {
		prompt = prompt[:req.MaxChars] + "\n[...truncated]"
	}

	writeJSON(w, http.StatusOK, map[string]string{"prompt": prompt})
}
