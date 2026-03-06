package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sync"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/metrics"
)

// generatedImagesFolderCache caches the folder ID per workspace slug.
var generatedImagesFolderCache sync.Map

// ensureGeneratedImagesFolder returns the folder ID for "Generated Images",
// creating it if it doesn't exist.
func (s *Server) ensureGeneratedImagesFolder(slug string) string {
	if cached, ok := generatedImagesFolderCache.Load(slug); ok {
		return cached.(string)
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}

	var folderID string
	err = wdb.DB.QueryRow("SELECT id FROM folders WHERE name = 'Generated Images' AND parent_id IS NULL LIMIT 1").Scan(&folderID)
	if err == nil {
		generatedImagesFolderCache.Store(slug, folderID)
		return folderID
	}

	// Create the folder
	folderID = id.New()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = wdb.DB.Exec(
		"INSERT INTO folders (id, parent_id, name, created_by, is_private, created_at, updated_at) VALUES (?, NULL, 'Generated Images', 'system', 0, ?, ?)",
		folderID, now, now,
	)
	if err != nil {
		logger.WithCategory(logger.CatAgent).Error().Str("workspace", slug).Err(err).Msg("failed to create Generated Images folder")
		return ""
	}

	generatedImagesFolderCache.Store(slug, folderID)
	return folderID
}

// broadcastAgentState sends an agent.state event to all clients in the channel.
func (s *Server) broadcastAgentState(slug, channelID, agentID, agentName, state, toolName string) {
	h := s.hubs.Get(slug)
	h.Broadcast(channelID, hub.MakeEnvelope(hub.TypeAgentState, hub.AgentStatePayload{
		AgentID:   agentID,
		AgentName: agentName,
		ChannelID: channelID,
		State:     state,
		ToolName:  toolName,
	}), "")
}

