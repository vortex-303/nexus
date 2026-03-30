package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// BehaviorConfig holds agent behavior settings.
type BehaviorConfig struct {
	FollowTTLMinutes  int               `json:"follow_ttl_minutes"`
	FollowMaxMessages int               `json:"follow_max_messages"`
	CooldownSeconds   int               `json:"cooldown_seconds"`
	ChannelModes      map[string]string `json:"channel_modes"` // "active" or "silent" per channel

	RespondToAgents   bool `json:"respond_to_agents"`    // respond to other agent messages
	AutoFollowThreads bool `json:"auto_follow_threads"`  // auto-follow threads when agent's message becomes a thread parent
	RespondInThreads  bool `json:"respond_in_threads"`   // respond to thread replies
}

// isChannelSilenced returns true if the agent has explicitly silenced this channel.
func isChannelSilenced(agent *Agent, channelID string) bool {
	if mode, ok := agent.BehaviorConfig.ChannelModes[channelID]; ok {
		return mode == "silent"
	}
	return false
}

// Agent represents an AI agent in a workspace.
type Agent struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Avatar      string  `json:"avatar"`
	Role        string  `json:"role"`
	Goal        string  `json:"goal"`
	Backstory   string  `json:"backstory"`
	Instructions string `json:"instructions"`

	Model       string  `json:"model"`
	ImageModel  string  `json:"image_model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`

	Tools           json.RawMessage `json:"tools"`
	Channels        json.RawMessage `json:"channels"`
	KnowledgeAccess bool            `json:"knowledge_access"`
	MemoryAccess    bool            `json:"memory_access"`
	CanDelegate     bool            `json:"can_delegate"`

	MaxIterations    int    `json:"max_iterations"`
	RequiresApproval json.RawMessage `json:"requires_approval"`
	Constraints      string `json:"constraints"`
	EscalationPrompt string `json:"escalation_prompt"`

	TriggerType    string         `json:"trigger_type"`
	TriggerConfig  string         `json:"trigger_config"`
	BehaviorConfig BehaviorConfig `json:"behavior_config"`

	IsSystem bool `json:"is_system"`
	IsActive bool `json:"is_active"`

	TemplateID string `json:"template_id,omitempty"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		Name             string   `json:"name"`
		Description      string   `json:"description"`
		Avatar           string   `json:"avatar"`
		Role             string   `json:"role"`
		Goal             string   `json:"goal"`
		Backstory        string   `json:"backstory"`
		Instructions     string   `json:"instructions"`
		Model            string   `json:"model"`
		ImageModel       string   `json:"image_model"`
		Temperature      *float64 `json:"temperature"`
		MaxTokens        *int     `json:"max_tokens"`
		Tools            []string `json:"tools"`
		Channels         []string `json:"channels"`
		KnowledgeAccess  bool     `json:"knowledge_access"`
		MemoryAccess     bool     `json:"memory_access"`
		CanDelegate      bool     `json:"can_delegate"`
		MaxIterations    *int     `json:"max_iterations"`
		Constraints      string   `json:"constraints"`
		EscalationPrompt string   `json:"escalation_prompt"`
		TriggerType      string          `json:"trigger_type"`
		BehaviorConfig   json.RawMessage `json:"behavior_config"`
		TemplateID       string          `json:"template_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	agentID := "agent_" + id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	temp := 0.7
	if req.Temperature != nil {
		temp = *req.Temperature
	}
	maxTok := 2048
	if req.MaxTokens != nil {
		maxTok = *req.MaxTokens
	}
	maxIter := 5
	if req.MaxIterations != nil {
		maxIter = *req.MaxIterations
	}
	if req.TriggerType == "" {
		req.TriggerType = "mention"
	}

	toolsJSON, _ := json.Marshal(req.Tools)
	if req.Tools == nil {
		toolsJSON = []byte("[]")
	}
	channelsJSON, _ := json.Marshal(req.Channels)
	if req.Channels == nil {
		channelsJSON = []byte("[]")
	}
	behaviorJSON := "{}"
	if len(req.BehaviorConfig) > 0 {
		behaviorJSON = string(req.BehaviorConfig)
	}

	_, err = wdb.DB.Exec(`
		INSERT INTO agents (id, name, description, avatar, role, goal, backstory, instructions,
			model, image_model, temperature, max_tokens, tools, channels, knowledge_access, memory_access, can_delegate,
			max_iterations, constraints, escalation_prompt, trigger_type, behavior_config, template_id, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		agentID, req.Name, req.Description, req.Avatar, req.Role, req.Goal, req.Backstory, req.Instructions,
		req.Model, req.ImageModel, temp, maxTok, string(toolsJSON), string(channelsJSON), req.KnowledgeAccess, req.MemoryAccess, req.CanDelegate,
		maxIter, req.Constraints, req.EscalationPrompt, req.TriggerType, behaviorJSON, req.TemplateID, claims.UserID, now, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create agent: "+err.Error())
		return
	}

	// Create matching member so agent appears in channels and can send messages
	agentColor := assignMemberColor(wdb)
	_, err = wdb.DB.Exec(
		"INSERT INTO members (id, display_name, role, color) VALUES (?, ?, 'agent', ?)",
		agentID, req.Name, agentColor,
	)
	if err != nil {
		logger.WithCategory(logger.CatAgent).Error().Err(err).Str("workspace", slug).Str("agent_id", agentID).Msg("failed to create member for agent")
	}

	agent := s.loadAgentByID(slug, agentID)
	writeJSON(w, http.StatusCreated, agent)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	// Ensure Brain + built-in agents are seeded
	s.ensureBrainMember(slug)
	s.ensureBuiltinAgents(slug)

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	rows, err := wdb.DB.Query(`
		SELECT id, name, description, avatar, role, goal, backstory, instructions,
			model, image_model, temperature, max_tokens, tools, channels, knowledge_access, memory_access, can_delegate,
			max_iterations, requires_approval, constraints, escalation_prompt,
			trigger_type, trigger_config, behavior_config, is_system, is_active, COALESCE(template_id,''), created_by, created_at, updated_at
		FROM agents ORDER BY is_system DESC, created_at ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query error")
		return
	}
	defer rows.Close()

	agents := []Agent{}
	for rows.Next() {
		var a Agent
		var toolsStr, channelsStr, reqApprovalStr, behaviorStr string
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.Avatar, &a.Role, &a.Goal, &a.Backstory, &a.Instructions,
			&a.Model, &a.ImageModel, &a.Temperature, &a.MaxTokens, &toolsStr, &channelsStr, &a.KnowledgeAccess, &a.MemoryAccess, &a.CanDelegate,
			&a.MaxIterations, &reqApprovalStr, &a.Constraints, &a.EscalationPrompt,
			&a.TriggerType, &a.TriggerConfig, &behaviorStr, &a.IsSystem, &a.IsActive, &a.TemplateID, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			continue
		}
		a.Tools = json.RawMessage(toolsStr)
		a.Channels = json.RawMessage(channelsStr)
		a.RequiresApproval = json.RawMessage(reqApprovalStr)
		_ = json.Unmarshal([]byte(behaviorStr), &a.BehaviorConfig)
		if !strings.Contains(behaviorStr, "auto_follow_threads") {
			a.BehaviorConfig.AutoFollowThreads = true
		}
		if !strings.Contains(behaviorStr, "respond_in_threads") {
			a.BehaviorConfig.RespondInThreads = true
		}
		agents = append(agents, a)
	}

	writeJSON(w, http.StatusOK, agents)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	agentID := r.PathValue("agentID")

	agent := s.loadAgentByID(slug, agentID)
	if agent == nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	agentID := r.PathValue("agentID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req map[string]any
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Don't allow updating system agent's core identity
	var isSystem bool
	_ = wdb.DB.QueryRow("SELECT is_system FROM agents WHERE id = ?", agentID).Scan(&isSystem)
	if isSystem {
		delete(req, "name")
		delete(req, "is_system")
	}

	allowed := map[string]string{
		"name": "name", "description": "description", "avatar": "avatar",
		"role": "role", "goal": "goal", "backstory": "backstory", "instructions": "instructions",
		"model": "model", "image_model": "image_model", "temperature": "temperature", "max_tokens": "max_tokens",
		"knowledge_access": "knowledge_access", "memory_access": "memory_access", "can_delegate": "can_delegate",
		"max_iterations": "max_iterations", "constraints": "constraints", "escalation_prompt": "escalation_prompt",
		"trigger_type": "trigger_type", "trigger_config": "trigger_config", "is_active": "is_active",
	}

	var sets []string
	var args []any
	for key, col := range allowed {
		if val, ok := req[key]; ok {
			sets = append(sets, col+" = ?")
			args = append(args, val)
		}
	}

	// Handle JSON array/object fields
	if tools, ok := req["tools"]; ok {
		j, _ := json.Marshal(tools)
		sets = append(sets, "tools = ?")
		args = append(args, string(j))
	}
	if channels, ok := req["channels"]; ok {
		j, _ := json.Marshal(channels)
		sets = append(sets, "channels = ?")
		args = append(args, string(j))
	}
	if bc, ok := req["behavior_config"]; ok {
		j, _ := json.Marshal(bc)
		sets = append(sets, "behavior_config = ?")
		args = append(args, string(j))
	}

	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "no valid fields to update")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sets = append(sets, "updated_at = ?")
	args = append(args, now)
	args = append(args, agentID)

	_, err = wdb.DB.Exec("UPDATE agents SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed: "+err.Error())
		return
	}

	// Update member display_name if name changed
	if name, ok := req["name"]; ok {
		_, _ = wdb.DB.Exec("UPDATE members SET display_name = ? WHERE id = ?", name, agentID)
	}

	agent := s.loadAgentByID(slug, agentID)
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	agentID := r.PathValue("agentID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Don't allow deleting system agent
	var isSystem bool
	_ = wdb.DB.QueryRow("SELECT is_system FROM agents WHERE id = ?", agentID).Scan(&isSystem)
	if isSystem {
		writeError(w, http.StatusForbidden, "cannot delete system agent")
		return
	}

	_, _ = wdb.DB.Exec("DELETE FROM agents WHERE id = ?", agentID)
	_, _ = wdb.DB.Exec("DELETE FROM members WHERE id = ?", agentID)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGenerateAgentConfig(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		Description string `json:"description"`
	}
	if err := readJSON(r, &req); err != nil || req.Description == "" {
		writeError(w, http.StatusBadRequest, "description is required")
		return
	}

	apiKey, model := s.getBrainSettings(slug)
	if apiKey == "" {
		writeError(w, http.StatusBadRequest, "no API key configured")
		return
	}

	metaPrompt := `You are a team builder. Given a description of a desired AI agent, generate a structured configuration for it.

Return ONLY valid JSON with these fields:
{
  "name": "Short agent name (2-3 words)",
  "description": "One sentence description",
  "avatar": "A single emoji that represents this agent",
  "role": "Their job title/role (e.g. 'Sales Development Rep')",
  "goal": "Their primary objective in one sentence",
  "backstory": "2-3 sentences about their background and expertise",
  "instructions": "Specific instructions for how they should behave, respond, and handle situations. 3-5 bullet points.",
  "constraints": "Things they should NOT do. 1-3 bullet points.",
  "escalation_prompt": "When and how they should hand off to Brain or a human",
  "tools": ["list", "of", "tool", "names"],
  "knowledge_access": true/false,
  "memory_access": true/false
}

Available tools: create_task, list_tasks, search_workspace, create_document, search_knowledge

User description: ` + req.Description

	client := brain.NewClient(apiKey, model)
	response, agentUsage, err := client.Complete(metaPrompt, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LLM error: "+err.Error())
		return
	}
	s.trackUsage(slug, agentUsage, model, "agent", "", "")

	// Try to parse as JSON, return raw if valid
	response = strings.TrimSpace(response)
	// Strip markdown code fences if present
	if strings.HasPrefix(response, "```") {
		lines := strings.Split(response, "\n")
		if len(lines) > 2 {
			lines = lines[1 : len(lines)-1]
			response = strings.Join(lines, "\n")
		}
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(response), &config); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse LLM response")
		return
	}

	writeJSON(w, http.StatusOK, config)
}

