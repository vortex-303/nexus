package server

import (
	"log"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
)

// ingestExternalMessage is the shared entry point for all external adapters (webhooks, email, telegram).
// It saves the message to a channel, broadcasts it, and optionally triggers Brain based on autonomy level.
func (s *Server) ingestExternalMessage(slug, channelID, senderID, senderName, content, source, autonomy string, replyFn func(string)) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		log.Printf("[ingest:%s] workspace error: %v", slug, err)
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
		log.Printf("[ingest:%s] failed to save message: %v", slug, err)
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
		s.handleBrainMentionWithTools(slug, channelID, senderName, content)
	case "autonomous":
		// Brain responds + calls replyFn to send back to external source
		s.handleBrainMentionWithReply(slug, channelID, senderName, content, replyFn)
	default:
		// Default to draft
		s.handleBrainMentionWithTools(slug, channelID, senderName, content)
	}
}

// handleBrainMentionWithReply is like handleBrainMentionWithTools but captures
// the final response and calls onReply to send it back to the external source.
func (s *Server) handleBrainMentionWithReply(slug, channelID, senderName, content string, onReply func(string)) {
	if onReply == nil {
		// No reply function — fall back to normal mention
		s.handleBrainMentionWithTools(slug, channelID, senderName, content)
		return
	}

	go func() {
		apiKey, model := s.getBrainSettings(slug)
		if apiKey == "" {
			log.Printf("[brain:%s] no API key configured, ignoring mention", slug)
			return
		}

		// Broadcast thinking state
		s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "")
		defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "")

		brainDir := brain.BrainDir(s.cfg.DataDir, slug)
		systemPrompt, err := brain.BuildSystemPrompt(brainDir)
		if err != nil {
			log.Printf("[brain:%s] failed to build system prompt: %v", slug, err)
			return
		}

		wdb, err := s.ws.Open(slug)
		if err != nil {
			log.Printf("[brain:%s] workspace error: %v", slug, err)
			return
		}

		// Append memories
		memoryContext := brain.BuildMemoryContext(wdb.DB)
		if memoryContext != "" {
			systemPrompt += "\n\n---\n\n" + memoryContext
		}

		// Append skills
		skills := brain.LoadSkills(brainDir)
		skillContext := brain.BuildSkillContext(skills)
		if skillContext != "" {
			systemPrompt += "\n\n---\n\n" + skillContext
		}

		// Append knowledge base
		kbContext := brain.BuildKnowledgeContext(wdb.DB, content)
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

		// First call with tools
		responseContent, toolCalls, err := client.CompleteWithTools(systemPrompt, messages, brain.Tools)
		if err != nil {
			log.Printf("[brain:%s] LLM error: %v", slug, err)
			s.sendBrainMessage(slug, channelID, "Sorry, I encountered an error.")
			return
		}

		if len(toolCalls) == 0 {
			responseContent = strings.TrimSpace(responseContent)
			if responseContent != "" {
				s.sendBrainMessage(slug, channelID, responseContent)
				onReply(responseContent)
			}
			brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
				truncate(content, 200), truncate(responseContent, 500), model, nil)
			return
		}

		// Execute tools
		log.Printf("[brain:%s] executing %d tool calls", slug, len(toolCalls))
		assistantMsg := brain.Message{Role: "assistant", Content: responseContent, ToolCalls: toolCalls}
		followUp := append(messages, assistantMsg)

		var imageRefs []string
		for _, call := range toolCalls {
			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "tool_executing", call.Function.Name)
			result := s.executeTool(slug, channelID, call)
			log.Printf("[brain:%s] tool %s → %s", slug, call.Function.Name, truncate(result, 100))
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
			log.Printf("[brain:%s] follow-up LLM error: %v", slug, err)
			if responseContent != "" {
				s.sendBrainMessage(slug, channelID, appendMissingImages(responseContent, imageRefs))
				onReply(responseContent)
			}
			return
		}

		finalResponse = strings.TrimSpace(finalResponse)
		finalResponse = appendMissingImages(finalResponse, imageRefs)
		if finalResponse != "" {
			s.sendBrainMessage(slug, channelID, finalResponse)
			onReply(finalResponse)
		}

		var toolNames []string
		for _, call := range toolCalls {
			toolNames = append(toolNames, call.Function.Name)
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
