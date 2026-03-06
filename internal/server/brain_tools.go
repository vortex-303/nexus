package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/metrics"
)

// executeTool runs a tool call and returns the result as a string.
// senderMemberID is the member who triggered the tool (for attribution). Empty = Brain.
func (s *Server) executeTool(slug, channelID, senderMemberID string, call brain.ToolCall) string {
	start := time.Now()
	result := s.executeToolInner(slug, channelID, senderMemberID, call)
	metrics.ToolLatency.WithLabelValues(call.Function.Name).Observe(time.Since(start).Seconds())
	status := "ok"
	if strings.HasPrefix(result, "Error") {
		status = "error"
	}
	metrics.ToolCallsTotal.WithLabelValues(call.Function.Name, status).Inc()
	return result
}

func (s *Server) executeToolInner(slug, channelID, senderMemberID string, call brain.ToolCall) string {
	switch call.Function.Name {
	case "create_task":
		return s.toolCreateTask(slug, channelID, call.Function.Arguments)
	case "list_tasks":
		return s.toolListTasks(slug, call.Function.Arguments)
	case "search_messages":
		return s.toolSearchMessages(slug, channelID, call.Function.Arguments)
	case "create_document":
		return s.toolCreateDocument(slug, call.Function.Arguments)
	case "search_knowledge":
		return s.toolSearchKnowledge(slug, call.Function.Arguments)
	case "delegate_to_agent":
		return s.handleDelegateToAgent(slug, channelID, call.Function.Arguments)
	case "generate_image":
		return s.toolGenerateImage(slug, channelID, call.Function.Arguments)
	case "create_calendar_event":
		return s.toolCreateCalendarEvent(slug, channelID, senderMemberID, call.Function.Arguments)
	case "list_calendar_events":
		return s.toolListCalendarEvents(slug, call.Function.Arguments)
	case "update_calendar_event":
		return s.toolUpdateCalendarEvent(slug, call.Function.Arguments)
	case "delete_calendar_event":
		return s.toolDeleteCalendarEvent(slug, call.Function.Arguments)
	case "send_email":
		return s.toolSendEmail(slug, call.Function.Arguments)
	case "send_telegram":
		return s.toolSendTelegram(slug, channelID, call.Function.Arguments)
	case "web_search":
		return toolWebSearch(call.Function.Arguments)
	case "fetch_url":
		return toolFetchURL(call.Function.Arguments)
	default:
		// Route to MCP server
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		mgr := s.getMCPManager(slug)
		var args map[string]any
		json.Unmarshal([]byte(call.Function.Arguments), &args)
		result, err := mgr.CallTool(ctx, call.Function.Name, args)
		if err != nil {
			return fmt.Sprintf("MCP tool error: %s", err)
		}
		return result
	}
}