func (s *Server) handleEditAgentWithAI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		Instruction string         `json:"instruction"`
		Current     map[string]any `json:"current"`
	}
	if err := readJSON(r, &req); err != nil || req.Instruction == "" {
		writeError(w, http.StatusBadRequest, "instruction is required")
		return
	}

	apiKey, model := s.getBrainSettings(slug)
	if apiKey == "" {
		writeError(w, http.StatusBadRequest, "no API key configured")
		return
	}

	currentJSON, _ := json.Marshal(req.Current)

	metaPrompt := `You are editing an existing AI agent's configuration. The user will describe what they want to change. Apply their instructions to the current config and return the COMPLETE updated config.

Rules:
- Only change the fields the user's instruction targets. Keep everything else exactly as-is.
- If the user asks to improve or enhance a field, rewrite it better while keeping the intent.
- If the user asks to add tools, add them to the existing tools list.
- If the user asks to change personality/tone, update backstory + instructions accordingly.
- Return the COMPLETE config (not just changed fields).

Return ONLY valid JSON with these fields:
{
  "name": "Short agent name (2-3 words)",
  "description": "One sentence description",
  "avatar": "A single emoji that represents this agent",
  "role": "Their job title/role",
  "goal": "Their primary objective in one sentence",
  "backstory": "2-3 sentences about their background and expertise",
  "instructions": "Specific instructions for how they should behave. 3-5 bullet points.",
  "constraints": "Things they should NOT do. 1-3 bullet points.",
  "escalation_prompt": "When and how they should hand off to Brain or a human",
  "tools": ["list", "of", "tool", "names"],
  "knowledge_access": true/false,
  "memory_access": true/false,
  "temperature": 0.0-2.0,
  "can_delegate": true/false,
  "max_iterations": 1-20,
  "trigger_type": "mention"
}

Available tools: create_task, list_tasks, search_workspace, create_document, search_knowledge, generate_image, web_search, fetch_url, delegate_to_agent

Current agent config:
` + string(currentJSON) + `

User instruction: ` + req.Instruction

	client := brain.NewClient(apiKey, model)
	response, agentUsage2, err := client.Complete(metaPrompt, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LLM error: "+err.Error())
		return
	}
	s.trackUsage(slug, agentUsage2, model, "agent", "", "")

	response = strings.TrimSpace(response)
	if strings.HasPrefix(response, "```") {
		lines := strings.Split(response, "\n")
		if len(lines) > 2 {
			lines = lines[1 : len(lines)-1]
			response = strings.Join(lines, "\n")
		}
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(response), &config); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse LLM response")
		return
	}

	writeJSON(w, http.StatusOK, config)
}

