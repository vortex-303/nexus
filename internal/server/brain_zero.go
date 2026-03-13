package server

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// tryZeroLLMResponse handles simple queries using local data (search + SQL).
// Returns (response, true) if handled, or ("", false) to fall through to LLM.
func (s *Server) tryZeroLLMResponse(slug, content string, db *sql.DB, senderName string) (string, bool) {
	// Strip @brain prefix
	cleaned := strings.TrimSpace(content)
	cleaned = regexp.MustCompile(`(?i)^@brain\s*`).ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)
	lower := strings.ToLower(cleaned)

	// Pattern: search/find/look for
	if m := matchSearch(lower); m != "" {
		return s.zeroSearch(slug, m)
	}

	// Pattern: my tasks / assigned to me
	if matchMyTasks(lower) {
		return zeroMyTasks(db, senderName)
	}

	// Pattern: overdue tasks
	if matchOverdue(lower) {
		return zeroOverdueTasks(db)
	}

	// Pattern: tasks due today/tomorrow/this week
	if m := matchTasksDue(lower); m != "" {
		return zeroTasksDue(db, m)
	}

	// Pattern: urgent/high/medium/low priority tasks
	if m := matchPriority(lower); m != "" {
		return zeroPriorityTasks(db, m)
	}

	// Pattern: upcoming events / agenda / calendar
	if matchUpcoming(lower) {
		return zeroUpcomingEvents(db)
	}

	// Pattern: how many <entity>
	if m := matchHowMany(lower); m != "" {
		return zeroCount(db, m)
	}

	// Pattern: help / what can you do
	if helpRe.MatchString(lower) {
		return zeroHelp()
	}

	// Pattern: task summary / task board
	if taskSummaryRe.MatchString(lower) {
		return zeroTaskSummary(db)
	}

	// Pattern: recent activity / what's new
	if recentRe.MatchString(lower) {
		return zeroRecentActivity(db)
	}

	// Pattern: team / team members
	if teamRe.MatchString(lower) {
		return zeroList(db, "members")
	}

	// Pattern: show channels / show me tasks
	if m := matchShow(lower); m != "" {
		return zeroList(db, m)
	}

	// Pattern: what channels are there / what tasks do we have
	if m := matchWhatAre(lower); m != "" {
		return zeroList(db, m)
	}

	// Pattern: bare entity name ("channels", "tasks")
	if m := matchBareEntity(lower); m != "" {
		return zeroList(db, m)
	}

	// Pattern: list channels/members/tasks/documents/files/events
	if m := matchList(lower); m != "" {
		return zeroList(db, m)
	}

	// Pattern: who is online/here
	if matchWhoOnline(lower) {
		return s.zeroWho(slug)
	}

	// Pattern: workspace info/stats/status
	if matchWorkspaceStats(lower) {
		return zeroStats(db)
	}

	return "", false
}

var searchRe = regexp.MustCompile(`(?i)^(?:search|find|look)\s+(?:for\s+)?(.+)`)

// webSearchIndicators are phrases that signal the user wants internet search, not workspace search.
var webSearchIndicators = []string{
	"the web", "the internet", "online", "on google", "web for",
	"current price", "latest news", "today's", "right now",
}

func matchSearch(s string) string {
	m := searchRe.FindStringSubmatch(s)
	if len(m) > 1 {
		query := strings.TrimSpace(m[1])
		// Don't match if the user wants web search — let LLM handle it with web_search tool
		lower := strings.ToLower(query)
		for _, indicator := range webSearchIndicators {
			if strings.Contains(lower, indicator) {
				return "" // Not a workspace search — fall through to LLM
			}
		}
		return query
	}
	return ""
}

var howManyRe = regexp.MustCompile(`(?i)how many\s+(messages?|tasks?|members?|channels?|documents?|notes?|files?|knowledge|events?)`)

func matchHowMany(s string) string {
	m := howManyRe.FindStringSubmatch(s)
	if len(m) > 1 {
		return normalizeEntity(m[1])
	}
	return ""
}

