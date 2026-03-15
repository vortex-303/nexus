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
	s.trackMessageAndMaybeExtract(slug, channelID, msgID, content, senderName)

	s.onPulse(slug, Pulse{
		Type: "integration.received", ActorID: senderID, ActorName: senderName,
		ChannelID: channelID, EntityID: msgID, Source: source,
		Summary: source + " received in channel",
	})

	// Autonomy check
	switch autonomy {
	case "never":
		return
	case "draft":
		// Brain responds in channel only — no external reply
		s.handleBrainMentionWithTools(slug, channelID, "", senderName, content, time.Now())
	case "autonomous":
		// Brain responds + calls replyFn to send back to external source
		s.handleBrainMentionWithReply(slug, channelID, senderName, content, replyFn)
	default:
		// Default to draft
		s.handleBrainMentionWithTools(slug, channelID, "", senderName, content, time.Now())
	}
}

// handleBrainMentionWithReply is like handleBrainMentionWithTools but captures
// the final response and calls onReply to send it back to the external source.
func (s *Server) handleBrainMentionWithReply(slug, channelID, senderName, content string, onReply func(string)) {
	messageTime := time.Now()
	if onReply == nil {
		// No reply function — fall back to normal mention
		s.handleBrainMentionWithTools(slug, channelID, "", senderName, content, messageTime)
		return
	}

	go func() {
		// Acquire semaphore to limit concurrent brain goroutines
		select {
		case s.agentSem <- struct{}{}:
			defer func() { <-s.agentSem }()
		default:
			s.agentSem <- struct{}{} // block until slot available
			defer func() { <-s.agentSem }()
		}

		// Skip stale messages that waited too long on the semaphore
		if time.Since(messageTime) > maxBrainChannelAge {
			logger.WithCategory(logger.CatBrain).Info().Dur("age", time.Since(messageTime)).Msg("skipping stale ingest message")
			return
		}

		// When workspace is Local LLM mode, external sources can't use WebLLM.
		// Try Standard Chat patterns; otherwise return a fallback message.
		webllmOnly := s.getBrainSetting(slug, "webllm_enabled") == "true" &&
			s.getBrainSetting(slug, "llm_enabled", "true") == "false"

		if webllmOnly {
			// Still try zero-LLM patterns (search, stats, lists)
			if wdb, err := s.ws.Open(slug); err == nil {
				if response, handled := s.tryZeroLLMResponse(slug, content, wdb.DB, senderName); handled {
					s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "")
					s.sendBrainMessage(slug, channelID, "", response)
					s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "")
					onReply(response)
					brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
						truncate(content, 200), truncate(response, 500), "zero-llm", nil)
					return
				}
			}
			fallback := "This workspace uses Local LLM — AI responses are only available in the browser. " +
				"Try commands like: **list tasks**, **search for** *something*, **workspace stats**."
			s.sendBrainMessage(slug, channelID, "", fallback)
			onReply(fallback)
			return
		}

		// Standard chat pre-filter disabled — all messages go to LLM
		// TODO: re-enable when standard chat patterns are production-ready

		// LLM-disabled gate removed — makeBrainClient handles xAI/OpenRouter routing

		apiKey, model := s.getBrainSettings(slug)
		ollamaEnabled := s.getBrainSetting(slug, "ollama_enabled") == "true"
		if apiKey == "" && s.getXAIKey(slug) == "" && !ollamaEnabled {
			s.sendBrainMessage(slug, channelID, "",
				"I can answer search and stats queries without an API key. Try: \"search for X\", \"how many messages\", \"who is online\". For general questions, configure an API key in Settings.")
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

		// Append workspace snapshot
		wsContext := brain.BuildWorkspaceContext(wdb.DB)
		if wsContext != "" {
			systemPrompt += "\n\n---\n\n" + wsContext
		}

		// Append memories
		memoryContext := brain.BuildMemoryContext(wdb.DB, content)
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
		resolvedModel, fallbacks := s.resolveFreeAuto(model, slug)
		client := s.makeBrainClient(slug, apiKey, resolvedModel, fallbacks)

		// First call with tools (built-in + MCP)
		allTools := s.getAllTools(slug)
		responseContent, toolCalls, ingestUsage, err := client.CompleteWithTools(systemPrompt, messages, allTools)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("LLM error")
			s.sendBrainMessage(slug, channelID, "", "Sorry, I encountered an error.")
			return
		}
		s.trackUsage(slug, ingestUsage, resolvedModel, "tools", channelID, "")

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
		finalResponse, ingestUsage2, err := client.Complete(systemPrompt, followUp)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Err(err).Str("workspace", slug).Msg("follow-up LLM error")
			if responseContent != "" {
				s.sendBrainMessage(slug, channelID, "", appendMissingImages(responseContent, imageRefs))
				onReply(responseContent)
			}
			return
		}
		s.trackUsage(slug, ingestUsage2, resolvedModel, "tools", channelID, "")

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
// If defaultVal is provided, it is returned when the key is not found.
func (s *Server) getBrainSetting(slug, key string, defaultVal ...string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		if len(defaultVal) > 0 {
			return defaultVal[0]
		}
		return ""
	}
	var val string
	if wdb.DB.QueryRow("SELECT value FROM brain_settings WHERE key = ?", key).Scan(&val) != nil || val == "" {
		if len(defaultVal) > 0 {
			return defaultVal[0]
		}
		return ""
	}
	return val
}