func (s *Server) toolCreateTask(slug, channelID, argsJSON string) string {
	var args struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		Status       string `json:"status"`
		Priority     string `json:"priority"`
		AssigneeName string `json:"assignee_name"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	if args.Status == "" {
		args.Status = "todo"
	}
	if args.Priority == "" {
		args.Priority = "medium"
	}

	// Resolve assignee name to ID
	var assigneeID string
	if args.AssigneeName != "" {
		_ = wdb.DB.QueryRow(
			"SELECT id FROM members WHERE LOWER(display_name) = LOWER(?)",
			args.AssigneeName,
		).Scan(&assigneeID)
	}

	taskID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = wdb.DB.Exec(
		`INSERT INTO tasks (id, title, description, status, priority, assignee_id, created_by, channel_id, tags, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, '[]', ?, ?)`,
		taskID, args.Title, args.Description, args.Status, args.Priority,
		assigneeID, brain.BrainMemberID, channelID, now, now,
	)
	if err != nil {
		return "Error creating task: " + err.Error()
	}

	// Broadcast task.created
	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeTaskCreated, map[string]any{
		"id": taskID, "title": args.Title, "description": args.Description,
		"status": args.Status, "priority": args.Priority,
		"assignee_id": assigneeID, "created_by": brain.BrainMemberID,
		"tags": json.RawMessage("[]"), "created_at": now, "updated_at": now,
	}), "")

	result := fmt.Sprintf("Task created: \"%s\" [%s, %s]", args.Title, args.Status, args.Priority)
	if assigneeID != "" {
		result += fmt.Sprintf(" assigned to %s", args.AssigneeName)
	}
	return result
}

func (s *Server) toolListTasks(slug, argsJSON string) string {
	var args struct {
		Status       string `json:"status"`
		AssigneeName string `json:"assignee_name"`
	}
	if argsJSON != "" {
		json.Unmarshal([]byte(argsJSON), &args)
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	query := `SELECT t.id, t.title, t.status, t.priority, COALESCE(m.display_name, ''), t.due_date
		FROM tasks t LEFT JOIN members m ON m.id = t.assignee_id WHERE 1=1`
	var qargs []any

	if args.Status != "" {
		query += " AND t.status = ?"
		qargs = append(qargs, args.Status)
	}
	if args.AssigneeName != "" {
		query += " AND LOWER(m.display_name) = LOWER(?)"
		qargs = append(qargs, args.AssigneeName)
	}
	query += " ORDER BY t.created_at DESC LIMIT 20"

	rows, err := wdb.DB.Query(query, qargs...)
	if err != nil {
		return "Error querying tasks"
	}
	defer rows.Close()

	var lines []string
	count := 0
	for rows.Next() {
		var tid, title, status, priority, assignee, dueDate string
		rows.Scan(&tid, &title, &status, &priority, &assignee, &dueDate)
		line := fmt.Sprintf("- [%s] %s (%s)", status, title, priority)
		if assignee != "" {
			line += " → " + assignee
		}
		if dueDate != "" {
			line += " due:" + dueDate
		}
		lines = append(lines, line)
		count++
	}

	if count == 0 {
		return "No tasks found matching the criteria."
	}
	return fmt.Sprintf("%d tasks found:\n%s", count, strings.Join(lines, "\n"))
}

func (s *Server) toolSearchMessages(slug, channelID, argsJSON string) string {
	var args struct {
		Query     string `json:"query"`
		ChannelID string `json:"channel_id"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments"
	}
	if args.Limit <= 0 || args.Limit > 20 {
		args.Limit = 10
	}

	results, err := s.search.Search(slug, args.Query, []string{"message"}, args.Limit)
	if err != nil {
		return "Error searching messages"
	}

	var lines []string
	for _, r := range results {
		content := r.Content
		if len(content) > 150 {
			content = content[:147] + "..."
		}
		lines = append(lines, fmt.Sprintf("[%.2f] %s", r.Score, content))
	}

	if len(lines) == 0 {
		return fmt.Sprintf("No messages found matching \"%s\"", args.Query)
	}
	return fmt.Sprintf("Found %d messages:\n%s", len(lines), strings.Join(lines, "\n"))
}

func (s *Server) toolCreateDocument(slug, argsJSON string) string {
	var args struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	docID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = wdb.DB.Exec(
		"INSERT INTO documents (id, title, content, created_by, updated_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		docID, args.Title, args.Content, brain.BrainMemberID, brain.BrainMemberID, now, now,
	)
	if err != nil {
		return "Error creating document: " + err.Error()
	}

	// Broadcast doc.created
	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope("doc.created", map[string]any{
		"id": docID, "title": args.Title, "content": args.Content,
		"created_by": brain.BrainMemberID, "updated_by": brain.BrainMemberID,
		"sharing": "workspace", "created_at": now, "updated_at": now,
	}), "")

	return fmt.Sprintf("Document created: \"%s\"", args.Title)
}

func (s *Server) toolSearchKnowledge(slug, argsJSON string) string {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	apiKey, _ := s.getBrainSettings(slug)
	return brain.SearchKnowledgeForTool(wdb.DB, args.Query, brain.SemanticOpts{
		VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
	})
}

// resolveMemberIDByName looks up a member ID by display name.
func (s *Server) resolveMemberIDByName(slug, name string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}
	var memberID string
	_ = wdb.DB.QueryRow(
		"SELECT id FROM members WHERE LOWER(display_name) = LOWER(?)", name,
	).Scan(&memberID)
	return memberID
}