var listRe = regexp.MustCompile(`(?i)^list\s+(channels?|members?|tasks?|documents?|notes?|docs?|files?|events?)$`)
var showRe = regexp.MustCompile(`(?i)^show\s+(?:me\s+)?(?:(?:all|the)\s+)?(channels?|members?|tasks?|documents?|notes?|docs?|files?|events?)$`)
var whatAreRe = regexp.MustCompile(`(?i)^what\s+(channels?|members?|tasks?|documents?|notes?|docs?|files?|events?)\s+(?:are there|do we have|exist|are available)`)
var bareEntityRe = regexp.MustCompile(`(?i)^(channels|tasks|members|documents|events|files)$`)
var helpRe = regexp.MustCompile(`(?i)^(?:help|what can you do|commands|capabilities)$`)
var taskSummaryRe = regexp.MustCompile(`(?i)^task\s+(?:summary|board|overview|breakdown)$`)
var recentRe = regexp.MustCompile(`(?i)^(?:recent\s+(?:activity|messages)|what'?s new|what happened)$`)
var teamRe = regexp.MustCompile(`(?i)^(?:team|team members|who is on the team|who'?s on the team)$`)

var myTasksRe = regexp.MustCompile(`(?i)(?:^my\s+(?:tasks?|todos?|assignments?)|assigned to me)`)
var overdueRe = regexp.MustCompile(`(?i)(?:overdue|past due|late)\s+(?:tasks?|todos?)`)
var tasksDueRe = regexp.MustCompile(`(?i)tasks?\s+due\s+(today|tomorrow|this week)`)
var priorityRe = regexp.MustCompile(`(?i)(urgent|high|medium|low)\s+(?:priority\s+)?(?:tasks?|todos?)`)
var upcomingRe = regexp.MustCompile(`(?i)(?:upcoming|next|what'?s on (?:the|my))\s*(?:events?|calendar|schedule|agenda)|(?:^agenda$)`)

func matchList(s string) string {
	m := listRe.FindStringSubmatch(s)
	if len(m) > 1 {
		return normalizeEntity(m[1])
	}
	return ""
}

func matchShow(s string) string {
	m := showRe.FindStringSubmatch(s)
	if len(m) > 1 {
		return normalizeEntity(m[1])
	}
	return ""
}

func matchWhatAre(s string) string {
	m := whatAreRe.FindStringSubmatch(s)
	if len(m) > 1 {
		return normalizeEntity(m[1])
	}
	return ""
}

func matchBareEntity(s string) string {
	m := bareEntityRe.FindStringSubmatch(s)
	if len(m) > 1 {
		return normalizeEntity(m[1])
	}
	return ""
}

func matchMyTasks(s string) bool {
	return myTasksRe.MatchString(s)
}

func matchOverdue(s string) bool {
	return overdueRe.MatchString(s)
}

func matchTasksDue(s string) string {
	m := tasksDueRe.FindStringSubmatch(s)
	if len(m) > 1 {
		return strings.ToLower(m[1])
	}
	return ""
}

func matchPriority(s string) string {
	m := priorityRe.FindStringSubmatch(s)
	if len(m) > 1 {
		return strings.ToLower(m[1])
	}
	return ""
}

func matchUpcoming(s string) bool {
	return upcomingRe.MatchString(s)
}

var whoOnlineRe = regexp.MustCompile(`(?i)(?:who(?:'s| is)\s+(?:online|here)|whos here)`)

func matchWhoOnline(s string) bool {
	return whoOnlineRe.MatchString(s)
}

var statsRe = regexp.MustCompile(`(?i)(?:workspace|space)\s+(?:info|stats|status|summary)`)

func matchWorkspaceStats(s string) bool {
	return statsRe.MatchString(s)
}

