package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/brain3"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/metrics"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// brain3Settings adapts the Server's existing getBrainSetting helper plus a
// direct-SQL writer to brain3.SettingsStore. Kept tiny to avoid leaking any
// brain3 concerns into the broader Server type.
type brain3Settings struct {
	s    *Server
	slug string
}

func (a brain3Settings) Get(slug, key string) string {
	return a.s.getBrainSetting(slug, key)
}

func (a brain3Settings) Set(slug, key, value string) error {
	wdb, err := a.s.ws.Open(slug)
	if err != nil {
		return err
	}
	_, err = wdb.DB.Exec(
		"INSERT INTO brain_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	)
	return err
}

// handleBrainV3 is the Brain v3 handler — Claude Managed Agents-backed.
// Called from ws.go when brain_version == "v3". Mirrors handleBrainV2's
// structure (semaphore, staleness, thinking-state broadcast, context
// assembly, response send, action log) so v3 traces light up the existing
// observatory automatically.
//
// Phase 1 scope: provisions an Anthropic environment + agent on first run
// per workspace, stores the IDs in brain_settings, and returns a placeholder
// response. Session lookup/create + streaming come in Phase 2 / 3.
func (s *Server) handleBrainV3(slug, channelID, parentID, senderName, content string, messageTime time.Time) {
	go func() {
		// Acquire semaphore (same pool as v1/v2)
		select {
		case s.agentSem <- struct{}{}:
			defer func() { <-s.agentSem }()
		default:
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Msg("v3: queuing (semaphore full)")
			s.agentSem <- struct{}{}
			defer func() { <-s.agentSem }()
		}

		// Skip stale messages — match v2's policy.
		if messageTime.Before(s.bootedAt) {
			return
		}
		threshold := 10 * time.Minute
		if parentID != "" {
			threshold = 5 * time.Minute
		}
		if time.Since(messageTime) > threshold {
			logger.WithCategory(logger.CatBrain).Debug().Str("workspace", slug).Msg("v3: skipping stale message")
			return
		}

		// Broadcast thinking state.
		s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "", parentID)
		defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "", parentID)

		// Assemble system prompt — exactly as v2 does. The prompt is captured
		// at agent-create time inside brain3.EnsureProvisioned; subsequent
		// turns reuse the agent and do not re-send it.
		brainDir := brain.BrainDir(s.cfg.DataDir, slug)
		systemPrompt, err := brain.BuildSystemPrompt(brainDir)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("v3: failed to build prompt")
			return
		}

		wdb, err := s.ws.Open(slug)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("v3: failed to open workspace db")
			return
		}

		apiKey := s.getBrainSetting(slug, "api_key")
		systemPrompt = s.buildContextForMode(slug, wdb, channelID, parentID, content, senderName, apiKey, brainDir, systemPrompt)

		// Recent conversation — Phase 2 will use these as the seed user message
		// when creating a new session for a (channel, parent) pair.
		messages := s.getThreadOrChannelMessages(wdb, channelID, parentID, 40)

		// Tool catalog — same set v1/v2 use. Bridged to Anthropic custom tools
		// inside brain3.ConvertTools.
		allTools := s.getAllTools(slug)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// brain3.TraceCollector — writes to the same observatory tables v2 uses
		// (brain_traces / brain_trace_steps) so the existing UI lights up
		// for v3 with brain_version='v3' as the discriminator.
		trace := brain3.NewTraceCollector()

		// Pre-create an empty brain message so the streaming UI has a target
		// to append deltas to. The final content gets written via UPDATE +
		// message.edited broadcast at the end.
		streamMsgID := s.createEmptyBrainMessage(slug, channelID, parentID)

		result := brain3.Run(ctx, brain3.PipelineConfig{
			Slug:         slug,
			ChannelID:    channelID,
			ParentID:     parentID,
			SenderName:   senderName,
			Content:      content,
			SystemPrompt: systemPrompt,
			Messages:     messages,
			AllTools:     allTools,
			Settings:     brain3Settings{s: s, slug: slug},
			DB:           wdb.DB,
			ExecuteTool:  s.executeTool,
			Trace:        trace,
			OnTextDelta: func(delta string) {
				if streamMsgID == "" {
					return
				}
				s.broadcastBrainChunk(slug, channelID, parentID, streamMsgID, delta)
			},
		})

		if result.Response == "" {
			result.Response = "I processed your request but couldn't generate a response."
		}

		// Finalize the streaming message: write final content + broadcast
		// message.edited so any client that joined mid-stream catches up.
		// If pre-create failed, fall back to a fresh sendBrainMessage.
		var msgID string
		if streamMsgID != "" {
			s.finalizeBrainMessage(slug, channelID, parentID, streamMsgID, result.Response, result.ToolsUsed)
			msgID = streamMsgID
		} else {
			msgID = s.sendBrainMessage(slug, channelID, parentID, result.Response)
		}

		actionID := id.New()
		brain.LogAction(wdb.DB, actionID, "brain_v3", channelID, content, result.Response, brain3.DefaultModel, result.ToolsUsed)

		// Persist the trace to the existing observatory tables so v3 turns
		// show up alongside v2 with brain_version='v3'.
		errMsg := ""
		if !result.Metrics.Success {
			errMsg = result.Response
		}
		traceID := id.New()
		if err := trace.FlushToDB(wdb.DB, brain3.TraceRecord{
			ID:             traceID,
			ActionLogID:    actionID,
			BrainVersion:   brain3.VersionTag,
			ChannelID:      channelID,
			SenderName:     senderName,
			TriggerText:    content,
			Model:          s.getBrainSetting(slug, "mga_provisioned_model", brain3.DefaultModel),
			TotalLatencyMs: result.Metrics.TotalLatency.Milliseconds(),
			ExecLatencyMs:  result.Metrics.StreamMs, // closest analog to v2's "exec" — time spent on the agent loop
			SynthLatencyMs: result.Metrics.PreloadMs + result.Metrics.SessionMs,
			ToolCalls:      result.Metrics.ToolCalls,
			LLMCalls:       result.Metrics.LLMCalls,
			InputTokens:    result.Metrics.InputTokens,
			OutputTokens:   result.Metrics.OutputTokens,
			CostUSD:        result.Metrics.CostUSD,
			Success:        result.Metrics.Success,
			ErrorMessage:   errMsg,
		}); err != nil {
			logger.WithCategory(logger.CatBrain).Warn().Err(err).Msg("v3: failed to persist trace")
		}

		s.trackMessageAndMaybeExtract(slug, channelID, msgID, result.Response, brain.BrainName)

		// Phase 1.3: dual-write each /decisions/*.md to brain_memories so the
		// existing memory panel UI shows v3's contributions. Tagged
		// source='v3' + type='decision' so v1/v2 queries can filter as needed.
		// Best-effort — turn already succeeded, don't block on this.
		if len(result.DecisionWrites) > 0 {
			s.persistV3DecisionsToBrainMemories(slug, channelID, msgID, senderName, result.DecisionWrites)
		}

		metrics.MessagesTotal.WithLabelValues(slug).Inc()

		logger.WithCategory(logger.CatBrain).Info().
			Str("workspace", slug).
			Str("version", brain3.VersionTag).
			Str("agent", result.Metrics.AgentID).
			Int64("provision_ms", result.Metrics.ProvisionMs).
			Dur("total", result.Metrics.TotalLatency).
			Bool("success", result.Metrics.Success).
			Msg("brain v3 complete")
	}()
}