// handleBrainMentionWithTools processes a @Brain mention with tool support.
func (s *Server) handleBrainMentionWithTools(slug, channelID, parentID, senderName, content string) {
	go func() {
		metrics.AgentExecutionsTotal.WithLabelValues("Brain", "started").Inc()
		apiKey, model := s.getBrainSettings(slug)
		if apiKey == "" {
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Msg("no API key configured, ignoring mention")
			return
		}

		// Broadcast thinking state
		s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "thinking", "")
		defer s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "idle", "")

		brainDir := brain.BrainDir(s.cfg.DataDir, slug)
		systemPrompt, err := brain.BuildSystemPrompt(brainDir)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("failed to build system prompt")
			return
		}
		logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(systemPrompt)).Msg("base prompt")

		wdb, err := s.ws.Open(slug)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("workspace error")
			return
		}

		// Append memories to system prompt
		memoryContext := brain.BuildMemoryContext(wdb.DB)
		if memoryContext != "" {
			systemPrompt += "\n\n---\n\n" + memoryContext
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(memoryContext)).Int("total", len(systemPrompt)).Msg("+memories")
		}

		// Append skills context (role-gated + enabled filter)
		skills := brain.LoadSkills(brainDir)
		s.applySkillEnabledState(slug, skills)
		var senderRole string
		_ = wdb.DB.QueryRow("SELECT role FROM members WHERE LOWER(display_name) = LOWER(?)", senderName).Scan(&senderRole)
		skills = brain.FilterSkillsByRole(skills, senderRole)
		skills = filterEnabledSkills(skills)
		skillContext := brain.BuildSkillContext(skills)
		if skillContext != "" {
			systemPrompt += "\n\n---\n\n" + skillContext
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(skillContext)).Int("total", len(systemPrompt)).Msg("+skills")
		}

		// Append knowledge base context
		kbContext := brain.BuildKnowledgeContext(wdb.DB, content, brain.SemanticOpts{
			VectorStore: s.vectors, APIKey: apiKey, Slug: slug,
		})
		if kbContext != "" {
			systemPrompt += "\n\n---\n\n" + kbContext
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(kbContext)).Int("total", len(systemPrompt)).Msg("+knowledge")
		}

		// Append channel history summary (this channel)
		if chSummary := brain.BuildSingleChannelContext(wdb.DB, channelID); chSummary != "" {
			systemPrompt += "\n\n---\n\n" + chSummary
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(chSummary)).Int("total", len(systemPrompt)).Msg("+channel_summary")
		}

		// Append cross-channel awareness (Brain only)
		if crossCtx := brain.BuildCrossChannelContext(wdb.DB, channelID); crossCtx != "" {
			systemPrompt += "\n\n---\n\n" + crossCtx
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("chars", len(crossCtx)).Int("total", len(systemPrompt)).Msg("+cross_channel")
		}

		// Hard cap: if system prompt is too large, truncate to prevent token overflow
		if len(systemPrompt) > 100000 {
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Int("chars", len(systemPrompt)).Msg("system prompt too large, truncating to 100000")
			systemPrompt = systemPrompt[:100000]
		}

		messages := s.getThreadOrChannelMessages(wdb, channelID, parentID, 40)
		logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("count", len(messages)).Msg("messages")

		// Resolve free-auto virtual model
		resolvedModel, fallbacks := s.resolveFreeAuto(model)

		client := brain.NewClient(apiKey, resolvedModel)
		client.FreeModelFallbacks = fallbacks

		// First call: with tools (built-in + MCP)
		allTools := s.getAllTools(slug)
		responseContent, toolCalls, err := client.CompleteWithTools(systemPrompt, messages, allTools)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("LLM error")
			s.sendBrainMessage(slug, channelID, parentID, "Sorry, I encountered an error. Check that the API key is configured correctly.")
			return
		}

		// If no tool calls, just send the text response
		if len(toolCalls) == 0 {
			responseContent = strings.TrimSpace(responseContent)
			if responseContent != "" {
				s.sendBrainMessage(slug, channelID, parentID, responseContent)
			}
			// Log the action
			brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
				truncate(content, 200), truncate(responseContent, 500), model, nil)
			return
		}

		// Execute tool calls and build follow-up messages
		logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("count", len(toolCalls)).Msg("executing tool calls")

		// Add the assistant's tool-call message
		assistantMsg := brain.Message{Role: "assistant", Content: responseContent, ToolCalls: toolCalls}
		followUp := append(messages, assistantMsg)

		var imageRefs []string
		for _, call := range toolCalls {
			s.broadcastAgentState(slug, channelID, brain.BrainMemberID, brain.BrainName, "tool_executing", call.Function.Name)
			senderID := s.resolveMemberIDByName(slug, senderName)
			result := s.executeTool(slug, channelID, senderID, call)
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("tool", call.Function.Name).Str("result", truncate(result, 100)).Msg("tool result")

			// Extract image markdown from tool results so the follow-up LLM can't drop it
			imageRefs = append(imageRefs, extractImageMarkdown(result)...)

			// Add tool result with tool_call_id
			followUp = append(followUp, brain.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ID,
			})
		}

		// Second call: get final response incorporating tool results
		finalResponse, err := client.Complete(systemPrompt, followUp)
		if err != nil {
			logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("follow-up LLM error")
			// Fall back to the initial response if any
			if responseContent != "" {
				s.sendBrainMessage(slug, channelID, parentID, appendMissingImages(responseContent, imageRefs))
			}
			return
		}

		finalResponse = strings.TrimSpace(finalResponse)
		// Append any image markdown that the LLM may have omitted
		finalResponse = appendMissingImages(finalResponse, imageRefs)

		// Collect tool names for metadata
		var toolNames []string
		for _, call := range toolCalls {
			toolNames = append(toolNames, call.Function.Name)
		}

		if finalResponse != "" {
			s.sendBrainMessage(slug, channelID, parentID, finalResponse, toolNames...)
		}

		brain.LogAction(wdb.DB, id.New(), brain.ActionMention, channelID,
			truncate(content, 200), truncate(finalResponse, 500), model, toolNames)
	}()
}