func normalizeEntity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// Singularize and normalize
	switch {
	case strings.HasPrefix(s, "message"):
		return "messages"
	case strings.HasPrefix(s, "task"):
		return "tasks"
	case strings.HasPrefix(s, "member"):
		return "members"
	case strings.HasPrefix(s, "channel"):
		return "channels"
	case strings.HasPrefix(s, "document"), strings.HasPrefix(s, "note"):
		return "documents"
	case strings.HasPrefix(s, "file"):
		return "files"
	case strings.HasPrefix(s, "knowledge"):
		return "knowledge"
	case strings.HasPrefix(s, "event"):
		return "events"
	}
	return s
}

func (s *Server) zeroSearch(slug, query string) (string, bool) {
	results, err := s.search.Search(slug, query, nil, 10)
	if err != nil {
		return "", false
	}
	if len(results) == 0 {
		return fmt.Sprintf("No results found for **%s**.", query), true
	}

	var lines []string
	for _, r := range results {
		content := r.Content
		if r.Highlight != "" {
			content = r.Highlight
		}
		if len(content) > 150 {
			content = content[:147] + "..."
		}
		line := fmt.Sprintf("- **[%s]** ", r.Type)
		if r.Title != "" {
			line += r.Title + ": "
		}
		line += content
		lines = append(lines, line)
	}
	return fmt.Sprintf("Found **%d** results for **%s**:\n\n%s", len(results), query, strings.Join(lines, "\n")), true
}

func zeroCount(db *sql.DB, entity string) (string, bool) {
	queryMap := map[string]string{
		"messages":  "SELECT COUNT(*) FROM messages WHERE deleted = FALSE",
		"tasks":     "SELECT COUNT(*) FROM tasks",
		"members":   "SELECT COUNT(*) FROM members WHERE role NOT IN ('agent','brain')",
		"channels":  "SELECT COUNT(*) FROM channels WHERE archived = FALSE",
		"documents": "SELECT COUNT(*) FROM documents",
		"files":     "SELECT COUNT(*) FROM files",
		"knowledge": "SELECT COUNT(*) FROM brain_knowledge",
		"events":    "SELECT COUNT(*) FROM calendar_events",
	}

	q, ok := queryMap[entity]
	if !ok {
		return "", false
	}

	var count int
	if err := db.QueryRow(q).Scan(&count); err != nil {
		return "", false
	}

	label := entity
	if count == 1 {
		label = strings.TrimSuffix(entity, "s")
	}
	return fmt.Sprintf("There are **%d** %s in this workspace.", count, label), true
}

