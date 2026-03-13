package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
)

// handleListSkills returns loaded skills for a workspace.
func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	skillsDir := filepath.Join(brainDir, "skills")
	os.MkdirAll(skillsDir, 0755)
	skills := brain.LoadSkills(brainDir)
	if skills == nil {
		skills = []brain.Skill{}
	}

	// Set enabled state from brain_settings
	s.applySkillEnabledState(slug, skills)

	writeJSON(w, http.StatusOK, map[string]any{"skills": skills})
}

// handleGetSkill returns a single skill file content.
func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileName := r.PathValue("file")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	path := filepath.Join(brainDir, "skills", fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"file": fileName, "content": string(data)})
}

// handleUpdateSkill updates a skill file.
func (s *Server) handleUpdateSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileName := r.PathValue("file")

	var req struct {
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	skillsDir := filepath.Join(brainDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create skills dir")
		return
	}

	path := filepath.Join(skillsDir, fileName)
	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save skill")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListSkillTemplates returns all built-in skill templates with installed status.
func (s *Server) handleListSkillTemplates(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	installed := brain.LoadSkills(brainDir)
	installedSet := make(map[string]bool, len(installed))
	for _, sk := range installed {
		installedSet[sk.FileName] = true
	}

	type SkillTemplate struct {
		brain.Skill
		Installed bool   `json:"installed"`
		Content   string `json:"content"`
	}

	var templates []SkillTemplate
	for fileName, content := range brain.DefaultSkills {
		sk := brain.ParseSkillContent(content)
		sk.FileName = fileName
		templates = append(templates, SkillTemplate{Skill: sk, Installed: installedSet[fileName], Content: content})
	}

	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

// handleCreateSkill creates a new skill file.
func (s *Server) handleCreateSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var req struct {
		FileName string `json:"file_name"`
		Content  string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.FileName == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "file_name and content required")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	skillsDir := filepath.Join(brainDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create skills dir")
		return
	}

	path := filepath.Join(skillsDir, req.FileName)
	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save skill")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "file_name": req.FileName})
}

// handleDeleteSkill deletes a skill file.
func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileName := r.PathValue("file")

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	path := filepath.Join(brainDir, "skills", fileName)
	if err := os.Remove(path); err != nil {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleGenerateSkill uses LLM to generate a skill config from natural language description.
func (s *Server) handleGenerateSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

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

	metaPrompt := `You are a skill designer for a team AI assistant called Brain. Given a description, generate a structured skill configuration.

Return ONLY valid JSON with these fields:
{
  "name": "Short skill name (2-4 words)",
  "description": "One sentence describing what the skill does",
  "trigger": "mention|schedule|keyword",
  "autonomy": "reactive|proactive",
  "channels": "",
  "prompt": "Detailed multi-line instructions for how the skill should behave. Include specific steps, output formats, and trigger phrases."
}

Trigger types:
- "mention": activated when someone @mentions Brain with a relevant request
- "schedule": runs on a schedule (daily, weekly, etc.)
- "keyword": activated when certain keywords appear in conversation

Autonomy types:
- "reactive": only responds when triggered
- "proactive": can initiate actions on its own (e.g., reminders, digests)

User description: ` + req.Description

	client := brain.NewClient(apiKey, model)
	response, skillUsage, err := client.Complete(metaPrompt, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LLM error: "+err.Error())
		return
	}
	s.trackUsage(slug, skillUsage, model, "agent", "", "")

	// Strip markdown code fences if present
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

// handleToggleSkill enables or disables a skill via brain_settings.
func (s *Server) handleToggleSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileName := r.PathValue("file")

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	key := "skill_enabled_" + fileName
	val := "true"
	if !req.Enabled {
		val = "false"
	}
	_, err = wdb.DB.Exec(
		"INSERT INTO brain_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, val,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save setting")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "enabled": req.Enabled})
}

// applySkillEnabledState sets the Enabled field on each skill based on brain_settings.
// Default is true (enabled) if no setting exists.
func (s *Server) applySkillEnabledState(slug string, skills []brain.Skill) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		// Default all to enabled
		for i := range skills {
			skills[i].Enabled = true
		}
		return
	}

	rows, err := wdb.DB.Query("SELECT key, value FROM brain_settings WHERE key LIKE 'skill_enabled_%'")
	if err != nil {
		for i := range skills {
			skills[i].Enabled = true
		}
		return
	}
	defer rows.Close()

	disabled := make(map[string]bool)
	for rows.Next() {
		var key, val string
		rows.Scan(&key, &val)
		fileName := strings.TrimPrefix(key, "skill_enabled_")
		if val == "false" {
			disabled[fileName] = true
		}
	}

	for i := range skills {
		skills[i].Enabled = !disabled[skills[i].FileName]
	}
}
