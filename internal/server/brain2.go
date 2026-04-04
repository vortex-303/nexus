package server

import (
	"strconv"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/brain2"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/metrics"
)

// handleBrainV2 is the Brain 2.0 pipeline handler. It runs the Plan → Execute → Synthesize
// pipeline using the same tools, context, and memory system as v1.
// Called from ws.go when brain_version == "v2".
func (s *Server) handleBrainV2(slug, channelID, parentID, senderName, content string, messageTime time.Time) {
	go func() {
		// Acquire semaphore (same pool as v1)
		select {
		case s.agentSem <- struct{}{}:
			defer func() { <-s.agentSem }()
		default:
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Msg("v2: queuing (semaphore full)")
			s.agentSem <- struct{}{}
			defer func() { <-s.agentSem }()
		}

		// Skip stale messages
		if messageTime.Before(s.bootedAt) {
			return
		}
		threshold := 10 * time.Minute
		if parentID != "" {
			threshold = 5 * time.Minute
		}
		if time.Since(messageTime) > threshold {
			logger.WithCategory(logger.CatBrain).Debug().Str("workspace", slug).Msg("v2: skipping stale message")
			return
		}

		// Broadcast thinking state
		s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "", parentID)
		defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "", parentID)

		// Build system prompt (reuses v1)
		brainDir := brain.BrainDir(s.cfg.DataDir, slug)
		systemPrompt, err := brain.BuildSystemPrompt(brainDir)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("v2: failed to build prompt")
			return
		}

		wdb, err := s.ws.Open(slug)
		if err != nil {
			return
		}

		// Build context (reuses v1 — memories, skills, knowledge, channel summaries)
		apiKey := s.getBrainSetting(slug, "api_key")
		systemPrompt = s.buildContextForMode(slug, wdb, channelID, parentID, content, senderName, apiKey, brainDir, systemPrompt)

		// v2 additions: pinned memories, feedback, self-memories (always in context)
		systemPrompt += brain2.BuildPinnedMemoryContext(wdb.DB)
		systemPrompt += brain2.BuildFeedbackContext(wdb.DB)
		systemPrompt += brain2.BuildSelfMemoryContext(wdb.DB)

		// Get messages (reuses v1)
		messages := s.getThreadOrChannelMessages(wdb, channelID, parentID, 40)

		// Attach recent channel images to the last user message (vision support)
		if images := s.getRecentChannelImages(slug, wdb, channelID, messageTime.Add(-2*time.Minute), 3); len(images) > 0 {
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == "user" {
					messages[i].Images = images
					break
				}
			}
		}

		// Resolve model and create client (reuses v1)
		model := s.getBrainSetting(slug, "model", "openai/gpt-4o-mini")
		resolvedModel, fallbacks := s.resolveFreeAuto(model, slug)
		client := s.makeBrainClient(slug, apiKey, resolvedModel, fallbacks)

		// Get all tools (reuses v1)
		allTools := s.getAllTools(slug)

		// Read max depth setting
		maxDepth := 5
		if v := s.getBrainSetting(slug, "tool_max_depth"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 10 {
				maxDepth = n
			}
		}

		// Run the v2 pipeline
		result := brain2.Run(brain2.PipelineConfig{
			Slug:         slug,
			ChannelID:    channelID,
			ParentID:     parentID,
			SenderName:   senderName,
			Content:      content,
			SystemPrompt: systemPrompt,
			Messages:     messages,
			AllTools:     allTools,
			Client:       client,
			MaxDepth:     maxDepth,
			ExecuteTool:  s.executeTool,
		})

		if result.Response == "" {
			result.Response = "I processed your request but couldn't generate a response."
		}

		// Send the response (reuses v1)
		msgID := s.sendBrainMessage(slug, channelID, parentID, result.Response)

		// Log the action (reuses v1 action log)
		brain.LogAction(wdb.DB, id.New(), "brain_v2", channelID,
			content, result.Response, resolvedModel, result.ToolsUsed)

		// Track for memory extraction (reuses v1)
		s.trackMessageAndMaybeExtract(slug, channelID, msgID, result.Response, brain.BrainName)

		// Async reflector — detects feedback, updates profiles, saves self-memories
		go brain2.RunReflector(brain2.ReflectorConfig{
			DB:            wdb.DB,
			Slug:          slug,
			ChannelID:     channelID,
			SenderName:    senderName,
			SenderID:      "", // TODO: pass sender member ID when available
			UserMessage:   content,
			BrainResponse: result.Response,
			ToolsUsed:     result.ToolsUsed,
		})

		metrics.MessagesTotal.WithLabelValues(slug).Inc()

		logger.WithCategory(logger.CatBrain).Info().
			Str("workspace", slug).
			Str("version", "v2").
			Int("tools", result.Metrics.ToolCalls).
			Dur("total", result.Metrics.TotalLatency).
			Dur("plan", result.Metrics.PlanLatency).
			Dur("exec", result.Metrics.ExecLatency).
			Dur("synth", result.Metrics.SynthLatency).
			Bool("success", result.Metrics.Success).
			Msg("brain v2 complete")
	}()
}
