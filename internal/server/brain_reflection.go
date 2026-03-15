package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/logger"
)

// ReflectionData holds aggregated workspace metrics for a reflection cycle.
type ReflectionData struct {
	Period string // "daily" or "weekly"

	// Member activity
	MessagesByMember map[string]int
	TotalMessages    int
	ActiveMembers    []string // members with >0 messages
	QuietMembers     []string // members with 0 messages in period

	// Tasks
	TasksCreated   int
	TasksCompleted int
	TasksOverdue   int
	OpenTasks      int

	// Channels
	ChannelActivity map[string]int // channel name → message count

	// Brain performance
	BrainMentions int
	ToolsUsed     map[string]int

	// Memory
	MemoryCount int

	// Docs & files
	DocsCreated  int
	FilesUploaded int

	// Context
	NorthStar          string
	NorthStarWhy       string
	NorthStarSuccess   string
	StrategicThemes    string
	ParsedThemes       []string
	RecentTaskTitles   []string
	RecentChannelTopics []string
	PreviousReflection string
}

// gatherReflectionData aggregates workspace metrics from existing tables.
func (s *Server) gatherReflectionData(slug string, period string) (*ReflectionData, error) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return nil, err
	}

	hours := 24
	if period == "weekly" {
		hours = 168
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour).Format("2006-01-02T15:04:05Z")

	rd := &ReflectionData{
		Period:           period,
		MessagesByMember: make(map[string]int),
		ChannelActivity:  make(map[string]int),
		ToolsUsed:        make(map[string]int),
	}

	// Messages by member (from activity_stream)
	rows, err := wdb.DB.Query(`
		SELECT actor_name, SUM(CAST(COALESCE(detail, '1') AS INTEGER))
		FROM activity_stream
		WHERE pulse_type = 'message.sent' AND created_at >= ?
		  AND source = 'user'
		GROUP BY actor_name
		ORDER BY 2 DESC
	`, since)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var count int
			rows.Scan(&name, &count)
			rd.MessagesByMember[name] = count
			rd.TotalMessages += count
			rd.ActiveMembers = append(rd.ActiveMembers, name)
		}
		rows.Close()
	}

	// Quiet members (all non-agent members not in active list)
	allMembers := queryStringSlice(wdb.DB,
		"SELECT display_name FROM members WHERE role NOT IN ('brain', 'agent') AND display_name != ''")
	activeSet := make(map[string]bool)
	for _, m := range rd.ActiveMembers {
		activeSet[m] = true
	}
	for _, m := range allMembers {
		if !activeSet[m] {
			rd.QuietMembers = append(rd.QuietMembers, m)
		}
	}

	// Channel activity
	rows2, err := wdb.DB.Query(`
		SELECT c.name, COUNT(*)
		FROM activity_stream a
		JOIN channels c ON c.id = a.channel_id
		WHERE a.pulse_type = 'message.sent' AND a.created_at >= ?
		GROUP BY c.name
		ORDER BY 2 DESC
		LIMIT 10
	`, since)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var name string
			var count int
			rows2.Scan(&name, &count)
			rd.ChannelActivity[name] = count
		}
		rows2.Close()
	}

	// Task stats
	_ = wdb.DB.QueryRow(`
		SELECT COUNT(*) FROM activity_stream
		WHERE pulse_type = 'task.created' AND created_at >= ?
	`, since).Scan(&rd.TasksCreated)

	_ = wdb.DB.QueryRow(`
		SELECT COUNT(*) FROM activity_stream
		WHERE pulse_type = 'task.updated' AND summary LIKE '%completed%' AND created_at >= ?
	`, since).Scan(&rd.TasksCompleted)

	_ = wdb.DB.QueryRow(`
		SELECT COUNT(*) FROM tasks
		WHERE status NOT IN ('done', 'cancelled') AND due_date != '' AND due_date < date('now')
	`).Scan(&rd.TasksOverdue)

	_ = wdb.DB.QueryRow(`
		SELECT COUNT(*) FROM tasks WHERE status NOT IN ('done', 'cancelled')
	`).Scan(&rd.OpenTasks)

	// Brain mentions
	_ = wdb.DB.QueryRow(`
		SELECT COUNT(*) FROM brain_action_log
		WHERE action_type = 'mention' AND created_at >= ?
	`, since).Scan(&rd.BrainMentions)

	// Docs created
	_ = wdb.DB.QueryRow(`
		SELECT COUNT(*) FROM activity_stream
		WHERE pulse_type = 'document.created' AND created_at >= ?
	`, since).Scan(&rd.DocsCreated)

	// Files uploaded
	_ = wdb.DB.QueryRow(`
		SELECT COUNT(*) FROM activity_stream
		WHERE pulse_type = 'file.uploaded' AND created_at >= ?
	`, since).Scan(&rd.FilesUploaded)

	// Memory count
	_ = wdb.DB.QueryRow("SELECT COUNT(*) FROM brain_memories WHERE superseded_by IS NULL OR superseded_by = ''").Scan(&rd.MemoryCount)

	// North Star
	rd.NorthStar = s.getBrainSetting(slug, "north_star")
	rd.NorthStarWhy = s.getBrainSetting(slug, "north_star_why")
	rd.NorthStarSuccess = s.getBrainSetting(slug, "north_star_success")
	rd.StrategicThemes = s.getBrainSetting(slug, "strategic_themes")
	if rd.StrategicThemes != "" {
		_ = json.Unmarshal([]byte(rd.StrategicThemes), &rd.ParsedThemes)
	}

	// Recent task titles for alignment analysis
	rd.RecentTaskTitles = queryStringSlice(wdb.DB,
		"SELECT title FROM tasks WHERE created_at >= ? ORDER BY created_at DESC LIMIT 20", since)
	rd.RecentChannelTopics = queryStringSlice(wdb.DB,
		`SELECT DISTINCT summary FROM activity_stream
		 WHERE pulse_type = 'message.sent' AND created_at >= ? AND summary != ''
		 ORDER BY created_at DESC LIMIT 15`, since)

	// Previous reflection
	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	if data, err := os.ReadFile(brainDir + "/REFLECTIONS.md"); err == nil {
		rd.PreviousReflection = string(data)
	}

	return rd, nil
}

