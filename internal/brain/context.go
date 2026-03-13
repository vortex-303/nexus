package brain

import (
	"database/sql"
	"fmt"
	"strings"
)

// BuildWorkspaceContext builds a concise workspace snapshot for the LLM system prompt.
// Returns ~300-800 tokens of markdown summarizing channels, members, tasks, documents, and events.
func BuildWorkspaceContext(db *sql.DB) string {
	var sections []string

	// Channels
	if s := buildChannelsSection(db); s != "" {
		sections = append(sections, s)
	}

	// Members
	if s := buildMembersSection(db); s != "" {
		sections = append(sections, s)
	}

	// Tasks
	if s := buildTasksSection(db); s != "" {
		sections = append(sections, s)
	}

	// Documents
	if s := buildDocsSection(db); s != "" {
		sections = append(sections, s)
	}

	// Events
	if s := buildEventsSection(db); s != "" {
		sections = append(sections, s)
	}

	if len(sections) == 0 {
		return ""
	}

	return "## Workspace Snapshot\n\n" + strings.Join(sections, "\n\n")
}

func buildChannelsSection(db *sql.DB) string {
	rows, err := db.Query("SELECT name, COALESCE(topic,'') FROM channels WHERE archived = FALSE ORDER BY name")
	if err != nil {
		return ""
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var name, topic string
		rows.Scan(&name, &topic)
		line := "- #" + name
		if topic != "" {
			line += " — " + topic
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("**Channels (%d):**\n%s", len(lines), strings.Join(lines, "\n"))
}

func buildMembersSection(db *sql.DB) string {
	rows, err := db.Query("SELECT display_name, role FROM members WHERE role NOT IN ('agent','brain') ORDER BY display_name")
	if err != nil {
		return ""
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var name, role string
		rows.Scan(&name, &role)
		lines = append(lines, fmt.Sprintf("- %s (%s)", name, role))
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("**Members (%d):**\n%s", len(lines), strings.Join(lines, "\n"))
}

func buildTasksSection(db *sql.DB) string {
	// Status counts
	rows, err := db.Query("SELECT status, COUNT(*) FROM tasks GROUP BY status ORDER BY status")
	if err != nil {
		return ""
	}
	defer rows.Close()
	var counts []string
	total := 0
	for rows.Next() {
		var status string
		var count int
		rows.Scan(&status, &count)
		counts = append(counts, fmt.Sprintf("%s: %d", status, count))
		total += count
	}
	if total == 0 {
		return ""
	}

	result := fmt.Sprintf("**Tasks (%d):** %s", total, strings.Join(counts, ", "))

	// 5 most recent active tasks
	activeRows, err := db.Query(`SELECT t.title, COALESCE(m.display_name,'unassigned'), t.priority
		FROM tasks t LEFT JOIN members m ON m.id = t.assignee_id
		WHERE t.status NOT IN ('done','cancelled')
		ORDER BY t.updated_at DESC LIMIT 5`)
	if err != nil {
		return result
	}
	defer activeRows.Close()
	var lines []string
	for activeRows.Next() {
		var title, assignee, priority string
		activeRows.Scan(&title, &assignee, &priority)
		lines = append(lines, fmt.Sprintf("- %s (%s, %s)", title, assignee, priority))
	}
	if len(lines) > 0 {
		result += "\nActive:\n" + strings.Join(lines, "\n")
	}

	return result
}

func buildDocsSection(db *sql.DB) string {
	rows, err := db.Query("SELECT title, updated_at FROM documents ORDER BY updated_at DESC LIMIT 5")
	if err != nil {
		return ""
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var title, updatedAt string
		rows.Scan(&title, &updatedAt)
		lines = append(lines, fmt.Sprintf("- %s (updated %s)", title, shortDate(updatedAt)))
	}
	if len(lines) == 0 {
		return ""
	}
	return "**Recent Documents:**\n" + strings.Join(lines, "\n")
}

func buildEventsSection(db *sql.DB) string {
	rows, err := db.Query(`SELECT title, start_time, COALESCE(location,'')
		FROM calendar_events
		WHERE start_time >= datetime('now')
		ORDER BY start_time LIMIT 5`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var title, startTime, location string
		rows.Scan(&title, &startTime, &location)
		line := fmt.Sprintf("- %s — %s", title, shortDate(startTime))
		if location != "" {
			line += " @ " + location
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return "**Upcoming Events:**\n" + strings.Join(lines, "\n")
}

// shortDate trims a datetime to a short readable form (for context.go only).
func shortDate(s string) string {
	// Just take the first 10 chars (date) or 16 chars (date+time) for brevity
	if len(s) >= 16 {
		return s[:16]
	}
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
