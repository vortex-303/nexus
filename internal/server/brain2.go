package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/brain2"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/metrics"
)

// looksLikeCreativeImageRequest returns true when the user message is most
// likely asking for an image (banner, ad, poster, etc.) AND looks like
// it's targeting the Creative Director persona — explicit /persona slash,
// the [persona:creative-director] prefix, or keyword overlap with the
// persona's trigger set.
//
// Used to set tool_choice="required" on the next LLM call so cheap MoE
// models can't skip generate_image and hallucinate an external URL.
func looksLikeCreativeImageRequest(content string) bool {
	lower := strings.ToLower(content)
	// Strong signal: explicit persona invocation
	if strings.Contains(lower, "[persona:creative-director]") {
		return true
	}
	// Otherwise: classic image-gen ask
	return looksLikeImageRequest(content)
}

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
		images := s.getRecentChannelImages(slug, wdb, channelID, messageTime.Add(-5*time.Minute), 3)
		if len(images) > 0 {
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("images", len(images)).Msg("v2: attaching images to message")
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == "user" {
					messages[i].Images = images
					break
				}
			}
		} else {
			logger.WithCategory(logger.CatBrain).Debug().Str("workspace", slug).Str("channel", channelID).Msg("v2: no recent images found")
		}

		// Resolve model and create client (reuses v1)
		model := s.getBrainSetting(slug, "model", "openai/gpt-4o-mini")
		resolvedModel, fallbacks := s.resolveFreeAuto(model, slug)
		client := s.makeBrainClient(slug, apiKey, resolvedModel, fallbacks)

		// Force tool_choice on the first LLM call when the user is clearly
		// asking for an image via Creative Director. Cheap MoE models
		// (DeepSeek V4 Flash, Qwen3.5-Flash on certain prompts) sometimes
		// skip generate_image and hallucinate external image URLs
		// (image.pollinations.ai, etc.). tool_choice="required" forces
		// the model to call SOME tool — the persona directive + tool
		// catalog make generate_image the obvious pick. Consumed once by
		// the next CompleteWithTools call, then cleared automatically.
		//
		// Only OpenRouter clients honor NextToolChoice. Ollama/xAI/bridge
		// clients ignore it (no-op via the type assertion).
		if looksLikeCreativeImageRequest(content) {
			if orClient, ok := client.(*brain.Client); ok {
				orClient.NextToolChoice = "required"
				logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Msg("forcing tool_choice=required for image request")
			}
		}

		// Runtime ground truth: tell the model what it is. Without this, when
		// the user switches models (e.g. Claude Sonnet 4.6 -> DeepSeek V4
		// Flash), the new model still parrots the old name from prior turns
		// in the channel because that's the only "model" reference it sees in
		// context. Naming the model explicitly anchors it.
		systemPrompt += fmt.Sprintf("\n\n---\n\n## Runtime\n\nYou are Brain, running on `%s` via OpenRouter for this turn. When asked which model or LLM you are, name this exactly. Do not quote earlier turns — Brain's underlying model can change between messages.\n", resolvedModel)
		// Capabilities — engine + service + MCP awareness, code-generated
		// per turn. Brain self-describes accurately instead of guessing.
		systemPrompt += s.BuildCapabilitiesSection(slug, "openrouter", resolvedModel)

		// Get all tools (reuses v1)
		allTools := s.getAllTools(slug)

		// Tool cheatsheet — Codebuff-style compact name(args) — desc list of
		// the available tools, in addition to the structured schemas the
		// model already sees via the OpenAI tools field. Cheap MoE models
		// (DeepSeek V4 Flash, Qwen, Gemma) follow a readable cheatsheet
		// noticeably better than parsing 18+ JSON-schema objects in
		// isolation. The OpenAI tools field stays the formal contract;
		// this is just the menu.
		if cheatsheet := brain.BuildToolCheatsheet(allTools); cheatsheet != "" {
			systemPrompt += "\n\n---\n\n" + cheatsheet
		}

		// Read max depth setting
		maxDepth := 5
		if v := s.getBrainSetting(slug, "tool_max_depth"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 10 {
				maxDepth = n
			}
		}

		// Per-turn cost cap. Default $1.00 — tuned for the V4 Flash default
		// where a normal multi-tool turn rings in well under $0.05; the cap
		// is a runaway-loop guardrail, not a typical-turn limit. Admins can
		// override via brain_settings.tool_max_cost_usd. Setting to 0
		// disables the cap entirely.
		maxCostUSD := 1.00
		if v := s.getBrainSetting(slug, "tool_max_cost_usd"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				maxCostUSD = f
			}
		}

		// Run the v2 pipeline. Wrap ExecuteTool so each tool dispatch
		// broadcasts a `tool_executing` agent state to the channel — the
		// frontend uses this to show progress (e.g. the prominent
		// "generating an image..." placeholder card when generate_image
		// fires). After the tool returns we flip back to `thinking` so
		// the next tool call (if any) gets its own indicator.
		//
		// Also captures image markdown + <image-prompt> blocks from
		// every generate_image tool result so we can re-append them
		// after the synthesizer LLM runs. v2's synthesizer otherwise
		// summarizes the tool output into prose and drops the
		// `![alt](url)` markdown — observed in production: Image Log
		// shows successful Gemini calls but Brain's reply contains no
		// image. Mirrors the appendMissingImages protection that's
		// long-existed in agent_runtime.go but never ported to brain2.
		var imageRefs []string
		var imagePromptTag string
		wrappedExecute := func(slug2, channelID2, senderMemberID string, call brain.ToolCall) string {
			s.broadcastAgentState(slug2, channelID2, brain.BrainMemberID, brain.BrainName, "tool_executing", call.Function.Name, parentID)
			out := s.executeTool(slug2, channelID2, senderMemberID, call)
			if call.Function.Name == "generate_image" {
				imageRefs = append(imageRefs, extractImageMarkdown(out)...)
				// Strip the <image-prompt> block from the tool result that
				// the synthesizer sees (otherwise the LLM echoes it inline),
				// stash it for re-attachment to the final response.
				cleaned, tag := extractImagePromptTag(out)
				if tag != "" {
					imagePromptTag = tag
					out = cleaned
				}
			}
			s.broadcastAgentState(slug2, channelID2, brain.BrainMemberID, brain.BrainName, "thinking", "", parentID)
			return out
		}
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
			MaxCostUSD:   maxCostUSD,
			ExecuteTool:  wrappedExecute,
		})

		if result.Response == "" {
			if friendly := brain2.FriendlyError(result.LastError); friendly != "" {
				result.Response = friendly
			} else {
				result.Response = "I processed your request but couldn't generate a response."
			}
		}

		// Strip hallucinated external image URLs (image.pollinations.ai,
		// api.dicebear.com, etc.) that cheap MoE models sometimes emit
		// instead of calling the generate_image tool. Real images go
		// through Gemini via toolGenerateImage; the response there
		// contains internal blob references that pass through unchanged.
		if cleaned, stripped := brain.SanitizeBrainResponse(result.Response); stripped > 0 {
			logger.WithCategory(logger.CatBrain).Warn().
				Str("workspace", slug).
				Int("stripped", stripped).
				Msg("stripped hallucinated external image URLs from Brain response")
			result.Response = cleaned
		}

		// Re-attach images that generate_image returned but the synthesizer
		// LLM dropped (or fabricated different URLs for). appendMissingImages
		// strips any /api/workspaces/ refs that aren't in our captured set
		// (kills synthesizer hallucinations) and appends any captured refs
		// that aren't already in the response. Without this, the user sees
		// Brain describing an image that "should be" there but no actual
		// image renders.
		if len(imageRefs) > 0 {
			before := result.Response
			result.Response = appendMissingImages(result.Response, imageRefs)
			if result.Response != before {
				logger.WithCategory(logger.CatBrain).Info().
					Str("workspace", slug).
					Int("images", len(imageRefs)).
					Msg("re-attached image markdown that synthesizer dropped")
			}
		}
		if imagePromptTag != "" {
			result.Response += imagePromptTag
		}

		// Send the response (reuses v1)
		msgID := s.sendBrainMessage(slug, channelID, parentID, result.Response)

		// Track per-turn LLM spend so the Console → Cost subsection shows
		// v2 turns alongside v3. The pipeline already accumulates cost from
		// each round's CompletionUsage; we just stamp it into llm_usage.
		s.trackUsage(slug, &brain.CompletionUsage{Cost: result.CostUSD}, resolvedModel, "brain_v2", channelID, senderName)

		// Log the action (reuses v1 action log)
		brain.LogAction(wdb.DB, id.New(), "brain_v2", channelID,
			content, result.Response, resolvedModel, result.ToolsUsed)

		// Track for memory extraction (reuses v1)
		s.trackMessageAndMaybeExtract(slug, channelID, msgID, result.Response, brain.BrainName)

		// Async reflector — detects feedback, updates profiles, saves self-memories (requires automations)
		if s.getBrainSetting(slug, "automations_enabled") == "true" {
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
		}

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
