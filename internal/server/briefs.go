package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// Brief represents a Living Brief.
type Brief struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Template     string `json:"template"`
	Topic        string `json:"topic"`
	Content      string `json:"content"`
	GeneratedAt  string `json:"generated_at"`
	Schedule     string `json:"schedule"`
	ScheduleTime string `json:"schedule_time"`
	ShareToken   string `json:"share_token,omitempty"`
	IsPublic     bool   `json:"is_public"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// Built-in brief templates with their generation prompts.
var briefTemplates = map[string]struct {
	Title       string
	Description string
}{
	"workspace_pulse": {
		Title:       "Workspace Pulse",
		Description: "How's the team doing? Activity, velocity, energy.",
	},
	"north_star_status": {
		Title:       "North Star Status",
		Description: "Are we aligned with our mission? What's drifting?",
	},
	"team_health": {
		Title:       "Team Health",
		Description: "Behavioral analysis: engagement, workload, collaboration.",
	},
	"custom": {
		Title:       "Custom Brief",
		Description: "Brief on a custom topic.",
	},
}

// handleListBriefs returns all briefs for a workspace.
// GET /api/workspaces/{slug}/briefs
func (s *Server) handleListBriefs(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	rows, err := wdb.DB.Query(`
		SELECT id, title, template, topic, content, COALESCE(generated_at, ''), schedule, schedule_time, COALESCE(share_token, ''), COALESCE(is_public, 0), created_at, updated_at
		FROM living_briefs ORDER BY created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query error")
		return
	}
	defer rows.Close()

	var briefs []Brief
	for rows.Next() {
		var b Brief
		rows.Scan(&b.ID, &b.Title, &b.Template, &b.Topic, &b.Content, &b.GeneratedAt, &b.Schedule, &b.ScheduleTime, &b.ShareToken, &b.IsPublic, &b.CreatedAt, &b.UpdatedAt)
		briefs = append(briefs, b)
	}
	if briefs == nil {
		briefs = []Brief{}
	}
	writeJSON(w, http.StatusOK, briefs)
}

