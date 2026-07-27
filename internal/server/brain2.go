package server

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/brain2"
	"github.com/nexus-chat/nexus/internal/hub"
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

// handleBrainV2 is the Brain pipeline handler: the self-correcting
// Execute → Synthesize loop over the shared tool/context/memory system.
// Since the v4 consolidation this is the only chat loop — the v1 fixed
// 2-round loop was retired into it.
func (s *Server) handleBrainV2(slug, channelID, parentID, senderName, content string, messageTime time.Time) {
	s.handleBrainV2Ex(slug, channelID, parentID, senderName, content, messageTime, nil)
}

// handleBrainV2Ex is handleBrainV2 plus completion tracking and model
// override. onComplete, if non-nil, receives the sent message ID + response
// when the turn finishes (task scheduler, external-reply integrations).
// modelOverride, if non-empty, replaces the workspace default model.
func (s *Server) handleBrainV2Ex(slug, channelID, parentID, senderName, content string, messageTime time.Time, onComplete TaskCompletionCallback, modelOverride ...string) {
	// Per-conversation busy lock (lifted from v3). Brain mimics human
	// turn-taking: one reply per (channel, parent_id) at a time, parallel
	// across conversations. If a turn is already in flight for this exact
	// conversation, drop this mention silently — the per-channel agent
	// indicator (broadcast by the in-flight turn) already signals Brain is
	// busy, so the sender knows. They can re-mention once it clears.
	busyKey := slug + "|" + channelID + "|" + parentID
	s.brainBusyMu.Lock()
	if s.brainBusy[busyKey] {
		s.brainBusyMu.Unlock()
		logger.WithCategory(logger.CatBrain).Info().
			Str("workspace", slug).
			Str("channel", channelID).
			Str("parent", parentID).
			Str("sender", senderName).
			Msg("dropping mention — turn already in flight in this conversation")
		if onComplete != nil {
			onComplete("", "", fmt.Errorf("conversation busy"))
		}
		return
	}
	s.brainBusy[busyKey] = true
	s.brainBusyMu.Unlock()

	go func() {
		defer func() {
			s.brainBusyMu.Lock()
			delete(s.brainBusy, busyKey)
			s.brainBusyMu.Unlock()
		}()

		// Completion tracking for task scheduler / external-reply callbacks
		var completionMsgID, completionResponse string
		var completionErr error
		defer func() {
			if onComplete != nil {
				onComplete(completionMsgID, completionResponse, completionErr)
			}
		}()

		// Acquire semaphore (shared pool with agents/ingest)
		select {
		case s.agentSem <- struct{}{}:
			defer func() { <-s.agentSem }()
		default:
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Msg("v2: queuing (semaphore full)")
			s.agentSem <- struct{}{}
			defer func() { <-s.agentSem }()
		}

		// Skip messages from before this server boot (e.g., after restart);
		// tiered staleness — threads are faster-paced than channel mentions.
		if messageTime.Before(s.bootedAt) {
			return
		}
		maxAge := maxBrainChannelAge
		if parentID != "" {
			maxAge = maxBrainThreadAge
		}
		if time.Since(messageTime) > maxAge {
			logger.WithCategory(logger.CatBrain).Debug().Str("workspace", slug).Dur("age", time.Since(messageTime)).Msg("v2: skipping stale message")
			return
		}
		metrics.AgentExecutionsTotal.WithLabelValues("Brain", "started").Inc()

		// Handle /search, /localsearch, and natural language web search
		// directly — zero-LLM, zero-cost bypasses (ported from v1).
		trimmed := strings.TrimSpace(content)
		if webQuery := extractWebSearchQuery(trimmed); webQuery != "" {
			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "tool_executing", humanToolLabel("web_search"), parentID)
			defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "", parentID)
			argsJSON := fmt.Sprintf(`{"query":%q}`, webQuery)
			result := s.toolWebSearch(slug, argsJSON)
			completionMsgID = s.sendBrainMessage(slug, channelID, parentID, result, "web_search")
			completionResponse = result
			return
		}
		if strings.HasPrefix(trimmed, "/localsearch ") {
			if query := strings.TrimSpace(strings.TrimPrefix(trimmed, "/localsearch")); query != "" {
				s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "tool_executing", humanToolLabel("search_workspace"), parentID)
				defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "", parentID)
				argsJSON := fmt.Sprintf(`{"query":%q}`, query)
				result := s.toolSearchMessages(slug, channelID, argsJSON)
				completionMsgID = s.sendBrainMessage(slug, channelID, parentID, result, "search_workspace")
				completionResponse = result
				return
			}
		}

		apiKey, model := s.getBrainSettings(slug)
		if len(modelOverride) > 0 && modelOverride[0] != "" {
			model = modelOverride[0]
		}

		// Parse `[persona:<slug>] ...` prefix for explicit /persona invocation
		// (ported from v1). Strip from content before downstream sees it; load
		// the persona row; apply its model override (persona wins); pass to
		// buildContextForModeWithPersona so the clean-swap path replaces the
		// polymorphic persona-context slot with the persona's operating
		// directive. Built-ins keep working via keyword backward compat when
		// no prefix is present.
		var activePersona *brain.Persona
		if personaSlug, rest := brain.ParsePersonaPrefix(content); personaSlug != "" {
			if wdbForPersona, err := s.ws.Open(slug); err == nil {
				p, perr := brain.LoadPersonaBySlug(wdbForPersona.DB, personaSlug)
				if perr == nil && p.Enabled {
					activePersona = &p
					content = rest // strip the prefix from the downstream content
					if p.Model != "" {
						model = p.Model
					}
					logger.WithCategory(logger.CatBrain).Info().
						Str("workspace", slug).
						Str("persona", p.Slug).
						Str("model", model).
						Msg("persona invoked via slash")
				} else {
					logger.WithCategory(logger.CatBrain).Warn().
						Str("workspace", slug).
						Str("persona", personaSlug).
						Err(perr).
						Msg("persona prefix parsed but not found or disabled; falling through")
				}
			}
		}

		ollamaEnabled := s.getBrainSetting(slug, "ollama_enabled") == "true"
		if apiKey == "" && s.getXAIKey(slug) == "" && !ollamaEnabled {
			errMsg := "I can answer search and stats queries without an API key. Try: \"search for X\", \"how many messages\", \"who is online\", \"list channels\". For general questions, configure an API key in Settings."
			completionMsgID = s.sendBrainMessage(slug, channelID, parentID, errMsg)
			completionResponse = errMsg
			completionErr = fmt.Errorf("no API key configured")
			return
		}

		// Broadcast thinking state
		s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "", parentID)
		defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "", parentID)

		// Build system prompt
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

		// Build context — memories, skills, knowledge, channel summaries.
		// activePersona non-nil triggers the clean-swap persona path.
		systemPrompt = s.buildContextForModeWithPersona(slug, wdb, channelID, parentID, content, senderName, apiKey, brainDir, systemPrompt, activePersona)

		// v2 additions: pinned memories, feedback, self-memories (always in context)
		systemPrompt += brain2.BuildPinnedMemoryContext(wdb.DB)
		systemPrompt += brain2.BuildFeedbackContext(wdb.DB)
		systemPrompt += brain2.BuildSelfMemoryContext(wdb.DB)

		// Get messages
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

		// Resolve model and create client
		resolvedModel, fallbacks := s.resolveFreeAuto(model, slug)
		client := s.makeBrainClient(slug, apiKey, resolvedModel, fallbacks)

		// Per-model output cap — large models were being clipped at the
		// legacy flat 2048 max_tokens.
		if orClient, ok := client.(*brain.Client); ok {
			orClient.MaxTokens = brain2.MaxTokensForModel(resolvedModel)
		}

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

		// Get all tools (built-in + MCP)
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
		senderID := s.resolveMemberIDByName(slug, senderName)
		wrappedExecute := func(slug2, channelID2, senderMemberID string, call brain.ToolCall) string {
			s.broadcastAgentState(slug2, channelID2, brain.BrainMemberID, brain.BrainName, "tool_executing", humanToolLabel(call.Function.Name), parentID)
			if senderMemberID == "" {
				senderMemberID = senderID
			}
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

		// Token streaming (lifted from v3, upgraded to real token SSE via
		// OpenRouter). The message row is created lazily on the FIRST delta —
		// that's what killed v3's streaming UX (empty bubbles when a turn
		// errored before producing text). Deltas append via brain.chunk;
		// finalizeBrainMessage below reconciles the streamed text with the
		// post-processed final response via message.edited.
		var streamMu sync.Mutex
		var streamMsgID string
		trace := brain2.NewTraceCollector()
		onTextDelta := func(delta string) {
			streamMu.Lock()
			if streamMsgID == "" {
				streamMsgID = s.createEmptyBrainMessage(slug, channelID, parentID)
			}
			msgID := streamMsgID
			streamMu.Unlock()
			if msgID != "" {
				s.broadcastBrainChunk(slug, channelID, parentID, msgID, delta)
			}
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
			Model:        resolvedModel,
			Trace:        trace,
			OnTextDelta:  onTextDelta,
		})

		if result.Response == "" {
			if friendly := brain2.FriendlyError(result.LastError); friendly != "" {
				result.Response = friendly
			} else {
				result.Response = "I processed your request but couldn't generate a response."
			}
			if result.LastError != "" {
				completionErr = fmt.Errorf("%s", result.LastError)
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

		// Send the response. If streaming already created the message row,
		// finalize it (content + tools metadata + message.edited broadcast);
		// otherwise send normally.
		var msgID string
		if streamMsgID != "" {
			s.finalizeBrainMessage(slug, channelID, parentID, streamMsgID, result.Response, result.ToolsUsed)
			msgID = streamMsgID
		} else {
			msgID = s.sendBrainMessage(slug, channelID, parentID, result.Response, result.ToolsUsed...)
		}
		completionMsgID = msgID
		completionResponse = result.Response

		// Track per-turn LLM spend + real token counts so the Console →
		// Cost subsection shows v2 turns with tokens, not just dollars.
		s.trackUsage(slug, &brain.CompletionUsage{
			PromptTokens:     result.InputTokens,
			CompletionTokens: result.OutputTokens,
			Cost:             result.CostUSD,
		}, resolvedModel, "brain_v2", channelID, senderName)

		// Log the action + flush the turn trace (brain_traces tables)
		actionID := id.New()
		brain.LogAction(wdb.DB, actionID, "brain_v2", channelID,
			content, result.Response, resolvedModel, result.ToolsUsed)
		if err := trace.FlushToDB(wdb.DB, brain2.TraceRecord{
			ID:             id.New(),
			ActionLogID:    actionID,
			BrainVersion:   "v2",
			ChannelID:      channelID,
			SenderName:     senderName,
			TriggerText:    content,
			Model:          resolvedModel,
			TotalLatencyMs: result.Metrics.TotalLatency.Milliseconds(),
			ExecLatencyMs:  result.Metrics.ExecLatency.Milliseconds(),
			SynthLatencyMs: result.Metrics.SynthLatency.Milliseconds(),
			ToolCalls:      len(result.ToolsUsed),
			LLMCalls:       result.Metrics.LLMCalls,
			InputTokens:    result.InputTokens,
			OutputTokens:   result.OutputTokens,
			CostUSD:        result.CostUSD,
			Success:        result.Metrics.Success,
			ErrorMessage:   result.LastError,
		}); err != nil {
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("trace flush failed")
		}

		// Track for memory extraction
		s.trackMessageAndMaybeExtract(slug, channelID, msgID, result.Response, brain.BrainName)

		// Async reflector — detects feedback, updates profiles, saves self-memories (requires automations)
		if s.getBrainSetting(slug, "automations_enabled") == "true" {
			go brain2.RunReflector(brain2.ReflectorConfig{
				DB:            wdb.DB,
				Client:        client,
				Slug:          slug,
				ChannelID:     channelID,
				SenderName:    senderName,
				SenderID:      senderID,
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

// createEmptyBrainMessage inserts an empty Brain message and broadcasts
// message.new so clients render the bubble that brain.chunk deltas fill.
// Returns "" on failure (callers fall back to non-streaming send).
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
		logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("failed to pre-create streaming message")
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
		logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("failed to finalize streaming message")
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

// humanToolLabels maps tool names to noun phrases that read naturally in
// the chat indicator's "{agentName} is using {label}..." pattern (lifted
// from v3). Anything not in the map falls through unchanged, so an MCP
// tool like "linear__create_issue" displays as itself — accurate, just
// less polished. generate_image is deliberately ABSENT: the frontend
// special-cases that raw name to render the large image-gen placeholder
// card, so it must pass through untranslated.
var humanToolLabels = map[string]string{
	"web_search":            "web search",
	"search_x":              "X search",
	"fetch_url":             "the URL fetcher",
	"list_social_pulses":    "the social pulse history",
	"get_social_pulse":      "social pulse data",
	"create_task":           "task creation",
	"list_tasks":            "the task list",
	"update_task":           "the task editor",
	"delete_task":           "task deletion",
	"create_document":       "the document creator",
	"search_messages":       "message search",
	"search_workspace":      "workspace search",
	"search_knowledge":      "the knowledge base",
	"trace_knowledge":       "the knowledge tracer",
	"create_calendar_event": "the calendar",
	"list_calendar_events":  "the calendar",
	"update_calendar_event": "the calendar",
	"delete_calendar_event": "the calendar",
	"send_email":            "email",
	"send_telegram":         "Telegram",
	"delegate_to_agent":     "another agent",
	"ask_agent":             "another agent",
	"save_memory":           "memory write",
	"recall_memory":         "memory recall",
}

// humanToolLabel returns a chat-friendly label for a tool name, so the
// indicator reads "Brain is using web search..." instead of the raw id.
func humanToolLabel(toolName string) string {
	if label, ok := humanToolLabels[toolName]; ok {
		return label
	}
	return toolName
}