func (s *Server) handleListAgentTemplates(w http.ResponseWriter, r *http.Request) {
	templates := brain.GetTemplates()
	writeJSON(w, http.StatusOK, templates)
}

func (s *Server) handleCreateAgentFromTemplate(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		TemplateID string `json:"template_id"`
	}
	if err := readJSON(r, &req); err != nil || req.TemplateID == "" {
		writeError(w, http.StatusBadRequest, "template_id is required")
		return
	}

	tmpl := brain.GetTemplate(req.TemplateID)
	if tmpl == nil {
		writeError(w, http.StatusNotFound, "template not found")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	agentID := "agent_" + id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	toolsJSON, _ := json.Marshal(tmpl.Tools)

	_, err = wdb.DB.Exec(`
		INSERT INTO agents (id, name, description, avatar, role, goal, backstory, instructions,
			model, temperature, max_tokens, tools, channels, knowledge_access, memory_access, can_delegate,
			max_iterations, constraints, escalation_prompt, trigger_type, template_id, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', 0.7, 2048, ?, '[]', ?, ?, FALSE, 5, ?, ?, 'mention', ?, ?, ?, ?)`,
		agentID, tmpl.Name, tmpl.Description, tmpl.Avatar, tmpl.Role, tmpl.Goal, tmpl.Backstory, tmpl.Instructions,
		string(toolsJSON), tmpl.KnowledgeAccess, tmpl.MemoryAccess,
		tmpl.Constraints, tmpl.EscalationPrompt, tmpl.ID, claims.UserID, now, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create agent: "+err.Error())
		return
	}

	// Create matching member
	tmplColor := assignMemberColor(wdb)
	_, _ = wdb.DB.Exec(
		"INSERT INTO members (id, display_name, role, color) VALUES (?, ?, 'agent', ?)",
		agentID, tmpl.Name, tmplColor,
	)

	// Copy default skills from template
	if len(tmpl.DefaultSkills) > 0 {
		if err := brain.EnsureAgentSkillsDir(s.cfg.DataDir, slug, agentID); err == nil {
			skillsDir := brain.AgentSkillsDir(s.cfg.DataDir, slug, agentID)
			for _, sk := range tmpl.DefaultSkills {
				_ = os.WriteFile(filepath.Join(skillsDir, sk.FileName), []byte(sk.Content), 0644)
			}
		}
	}

	agent := s.loadAgentByID(slug, agentID)
	writeJSON(w, http.StatusCreated, agent)
}

