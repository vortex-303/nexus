package server

import (
	"context"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/brain3"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/metrics"
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

		// brain2.TraceCollector satisfies brain3.TraceRecorder via duck typing —
		// keeps brain3 free of any internal/brain2 import while v2 and v3
		// traces share the same observatory tables and UI.
		trace := brain3.NewTraceCollector()

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
		})

		if result.Response == "" {
			result.Response = "I processed your request but couldn't generate a response."
		}

		msgID := s.sendBrainMessage(slug, channelID, parentID, result.Response)

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