// formatReflectionPrompt builds the user message for the reflection LLM call.
func formatReflectionPrompt(rd *ReflectionData) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("You are Brain performing a %s self-reflection on your workspace.\n\n", rd.Period))
	b.WriteString("Analyze the following workspace data and produce an updated REFLECTIONS.md file.\n\n")

	// North Star
	if rd.NorthStar != "" {
		b.WriteString("## North Star\n")
		b.WriteString("**Goal:** " + rd.NorthStar + "\n")
		if rd.NorthStarWhy != "" {
			b.WriteString("**Why:** " + rd.NorthStarWhy + "\n")
		}
		if rd.StrategicThemes != "" {
			b.WriteString("**Themes:** " + rd.StrategicThemes + "\n")
		}
		b.WriteString("\n")
	}

	// Activity data
	b.WriteString("## Workspace Data (last ")
	if rd.Period == "weekly" {
		b.WriteString("7 days)\n\n")
	} else {
		b.WriteString("24 hours)\n\n")
	}

	b.WriteString(fmt.Sprintf("**Messages:** %d total\n", rd.TotalMessages))
	if len(rd.MessagesByMember) > 0 {
		b.WriteString("**By member:**\n")
		for name, count := range rd.MessagesByMember {
			b.WriteString(fmt.Sprintf("- %s: %d messages\n", name, count))
		}
	}
	if len(rd.QuietMembers) > 0 {
		b.WriteString("**Quiet members:** " + strings.Join(rd.QuietMembers, ", ") + "\n")
	}
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("**Tasks:** %d open, %d created, %d completed, %d overdue\n", rd.OpenTasks, rd.TasksCreated, rd.TasksCompleted, rd.TasksOverdue))
	b.WriteString(fmt.Sprintf("**Brain:** %d mentions answered\n", rd.BrainMentions))
	b.WriteString(fmt.Sprintf("**Content:** %d docs created, %d files uploaded\n", rd.DocsCreated, rd.FilesUploaded))
	b.WriteString(fmt.Sprintf("**Memory:** %d active memories\n", rd.MemoryCount))
	b.WriteString("\n")

	if len(rd.ChannelActivity) > 0 {
		b.WriteString("**Channel activity:**\n")
		for ch, count := range rd.ChannelActivity {
			b.WriteString(fmt.Sprintf("- #%s: %d messages\n", ch, count))
		}
		b.WriteString("\n")
	}

	// Recent tasks for alignment analysis
	if len(rd.RecentTaskTitles) > 0 {
		b.WriteString("**Recent tasks:**\n")
		for _, t := range rd.RecentTaskTitles {
			b.WriteString("- " + t + "\n")
		}
		b.WriteString("\n")
	}

	// Instructions
	b.WriteString(`## Output Format

Write the complete updated REFLECTIONS.md file. Use this exact structure:

` + "```" + `markdown
# Reflections

Brain's self-reflection journal. Updated: {current date}

## Workspace Pulse
{2-4 bullet points: team energy, velocity, what's hot, who's active/quiet}

## North Star Alignment
{If North Star is set: give an overall alignment score 0-10 with one line of reasoning.
Then list 2-3 specific tasks or activities that ALIGN with the mission.
Then list any tasks or activities that seem to DRIFT from the mission.
If strategic themes are set, rate each theme: progressing / stalled / needs attention — with one line why.
Skip this section entirely if no North Star is configured.}

## What's Working
{2-3 bullet points: positive patterns observed}

## Concerns
{2-3 bullet points: risks, quiet members, overdue work, misalignment with North Star}

## My Performance
{2-3 bullet points: how many questions answered, what I was useful for, where I could improve}

## Learnings
{2-3 bullet points: patterns about the team, communication preferences, recurring topics}
` + "```" + `

Be concise, specific, and honest. Reference real data — names, numbers, channels. Don't be generic.
If this is the first reflection, focus on what the data shows. For subsequent reflections, note trends vs previous.
Output ONLY the markdown content, no code fences around it.`)

	return b.String()
}

