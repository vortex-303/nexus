package brain2

import (
	"database/sql"
	"strings"
)

// BuildPinnedMemoryContext returns all pinned memories, always included in the system prompt
// regardless of query relevance. These are critical facts the Brain should always know.
func BuildPinnedMemoryContext(db *sql.DB) string {
	rows, err := db.Query(`
		SELECT type, content FROM brain_memories
		WHERE pinned = TRUE AND superseded_by = '' AND valid_until = ''
		ORDER BY created_at DESC LIMIT 20
	`)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var b strings.Builder
	count := 0
	for rows.Next() {
		var mType, content string
		if rows.Scan(&mType, &content) != nil {
			continue
		}
		if count == 0 {
			b.WriteString("\n\n## Pinned Memories (always relevant)\n")
		}
		b.WriteString("- [")
		b.WriteString(mType)
		b.WriteString("] ")
		b.WriteString(content)
		b.WriteString("\n")
		count++
	}
	return b.String()
}

// BuildFeedbackContext returns feedback memories (corrections + confirmations)
// to guide Brain's behavior.
func BuildFeedbackContext(db *sql.DB) string {
	rows, err := db.Query(`
		SELECT content, source FROM brain_memories
		WHERE type = 'feedback' AND superseded_by = '' AND valid_until = ''
		ORDER BY created_at DESC LIMIT 10
	`)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var b strings.Builder
	count := 0
	for rows.Next() {
		var content, source string
		if rows.Scan(&content, &source) != nil {
			continue
		}
		if count == 0 {
			b.WriteString("\n\n## Behavioral Notes (learned from past interactions)\n")
		}
		label := "Note"
		if strings.Contains(source, "correction") {
			label = "Avoid"
		} else if strings.Contains(source, "confirmation") {
			label = "Keep doing"
		}
		b.WriteString("- [")
		b.WriteString(label)
		b.WriteString("] ")
		b.WriteString(content)
		b.WriteString("\n")
		count++
	}
	return b.String()
}

// BuildSelfMemoryContext returns Brain's own behavioral memories.
func BuildSelfMemoryContext(db *sql.DB) string {
	rows, err := db.Query(`
		SELECT content FROM brain_memories
		WHERE type = 'self' AND superseded_by = '' AND valid_until = ''
		ORDER BY created_at DESC LIMIT 10
	`)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var b strings.Builder
	count := 0
	for rows.Next() {
		var content string
		if rows.Scan(&content) != nil {
			continue
		}
		if count == 0 {
			b.WriteString("\n\n## Self-Improvement Notes\n")
		}
		b.WriteString("- ")
		b.WriteString(content)
		b.WriteString("\n")
		count++
	}
	return b.String()
}
