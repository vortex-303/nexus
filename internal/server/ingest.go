package server

import (
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// ingestExternalMessage is the shared entry point for all external adapters (webhooks, email, telegram).
// It saves the message to a channel, broadcasts it, and optionally triggers Brain based on autonomy level.
func (s *Server) ingestExternalMessage(slug, channelID, senderID, senderName, content, source, autonomy string, replyFn func(string)) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("workspace error during ingest")
		return
	}

	// Ensure sender member row exists
	_, _ = wdb.DB.Exec(
		"INSERT OR IGNORE INTO members (id, display_name, role) VALUES (?, ?, 'external')",
		senderID, senderName,
	)

	// Insert message
	msgID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = wdb.DB.Exec(
		"INSERT INTO messages (id, channel_id, sender_id, content, created_at) VALUES (?, ?, ?, ?, ?)",
		msgID, channelID, senderID, content, now,
	)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("failed to save ingest message")
		return
	}

	// Broadcast message.new
	h := s.hubs.Get(slug)
	h.Broadcast(channelID, hub.MakeEnvelope(hub.TypeMessageNew, hub.MessageNewPayload{
		ID:         msgID,
		ChannelID:  channelID,
		SenderID:   senderID,
		SenderName: senderName,
		Content:    content,
		CreatedAt:  now,
	}), "")

	// Track for memory extraction
	s.trackMessageAndMaybeExtract(slug, channelID)

	// Autonomy check
	switch autonomy {
	case "never":
		return
	case "draft":
		// Brain responds in channel only — no external reply
		s.handleBrainMentionWithTools(slug, channelID, "", senderName, content)
	case "autonomous":
		// Brain responds + calls replyFn to send back to external source
		s.handleBrainMentionWithReply(slug, channelID, senderName, content, replyFn)
	default:
		// Default to draft
		s.handleBrainMentionWithTools(slug, channelID, "", senderName, content)
	}
}

// handleBrainMentionWithReply is like handleBrainMentionWithTools but captures
// the final response and calls onReply to send it back to the external source.
func (s *Server) handleBrainMentionWithReply(slug, channelID, senderName, content string, onReply func(string)) {
	if onReply == nil {
		// No reply function — fall back to normal mention
		s.handleBrainMentionWithTools(slug, channelID, "", senderName, content)
		return
	}

	go func() {
		apiKey, model := s.getBrainSettings(slug)
		if apiKey == "" {
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Msg("no API key configured, ignoring mention")
			return
		}

		// Broadcast thinking state
		s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "")
		defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "")

		brainDir := brain.BrainDir(s.cfg.DataDir, slug)
		systemPrompt, err := brain.BuildSystemPrompt(brainDir)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("failed to build system prompt")
			return
		}

		wdb, err := s.ws.Open(slug)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("workspace error")
			return
		}

		// Append memories
		memoryContext := brain.BuildMemoryContext(wdb.DB)
		if memoryContext != "" {
			systemPrompt += "\n\n---\n\n" + memoryContext
		}

		// Append skills (role-gated + enabled filter)
		skills := brain.LoadSkills(brainDir)
		s.applySkillEnabledState(slug, skills)
		var senderRole string
		_ = wdb.DB.QueryRow("SELECT role FROM members WHERE LOWER(display_name) = LOWER(?)", senderName).Scan(&senderRole)
		skills = brain.FilterSkillsByRole(skills, senderRole)
		skills = filterEnabledSkills(skills)
		skillContext := brain.BuildSkillContext(skills)
		if skillContext != "" {
			systemPrompt += "\n\n---\n\n" + skillContext
		}

		// Append knowledge base
		kbContext := brain.BuildKnowledgeContext(wdb.DB, content, brain.SemanticOpts{
			VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
		})
		if kbContext != "" {
			systemPrompt += "\n\n---\n\n" + kbContext
		}

		// Append channel history
		if chSummary := brain.BuildSingleChannelContext(wdb.DB, channelID); chSummary != "" {
			systemPrompt += "\n\n---\n\n" + chSummary
		}

		// Cross-channel awareness
		if crossCtx := brain.BuildCrossChannelContext(wdb.DB, channelID); crossCtx != "" {
			systemPrompt += "\n\n---\n\n" + crossCtx
		}

		messages := s.getRecentMessages(wdb, channelID, 40)
		client := brain.NewClient(apiKey, model)

		// First call with tools (built-in + MCP)
		allTools := s.getAllTools(slug)
		responseContent, toolCalls, err := client.CompleteWithTools(systemPrompt, messages, allTools)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("LLM error")
			s.sendBrainMessage(slug, channelID, "", "Sorry, I encountered an error.")
			return
		}

		if len(toolCalls) == 0 {
			responseContent = strings.TrimSpace(responseContent)
			if responseContent != "" {
				s.sendBrainMessage(slug, channelID, "", responseContent)
				onReply(responseContent)
			}
			brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
				truncate(content, 200), truncate(responseContent, 500), model, nil)
			return
		}

		// Execute tools
		logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("count", len(toolCalls)).Msg("executing tool calls")
		assistantMsg := brain.Message{Role: "assistant", Content: responseContent, ToolCalls: toolCalls}
		followUp := append(messages, assistantMsg)

		var imageRefs []string
		for _, call := range toolCalls {
			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "tool_executing", call.Function.Name)
			result := s.executeTool(slug, channelID, "", call)
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("tool", call.Function.Name).Str("result", truncate(result, 100)).Msg("tool executed")
			imageRefs = append(imageRefs, extractImageMarkdown(result)...)
			followUp = append(followUp, brain.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ID,
			})
		}

		// Second call: final response
		finalResponse, err := client.Complete(systemPrompt, followUp)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("follow-up LLM error")
			if responseContent != "" {
				s.sendBrainMessage(slug, channelID, "", appendMissingImages(responseContent, imageRefs))
				onReply(responseContent)
			}
			return
		}

		finalResponse = strings.TrimSpace(finalResponse)
		finalResponse = appendMissingImages(finalResponse, imageRefs)

		var toolNames []string
		for _, call := range toolCalls {
			toolNames = append(toolNames, call.Function.Name)
		}

		if finalResponse != "" {
			s.sendBrainMessage(slug, channelID, "", finalResponse, toolNames...)
			onReply(finalResponse)
		}
		brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
			truncate(content, 200), truncate(finalResponse, 500), model, toolNames)
	}()
}

// getBrainSetting reads a single brain_settings value for a workspace.
func (s *Server) getBrainSetting(slug, key string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}
	var val string
	_ = wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = ?", key).Scan(&val)
	return val
}