// runReflection executes a reflection cycle for a workspace.
// Set checkEnabled=true for scheduled runs, false for manual triggers.
func (s *Server) runReflection(slug string, period string, checkEnabled ...bool) {
	log := logger.WithCategory(logger.CatBrain)
	log.Info().Str("workspace", slug).Str("period", period).Msg("starting reflection")

	// Check if reflection is enabled (skip for manual triggers)
	shouldCheck := len(checkEnabled) == 0 || checkEnabled[0]
	if shouldCheck && s.getBrainSetting(slug, "reflection_enabled", "true") == "false" {
		log.Info().Str("workspace", slug).Msg("reflection disabled, skipping")
		return
	}

	rd, err := s.gatherReflectionData(slug, period)
	if err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("reflection gather error")
		return
	}

	log.Info().Str("workspace", slug).
		Int("messages", rd.TotalMessages).
		Int("tasks_open", rd.OpenTasks).
		Int("brain_mentions", rd.BrainMentions).
		Int("members_active", len(rd.ActiveMembers)).
		Int("memories", rd.MemoryCount).
		Msg("reflection data gathered")

	prompt := formatReflectionPrompt(rd)

	result, usage, err := s.memoryComplete(slug, "You are Brain, reflecting on your workspace activity. Be concise, data-driven, and honest.", []brain.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("reflection LLM error")
		return
	}
	s.trackUsage(slug, usage, "", "reflection", "", "")

	result = strings.TrimSpace(result)
	if result == "" {
		log.Warn().Str("workspace", slug).Msg("reflection produced empty result")
		return
	}

	// Strip code fences if the model wrapped the output
	result = stripCodeFences(result)

	// Write to REFLECTIONS.md
	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	if err := os.WriteFile(brainDir+"/REFLECTIONS.md", []byte(result), 0644); err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("failed to write REFLECTIONS.md")
		return
	}

	// Save to reflection history
	if wdb, err := s.ws.Open(slug); err == nil {
		_, _ = wdb.DB.Exec("INSERT INTO reflection_history (period, content) VALUES (?, ?)", period, result)
	}

	log.Info().Str("workspace", slug).Str("period", period).Int("chars", len(result)).Msg("reflection complete")
}

// scheduleReflections registers the reflection cron job.
func (s *Server) scheduleReflections() {
	// Check every minute for reflection + scheduled briefs
	s.cron.AddFunc("@every 1m", func() {
		s.checkReflections()
		s.checkScheduledBriefs()
	})
	logger.WithCategory(logger.CatBrain).Info().Msg("reflection scheduler started")
}

// checkReflections runs reflection for active workspaces at the configured time.
func (s *Server) checkReflections() {
	now := time.Now().UTC()
	slugs := s.hubs.ActiveSlugs()

	for _, slug := range slugs {
		// Check if workspace has an API key
		apiKey, _ := s.getBrainSettings(slug)
		if apiKey == "" {
			continue
		}

		// Get configured reflection time (default "3:00")
		reflectionTime := s.getBrainSetting(slug, "reflection_time", "3:00")
		parts := strings.Split(reflectionTime, ":")
		if len(parts) != 2 {
			continue
		}
		hour := brain.ParseIntSafe(parts[0])
		minute := brain.ParseIntSafe(parts[1])

		if now.Hour() != hour || now.Minute() != minute {
			continue
		}

		// Determine period: weekly on Mondays, daily otherwise
		period := "daily"
		if now.Weekday() == time.Monday {
			period = "weekly"
		}

		go s.runReflection(slug, period, true)
	}
}

// stripCodeFences removes wrapping ```markdown ... ``` from LLM output.
func stripCodeFences(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) < 3 {
		return s
	}
	first := strings.TrimSpace(lines[0])
	if strings.HasPrefix(first, "```") {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "```" {
			return strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	return s
}

// queryStringSlice runs a query and returns a slice of strings from the first column.
func queryStringSlice(db *sql.DB, query string, args ...any) []string {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		result = append(result, s)
	}
	return result
}

// handleReflectNow triggers an immediate reflection cycle.
// POST /api/workspaces/{slug}/brain/reflect
func (s *Server) handleReflectNow(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	go s.runReflection(slug, "daily", false)

	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// handleReflectionHistory returns all past reflections.
// GET /api/workspaces/{slug}/brain/reflections
func (s *Server) handleReflectionHistory(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	wdb, err := s.ws.Open(slug)
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	rows, err := wdb.DB.Query("SELECT id, period, content, created_at FROM reflection_history ORDER BY created_at DESC")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"reflections": []any{}})
		return
	}
	defer rows.Close()

	type ReflectionEntry struct {
		ID        int    `json:"id"`
		Period    string `json:"period"`
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
	}

	var entries []ReflectionEntry
	for rows.Next() {
		var e ReflectionEntry
		rows.Scan(&e.ID, &e.Period, &e.Content, &e.CreatedAt)
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []ReflectionEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"reflections": entries})
}