// handleAgentMention is called when a message mentions an agent.
// It runs asynchronously in a goroutine. fromAgent indicates this was triggered by another agent.
func (s *Server) handleAgentMention(slug, channelID, parentID, senderName, content string, agent *Agent, fromAgent ...bool) {
	isFromAgent := len(fromAgent) > 0 && fromAgent[0]
	go func() {
		metrics.AgentExecutionsTotal.WithLabelValues(agent.Name, "started").Inc()
		logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Bool("active", agent.IsActive).Msg("handleAgentMention triggered")
		if !agent.IsActive {
			logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Msg("agent not active, skipping")
			return
		}

		// Check channel scope
		if !isAgentInChannel(agent, channelID) {
			logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Str("channel", channelID).Msg("agent not in channel, skipping")
			return
		}

		// Broadcast thinking state
		s.broadcastAgentState(slug, channelID, agent.ID, agent.Name, "thinking", "")
		defer s.broadcastAgentState(slug, channelID, agent.ID, agent.Name, "idle", "")

		apiKey, _ := s.getBrainSettings(slug)
		if apiKey == "" {
			logger.WithCategory(logger.CatAgent).Warn().Str("workspace", slug).Str("agent", agent.Name).Msg("no API key configured")
			return
		}

		// Use agent's model or fall back to workspace default
		model := agent.Model
		if model == "" {
			_, model = s.getBrainSettings(slug)
		}

		systemPrompt := buildAgentSystemPrompt(agent)
		logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Int("chars", len(systemPrompt)).Msg("base prompt")

		// Append agent-specific skills
		agentSkills := brain.LoadAgentSkills(s.cfg.DataDir, slug, agent.ID)
		skillContext := brain.BuildAgentSkillContext(agentSkills)
		if skillContext != "" {
			systemPrompt += "\n\n---\n\n" + skillContext
			logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Int("skill_chars", len(skillContext)).Int("total_chars", len(systemPrompt)).Msg("+skills")
		}

		wdb, err := s.ws.Open(slug)
		if err != nil {
			logger.WithCategory(logger.CatAgent).Error().Str("workspace", slug).Str("agent", agent.Name).Err(err).Msg("workspace error")
			return
		}

		// Optionally append knowledge context
		if agent.KnowledgeAccess {
			kbContext := brain.BuildKnowledgeContext(wdb.DB, content, brain.SemanticOpts{
				VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
			})
			if kbContext != "" {
				systemPrompt += "\n\n---\n\n" + kbContext
				logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Int("knowledge_chars", len(kbContext)).Int("total_chars", len(systemPrompt)).Msg("+knowledge")
			}
		}

		// Optionally append memory context
		if agent.MemoryAccess {
			memContext := brain.BuildMemoryContext(wdb.DB)
			if memContext != "" {
				systemPrompt += "\n\n---\n\n" + memContext
				logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Int("memory_chars", len(memContext)).Int("total_chars", len(systemPrompt)).Msg("+memory")
			}
		}

		// Append channel history summary
		if chSummary := brain.BuildSingleChannelContext(wdb.DB, channelID); chSummary != "" {
			systemPrompt += "\n\n---\n\n" + chSummary
			logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Int("summary_chars", len(chSummary)).Int("total_chars", len(systemPrompt)).Msg("+channel_summary")
		}

		// Hard cap: if system prompt is too large, truncate to prevent token overflow
		if len(systemPrompt) > 100000 {
			logger.WithCategory(logger.CatAgent).Warn().Str("workspace", slug).Str("agent", agent.Name).Int("chars", len(systemPrompt)).Msg("system prompt too large, truncating to 100000")
			systemPrompt = systemPrompt[:100000]
		}

		messages := s.getThreadOrChannelMessages(wdb, channelID, parentID, 40)
		logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Int("system_chars", len(systemPrompt)).Int("message_count", len(messages)).Msg("final prompt")

		// Get agent's scoped tools (built-in + MCP)
		allTools := s.getAllTools(slug)
		agentTools := getAgentTools(agent, allTools)

		// Resolve free-auto virtual model
		resolvedModel, fallbacks := s.resolveFreeAuto(model)

		client := &brain.Client{
			APIKey:             apiKey,
			Model:              resolvedModel,
			FreeModelFallbacks: fallbacks,
			HTTPClient:         nil, // will use default
		}
		// Use default HTTP client
		if client.HTTPClient == nil {
			client.HTTPClient = brain.NewClient(apiKey, resolvedModel).HTTPClient
		}

		if len(agentTools) == 0 {
			// No tools — simple completion (or multimodal for image-capable models)
			if isMultimodalModel(model) {
				responseText, images, err := client.CompleteMultimodal(systemPrompt, messages)
				if err != nil {
					logger.WithCategory(logger.CatAgent).Error().Str("workspace", slug).Str("agent", agent.Name).Err(err).Msg("multimodal LLM error")
					return
				}
				responseText = strings.TrimSpace(responseText)
				// Save images and append markdown references
				imagesMd := s.saveAgentImages(slug, channelID, agent, images)
				responseText += imagesMd
				if responseText != "" {
					s.sendAgentMessage(slug, channelID, parentID, agent, responseText, isFromAgent)
				}
				brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
					truncate(content, 200), truncate(responseText, 500), model, nil)
				s.maybeStartFollowing(slug, channelID, agent)
				return
			}

			response, err := client.Complete(systemPrompt, messages)
			if err != nil {
				logger.WithCategory(logger.CatAgent).Error().Str("workspace", slug).Str("agent", agent.Name).Err(err).Msg("LLM error")
				return
			}
			response = strings.TrimSpace(response)
			if response != "" {
				s.sendAgentMessage(slug, channelID, parentID, agent, response, isFromAgent)
			}
			brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
				truncate(content, 200), truncate(response, 500), model, nil)
			s.maybeStartFollowing(slug, channelID, agent)
			return
		}

		// With tools
		logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Int("tool_count", len(agentTools)).Str("model", model).Msg("calling CompleteWithTools")
		for _, t := range agentTools {
			logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Str("tool", t.Function.Name).Msg("tool available")
		}
		responseContent, toolCalls, err := client.CompleteWithTools(systemPrompt, messages, agentTools)
		if err != nil {
			logger.WithCategory(logger.CatAgent).Error().Str("workspace", slug).Str("agent", agent.Name).Err(err).Msg("LLM error")
			return
		}

		if len(toolCalls) == 0 {
			logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Str("response", truncate(responseContent, 200)).Msg("no tool calls returned")
			responseContent = strings.TrimSpace(responseContent)
			if responseContent != "" {
				s.sendAgentMessage(slug, channelID, parentID, agent, responseContent, isFromAgent)
			}
			brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
				truncate(content, 200), truncate(responseContent, 500), model, nil)
			s.maybeStartFollowing(slug, channelID, agent)
			return
		}

		// Execute tools (up to max_iterations rounds)
		logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Int("count", len(toolCalls)).Msg("executing tool calls")

		assistantMsg := brain.Message{Role: "assistant", Content: responseContent, ToolCalls: toolCalls}
		followUp := append(messages, assistantMsg)

		var toolNames []string
		var imageRefs []string
		var imagePromptTag string
		for _, call := range toolCalls {
			s.broadcastAgentState(slug, channelID, agent.ID, agent.Name, "tool_executing", call.Function.Name)
			senderID := s.resolveMemberIDByName(slug, senderName)
			result := s.executeAgentTool(slug, channelID, senderID, agent, call)
			logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Str("tool", call.Function.Name).Str("result", truncate(result, 100)).Msg("tool executed")
			toolNames = append(toolNames, call.Function.Name)

			// Extract image markdown so the follow-up LLM can't drop it
			imageRefs = append(imageRefs, extractImageMarkdown(result)...)

			// Extract <image-prompt> tag — strip from tool result to avoid LLM echoing it
			result, promptTag := extractImagePromptTag(result)
			if promptTag != "" {
				imagePromptTag = promptTag
			}

			followUp = append(followUp, brain.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ID,
			})
		}

		var finalResponse string
		{
			var err2 error
			finalResponse, err2 = client.Complete(systemPrompt, followUp)
			if err2 != nil {
				logger.WithCategory(logger.CatAgent).Error().Str("workspace", slug).Str("agent", agent.Name).Err(err2).Msg("follow-up LLM error")
				if responseContent != "" {
					s.sendAgentMessage(slug, channelID, parentID, agent, appendMissingImages(responseContent, imageRefs)+imagePromptTag, isFromAgent)
				}
				return
			}
			finalResponse = strings.TrimSpace(finalResponse)
		}

		// Append any image markdown that the LLM may have omitted
		finalResponse = appendMissingImages(finalResponse, imageRefs)
		// Re-append the image prompt tag for frontend display
		finalResponse += imagePromptTag
		if finalResponse != "" {
			s.sendAgentMessage(slug, channelID, parentID, agent, finalResponse, isFromAgent, toolNames...)
		}

		brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
			truncate(content, 200), truncate(finalResponse, 500), model, toolNames)

		// Start conversation following if configured
		s.maybeStartFollowing(slug, channelID, agent)
	}()
}