func zeroList(db *sql.DB, entity string) (string, bool) {
	switch entity {
	case "channels":
		rows, err := db.Query("SELECT name, COALESCE(topic,'') FROM channels WHERE archived = FALSE ORDER BY name")
		if err != nil {
			return "", false
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var name, topic string
			rows.Scan(&name, &topic)
			line := "- **#" + name + "**"
			if topic != "" {
				line += " — " + topic
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			return "No channels found.", true
		}
		return fmt.Sprintf("**Channels (%d):**\n\n%s", len(lines), strings.Join(lines, "\n")), true

	case "members":
		rows, err := db.Query("SELECT display_name, role FROM members WHERE role NOT IN ('agent','brain') ORDER BY display_name")
		if err != nil {
			return "", false
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var name, role string
			rows.Scan(&name, &role)
			lines = append(lines, fmt.Sprintf("- **%s** (%s)", name, role))
		}
		if len(lines) == 0 {
			return "No members found.", true
		}
		return fmt.Sprintf("**Members (%d):**\n\n%s", len(lines), strings.Join(lines, "\n")), true

	case "tasks":
		rows, err := db.Query("SELECT title, status, priority FROM tasks ORDER BY created_at DESC LIMIT 20")
		if err != nil {
			return "", false
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var title, status, priority string
			rows.Scan(&title, &status, &priority)
			lines = append(lines, fmt.Sprintf("- [%s] %s (%s)", status, title, priority))
		}
		if len(lines) == 0 {
			return "No tasks found.", true
		}
		return fmt.Sprintf("**Tasks (%d):**\n\n%s", len(lines), strings.Join(lines, "\n")), true

	case "documents":
		rows, err := db.Query("SELECT title, updated_at, COALESCE(sharing,'private') FROM documents ORDER BY updated_at DESC LIMIT 20")
		if err != nil {
			return "", false
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var title, updatedAt, sharing string
			rows.Scan(&title, &updatedAt, &sharing)
			lines = append(lines, fmt.Sprintf("- **%s** (updated %s, %s)", title, formatTimeShort(updatedAt), sharing))
		}
		if len(lines) == 0 {
			return "No documents found.", true
		}
		return fmt.Sprintf("**Documents (%d):**\n\n%s", len(lines), strings.Join(lines, "\n")), true

	case "files":
		rows, err := db.Query("SELECT name, size, mime_type FROM files ORDER BY created_at DESC LIMIT 20")
		if err != nil {
			return "", false
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var name, mime string
			var size int64
			rows.Scan(&name, &size, &mime)
			lines = append(lines, fmt.Sprintf("- **%s** (%s, %s)", name, formatSize(size), mime))
		}
		if len(lines) == 0 {
			return "No files found.", true
		}
		return fmt.Sprintf("**Files (%d):**\n\n%s", len(lines), strings.Join(lines, "\n")), true

	case "events":
		rows, err := db.Query("SELECT title, start_time, COALESCE(location,'') FROM calendar_events WHERE start_time >= datetime('now') ORDER BY start_time LIMIT 20")
		if err != nil {
			return "", false
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var title, startTime, location string
			rows.Scan(&title, &startTime, &location)
			line := fmt.Sprintf("- **%s** — %s", title, formatTimeShort(startTime))
			if location != "" {
				line += " @ " + location
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			return "No upcoming events.", true
		}
		return fmt.Sprintf("**Events (%d):**\n\n%s", len(lines), strings.Join(lines, "\n")), true
	}

	return "", false
}

func (s *Server) zeroWho(slug string) (string, bool) {
	h := s.hubs.Get(slug)
	members := h.OnlineMembers()
	if len(members) == 0 {
		return "No one is currently online.", true
	}

	var names []string
	for _, m := range members {
		name := m["display_name"]
		if name == "" {
			name = m["user_id"]
		}
		names = append(names, "- **"+name+"**")
	}
	return fmt.Sprintf("**%d** member(s) online:\n\n%s", len(members), strings.Join(names, "\n")), true
}

func zeroStats(db *sql.DB) (string, bool) {
	counts := map[string]string{
		"Members":   "SELECT COUNT(*) FROM members WHERE role NOT IN ('agent','brain')",
		"Channels":  "SELECT COUNT(*) FROM channels WHERE archived = FALSE",
		"Messages":  "SELECT COUNT(*) FROM messages WHERE deleted = FALSE",
		"Tasks":     "SELECT COUNT(*) FROM tasks",
		"Documents": "SELECT COUNT(*) FROM documents",
		"Files":     "SELECT COUNT(*) FROM files",
		"Knowledge": "SELECT COUNT(*) FROM brain_knowledge",
		"Events":    "SELECT COUNT(*) FROM calendar_events",
	}

	order := []string{"Members", "Channels", "Messages", "Tasks", "Documents", "Files", "Knowledge", "Events"}
	var lines []string
	for _, key := range order {
		var c int
		if err := db.QueryRow(counts[key]).Scan(&c); err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("- **%s:** %d", key, c))
	}

	return "**Workspace Stats:**\n\n" + strings.Join(lines, "\n"), true
}

func zeroMyTasks(db *sql.DB, senderName string) (string, bool) {
	if senderName == "" {
		return "", false
	}
	// Resolve sender name to member ID
	var memberID string
	err := db.QueryRow("SELECT id FROM members WHERE display_name = ? COLLATE NOCASE", senderName).Scan(&memberID)
	if err != nil {
		return fmt.Sprintf("Couldn't find a member named **%s**.", senderName), true
	}

	rows, err := db.Query("SELECT title, status, priority, COALESCE(due_date,'') FROM tasks WHERE assignee_id = ? AND status NOT IN ('done','cancelled') ORDER BY created_at DESC LIMIT 20", memberID)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var title, status, priority, dueDate string
		rows.Scan(&title, &status, &priority, &dueDate)
		line := fmt.Sprintf("- [%s] %s (%s)", status, title, priority)
		if dueDate != "" {
			line += " — due " + formatTimeShort(dueDate)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "You have no open tasks.", true
	}
	return fmt.Sprintf("**Your Tasks (%d):**\n\n%s", len(lines), strings.Join(lines, "\n")), true
}

func zeroOverdueTasks(db *sql.DB) (string, bool) {
	rows, err := db.Query("SELECT title, status, priority, due_date, COALESCE((SELECT display_name FROM members WHERE id = tasks.assignee_id),'unassigned') FROM tasks WHERE due_date < datetime('now') AND status NOT IN ('done','cancelled') ORDER BY due_date ASC LIMIT 20")
	if err != nil {
		return "", false
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var title, status, priority, dueDate, assignee string
		rows.Scan(&title, &status, &priority, &dueDate, &assignee)
		lines = append(lines, fmt.Sprintf("- [%s] %s (%s) — was due %s, assigned to %s", status, title, priority, formatTimeShort(dueDate), assignee))
	}
	if len(lines) == 0 {
		return "No overdue tasks!", true
	}
	return fmt.Sprintf("**Overdue Tasks (%d):**\n\n%s", len(lines), strings.Join(lines, "\n")), true
}

func zeroTasksDue(db *sql.DB, timeframe string) (string, bool) {
	now := time.Now()
	var startDate, endDate string
	var label string

	switch timeframe {
	case "today":
		startDate = now.Format("2006-01-02")
		endDate = now.AddDate(0, 0, 1).Format("2006-01-02")
		label = "Today"
	case "tomorrow":
		startDate = now.AddDate(0, 0, 1).Format("2006-01-02")
		endDate = now.AddDate(0, 0, 2).Format("2006-01-02")
		label = "Tomorrow"
	case "this week":
		// From today through end of week (Sunday)
		startDate = now.Format("2006-01-02")
		daysUntilSunday := (7 - int(now.Weekday())) % 7
		if daysUntilSunday == 0 {
			daysUntilSunday = 7
		}
		endDate = now.AddDate(0, 0, daysUntilSunday+1).Format("2006-01-02")
		label = "This Week"
	default:
		return "", false
	}

	rows, err := db.Query("SELECT title, status, priority, due_date FROM tasks WHERE due_date >= ? AND due_date < ? AND status NOT IN ('done','cancelled') ORDER BY due_date ASC LIMIT 20", startDate, endDate)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var title, status, priority, dueDate string
		rows.Scan(&title, &status, &priority, &dueDate)
		lines = append(lines, fmt.Sprintf("- [%s] %s (%s) — due %s", status, title, priority, formatTimeShort(dueDate)))
	}
	if len(lines) == 0 {
		return fmt.Sprintf("No tasks due **%s**.", strings.ToLower(label)), true
	}
	return fmt.Sprintf("**Tasks Due %s (%d):**\n\n%s", label, len(lines), strings.Join(lines, "\n")), true
}

