package search

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	blevequery "github.com/blevesearch/bleve/v2/search/query"
)

// SearchDoc represents a document to index.
type SearchDoc struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // "message", "document", "task", "knowledge", "member", "channel"
	Title     string `json:"title,omitempty"`
	Content   string `json:"content"`
	Sender    string `json:"sender,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	Status    string `json:"status,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// SearchResult is returned from search queries.
type SearchResult struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Title     string  `json:"title,omitempty"`
	Content   string  `json:"content"`
	Sender    string  `json:"sender,omitempty"`
	ChannelID string  `json:"channel_id,omitempty"`
	Highlight string  `json:"highlight,omitempty"`
	Score     float64 `json:"score"`
}

// IndexManager manages per-workspace bleve indexes.
type IndexManager struct {
	dataDir    string
	mu         sync.RWMutex
	indexes    map[string]bleve.Index
	newIndexes map[string]bool // tracks newly created indexes
}

// NewIndexManager creates a new IndexManager.
func NewIndexManager(dataDir string) *IndexManager {
	return &IndexManager{
		dataDir:    dataDir,
		indexes:    make(map[string]bleve.Index),
		newIndexes: make(map[string]bool),
	}
}

// Open returns the bleve index for a workspace, creating it if needed.
func (m *IndexManager) Open(slug string) (bleve.Index, error) {
	m.mu.RLock()
	if idx, ok := m.indexes[slug]; ok {
		m.mu.RUnlock()
		return idx, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if idx, ok := m.indexes[slug]; ok {
		return idx, nil
	}

	indexPath := filepath.Join(m.dataDir, "workspaces", slug, "search.bleve")

	var idx bleve.Index
	var err error

	if _, statErr := os.Stat(indexPath); os.IsNotExist(statErr) {
		mapping := buildMapping()
		idx, err = bleve.New(indexPath, mapping)
		if err != nil {
			return nil, fmt.Errorf("creating index: %w", err)
		}
		m.newIndexes[slug] = true
		log.Printf("[search:%s] created new index", slug)
	} else {
		idx, err = bleve.Open(indexPath)
		if err != nil {
			// Index corrupted — recreate
			os.RemoveAll(indexPath)
			mapping := buildMapping()
			idx, err = bleve.New(indexPath, mapping)
			if err != nil {
				return nil, fmt.Errorf("recreating index: %w", err)
			}
			m.newIndexes[slug] = true
			log.Printf("[search:%s] recreated corrupted index", slug)
		}
	}

	m.indexes[slug] = idx
	return idx, nil
}

// NeedsReindex returns true if the workspace index is empty or was just created.
func (m *IndexManager) NeedsReindex(slug string) bool {
	m.mu.RLock()
	if m.newIndexes[slug] {
		m.mu.RUnlock()
		return true
	}
	m.mu.RUnlock()

	idx, err := m.Open(slug)
	if err != nil {
		return true
	}
	count, err := idx.DocCount()
	if err != nil {
		return true
	}
	return count == 0
}

func buildMapping() *mapping.IndexMappingImpl {
	mapping := bleve.NewIndexMapping()

	// Use keyword analyzer for "type" and "status" fields
	keywordFieldMapping := bleve.NewTextFieldMapping()
	keywordFieldMapping.Analyzer = "keyword"

	docMapping := bleve.NewDocumentMapping()
	docMapping.AddFieldMappingsAt("type", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("status", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("channel_id", keywordFieldMapping)

	mapping.DefaultMapping = docMapping
	return mapping
}

// Index upserts a document into the workspace index.
func (m *IndexManager) Index(slug string, doc SearchDoc) error {
	idx, err := m.Open(slug)
	if err != nil {
		log.Printf("[search:%s] index error: %v", slug, err)
		return err
	}
	return idx.Index(doc.ID, doc)
}

// Delete removes a document from the workspace index.
func (m *IndexManager) Delete(slug, docID string) error {
	idx, err := m.Open(slug)
	if err != nil {
		return err
	}
	return idx.Delete(docID)
}

// Search queries the workspace index.
func (m *IndexManager) Search(slug, query string, types []string, limit int) ([]SearchResult, error) {
	idx, err := m.Open(slug)
	if err != nil {
		return nil, err
	}

	q := bleve.NewQueryStringQuery(query)
	req := bleve.NewSearchRequestOptions(q, limit, 0, false)
	req.Fields = []string{"type", "title", "content", "sender", "channel_id", "created_at"}

	// Enable highlighting
	req.Highlight = bleve.NewHighlightWithStyle("html")
	req.Highlight.AddField("content")
	req.Highlight.AddField("title")

	// Filter by type if specified
	if len(types) > 0 {
		typeQueries := make([]blevequery.Query, 0, len(types))
		for _, t := range types {
			tq := bleve.NewTermQuery(t)
			tq.SetField("type")
			typeQueries = append(typeQueries, tq)
		}
		typeFilter := bleve.NewDisjunctionQuery(typeQueries...)
		combined := bleve.NewConjunctionQuery(q, typeFilter)
		req = bleve.NewSearchRequestOptions(combined, limit, 0, false)
		req.Fields = []string{"type", "title", "content", "sender", "channel_id", "created_at"}
		req.Highlight = bleve.NewHighlightWithStyle("html")
		req.Highlight.AddField("content")
		req.Highlight.AddField("title")
	}

	res, err := idx.Search(req)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(res.Hits))
	for _, hit := range res.Hits {
		r := SearchResult{
			ID:    hit.ID,
			Score: hit.Score,
		}
		if v, ok := hit.Fields["type"].(string); ok {
			r.Type = v
		}
		if v, ok := hit.Fields["title"].(string); ok {
			r.Title = v
		}
		if v, ok := hit.Fields["sender"].(string); ok {
			r.Sender = v
		}
		if v, ok := hit.Fields["channel_id"].(string); ok {
			r.ChannelID = v
		}

		// Use highlighted fragments if available, otherwise truncate content
		if frags, ok := hit.Fragments["content"]; ok && len(frags) > 0 {
			r.Highlight = frags[0]
		}
		if v, ok := hit.Fields["content"].(string); ok {
			if len(v) > 200 {
				v = v[:197] + "..."
			}
			r.Content = v
		}

		// Apply recency bias
		if createdAt, ok := hit.Fields["created_at"].(string); ok && createdAt != "" {
			r.Score = applyRecencyBias(r.Score, createdAt)
		}

		results = append(results, r)
	}

	// Re-sort by adjusted score
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// applyRecencyBias multiplies score by a decay factor based on age.
// 1.0 for today, 0.5 at 30 days, asymptotic to 0.3.
func applyRecencyBias(score float64, createdAt string) float64 {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return score
	}
	ageDays := time.Since(t).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	// Exponential decay: 0.3 + 0.7 * e^(-ageDays/43.3)
	// At 0 days: 1.0, at 30 days: ~0.65, at 90 days: ~0.39
	decay := 0.3 + 0.7*math.Exp(-ageDays/43.3)
	return score * decay
}

// Reindex backfills the index from SQLite. Runs synchronously — caller should use `go`.
func (m *IndexManager) Reindex(slug string, db *sql.DB) {
	log.Printf("[search:%s] starting reindex", slug)

	idx, err := m.Open(slug)
	if err != nil {
		log.Printf("[search:%s] reindex open error: %v", slug, err)
		return
	}

	batch := idx.NewBatch()
	count := 0

	// Messages
	rows, err := db.Query(`SELECT id, content, COALESCE(sender_id,''), channel_id, created_at FROM messages WHERE deleted = FALSE`)
	if err == nil {
		for rows.Next() {
			var id, content, sender, channelID, createdAt string
			rows.Scan(&id, &content, &sender, &channelID, &createdAt)
			batch.Index(id, SearchDoc{
				ID: id, Type: "message", Content: content,
				Sender: sender, ChannelID: channelID, CreatedAt: createdAt,
			})
			count++
			if count%500 == 0 {
				idx.Batch(batch)
				batch = idx.NewBatch()
			}
		}
		rows.Close()
	}

	// Documents
	rows, err = db.Query(`SELECT id, title, content, created_at FROM documents`)
	if err == nil {
		for rows.Next() {
			var id, title, content, createdAt string
			rows.Scan(&id, &title, &content, &createdAt)
			batch.Index(id, SearchDoc{
				ID: id, Type: "document", Title: title, Content: content, CreatedAt: createdAt,
			})
			count++
		}
		rows.Close()
	}

	// Tasks
	rows, err = db.Query(`SELECT id, title, description, status, created_at FROM tasks`)
	if err == nil {
		for rows.Next() {
			var id, title, desc, status, createdAt string
			rows.Scan(&id, &title, &desc, &status, &createdAt)
			batch.Index(id, SearchDoc{
				ID: id, Type: "task", Title: title, Content: desc, Status: status, CreatedAt: createdAt,
			})
			count++
		}
		rows.Close()
	}

	// Knowledge
	rows, err = db.Query(`SELECT id, title, content FROM brain_knowledge`)
	if err == nil {
		for rows.Next() {
			var id, title, content string
			rows.Scan(&id, &title, &content)
			batch.Index(id, SearchDoc{
				ID: id, Type: "knowledge", Title: title, Content: content,
			})
			count++
		}
		rows.Close()
	}

	// Members
	rows, err = db.Query(`SELECT id, display_name, role, COALESCE(title,''), COALESCE(bio,'') FROM members`)
	if err == nil {
		for rows.Next() {
			var id, displayName, role, title, bio string
			rows.Scan(&id, &displayName, &role, &title, &bio)
			content := role
			if title != "" {
				content += " " + title
			}
			if bio != "" {
				content += " " + bio
			}
			batch.Index(id, SearchDoc{
				ID: id, Type: "member", Title: displayName, Content: content,
			})
			count++
		}
		rows.Close()
	}

	// Channels
	rows, err = db.Query(`SELECT id, name, COALESCE(topic,'') FROM channels WHERE archived = FALSE`)
	if err == nil {
		for rows.Next() {
			var id, name, topic string
			rows.Scan(&id, &name, &topic)
			batch.Index(id, SearchDoc{
				ID: id, Type: "channel", Title: name, Content: topic,
			})
			count++
		}
		rows.Close()
	}

	if err := idx.Batch(batch); err != nil {
		log.Printf("[search:%s] reindex batch error: %v", slug, err)
		return
	}

	log.Printf("[search:%s] reindex complete: %d documents", slug, count)
}

// CloseAll closes all open indexes.
func (m *IndexManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for slug, idx := range m.indexes {
		if err := idx.Close(); err != nil {
			log.Printf("[search:%s] close error: %v", slug, err)
		}
	}
	m.indexes = make(map[string]bleve.Index)
}