func (s *Server) loadAgentByID(slug, agentID string) *Agent {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return nil
	}

	var a Agent
	var toolsStr, channelsStr, reqApprovalStr, behaviorStr string
	err = wdb.DB.QueryRow(`
		SELECT id, name, description, avatar, role, goal, backstory, instructions,
			model, image_model, temperature, max_tokens, tools, channels, knowledge_access, memory_access, can_delegate,
			max_iterations, requires_approval, constraints, escalation_prompt,
			trigger_type, trigger_config, behavior_config, is_system, is_active, COALESCE(template_id,''), created_by, created_at, updated_at
		FROM agents WHERE id = ?`, agentID,
	).Scan(&a.ID, &a.Name, &a.Description, &a.Avatar, &a.Role, &a.Goal, &a.Backstory, &a.Instructions,
		&a.Model, &a.ImageModel, &a.Temperature, &a.MaxTokens, &toolsStr, &channelsStr, &a.KnowledgeAccess, &a.MemoryAccess, &a.CanDelegate,
		&a.MaxIterations, &reqApprovalStr, &a.Constraints, &a.EscalationPrompt,
		&a.TriggerType, &a.TriggerConfig, &behaviorStr, &a.IsSystem, &a.IsActive, &a.TemplateID, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil
	}
	a.Tools = json.RawMessage(toolsStr)
	a.Channels = json.RawMessage(channelsStr)
	a.RequiresApproval = json.RawMessage(reqApprovalStr)
	_ = json.Unmarshal([]byte(behaviorStr), &a.BehaviorConfig)
	// Default auto_follow_threads and respond_in_threads to true when not explicitly set
	if !strings.Contains(behaviorStr, "auto_follow_threads") {
		a.BehaviorConfig.AutoFollowThreads = true
	}
	if !strings.Contains(behaviorStr, "respond_in_threads") {
		a.BehaviorConfig.RespondInThreads = true
	}
	return &a
}