// maybeStartFollowing starts conversation following for an agent if configured.
func (s *Server) maybeStartFollowing(slug, channelID string, agent *Agent) {
	if s.convTracker == nil || agent.BehaviorConfig.FollowTTLMinutes <= 0 {
		return
	}
	key := ConversationKey{Slug: slug, ChannelID: channelID, AgentID: agent.ID}
	s.convTracker.StartFollowing(key, agent.BehaviorConfig.FollowTTLMinutes, agent.BehaviorConfig.FollowMaxMessages)
	logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Str("channel", channelID).Int("ttl_minutes", agent.BehaviorConfig.FollowTTLMinutes).Int("max_messages", agent.BehaviorConfig.FollowMaxMessages).Msg("started following conversation")
}

// handleAgentFollowUp is called when an agent is following a conversation (not directly mentioned).
func (s *Server) handleAgentFollowUp(slug, channelID, parentID, senderName, content string, agent *Agent) {
	go func() {
		metrics.AgentExecutionsTotal.WithLabelValues(agent.Name, "follow_up").Inc()
		logger.WithCategory(logger.CatAgent).Info().Str("workspace", slug).Str("agent", agent.Name).Msg("handleAgentFollowUp triggered")
		if !agent.IsActive {
			return
		}

		s.broadcastAgentState(slug, channelID, agent.ID, agent.Name, "thinking", "")
		defer s.broadcastAgentState(slug, channelID, agent.ID, agent.Name, "idle", "")

		apiKey, _ := s.getBrainSettings(slug)
		if apiKey == "" {
			return
		}

		model := agent.Model
		if model == "" {
			_, model = s.getBrainSettings(slug)
		}

		systemPrompt := buildFollowUpSystemPrompt(agent)

		wdb, err := s.ws.Open(slug)
		if err != nil {
			return
		}

		if agent.KnowledgeAccess {
			kbContext := brain.BuildKnowledgeContext(wdb.DB, content, brain.SemanticOpts{
				VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
			})
			if kbContext != "" {
				systemPrompt += "\n\n---\n\n" + kbContext
			}
		}

		if chSummary := brain.BuildSingleChannelContext(wdb.DB, channelID); chSummary != "" {
			systemPrompt += "\n\n---\n\n" + chSummary
		}

		messages := s.getThreadOrChannelMessages(wdb, channelID, parentID, 40)

		resolvedModel, fallbacks := s.resolveFreeAuto(model)
		client := brain.NewClient(apiKey, resolvedModel)
		client.FreeModelFallbacks = fallbacks
		response, err := client.Complete(systemPrompt, messages)
		if err != nil {
			logger.WithCategory(logger.CatAgent).Error().Str("workspace", slug).Str("agent", agent.Name).Err(err).Msg("follow-up LLM error")
			return
		}
		response = strings.TrimSpace(response)

		// Agent can respond with empty string to stay silent
		if response == "" {
			return
		}

		s.sendAgentMessage(slug, channelID, parentID, agent, response, false)

		brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
			truncate(content, 200), truncate(response, 500), model, nil)
	}()
}