// createEmptyBrainMessage inserts a Brain-authored message with empty content
// and broadcasts message.new so streaming-aware clients can render an empty
// bubble that grows as deltas arrive. Returns the new message ID, or "" on
// error (caller falls back to the non-streaming sendBrainMessage path).
func (s *Server) createEmptyBrainMessage(slug, channelID, parentID string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}

	msgID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)
	metadata := "{}"

	if parentID != "" {
		_, err = wdb.DB.Exec(
			"INSERT INTO messages (id, channel_id, sender_id, content, metadata, parent_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			msgID, channelID, brain.BrainMemberID, "", metadata, parentID, now,
		)
	} else {
		_, err = wdb.DB.Exec(
			"INSERT INTO messages (id, channel_id, sender_id, content, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			msgID, channelID, brain.BrainMemberID, "", metadata, now,
		)
	}
	if err != nil {
		logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("v3: failed to pre-create streaming message")
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
		Content:    "",
		CreatedAt:  now,
		ParentID:   parentID,
	}), "")
	return msgID
}

// finalizeBrainMessage updates the streaming message's content + tools_used
// metadata and broadcasts message.edited so any late-joining client gets the
// final state. Idempotent — safe to call once at the end of a turn.
func (s *Server) finalizeBrainMessage(slug, channelID, parentID, msgID, content string, toolsUsed []string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	metadata := "{}"
	if len(toolsUsed) > 0 {
		if metaJSON, err := json.Marshal(map[string]any{"tools_used": toolsUsed}); err == nil {
			metadata = string(metaJSON)
		}
	}

	if _, err := wdb.DB.Exec(
		"UPDATE messages SET content = ?, metadata = ?, edited_at = ? WHERE id = ?",
		content, metadata, now, msgID,
	); err != nil {
		logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("v3: failed to finalize streaming message")
		return
	}

	h := s.hubs.Get(slug)
	h.Broadcast(channelID, hub.MakeEnvelope(hub.TypeMessageEdited, hub.MessageEditedPayload{
		MessageID: msgID,
		ChannelID: channelID,
		Content:   content,
		EditedAt:  now,
	}), "")
}