// loadAgentByName finds an agent by display name in a workspace.
func (s *Server) loadAgentByName(slug, name string) *Agent {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return nil
	}

	var agentID string
	err = wdb.DB.QueryRow("SELECT id FROM agents WHERE LOWER(name) = LOWER(?) AND is_active = TRUE", name).Scan(&agentID)
	if err != nil {
		return nil
	}
	return s.loadAgentByID(slug, agentID)
}

// getAgentTools returns the tool definitions filtered to only the tools this agent has access to.
func getAgentTools(agent *Agent, allTools []brain.ToolDef) []brain.ToolDef {
	var allowedTools []string
	_ = json.Unmarshal(agent.Tools, &allowedTools)
	if len(allowedTools) == 0 {
		return nil
	}

	allowed := map[string]bool{}
	for _, t := range allowedTools {
		allowed[t] = true
	}

	var filtered []brain.ToolDef
	for _, tool := range allTools {
		if allowed[tool.Function.Name] {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

// checkDMAgents returns active custom agents whose ID appears in the DM channel name.
func (s *Server) checkDMAgents(slug, channelName string) []*Agent {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return nil
	}
	rows, err := wdb.DB.Query("SELECT id FROM agents WHERE is_active = TRUE AND is_system = FALSE")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			continue
		}
		if strings.Contains(channelName, agentID) {
			if agent := s.loadAgentByID(slug, agentID); agent != nil {
				agents = append(agents, agent)
			}
		}
	}
	return agents
}