// handleGetBrief returns a single brief.
// GET /api/workspaces/{slug}/briefs/{briefID}
func (s *Server) handleGetBrief(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	briefID := r.PathValue("briefID")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	var b Brief
	err = wdb.DB.QueryRow(`
		SELECT id, title, template, topic, content, COALESCE(generated_at, ''), schedule, schedule_time, COALESCE(share_token, ''), COALESCE(is_public, 0), created_at, updated_at
		FROM living_briefs WHERE id = ?
	`, briefID).Scan(&b.ID, &b.Title, &b.Template, &b.Topic, &b.Content, &b.GeneratedAt, &b.Schedule, &b.ScheduleTime, &b.ShareToken, &b.IsPublic, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "brief not found")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// handleCreateBrief creates a new brief.
// POST /api/workspaces/{slug}/briefs
func (s *Server) handleCreateBrief(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var req struct {
		Title    string `json:"title"`
		Template string `json:"template"`
		Topic    string `json:"topic"`
		Schedule string `json:"schedule"`
		ScheduleTime string `json:"schedule_time"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Template == "" {
		req.Template = "custom"
	}
	if req.Title == "" {
		if tmpl, ok := briefTemplates[req.Template]; ok {
			req.Title = tmpl.Title
		} else {
			req.Title = "Untitled Brief"
		}
	}
	if req.Schedule == "" {
		req.Schedule = "manual"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	briefID := id.New()
	_, err = wdb.DB.Exec(`
		INSERT INTO living_briefs (id, title, template, topic, schedule, schedule_time)
		VALUES (?, ?, ?, ?, ?, ?)
	`, briefID, req.Title, req.Template, req.Topic, req.Schedule, req.ScheduleTime)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create error")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"id": briefID})
}

// handleDeleteBrief deletes a brief.
// DELETE /api/workspaces/{slug}/briefs/{briefID}
func (s *Server) handleDeleteBrief(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	briefID := r.PathValue("briefID")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	_, err = wdb.DB.Exec("DELETE FROM living_briefs WHERE id = ?", briefID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleGenerateBrief triggers generation for a specific brief.
// POST /api/workspaces/{slug}/briefs/{briefID}/generate
func (s *Server) handleGenerateBrief(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	briefID := r.PathValue("briefID")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	var b Brief
	err = wdb.DB.QueryRow(`
		SELECT id, title, template, topic FROM living_briefs WHERE id = ?
	`, briefID).Scan(&b.ID, &b.Title, &b.Template, &b.Topic)
	if err != nil {
		writeError(w, http.StatusNotFound, "brief not found")
		return
	}

	go s.generateBrief(slug, b)

	writeJSON(w, http.StatusOK, map[string]string{"status": "generating"})
}

// handleListBriefTemplates returns available brief templates.
// GET /api/workspaces/{slug}/briefs/templates
func (s *Server) handleListBriefTemplates(w http.ResponseWriter, r *http.Request) {
	type tmpl struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	var templates []tmpl
	for k, v := range briefTemplates {
		templates = append(templates, tmpl{ID: k, Title: v.Title, Description: v.Description})
	}
	writeJSON(w, http.StatusOK, templates)
}

// generateBrief produces content for a brief using the LLM.
func (s *Server) generateBrief(slug string, b Brief) {
	log := logger.WithCategory(logger.CatBrain)
	log.Info().Str("workspace", slug).Str("brief", b.Title).Str("template", b.Template).Msg("generating brief")

	rd, err := s.gatherReflectionData(slug, "daily")
	if err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("brief gather error")
		return
	}

	prompt := buildBriefPrompt(b, rd)

	result, usage, err := s.memoryComplete(slug,
		"You are Brain, generating a Living Brief for your workspace. Be engaging, data-driven, and concise. Write like a smart colleague giving a friendly update — not a corporate dashboard.",
		[]brain.Message{{Role: "user", Content: prompt}},
	)
	if err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("brief LLM error")
		return
	}
	s.trackUsage(slug, usage, "", "brief", "", "")

	result = strings.TrimSpace(result)
	result = stripCodeFences(result)
	if result == "" {
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	_, err = wdb.DB.Exec(`
		UPDATE living_briefs SET content = ?, generated_at = ?, updated_at = ? WHERE id = ?
	`, result, now, now, b.ID)
	if err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("brief save error")
		return
	}

	log.Info().Str("workspace", slug).Str("brief", b.Title).Int("chars", len(result)).Msg("brief generated")
}

// buildBriefPrompt constructs the LLM prompt for a specific brief template.
func buildBriefPrompt(b Brief, rd *ReflectionData) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Generate a Living Brief: **%s**\n\n", b.Title))

	// Add workspace data context
	sb.WriteString(formatWorkspaceDataSection(rd))

	// Template-specific instructions
	switch b.Template {
	case "workspace_pulse":
		sb.WriteString(workspacePulseInstructions)
	case "north_star_status":
		sb.WriteString(northStarStatusInstructions(rd))
	case "team_health":
		sb.WriteString(teamHealthInstructions)
	default: // custom
		sb.WriteString(customBriefInstructions(b.Topic))
	}

	sb.WriteString("\n\nOutput ONLY the markdown content. No code fences. Be specific — use real names, numbers, and channel names from the data. Keep it under 500 words.")

	return sb.String()
}

// formatWorkspaceDataSection creates the data context shared by all briefs.
func formatWorkspaceDataSection(rd *ReflectionData) string {
	var sb strings.Builder

	sb.WriteString("## Workspace Data\n\n")

	if rd.NorthStar != "" {
		sb.WriteString("**North Star:** " + rd.NorthStar + "\n")
		if rd.NorthStarWhy != "" {
			sb.WriteString("**Why:** " + rd.NorthStarWhy + "\n")
		}
		if rd.NorthStarSuccess != "" {
			sb.WriteString("**Success looks like:** " + rd.NorthStarSuccess + "\n")
		}
		if len(rd.ParsedThemes) > 0 {
			sb.WriteString("**Strategic Themes:** " + strings.Join(rd.ParsedThemes, ", ") + "\n")
		} else if rd.StrategicThemes != "" {
			sb.WriteString("**Themes:** " + rd.StrategicThemes + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("**Messages:** %d total (last 24h)\n", rd.TotalMessages))
	if len(rd.MessagesByMember) > 0 {
		sb.WriteString("**By member:** ")
		var parts []string
		for name, count := range rd.MessagesByMember {
			parts = append(parts, fmt.Sprintf("%s (%d)", name, count))
		}
		sb.WriteString(strings.Join(parts, ", ") + "\n")
	}
	if len(rd.QuietMembers) > 0 {
		sb.WriteString("**Quiet:** " + strings.Join(rd.QuietMembers, ", ") + "\n")
	}

	sb.WriteString(fmt.Sprintf("\n**Tasks:** %d open, %d created, %d completed, %d overdue\n", rd.OpenTasks, rd.TasksCreated, rd.TasksCompleted, rd.TasksOverdue))
	sb.WriteString(fmt.Sprintf("**Brain:** %d mentions | **Content:** %d docs, %d files | **Memory:** %d active\n\n", rd.BrainMentions, rd.DocsCreated, rd.FilesUploaded, rd.MemoryCount))

	if len(rd.ChannelActivity) > 0 {
		sb.WriteString("**Channels:** ")
		var chParts []string
		for ch, count := range rd.ChannelActivity {
			chParts = append(chParts, fmt.Sprintf("#%s (%d)", ch, count))
		}
		sb.WriteString(strings.Join(chParts, ", ") + "\n\n")
	}

	// Recent tasks for context
	if len(rd.RecentTaskTitles) > 0 {
		sb.WriteString("**Recent tasks:** ")
		sb.WriteString(strings.Join(rd.RecentTaskTitles, " | ") + "\n\n")
	}

	// Include previous reflection if available
	if rd.PreviousReflection != "" && len(rd.PreviousReflection) < 2000 {
		sb.WriteString("**Brain's Latest Self-Reflection:**\n" + rd.PreviousReflection + "\n\n")
	}

	return sb.String()
}

const workspacePulseInstructions = `
## Instructions

Write a "Workspace Pulse" — a friendly, synthetic overview of how the workspace is doing.

Structure:
### Team Activity
Who's active, who's been quiet, energy level (high/moderate/low).

### Work Velocity
Tasks created vs completed. Trend direction. Any bottlenecks.

### Hot Topics
Most active channels and what people are talking about.

### Mission Alignment
If a North Star is configured: are current tasks and conversations moving toward it?
Flag any drift — work that seems unrelated to the mission or strategic themes.
Keep it to 2-3 sentences, not a full analysis.

### Brain Status
How many questions Brain answered. What Brain was most useful for.

### What's Coming Up
Approaching deadlines, upcoming events, things that need attention.

### Vibe Check
One-sentence overall mood/energy read.
`

func northStarStatusInstructions(rd *ReflectionData) string {
	if rd.NorthStar == "" {
		return `
## Instructions

No North Star has been configured for this workspace yet.
Write a brief encouraging the team to set one, explaining the value of having a clear mission.
Suggest they go to Brain Settings → North Star to configure it.
`
	}

	var sb strings.Builder
	sb.WriteString(`
## Instructions

Write a "North Star Status" brief — a strategic alignment review.

`)
	sb.WriteString("**The Mission:** " + rd.NorthStar + "\n")
	if rd.NorthStarWhy != "" {
		sb.WriteString("**Why it matters:** " + rd.NorthStarWhy + "\n")
	}
	if rd.NorthStarSuccess != "" {
		sb.WriteString("**Success looks like:** " + rd.NorthStarSuccess + "\n")
	}
	if len(rd.ParsedThemes) > 0 {
		sb.WriteString("**Strategic Themes:** " + strings.Join(rd.ParsedThemes, ", ") + "\n")
	}

	sb.WriteString(`
Structure:
### Mission Recap
One-line reminder of the goal and why it matters.

### Alignment Score
Rate 0-10 with specific reasoning based on actual workspace data.
Reference specific tasks, conversations, or decisions that inform the score.

### Aligned Work
List specific tasks and activities that are actively advancing the mission.
Name names — who is driving progress and in what area.

### Drift Alerts
Be honest: is any work happening that doesn't connect to the mission?
List specific tasks or activities that seem off-track. This isn't criticism — it's awareness.

### Theme Check
For EACH strategic theme, give a one-line status:
- Theme name: **progressing** / **stalled** / **needs attention** — why (reference specific data)
`)

	if rd.NorthStarSuccess != "" {
		sb.WriteString(`
### Success Metrics
Based on the success criteria defined ("` + rd.NorthStarSuccess + `"):
How close are we? What's measurable? What evidence exists in the workspace data?
`)
	}

	sb.WriteString(`
### Recommended Focus
Top 2-3 specific, actionable things the team should prioritize THIS WEEK to advance the North Star.
Be concrete — not "work on X" but "finish task Y" or "address the gap in Z".
`)

	return sb.String()
}

const teamHealthInstructions = `
## Instructions

Write a "Team Health" brief — a behavioral analysis of the team.

Structure:
### Member Spotlight
For each active member: what they seem to be working on, activity level.

### Engagement Patterns
Who's most engaged. Any collaboration patterns (who talks in which channels).

### Engagement Risks
Members who've gone quiet or whose activity is declining. Be tactful but honest.

### Workload Check
Based on task assignments: who has the most open tasks? Anyone overloaded?

### Team Energy
Overall assessment of team health and engagement.
`

func customBriefInstructions(topic string) string {
	if topic == "" {
		topic = "the current state of the workspace"
	}
	return fmt.Sprintf(`
## Instructions

Write a brief on this topic: **%s**

Use the workspace data above to inform your analysis. Structure the brief with clear sections, be specific with data points, and provide actionable insights.
`, topic)
}

// handleShareBrief generates a share token for a brief.
// POST /api/workspaces/{slug}/briefs/{briefID}/share
func (s *Server) handleShareBrief(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	briefID := r.PathValue("briefID")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Check brief exists
	var existing string
	err = wdb.DB.QueryRow("SELECT id FROM living_briefs WHERE id = ?", briefID).Scan(&existing)
	if err != nil {
		writeError(w, http.StatusNotFound, "brief not found")
		return
	}

	token := id.Short()
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	_, err = wdb.DB.Exec(`
		UPDATE living_briefs SET share_token = ?, is_public = 1, updated_at = ? WHERE id = ?
	`, token, now, briefID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "share error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"share_token": token})
}

// handleUnshareBrief revokes the share token for a brief.
// DELETE /api/workspaces/{slug}/briefs/{briefID}/share
func (s *Server) handleUnshareBrief(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	briefID := r.PathValue("briefID")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	_, err = wdb.DB.Exec(`
		UPDATE living_briefs SET share_token = NULL, is_public = 0, updated_at = ? WHERE id = ?
	`, now, briefID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unshare error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "unshared"})
}

// handlePublicBrief serves a brief by its share token (no auth required).
// GET /api/briefs/public/{token}
func (s *Server) handlePublicBrief(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "token required")
		return
	}

	// Search all workspace DBs for this token
	slugs := s.hubs.ActiveSlugs()
	// Also check all workspaces from global DB
	rows, err := s.global.DB.Query("SELECT slug FROM workspaces")
	if err == nil {
		defer rows.Close()
		slugSet := make(map[string]bool)
		for _, sl := range slugs {
			slugSet[sl] = true
		}
		for rows.Next() {
			var sl string
			rows.Scan(&sl)
			if !slugSet[sl] {
				slugs = append(slugs, sl)
			}
		}
	}

	for _, slug := range slugs {
		wdb, err := s.ws.Open(slug)
		if err != nil {
			continue
		}

		var b Brief
		var workspaceName string
		err = wdb.DB.QueryRow(`
			SELECT id, title, template, topic, content, COALESCE(generated_at, ''), created_at
			FROM living_briefs WHERE share_token = ? AND is_public = 1
		`, token).Scan(&b.ID, &b.Title, &b.Template, &b.Topic, &b.Content, &b.GeneratedAt, &b.CreatedAt)
		if err != nil {
			continue
		}

		// Get workspace name
		s.global.DB.QueryRow("SELECT name FROM workspaces WHERE slug = ?", slug).Scan(&workspaceName)
		if workspaceName == "" {
			workspaceName = slug
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"title":        b.Title,
			"template":     b.Template,
			"content":      b.Content,
			"generated_at": b.GeneratedAt,
			"workspace":    workspaceName,
		})
		return
	}

	writeError(w, http.StatusNotFound, "brief not found or not shared")
}

// scheduleBriefGeneration checks for briefs that need auto-generation.
// Called from the reflection cron loop.
func (s *Server) checkScheduledBriefs() {
	now := time.Now().UTC()
	slugs := s.hubs.ActiveSlugs()

	for _, slug := range slugs {
		apiKey, _ := s.getBrainSettings(slug)
		if apiKey == "" {
			continue
		}

		wdb, err := s.ws.Open(slug)
		if err != nil {
			continue
		}

		rows, err := wdb.DB.Query(`
			SELECT id, title, template, topic, schedule, schedule_time
			FROM living_briefs WHERE schedule != 'manual'
		`)
		if err != nil {
			continue
		}

		var toGenerate []Brief
		for rows.Next() {
			var b Brief
			rows.Scan(&b.ID, &b.Title, &b.Template, &b.Topic, &b.Schedule, &b.ScheduleTime)

			parts := strings.Split(b.ScheduleTime, ":")
			if len(parts) != 2 {
				continue
			}
			hour := brain.ParseIntSafe(parts[0])
			minute := brain.ParseIntSafe(parts[1])
			if now.Hour() != hour || now.Minute() != minute {
				continue
			}

			if b.Schedule == "daily" {
				toGenerate = append(toGenerate, b)
			} else if b.Schedule == "weekly" && now.Weekday() == time.Monday {
				toGenerate = append(toGenerate, b)
			}
		}
		rows.Close()

		for _, b := range toGenerate {
			go s.generateBrief(slug, b)
		}
	}
}