// broadcastBrainChunk sends an incremental text delta for a streaming message.
// Clients append `delta` to the message identified by msgID.
func (s *Server) broadcastBrainChunk(slug, channelID, parentID, msgID, delta string) {
	h := s.hubs.Get(slug)
	h.Broadcast(channelID, hub.MakeEnvelope(hub.TypeBrainChunk, hub.BrainChunkPayload{
		ChannelID: channelID,
		ParentID:  parentID,
		MessageID: msgID,
		Delta:     delta,
	}), "")
}

// handleResetV3Agent archives the current Anthropic agent and clears the
// persisted IDs so the next @Brain message provisions a fresh one with the
// current settings (model, template, tool catalog, skills).
//
// Environment + memory_store are intentionally NOT cleared — those are stable
// resources we want to reuse. Only the agent and its sessions reset.
//
// Sessions are also cleared because they reference the now-archived agent;
// existing sessions would 404 on next message. Clearing forces fresh
// per-(channel, parent_id) sessions on the next round.
//
// POST /api/workspaces/{slug}/brain/v3/reset-agent (admin only)
func (s *Server) handleResetV3Agent(w http.ResponseWriter, r *http.Request) {
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

	apiKey := s.getBrainSetting(slug, "anthropic_api_key")
	agentID := s.getBrainSetting(slug, "mga_agent_id")

	// Best-effort archive on Anthropic side — log but don't fail the reset
	// if it errors (e.g. agent already archived, key rotated).
	archived := false
	if apiKey != "" && agentID != "" {
		if client, err := brain3.NewClient(apiKey); err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			if _, err := client.Beta.Agents.Archive(ctx, agentID, anthropic.BetaAgentArchiveParams{}); err != nil {
				logger.WithCategory(logger.CatBrain).Warn().
					Str("workspace", slug).
					Str("agent_id", agentID).
					Err(err).
					Msg("v3: archive agent failed (best-effort, continuing reset)")
			} else {
				archived = true
			}
		}
	}

	// Clear the persisted agent IDs so EnsureProvisioned rebuilds.
	keysToClear := []string{
		"mga_agent_id",
		"mga_agent_version",
		"mga_provisioned_model",
		"mga_provisioned_template",
		"mga_provisioned_tools_hash", // populated once tool-drift detection lands
	}
	for _, k := range keysToClear {
		if _, err := wdb.DB.Exec("DELETE FROM brain_settings WHERE key = ?", k); err != nil {
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Str("key", k).Err(err).Msg("v3: failed to clear brain setting")
		}
	}

	// Clear sessions so they don't try to reference the archived agent.
	var clearedSessions int64
	if res, err := wdb.DB.Exec("DELETE FROM brain_managed_sessions"); err == nil {
		clearedSessions, _ = res.RowsAffected()
	}

	logger.WithCategory(logger.CatBrain).Info().
		Str("workspace", slug).
		Str("agent_id", agentID).
		Bool("archived", archived).
		Int64("cleared_sessions", clearedSessions).
		Msg("v3: agent reset")

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"archived":         archived,
		"prior_agent_id":   agentID,
		"cleared_sessions": clearedSessions,
	})
}

// persistV3DecisionsToBrainMemories dual-writes v3's /decisions/*.md entries
// into the existing brain_memories table so the workspace's memory panel UI
// surfaces them. v1/v2 queries can filter on `source='v3'` to include or
// exclude these as desired — the column is purely additive.
//
// Best-effort: errors are logged, never propagated. The decision file in
// memory_store remains the source of truth; brain_memories is a derived
// projection for UI rendering.
func (s *Server) persistV3DecisionsToBrainMemories(slug, channelID, msgID, senderName string, writes []brain3.DecisionWrite) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	for _, dw := range writes {
		title := extractDecisionTitle(dw.Content, dw.Path)
		metaJSON := buildDecisionMetadata(dw.Path)

		memID := id.New()
		_, err := wdb.DB.Exec(`
			INSERT INTO brain_memories
				(id, type, content, source_channel, source_message_id, source,
				 importance, confidence, summary, participants, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			memID,
			"decision",
			dw.Content,
			channelID,
			msgID,
			"v3",
			0.8,           // importance — decisions are intentionally pinned high
			1.0,           // confidence — Claude wrote this with status:decided
			title,
			senderName,
			metaJSON,
		)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Warn().
				Str("workspace", slug).
				Str("path", dw.Path).
				Err(err).
				Msg("v3: failed to dual-write decision to brain_memories")
		}
	}
}

// extractDecisionTitle pulls the first H1 heading from the decision-log
// markdown for use as the brain_memories.summary field. Falls back to a
// slugified path if the markdown didn't follow the skill's template.
func extractDecisionTitle(content, path string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	// Fallback: derive from filename.
	base := path
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	base = strings.TrimSuffix(base, ".md")
	return base
}

// buildDecisionMetadata returns a small JSON blob recording the source path
// in memory_store, so debugging tools can correlate the brain_memories row
// to the canonical file.
func buildDecisionMetadata(path string) string {
	b, err := json.Marshal(map[string]string{
		"memory_store_path": path,
		"origin":            "brain_v3_decision_log",
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}