// checkChannelAgents returns active agents assigned to this channel that should auto-respond.
// An agent qualifies if: active, not brain, has a non-empty channels list that includes this channel,
// channel is not silenced, and cooldown has elapsed.
func (s *Server) checkChannelAgents(slug, channelID, senderID string) []*Agent {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return nil
	}
	rows, err := wdb.DB.Query("SELECT id FROM agents WHERE is_active = TRUE AND id != 'brain'")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			continue
		}
		// Don't trigger on agent's own messages
		if agentID == senderID {
			continue
		}
		agent := s.loadAgentByID(slug, agentID)
		if agent == nil {
			continue
		}

		// Check if agent should auto-respond in this channel:
		// 1. Via channels array (legacy)
		// 2. Via channel_modes with "always" or "active" mode
		inChannel := false
		var channels []string
		_ = json.Unmarshal(agent.Channels, &channels)
		if len(channels) > 0 && isAgentInChannel(agent, channelID) {
			inChannel = true
		}
		if mode, ok := agent.BehaviorConfig.ChannelModes[channelID]; ok {
			if mode == "always" || mode == "active" {
				inChannel = true
			} else if mode == "silent" {
				continue
			}
		}
		if !inChannel {
			continue
		}

		// Check cooldown
		if s.convTracker != nil {
			cooldown := agent.BehaviorConfig.CooldownSeconds
			if cooldown > 0 {
				key := ConversationKey{Slug: slug, ChannelID: channelID, AgentID: agentID}
				if !s.convTracker.CheckCooldown(key, cooldown) {
					continue
				}
			}
		}
		agents = append(agents, agent)
	}
	return agents
}

// checkFollowingAgents returns active agents that are following conversations in this channel.
func (s *Server) checkFollowingAgents(slug, channelID string, senderID string) []*Agent {
	if s.convTracker == nil {
		return nil
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return nil
	}
	rows, err := wdb.DB.Query("SELECT id FROM agents WHERE is_active = TRUE AND id != 'brain'")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			continue
		}
		// Don't trigger on agent's own messages
		if agentID == senderID {
			continue
		}
		key := ConversationKey{Slug: slug, ChannelID: channelID, AgentID: agentID}
		if s.convTracker.RecordMessage(key) {
			if agent := s.loadAgentByID(slug, agentID); agent != nil {
				agents = append(agents, agent)
			}
		}
	}
	return agents
}