func zeroPriorityTasks(db *sql.DB, priority string) (string, bool) {
	rows, err := db.Query("SELECT title, status, COALESCE(due_date,'') FROM tasks WHERE priority = ? AND status NOT IN ('done','cancelled') ORDER BY created_at DESC LIMIT 20", priority)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var title, status, dueDate string
		rows.Scan(&title, &status, &dueDate)
		line := fmt.Sprintf("- [%s] %s", status, title)
		if dueDate != "" {
			line += " — due " + formatTimeShort(dueDate)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return fmt.Sprintf("No **%s** priority tasks.", priority), true
	}
	return fmt.Sprintf("**%s Priority Tasks (%d):**\n\n%s", strings.Title(priority), len(lines), strings.Join(lines, "\n")), true
}

func zeroUpcomingEvents(db *sql.DB) (string, bool) {
	rows, err := db.Query("SELECT title, start_time, COALESCE(end_time,''), COALESCE(location,'') FROM calendar_events WHERE start_time >= datetime('now') AND start_time < datetime('now', '+7 days') ORDER BY start_time LIMIT 20")
	if err != nil {
		return "", false
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var title, startTime, endTime, location string
		rows.Scan(&title, &startTime, &endTime, &location)
		line := fmt.Sprintf("- **%s** — %s", title, formatTimeShort(startTime))
		if endTime != "" {
			line += " to " + formatTimeShort(endTime)
		}
		if location != "" {
			line += " @ " + location
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "No upcoming events in the next 7 days.", true
	}
	return fmt.Sprintf("**Upcoming Events (%d):**\n\n%s", len(lines), strings.Join(lines, "\n")), true
}

func zeroHelp() (string, bool) {
	return `**Here's what I can help with:**

**List & Browse:**
- **list channels** / **show me channels** / just say **channels**
- **list tasks** / **show tasks** / **tasks**
- **list members** / **team** / **members**
- **list documents** / **list events** / **list files**

**Tasks:**
- **my tasks** — your assigned tasks
- **task summary** — counts by status
- **overdue tasks** / **urgent tasks** / **high priority tasks**
- **tasks due today** / **tasks due this week**

**Activity & Stats:**
- **recent activity** — last 10 messages across channels
- **workspace stats** — counts of everything
- **who is online**
- **upcoming events** / **agenda**

**Search:**
- **search for** *keyword* — search messages, docs, and more
- **how many** tasks / messages / members / channels

Any other question will use the AI for a full response.`, true
}

func zeroTaskSummary(db *sql.DB) (string, bool) {
	rows, err := db.Query("SELECT status, COUNT(*) FROM tasks GROUP BY status ORDER BY status")
	if err != nil {
		return "", false
	}
	defer rows.Close()
	var lines []string
	total := 0
	for rows.Next() {
		var status string
		var count int
		rows.Scan(&status, &count)
		lines = append(lines, fmt.Sprintf("- **%s:** %d", status, count))
		total += count
	}
	if len(lines) == 0 {
		return "No tasks in this workspace.", true
	}
	return fmt.Sprintf("**Task Summary (%d total):**\n\n%s", total, strings.Join(lines, "\n")), true
}

func zeroRecentActivity(db *sql.DB) (string, bool) {
	rows, err := db.Query(`SELECT m.content, COALESCE(mem.display_name, m.sender_id), COALESCE(c.name, ''), m.created_at
		FROM messages m
		LEFT JOIN members mem ON mem.id = m.sender_id
		LEFT JOIN channels c ON c.id = m.channel_id
		WHERE m.deleted = FALSE
		ORDER BY m.created_at DESC LIMIT 10`)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var content, sender, channel, createdAt string
		rows.Scan(&content, &sender, &channel, &createdAt)
		if len(content) > 100 {
			content = content[:97] + "..."
		}
		line := fmt.Sprintf("- **%s**", sender)
		if channel != "" {
			line += " in #" + channel
		}
		line += fmt.Sprintf(" (%s): %s", formatTimeShort(createdAt), content)
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "No recent activity.", true
	}
	return fmt.Sprintf("**Recent Activity:**\n\n%s", strings.Join(lines, "\n")), true
}

// formatTimeShort trims a datetime string to a readable short form.
func formatTimeShort(s string) string {
	// Try parsing common SQLite formats
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			if t.Hour() == 0 && t.Minute() == 0 {
				return t.Format("Jan 2")
			}
			return t.Format("Jan 2 3:04pm")
		}
	}
	return s
}

// formatSize formats bytes into a human-readable size string.
func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