// executeAgentTool runs a tool call with agent context for enrichment.
func (s *Server) executeAgentTool(slug, channelID, senderMemberID string, agent *Agent, call brain.ToolCall) string {
	if call.Function.Name == "generate_image" {
		return s.toolGenerateImageForAgent(slug, channelID, agent, call.Function.Arguments)
	}
	return s.executeTool(slug, channelID, senderMemberID, call)
}

// buildAgentSystemPrompt assembles a system prompt from agent configuration.
func buildAgentSystemPrompt(agent *Agent) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("You are %s, %s.", agent.Name, agent.Role))

	if agent.Goal != "" {
		parts = append(parts, fmt.Sprintf("\n## Your Goal\n%s", agent.Goal))
	}
	if agent.Backstory != "" {
		parts = append(parts, fmt.Sprintf("\n## Background\n%s", agent.Backstory))
	}
	if agent.Instructions != "" {
		parts = append(parts, fmt.Sprintf("\n## Instructions\n%s", agent.Instructions))
	}
	if agent.Constraints != "" {
		parts = append(parts, fmt.Sprintf("\n## Constraints\n%s", agent.Constraints))
	}
	if agent.EscalationPrompt != "" {
		parts = append(parts, fmt.Sprintf("\n## Escalation\nIf you encounter something outside your scope: %s", agent.EscalationPrompt))
	}

	parts = append(parts, "\n## Response Guidelines\n- When you create something (task, document, etc.), ALWAYS tell the user where to find it.\n- When you use a tool, briefly mention what you did and the result.\n- Be concise.")

	return strings.Join(parts, "\n")
}

// buildFollowUpSystemPrompt builds a system prompt for conversation-following or ambient responses.
func buildFollowUpSystemPrompt(agent *Agent) string {
	base := buildAgentSystemPrompt(agent)
	base += "\n\n## Context\nYou were NOT directly @mentioned in this message. You are following this conversation because you were recently mentioned or the topic is relevant to your expertise.\n- Only respond if you have something genuinely useful to add.\n- If following up on a previous conversation, start with brief context like \"Following up on...\"\n- If the message doesn't need your input, respond with an empty string to stay silent.\n- Do NOT repeat information you've already shared."
	return base
}

// checkAgentMentions scans message content for @AgentName mentions.
// Returns a list of agents mentioned.
func (s *Server) checkAgentMentions(slug, content string) []*Agent {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return nil
	}

	// Include built-in system agents (except Brain, which has its own mention path)
	rows, err := wdb.DB.Query("SELECT id, name FROM agents WHERE is_active = TRUE AND id != ?", brain.BrainMemberID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	lower := strings.ToLower(content)
	var mentioned []*Agent

	for rows.Next() {
		var agentID, name string
		if err := rows.Scan(&agentID, &name); err != nil {
			continue
		}
		mention := "@" + strings.ToLower(name)
		if strings.Contains(lower, mention) {
			if agent := s.loadAgentByID(slug, agentID); agent != nil {
				mentioned = append(mentioned, agent)
			}
		}
	}

	return mentioned
}