// isAgentInChannel checks whether an agent is allowed to respond in a given channel.
func isAgentInChannel(agent *Agent, channelID string) bool {
	var channels []string
	_ = json.Unmarshal(agent.Channels, &channels)
	if len(channels) == 0 {
		return true // empty = all channels
	}
	for _, ch := range channels {
		if ch == channelID {
			return true
		}
	}
	return false
}

// sendAgentMessage saves and broadcasts a message from an agent. Returns the message ID.
// fromAgent indicates the message was triggered by another agent (prevents infinite chains).
func (s *Server) sendAgentMessage(slug, channelID, parentID string, agent *Agent, content string, fromAgent bool, toolsUsed ...string) string {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return ""
	}

	msgID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	metadata := "{}"
	if len(toolsUsed) > 0 {
		metaJSON, _ := json.Marshal(map[string]any{"tools_used": toolsUsed})
		metadata = string(metaJSON)
	}

	if parentID != "" {
		_, err = wdb.DB.Exec(
			"INSERT INTO messages (id, channel_id, sender_id, content, metadata, parent_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			msgID, channelID, agent.ID, content, metadata, parentID, now,
		)
	} else {
		_, err = wdb.DB.Exec(
			"INSERT INTO messages (id, channel_id, sender_id, content, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			msgID, channelID, agent.ID, content, metadata, now,
		)
	}
	if err != nil {
		logger.WithCategory(logger.CatAgent).Error().Err(err).Str("agent", agent.Name).Msg("failed to save message")
		return ""
	}

	// Update thread metadata if replying in a thread
	if parentID != "" {
		wdb.DB.Exec("UPDATE messages SET reply_count = reply_count + 1, latest_reply_at = ? WHERE id = ?", now, parentID)
	}

	h := s.hubs.Get(slug)
	h.Broadcast(channelID, hub.MakeEnvelope(hub.TypeMessageNew, hub.MessageNewPayload{
		ID:         msgID,
		ChannelID:  channelID,
		SenderID:   agent.ID,
		SenderName: agent.Name,
		Content:    content,
		CreatedAt:  now,
		ToolsUsed:  toolsUsed,
		ParentID:   parentID,
	}), "")

	s.onPulse(slug, Pulse{
		Type: "agent.responded", ActorID: agent.ID, ActorName: agent.Name,
		ChannelID: channelID, EntityID: msgID, Source: "agent",
		Summary: agent.Name + " responded in channel",
	})
	return msgID
}

// --- Agent Skills CRUD ---

func (s *Server) handleListAgentSkills(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	agentID := r.PathValue("agentID")

	skills := brain.LoadAgentSkills(s.cfg.DataDir, slug, agentID)
	if skills == nil {
		skills = []brain.Skill{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": skills})
}

func (s *Server) handleGetAgentSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	agentID := r.PathValue("agentID")
	file := r.PathValue("file")

	skillsDir := brain.AgentSkillsDir(s.cfg.DataDir, slug, agentID)
	path := filepath.Join(skillsDir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"file": file, "content": string(data)})
}

func (s *Server) handleUpdateAgentSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	agentID := r.PathValue("agentID")
	file := r.PathValue("file")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := brain.EnsureAgentSkillsDir(s.cfg.DataDir, slug, agentID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create skills directory")
		return
	}

	skillsDir := brain.AgentSkillsDir(s.cfg.DataDir, slug, agentID)
	path := filepath.Join(skillsDir, file)
	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save skill")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteAgentSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	agentID := r.PathValue("agentID")
	file := r.PathValue("file")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	skillsDir := brain.AgentSkillsDir(s.cfg.DataDir, slug, agentID)
	path := filepath.Join(skillsDir, file)
	if err := os.Remove(path); err != nil {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
