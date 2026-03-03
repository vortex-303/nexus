package brain

import (
	"database/sql"
	"fmt"
	"strings"
)

// BuildKnowledgeContext returns knowledge base content formatted for the system prompt.
// If total tokens <= 5000, all articles are included.
// Otherwise, keyword search selects the top 5 relevant articles.
func BuildKnowledgeContext(db *sql.DB, query string) string {
	var totalTokens int
	err := db.QueryRow("SELECT COALESCE(SUM(tokens), 0) FROM brain_knowledge").Scan(&totalTokens)
	if err != nil || totalTokens == 0 {
		return ""
	}

	var rows *sql.Rows
	if totalTokens <= 5000 {
		rows, err = db.Query("SELECT title, content FROM brain_knowledge ORDER BY created_at")
	} else {
		rows, err = searchKnowledge(db, query, 5)
	}
	if err != nil {
		return ""
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var title, content string
		if err := rows.Scan(&title, &content); err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("### %s\n%s", title, content))
	}

	if len(parts) == 0 {
		return ""
	}
	return "## Knowledge Base\n\n" + strings.Join(parts, "\n\n")
}

// SearchKnowledgeForTool searches the knowledge base and returns formatted results.
func SearchKnowledgeForTool(db *sql.DB, query string) string {
	rows, err := searchKnowledge(db, query, 5)
	if err != nil {
		return "Error searching knowledge base"
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var title, content string
		if err := rows.Scan(&title, &content); err != nil {
			continue
		}
		// Truncate long content
		if len(content) > 500 {
			content = content[:497] + "..."
		}
		parts = append(parts, fmt.Sprintf("### %s\n%s", title, content))
	}

	if len(parts) == 0 {
		return fmt.Sprintf("No knowledge base articles found matching \"%s\"", query)
	}
	return fmt.Sprintf("Found %d relevant articles:\n\n%s", len(parts), strings.Join(parts, "\n\n"))
}

func searchKnowledge(db *sql.DB, query string, limit int) (*sql.Rows, error) {
	// Extract significant words (>= 3 chars)
	words := strings.Fields(query)
	var conditions []string
	var args []any
	for _, w := range words {
		w = strings.Trim(w, ".,!?;:'\"")
		if len(w) < 3 {
			continue
		}
		conditions = append(conditions, "(title LIKE ? OR content LIKE ?)")
		pattern := "%" + w + "%"
		args = append(args, pattern, pattern)
	}

	if len(conditions) == 0 {
		// Fallback: return most recent
		return db.Query("SELECT title, content FROM brain_knowledge ORDER BY created_at DESC LIMIT ?", limit)
	}

	q := "SELECT title, content FROM brain_knowledge WHERE " +
		strings.Join(conditions, " OR ") +
		" ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	return db.Query(q, args...)
}
