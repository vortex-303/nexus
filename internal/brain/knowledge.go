package brain

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/nexus-chat/nexus/internal/vectorstore"
)

// SemanticOpts holds optional parameters for semantic knowledge search.
type SemanticOpts struct {
	VectorStore *vectorstore.VectorStore
	APIKey      string
	Slug        string
}

// BuildKnowledgeContext returns knowledge base content formatted for the system prompt.
// If total tokens <= 5000, all articles are included.
// Otherwise, uses semantic search (if opts provided) or keyword search.
func BuildKnowledgeContext(db *sql.DB, query string, opts ...SemanticOpts) string {
	var opt SemanticOpts
	if len(opts) > 0 {
		opt = opts[0]
	}
	return buildKnowledgeContextInner(db, query, opt)
}

func buildKnowledgeContextInner(db *sql.DB, query string, opt SemanticOpts) string {
	var totalTokens int
	err := db.QueryRow("SELECT COALESCE(SUM(tokens), 0) FROM brain_knowledge").Scan(&totalTokens)
	if err != nil || totalTokens == 0 {
		return ""
	}

	var rows *sql.Rows
	if totalTokens <= 5000 {
		rows, err = db.Query("SELECT title, content FROM brain_knowledge ORDER BY created_at")
	} else {
		// Try semantic search if vector store and API key are available
		if opt.VectorStore != nil && opt.APIKey != "" {
			if semanticRows, sErr := SearchKnowledgeSemanticWithSlug(db, opt.VectorStore, opt.APIKey, opt.Slug, query, 5); sErr == nil {
				rows = semanticRows
				err = nil
			} else {
				log.Printf("[knowledge] semantic search failed, falling back to keyword: %v", sErr)
				rows, err = searchKnowledge(db, query, 5)
			}
		} else {
			rows, err = searchKnowledge(db, query, 5)
		}
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
		// Truncate individual knowledge entries to prevent token overflow
		if len(content) > 5000 {
			log.Printf("[knowledge] truncating entry %q from %d to 5000 chars", title, len(content))
			content = content[:5000] + "\n[...truncated]"
		}
		log.Printf("[knowledge] entry: title=%q, content_len=%d", title, len(content))
		parts = append(parts, fmt.Sprintf("### %s\n%s", title, content))
	}

	if len(parts) == 0 {
		return ""
	}

	result := "## Knowledge Base\n\n" + strings.Join(parts, "\n\n")
	// Hard cap on total knowledge context
	if len(result) > 30000 {
		log.Printf("[knowledge] WARNING: total knowledge context too large (%d chars), truncating to 30000", len(result))
		result = result[:30000]
	}
	log.Printf("[knowledge] total: %d entries, %d chars, totalTokens=%d", len(parts), len(result), totalTokens)
	return result
}

// SearchKnowledgeForTool searches the knowledge base and returns formatted results.
// Accepts optional SemanticOpts for vector search.
func SearchKnowledgeForTool(db *sql.DB, query string, opts ...SemanticOpts) string {
	var rows *sql.Rows
	var err error

	var opt SemanticOpts
	if len(opts) > 0 {
		opt = opts[0]
	}

	if opt.VectorStore != nil && opt.APIKey != "" {
		rows, err = SearchKnowledgeSemanticWithSlug(db, opt.VectorStore, opt.APIKey, opt.Slug, query, 5)
		if err != nil {
			log.Printf("[knowledge] semantic search failed: %v", err)
			rows, err = searchKnowledge(db, query, 5)
		}
	} else {
		rows, err = searchKnowledge(db, query, 5)
	}
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

// SearchKnowledgeSemanticWithSlug does semantic search with an explicit workspace slug.
func SearchKnowledgeSemanticWithSlug(sqlDB *sql.DB, vs *vectorstore.VectorStore, apiKey, slug, query string, limit int) (*sql.Rows, error) {
	client := NewClient(apiKey, "")
	vector, err := client.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}

	results, err := vs.Search(slug, vector, limit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	if len(results) == 0 {
		return sqlDB.Query("SELECT title, content FROM brain_knowledge WHERE 1=0")
	}

	placeholders := make([]string, len(results))
	args := make([]any, len(results))
	for i, r := range results {
		placeholders[i] = "?"
		args[i] = r.ID
	}

	q := "SELECT title, content FROM brain_knowledge WHERE id IN (" +
		strings.Join(placeholders, ",") + ")"
	return sqlDB.Query(q, args...)
}