// filterEnabledSkills returns only skills where Enabled is true.
func filterEnabledSkills(skills []brain.Skill) []brain.Skill {
	var out []brain.Skill
	for _, sk := range skills {
		if sk.Enabled {
			out = append(out, sk)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// resolveAssigneeID looks up a member ID by display name.
func resolveAssigneeID(wdb *db.WorkspaceDB, name string) string {
	var id string
	_ = wdb.DB.QueryRow(
		"SELECT id FROM members WHERE LOWER(display_name) = LOWER(?)", name,
	).Scan(&id)
	return id
}

// extractImageMarkdown pulls out all ![...](/api/workspaces/...) references from a tool result.
// These are appended directly to the final message so the follow-up LLM can't drop them.
func extractImageMarkdown(s string) []string {
	var refs []string
	for {
		idx := strings.Index(s, "![")
		if idx < 0 {
			break
		}
		closeParen := strings.Index(s[idx:], ")")
		if closeParen < 0 {
			break
		}
		md := s[idx : idx+closeParen+1]
		if strings.Contains(md, "/api/workspaces/") {
			refs = append(refs, md)
		}
		s = s[idx+closeParen+1:]
	}
	return refs
}

// extractImagePromptTag strips <image-prompt>...</image-prompt> from a string and returns both.
func extractImagePromptTag(s string) (cleaned string, tag string) {
	start := strings.Index(s, "<image-prompt>")
	if start < 0 {
		return s, ""
	}
	end := strings.Index(s, "</image-prompt>")
	if end < 0 {
		return s, ""
	}
	end += len("</image-prompt>")
	tag = "\n\n" + strings.TrimSpace(s[start:end])
	cleaned = strings.TrimSpace(s[:start] + s[end:])
	return cleaned, tag
}

// appendMissingImages appends image markdown refs whose URLs are not already present in the response.
func appendMissingImages(response string, imageRefs []string) string {
	// First, strip any LLM-fabricated image refs that don't match our known refs
	response = stripFakeImageRefs(response, imageRefs)

	for _, ref := range imageRefs {
		// Extract just the URL from ![alt](URL) to compare — LLM may use different alt text
		url := extractURLFromImageMd(ref)
		if url != "" && strings.Contains(response, url) {
			continue // URL already present, skip
		}
		response += "\n\n" + ref
	}
	return response
}

// stripFakeImageRefs removes any ![...](...) refs pointing to /api/workspaces/ that aren't in our known set.
// The follow-up LLM sometimes fabricates plausible-looking image URLs that 404.
func stripFakeImageRefs(response string, knownRefs []string) string {
	knownURLs := map[string]bool{}
	for _, ref := range knownRefs {
		if u := extractURLFromImageMd(ref); u != "" {
			knownURLs[u] = true
		}
	}
	if len(knownURLs) == 0 {
		return response
	}

	// Extract all image refs from the response
	fakeRefs := extractImageMarkdown(response)
	for _, ref := range fakeRefs {
		url := extractURLFromImageMd(ref)
		if url != "" && !knownURLs[url] {
			// This is a fabricated ref — remove it from the response
			response = strings.Replace(response, ref, "", 1)
		}
	}
	return strings.TrimSpace(response)
}

// extractURLFromImageMd pulls the URL from a ![alt](url) markdown image reference.
func extractURLFromImageMd(md string) string {
	start := strings.Index(md, "](")
	if start < 0 {
		return ""
	}
	end := strings.Index(md[start:], ")")
	if end < 0 {
		return ""
	}
	return md[start+2 : start+end]
}

// PromptEnrichmentSystemPrompt instructs the LLM to craft a detailed image generation prompt.
const PromptEnrichmentSystemPrompt = `You are an expert creative advertising prompt engineer. Your job is to take a concept brief and the creative professional's identity, then produce a detailed image generation prompt for a production-quality advertisement.

Rules:
- Output ONLY the image generation prompt text — no explanations, no preamble, no quotes
- This is an ADVERTISEMENT, not just a pretty image. Include:
  - The product/brand prominently featured
  - Headline text to render on the image (in quotes, e.g. "Taste the Wild")
  - Tagline or call-to-action text
  - Layout/composition that leaves space for copy or integrates text naturally
  - Color palette that reinforces the brand message
  - Mood, lighting, and style that sell the product
  - Target audience and emotional appeal
- Specify the ad format: print ad, social media post, billboard, etc.
- Include what to AVOID (e.g., "no watermarks", "avoid cluttered backgrounds")
- Keep the prompt under 250 words — dense, specific, and actionable
- Incorporate the creative professional's aesthetic identity and style preferences
- Think like a senior art director briefing a production team`

// toolGenerateImageForAgent enriches the prompt using the agent's identity before calling Gemini.
func (s *Server) toolGenerateImageForAgent(slug, channelID string, agent *Agent, argsJSON string) string {
	var args struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.Prompt == "" {
		return "Error: prompt is required"
	}

	apiKey, model := s.getBrainSettings(slug)
	geminiKey := s.getGeminiAPIKey(slug)
	if geminiKey == "" {
		return "Error: no Gemini API key configured. Set it in Brain Settings."
	}

	// Use the agent's model (or cheap memory_model) for enrichment — NOT Gemini
	enrichModel := model
	_, _, memModel := s.getMemorySettings(slug)
	if memModel != "" {
		enrichModel = memModel
	}

	// Build agent context for enrichment
	var agentContext string
	if agent.Role != "" {
		agentContext += "Role: " + agent.Role + "\n"
	}
	if agent.Goal != "" {
		agentContext += "Goal: " + agent.Goal + "\n"
	}
	if agent.Backstory != "" {
		agentContext += "Background: " + agent.Backstory + "\n"
	}

	// Load agent's skills — find the image/ad-related skill for prompt formula
	agentSkills := brain.LoadAgentSkills(s.cfg.DataDir, slug, agent.ID)
	var skillPlaybook string
	for _, sk := range agentSkills {
		// Include any skill related to ad creative / image generation
		if strings.Contains(strings.ToLower(sk.Name), "ad") ||
			strings.Contains(strings.ToLower(sk.Name), "creative") ||
			strings.Contains(strings.ToLower(sk.Name), "image") ||
			strings.Contains(strings.ToLower(sk.Name), "visual") {
			skillPlaybook += "\n\n## Skill: " + sk.Name + "\n" + sk.Prompt
		}
	}

	// Enrich the prompt via LLM
	enrichedPrompt := args.Prompt
	if apiKey != "" {
		client := brain.NewClient(apiKey, enrichModel)
		userMsg := fmt.Sprintf("## Creative Professional Identity\n%s\n## Concept Brief\n%s", agentContext, args.Prompt)
		if skillPlaybook != "" {
			userMsg += "\n\n## Prompt Engineering Playbook (FOLLOW THIS STRUCTURE)" + skillPlaybook
		}
		userMsg += "\n\nCraft the image generation prompt following the structured formula above. MUST include headline text and CTA text to render on the image:"
		result, err := client.Complete(PromptEnrichmentSystemPrompt, []brain.Message{
			{Role: "user", Content: userMsg},
		})
		if err != nil {
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("prompt enrichment failed, using raw prompt")
		} else {
			enrichedPrompt = strings.TrimSpace(result)
			logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("prompt", truncate(enrichedPrompt, 200)).Msg("enriched prompt")
		}
	}

	imageModel := s.getImageModel(slug)
	logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("model", imageModel).Msg("using Gemini model")

	text, imageData, mimeType, err := brain.GenerateImageGemini(geminiKey, imageModel, enrichedPrompt)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("image generation error")
		return "Error generating image: " + err.Error()
	}

	if mimeType == "" {
		mimeType = "image/png"
	}
	saved := s.saveBase64ImageBlob(slug, channelID, imageData, mimeType)
	if saved == "" {
		return "Image generated but failed to save"
	}

	// Return: Gemini text + image + collapsible prompt (special marker for frontend)
	result := strings.TrimSpace(text) + saved
	result += fmt.Sprintf("\n\n<image-prompt>\n%s\n</image-prompt>", enrichedPrompt)
	return result
}

// toolGenerateImage calls the Gemini API to generate an image from a prompt.
// Returns markdown image reference or error string (used by Brain, not agents).
func (s *Server) toolGenerateImage(slug, channelID, argsJSON string) string {
	var args struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.Prompt == "" {
		return "Error: prompt is required"
	}

	geminiKey := s.getGeminiAPIKey(slug)
	if geminiKey == "" {
		return "Error: no Gemini API key configured. Set it in Brain Settings."
	}

	imageModel := s.getImageModel(slug)
	logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Str("model", imageModel).Msg("using Gemini model")

	text, imageData, mimeType, err := brain.GenerateImageGemini(geminiKey, imageModel, args.Prompt)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("image generation error")
		return "Error generating image: " + err.Error()
	}

	// Save as blob
	if mimeType == "" {
		mimeType = "image/png"
	}
	saved := s.saveBase64ImageBlob(slug, channelID, imageData, mimeType)
	if saved == "" {
		return "Image generated but failed to save"
	}

	result := strings.TrimSpace(text) + saved
	return result
}

// toolSendEmail sends an email via the configured outbound SMTP.
func (s *Server) toolSendEmail(slug, argsJSON string) string {
	var args struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.To == "" || args.Subject == "" || args.Body == "" {
		return "Error: to, subject, and body are all required"
	}

	host := s.getBrainSetting(slug, "email_outbound_host")
	if host == "" {
		return "Error: outbound email not configured. Set SMTP settings in Brain Settings → Integrations."
	}

	s.sendOutboundEmail(slug, args.To, args.Subject, args.Body, "")
	return fmt.Sprintf("Email sent to %s with subject: %s", args.To, args.Subject)
}

// toolSendTelegram sends a Telegram message via the channel's linked chat.
func (s *Server) toolSendTelegram(slug, channelID, argsJSON string) string {
	var args struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.Message == "" {
		return "Error: message is required"
	}

	botToken := s.getBrainSetting(slug, "telegram_bot_token")
	if botToken == "" {
		return "Error: Telegram bot not configured. Set bot token in Brain Settings → Integrations."
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return "Error opening workspace"
	}

	// Find linked Telegram chat for this channel
	var chatIDStr string
	_ = wdb.DB.QueryRow(
		"SELECT source_key FROM channel_integrations WHERE channel_id = ? AND source_type = 'telegram'",
		channelID,
	).Scan(&chatIDStr)

	if chatIDStr == "" {
		return "Error: this channel is not linked to a Telegram chat"
	}

	var chatID int64
	fmt.Sscanf(chatIDStr, "%d", &chatID)
	if chatID == 0 {
		return "Error: invalid Telegram chat ID"
	}

	if err := sendTelegramMessage(botToken, chatID, args.Message); err != nil {
		return "Error sending Telegram message: " + err.Error()
	}

	return fmt.Sprintf("Message sent to Telegram chat %s", chatIDStr)
}

// toolWebSearch searches DuckDuckGo HTML and returns formatted results.
func toolWebSearch(argsJSON string) string {
	var args struct {
		Query      string `json:"query"`
		NumResults int    `json:"num_results"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.Query == "" {
		return "Error: query is required"
	}
	if args.NumResults <= 0 {
		args.NumResults = 5
	}
	if args.NumResults > 10 {
		args.NumResults = 10
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm("https://html.duckduckgo.com/html/", url.Values{
		"q": {args.Query},
	})
	if err != nil {
		return "Error searching: " + err.Error()
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "Error reading search results: " + err.Error()
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "Error parsing search results"
	}

	type searchResult struct {
		Title   string
		Snippet string
		URL     string
	}
	var results []searchResult

	// Walk the DOM to find result elements
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" {
			for _, a := range n.Attr {
				if a.Key == "class" && strings.Contains(a.Val, "result__body") {
					r := extractDDGResult(n)
					if r.Title != "" && r.URL != "" {
						results = append(results, searchResult(r))
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if len(results) == 0 {
		return fmt.Sprintf("No results found for \"%s\"", args.Query)
	}
	if len(results) > args.NumResults {
		results = results[:args.NumResults]
	}

	var lines []string
	for i, r := range results {
		lines = append(lines, fmt.Sprintf("%d. **%s**\n   %s\n   %s", i+1, r.Title, r.Snippet, r.URL))
	}
	return fmt.Sprintf("Search results for \"%s\":\n\n%s", args.Query, strings.Join(lines, "\n\n"))
}

// extractDDGResult extracts title, URL, and snippet from a DuckDuckGo result__body div.
func extractDDGResult(n *html.Node) struct {
	Title, Snippet, URL string
} {
	var result struct{ Title, Snippet, URL string }

	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if node.Data == "a" {
				for _, a := range node.Attr {
					if a.Key == "class" && strings.Contains(a.Val, "result__a") {
						result.Title = textContent(node)
						for _, attr := range node.Attr {
							if attr.Key == "href" {
								u := attr.Val
								// DDG wraps URLs in a redirect; extract the actual URL
								if idx := strings.Index(u, "uddg="); idx >= 0 {
									u = u[idx+5:]
									if end := strings.Index(u, "&"); end >= 0 {
										u = u[:end]
									}
									if decoded, err := url.QueryUnescape(u); err == nil {
										u = decoded
									}
								}
								result.URL = u
							}
						}
					}
				}
			}
			if node.Data == "a" || node.Data == "span" || node.Data == "div" {
				for _, a := range node.Attr {
					if a.Key == "class" && strings.Contains(a.Val, "result__snippet") {
						result.Snippet = textContent(node)
					}
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return result
}

// textContent returns the text content of an HTML node and its descendants.
func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(textContent(c))
	}
	return strings.TrimSpace(sb.String())
}

// toolFetchURL fetches a URL and extracts text content.
func toolFetchURL(argsJSON string) string {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "Error parsing arguments: " + err.Error()
	}
	if args.URL == "" {
		return "Error: url is required"
	}

	parsed, err := url.Parse(args.URL)
	if err != nil {
		return "Error: invalid URL"
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "Error: only http and https URLs are supported"
	}

	// SSRF protection: resolve DNS and block private IPs
	host := parsed.Hostname()
	ips, err := net.LookupIP(host)
	if err != nil {
		return "Error: could not resolve host " + host
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return "Error: access to private/internal addresses is not allowed"
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", args.URL, nil)
	if err != nil {
		return "Error creating request: " + err.Error()
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; NexusBot/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return "Error fetching URL: " + err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Sprintf("Error: server returned status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "Error reading response: " + err.Error()
	}

	title, content := extractHTMLText(string(bodyBytes))

	// Truncate to 8000 chars
	if len(content) > 8000 {
		content = content[:8000] + "\n\n[content truncated]"
	}

	if title != "" {
		return fmt.Sprintf("**Title:** %s\n\n%s", title, content)
	}
	return content
}

// isPrivateIP checks if an IP is in a private/reserved range.
func isPrivateIP(ip net.IP) bool {
	privateRanges := []struct {
		network *net.IPNet
	}{
		{parseCIDR("127.0.0.0/8")},
		{parseCIDR("10.0.0.0/8")},
		{parseCIDR("172.16.0.0/12")},
		{parseCIDR("192.168.0.0/16")},
		{parseCIDR("169.254.0.0/16")},
		{parseCIDR("::1/128")},
		{parseCIDR("fc00::/7")},
		{parseCIDR("fe80::/10")},
	}
	for _, r := range privateRanges {
		if r.network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseCIDR(s string) *net.IPNet {
	_, n, _ := net.ParseCIDR(s)
	return n
}
