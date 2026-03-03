package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
)

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
// It runs asynchronously in a goroutine.
func (s *Server) handleAgentMention(slug, channelID, senderName, content string, agent *Agent) {
	go func() {
		log.Printf("[agent:%s:%s] handleAgentMention triggered, active=%v", slug, agent.Name, agent.IsActive)
		if !agent.IsActive {
			log.Printf("[agent:%s:%s] agent not active, skipping", slug, agent.Name)
			return
		}

		// Check channel scope
		if !isAgentInChannel(agent, channelID) {
			log.Printf("[agent:%s:%s] agent not in channel %s, skipping", slug, agent.Name, channelID)
			return
		}

		// Broadcast thinking state
		s.broadcastAgentState(slug, channelID, agent.ID, agent.Name, "thinking", "")
		defer s.broadcastAgentState(slug, channelID, agent.ID, agent.Name, "idle", "")

		apiKey, _ := s.getBrainSettings(slug)
		if apiKey == "" {
			log.Printf("[agent:%s:%s] no API key configured", slug, agent.Name)
			return
		}

		// Use agent's model or fall back to workspace default
		model := agent.Model
		if model == "" {
			_, model = s.getBrainSettings(slug)
		}

		systemPrompt := buildAgentSystemPrompt(agent)

		// Append agent-specific skills
		agentSkills := brain.LoadAgentSkills(s.cfg.DataDir, slug, agent.ID)
		skillContext := brain.BuildAgentSkillContext(agentSkills)
		if skillContext != "" {
			systemPrompt += "\n\n---\n\n" + skillContext
		}

		wdb, err := s.ws.Open(slug)
		if err != nil {
			log.Printf("[agent:%s:%s] workspace error: %v", slug, agent.Name, err)
			return
		}

		// Optionally append knowledge context
		if agent.KnowledgeAccess {
			kbContext := brain.BuildKnowledgeContext(wdb.DB, content)
			if kbContext != "" {
				systemPrompt += "\n\n---\n\n" + kbContext
			}
		}

		// Optionally append memory context
		if agent.MemoryAccess {
			memContext := brain.BuildMemoryContext(wdb.DB)
			if memContext != "" {
				systemPrompt += "\n\n---\n\n" + memContext
			}
		}

		// Append channel history summary
		if chSummary := brain.BuildSingleChannelContext(wdb.DB, channelID); chSummary != "" {
			systemPrompt += "\n\n---\n\n" + chSummary
		}

		messages := s.getRecentMessages(wdb, channelID, 40)

		// Get agent's scoped tools
		agentTools := getAgentTools(agent)

		client := &brain.Client{
			APIKey:     apiKey,
			Model:      model,
			HTTPClient: nil, // will use default
		}
		// Use default HTTP client
		if client.HTTPClient == nil {
			client.HTTPClient = brain.NewClient(apiKey, model).HTTPClient
		}

		if len(agentTools) == 0 {
			// No tools — simple completion (or multimodal for image-capable models)
			if isMultimodalModel(model) {
				responseText, images, err := client.CompleteMultimodal(systemPrompt, messages)
				if err != nil {
					log.Printf("[agent:%s:%s] multimodal LLM error: %v", slug, agent.Name, err)
					return
				}
				responseText = strings.TrimSpace(responseText)
				// Save images and append markdown references
				imagesMd := s.saveAgentImages(slug, channelID, agent, images)
				responseText += imagesMd
				if responseText != "" {
					s.sendAgentMessage(slug, channelID, agent, responseText)
				}
				brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
					truncate(content, 200), truncate(responseText, 500), model, nil)
				return
			}

			response, err := client.Complete(systemPrompt, messages)
			if err != nil {
				log.Printf("[agent:%s:%s] LLM error: %v", slug, agent.Name, err)
				return
			}
			response = strings.TrimSpace(response)
			if response != "" {
				s.sendAgentMessage(slug, channelID, agent, response)
			}
			brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
				truncate(content, 200), truncate(response, 500), model, nil)
			return
		}

		// With tools
		log.Printf("[agent:%s:%s] calling CompleteWithTools with %d tools, model=%s", slug, agent.Name, len(agentTools), model)
		for _, t := range agentTools {
			log.Printf("[agent:%s:%s]   tool: %s", slug, agent.Name, t.Function.Name)
		}
		responseContent, toolCalls, err := client.CompleteWithTools(systemPrompt, messages, agentTools)
		if err != nil {
			log.Printf("[agent:%s:%s] LLM error: %v", slug, agent.Name, err)
			return
		}

		if len(toolCalls) == 0 {
			log.Printf("[agent:%s:%s] no tool calls returned, response: %s", slug, agent.Name, truncate(responseContent, 200))
			responseContent = strings.TrimSpace(responseContent)
			if responseContent != "" {
				s.sendAgentMessage(slug, channelID, agent, responseContent)
			}
			brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
				truncate(content, 200), truncate(responseContent, 500), model, nil)
			return
		}

		// Execute tools (up to max_iterations rounds)
		log.Printf("[agent:%s:%s] executing %d tool calls", slug, agent.Name, len(toolCalls))

		assistantMsg := brain.Message{Role: "assistant", Content: responseContent, ToolCalls: toolCalls}
		followUp := append(messages, assistantMsg)

		var toolNames []string
		var imageRefs []string
		var imagePromptTag string
		for _, call := range toolCalls {
			s.broadcastAgentState(slug, channelID, agent.ID, agent.Name, "tool_executing", call.Function.Name)
			result := s.executeAgentTool(slug, channelID, agent, call)
			log.Printf("[agent:%s:%s] tool %s → %s", slug, agent.Name, call.Function.Name, truncate(result, 100))
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
				log.Printf("[agent:%s:%s] follow-up LLM error: %v", slug, agent.Name, err2)
				if responseContent != "" {
					s.sendAgentMessage(slug, channelID, agent, appendMissingImages(responseContent, imageRefs)+imagePromptTag)
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
			s.sendAgentMessage(slug, channelID, agent, finalResponse)
		}

		brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
			truncate(content, 200), truncate(finalResponse, 500), model, toolNames)
	}()
}

// executeAgentTool runs a tool call with agent context for enrichment.
func (s *Server) executeAgentTool(slug, channelID string, agent *Agent, call brain.ToolCall) string {
	if call.Function.Name == "generate_image" {
		return s.toolGenerateImageForAgent(slug, channelID, agent, call.Function.Arguments)
	}
	return s.executeTool(slug, channelID, call)
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

	return strings.Join(parts, "\n")
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
		kbContext := brain.BuildKnowledgeContext(wdb.DB, args.Task)
		if kbContext != "" {
			systemPrompt += "\n\n---\n\n" + kbContext
		}
	}

	client := brain.NewClient(apiKey, model)
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
		log.Printf("[image:%s] failed to decode base64: %v", slug, err)
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
	_, _ = wdb.DB.Exec(
		`INSERT INTO files (id, channel_id, uploader_id, name, mime, size, hash, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		fileID, channelID, "system",
		fmt.Sprintf("generated.%s", ext), mimeType, len(data), hash, now,
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
		log.Printf("[image:%s] failed to decode image %d: %v", slug, index, err)
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
	_, _ = wdb.DB.Exec(
		`INSERT INTO files (id, channel_id, uploader_id, name, mime, size, hash, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		fileID, channelID, "system",
		fmt.Sprintf("generated-%d.png", index+1), "image/png", len(data), hash, now,
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
