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
		s.handleBrainV2(slug, channelID, "", senderName, content, time.Now())
	case "autonomous":
		// Brain responds + calls replyFn to send back to external source
		s.handleBrainMentionWithReply(slug, channelID, senderName, content, replyFn)
	default:
		// Default to draft
		s.handleBrainV2(slug, channelID, "", senderName, content, time.Now())
	}
}

// handleBrainMentionWithReply is like handleBrainV2 but captures the final
// response and calls onReply to send it back to the external source.
func (s *Server) handleBrainMentionWithReply(slug, channelID, senderName, content string, onReply func(string)) {
	messageTime := time.Now()
	if onReply == nil {
		// No reply function — fall back to normal mention
		s.handleBrainV2(slug, channelID, "", senderName, content, messageTime)
		return
	}

	// When workspace is Local LLM mode, external sources can't use WebLLM.
	// Try Standard Chat patterns; otherwise return a fallback message.
	webllmOnly := s.getBrainSetting(slug, "webllm_enabled") == "true" &&
		s.getBrainSetting(slug, "llm_enabled", "true") == "false"
	if webllmOnly {
		go func() {
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
		}()
		return
	}

	// LLM path: run the standard pipeline, forward the final response
	// to the external source when the turn succeeds.
	s.handleBrainV2Ex(slug, channelID, "", senderName, content, messageTime, func(_, response string, err error) {
		if err == nil && strings.TrimSpace(response) != "" {
			onReply(response)
		}
	})
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