// handleDelegateToAgent is called when Brain uses the delegate_to_agent tool.
func (s *Server) handleDelegateToAgent(slug, channelID, argsJSON string) string {
	var args struct {
		AgentName string `json:"agent_name"`
		Task      string `json:"task"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments"
	}

	agent := s.loadAgentByName(slug, args.AgentName)
	if agent == nil {
		return fmt.Sprintf("Agent '%s' not found or not active", args.AgentName)
	}

	apiKey, _ := s.getBrainSettings(slug)
	if apiKey == "" {
		return "No API key configured"
	}

	model := agent.Model
	if model == "" {
		_, model = s.getBrainSettings(slug)
	}

	systemPrompt := buildAgentSystemPrompt(agent)

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Workspace error"
	}

	if agent.KnowledgeAccess {
		kbContext := brain.BuildKnowledgeContext(wdb.DB, args.Task, brain.SemanticOpts{
			VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
		})
		if kbContext != "" {
			systemPrompt += "\n\n---\n\n" + kbContext
		}
	}

	resolvedModel, fallbacks := s.resolveFreeAuto(model)
	client := brain.NewClient(apiKey, resolvedModel)
	client.FreeModelFallbacks = fallbacks
	taskMessages := []brain.Message{
		{Role: "user", Content: args.Task},
	}

	response, err := client.Complete(systemPrompt, taskMessages)
	if err != nil {
		return fmt.Sprintf("Agent error: %v", err)
	}

	return fmt.Sprintf("[%s]: %s", agent.Name, strings.TrimSpace(response))
}

// isMultimodalModel returns true if the model supports image generation output.
func isMultimodalModel(model string) bool {
	return strings.Contains(model, "image-preview")
}

// looksLikeImageRequest returns true if the user message likely wants image generation.
func looksLikeImageRequest(content string) bool {
	lower := strings.ToLower(content)
	keywords := []string{"image", "picture", "photo", "visual", "draw", "generate", "create", "design", "illustration", "logo", "banner", "poster", "ad", "graphic", "mockup", "render"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// saveBase64ImageBlob saves raw base64 image data as a blob and returns a markdown reference.
func (s *Server) saveBase64ImageBlob(slug, channelID, b64Data, mimeType string) string {
	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		logger.WithCategory(logger.CatAgent).Error().Str("workspace", slug).Err(err).Msg("failed to decode base64 image")
		return ""
	}

	hashBytes := sha256.Sum256(data)
	hash := hex.EncodeToString(hashBytes[:])

	blobsDir := s.ws.BlobsDir(slug)
	prefix := hash[:2]
	prefixDir := filepath.Join(blobsDir, prefix)
	if err := os.MkdirAll(prefixDir, 0700); err != nil {
		return ""
	}
	blobPath := filepath.Join(prefixDir, hash)
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		if err := os.WriteFile(blobPath, data, 0600); err != nil {
			return ""
		}
	}

	ext := "png"
	if mimeType == "image/jpeg" {
		ext = "jpg"
	} else if mimeType == "image/webp" {
		ext = "webp"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}
	fileID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)
	folderID := s.ensureGeneratedImagesFolder(slug)
	_, _ = wdb.DB.Exec(
		`INSERT INTO files (id, channel_id, uploader_id, name, mime, size, hash, folder_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fileID, channelID, "system",
		fmt.Sprintf("generated.%s", ext), mimeType, len(data), hash, nilIfEmpty(folderID), now,
	)

	return fmt.Sprintf("\n\n![Generated Image](/api/workspaces/%s/files/%s)", slug, hash)
}

// saveImageBlob decodes a single base64 image, saves to blobs, records in files table,
// and returns a markdown image reference.
func (s *Server) saveImageBlob(slug, channelID string, img brain.MessageImage, index int) string {
	url := img.ImageURL.URL
	commaIdx := strings.Index(url, ",")
	if commaIdx < 0 {
		return ""
	}
	b64Data := url[commaIdx+1:]
	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		logger.WithCategory(logger.CatAgent).Error().Str("workspace", slug).Int("index", index).Err(err).Msg("failed to decode image")
		return ""
	}

	hashBytes := sha256.Sum256(data)
	hash := hex.EncodeToString(hashBytes[:])

	blobsDir := s.ws.BlobsDir(slug)
	prefix := hash[:2]
	prefixDir := filepath.Join(blobsDir, prefix)
	if err := os.MkdirAll(prefixDir, 0700); err != nil {
		return ""
	}
	blobPath := filepath.Join(prefixDir, hash)
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		if err := os.WriteFile(blobPath, data, 0600); err != nil {
			return ""
		}
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}
	fileID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)
	folderID := s.ensureGeneratedImagesFolder(slug)
	_, _ = wdb.DB.Exec(
		`INSERT INTO files (id, channel_id, uploader_id, name, mime, size, hash, folder_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fileID, channelID, "system",
		fmt.Sprintf("generated-%d.png", index+1), "image/png", len(data), hash, nilIfEmpty(folderID), now,
	)

	return fmt.Sprintf("\n\n![Generated Image](/api/workspaces/%s/files/%s)", slug, hash)
}

// saveAgentImages decodes base64 images from a multimodal response, saves them as blobs,
// and returns markdown image references to append to the message content.
func (s *Server) saveAgentImages(slug, channelID string, agent *Agent, images []brain.MessageImage) string {
	if len(images) == 0 {
		return ""
	}
	var md strings.Builder
	for i, img := range images {
		md.WriteString(s.saveImageBlob(slug, channelID, img, i))
	}
	return md.String()
}
