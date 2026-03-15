package brain

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Memory types
const (
	MemoryTypeFact       = "fact"
	MemoryTypeDecision   = "decision"
	MemoryTypeCommitment = "commitment"
	MemoryTypePerson     = "person"
	MemoryTypePolicy     = "policy"
	MemoryTypeInsight    = "insight"
)

// Memory represents an extracted memory.
type Memory struct {
	ID              string  `json:"id"`
	Type            string  `json:"type"`
	Content         string  `json:"content"`
	Source          string  `json:"source"`
	Importance      float64 `json:"importance"`
	Summary         string  `json:"summary"`
	Confidence      float64 `json:"confidence"`
	Participants    string  `json:"participants"`
	Metadata        string  `json:"metadata"`
	ValidUntil      string  `json:"valid_until,omitempty"`
	SupersededBy    string  `json:"superseded_by,omitempty"`
	SourceChannel   string  `json:"source_channel,omitempty"`
	SourceMessageID string  `json:"source_message_id,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

// ChannelSummary holds a rolling summary for a channel.
type ChannelSummary struct {
	ChannelID    string `json:"channel_id"`
	Summary      string `json:"summary"`
	MessageCount int    `json:"message_count"`
	UpdatedAt    string `json:"updated_at"`
}

// ListMemories returns all memories, optionally filtered by type.
func ListMemories(db *sql.DB, memType string, limit int) ([]Memory, error) {
	query := `SELECT id, type, content, COALESCE(source,'llm'), COALESCE(importance, 0.5),
		COALESCE(summary,''), COALESCE(confidence, 0.5), COALESCE(participants,''),
		COALESCE(metadata,'{}'), COALESCE(valid_until,''), COALESCE(superseded_by,''),
		COALESCE(source_channel,''), COALESCE(source_message_id,''), created_at
		FROM brain_memories`
	var args []any

	if memType != "" {
		query += " WHERE type = ?"
		args = append(args, memType)
	}
	query += " ORDER BY created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, &m.Source, &m.Importance,
			&m.Summary, &m.Confidence, &m.Participants, &m.Metadata,
			&m.ValidUntil, &m.SupersededBy,
			&m.SourceChannel, &m.SourceMessageID, &m.CreatedAt); err != nil {
			continue
		}
		memories = append(memories, m)
	}

	// Sort by importance * recency decay for no-query listing
	sortByComposite(memories)

	return memories, nil
}

// CountMemories returns counts by type.
func CountMemories(db *sql.DB) (map[string]int, error) {
	rows, err := db.Query("SELECT type, COUNT(*) FROM brain_memories GROUP BY type")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var t string
		var c int
		if rows.Scan(&t, &c) == nil {
			counts[t] = c
		}
	}
	return counts, nil
}

// DeleteMemory removes a single memory.
func DeleteMemory(db *sql.DB, id string) error {
	_, err := db.Exec("DELETE FROM brain_memories WHERE id = ?", id)
	return err
}

// ClearMemories removes all memories.
func ClearMemories(db *sql.DB) error {
	_, err := db.Exec("DELETE FROM brain_memories")
	return err
}

// SaveMemory stores a new memory.
func SaveMemory(db *sql.DB, id, memType, content, source, sourceChannel, sourceMessageID string, importance float64) error {
	return SaveMemoryFull(db, id, memType, content, source, sourceChannel, sourceMessageID, importance, "", 0, "")
}

// SaveMemoryFull stores a new memory with all organizational fields.
func SaveMemoryFull(db *sql.DB, id, memType, content, source, sourceChannel, sourceMessageID string, importance float64, summary string, confidence float64, participants string) error {
	if source == "" {
		source = "llm"
	}
	if importance <= 0 {
		importance = defaultImportance(memType)
	}
	if confidence <= 0 {
		confidence = importance
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO brain_memories (id, type, content, source, source_channel, source_message_id, importance, summary, confidence, participants, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, memType, content, source, sourceChannel, sourceMessageID, importance, summary, confidence, participants, now,
	)
	return err
}

// defaultImportance returns a sensible default importance for a memory type.
func defaultImportance(memType string) float64 {
	switch memType {
	case MemoryTypeDecision:
		return 0.8
	case MemoryTypePolicy:
		return 0.8
	case MemoryTypeCommitment:
		return 0.7
	case MemoryTypeInsight:
		return 0.7
	case MemoryTypePerson:
		return 0.6
	default:
		return 0.5
	}
}

// MemoryExists checks if a memory with the same content already exists.
func MemoryExists(db *sql.DB, content string) bool {
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM brain_memories WHERE content = ?", content).Scan(&count)
	return count > 0
}

// MemorySimilarExists checks if a similar memory already exists (fuzzy dedup).
// Returns true if among the last 50 memories of the same type, one is a substring
// of the other or they share >80% word overlap.
func MemorySimilarExists(db *sql.DB, memType, content string) bool {
	if MemoryExists(db, content) {
		return true
	}

	rows, err := db.Query(
		"SELECT content FROM brain_memories WHERE type = ? ORDER BY created_at DESC LIMIT 50",
		memType,
	)
	if err != nil {
		return false
	}
	defer rows.Close()

	normNew := normalizeForDedup(content)
	newWords := wordSet(normNew)

	for rows.Next() {
		var existing string
		if rows.Scan(&existing) != nil {
			continue
		}
		normExisting := normalizeForDedup(existing)

		// Substring check
		if strings.Contains(normNew, normExisting) || strings.Contains(normExisting, normNew) {
			return true
		}

		// Word overlap check
		existingWords := wordSet(normExisting)
		if wordOverlap(newWords, existingWords) > 0.8 {
			return true
		}
	}
	return false
}

func normalizeForDedup(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == ' ' {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func wordOverlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	total := len(a)
	if len(b) > total {
		total = len(b)
	}
	return float64(shared) / float64(total)
}

// SearchMemories uses FTS5 full-text search with composite scoring.
func SearchMemories(db *sql.DB, query string, limit int) ([]Memory, error) {
	return SearchMemoriesTyped(db, query, "", limit)
}

// SearchMemoriesTyped uses FTS5 search with optional type filter and composite scoring.
func SearchMemoriesTyped(db *sql.DB, query, memType string, limit int) ([]Memory, error) {
	if query == "" || limit <= 0 {
		return ListMemories(db, memType, limit)
	}

	ftsQuery := sanitizeFTSQuery(query)
	if ftsQuery == "" {
		return ListMemories(db, memType, limit)
	}

	// Fetch more than needed so composite scoring can re-rank
	fetchLimit := limit * 3
	if fetchLimit < 30 {
		fetchLimit = 30
	}

	q := `
		SELECT m.id, m.type, m.content, COALESCE(m.source,'llm'), COALESCE(m.importance, 0.5),
			COALESCE(m.summary,''), COALESCE(m.confidence, 0.5), COALESCE(m.participants,''),
			COALESCE(m.metadata,'{}'), COALESCE(m.valid_until,''), COALESCE(m.superseded_by,''),
			COALESCE(m.source_channel,''), COALESCE(m.source_message_id,''), m.created_at,
			bm25(brain_memories_fts) AS rank
		FROM brain_memories m
		JOIN brain_memories_fts fts ON m.rowid = fts.rowid
		WHERE brain_memories_fts MATCH ?`
	args := []any{ftsQuery}

	if memType != "" {
		q += " AND m.type = ?"
		args = append(args, memType)
	}
	q += " ORDER BY rank LIMIT ?"
	args = append(args, fetchLimit)

	rows, err := db.Query(q, args...)
	if err != nil {
		return ListMemories(db, memType, limit)
	}
	defer rows.Close()

	type scoredMemory struct {
		Memory
		bm25Score float64
	}

	var scored []scoredMemory
	for rows.Next() {
		var sm scoredMemory
		var rank float64
		if err := rows.Scan(&sm.ID, &sm.Type, &sm.Content, &sm.Source, &sm.Importance,
			&sm.Summary, &sm.Confidence, &sm.Participants, &sm.Metadata,
			&sm.ValidUntil, &sm.SupersededBy,
			&sm.SourceChannel, &sm.SourceMessageID, &sm.CreatedAt, &rank); err != nil {
			continue
		}
		// BM25 returns negative scores (more negative = better match), normalize
		sm.bm25Score = -rank
		scored = append(scored, sm)
	}
	if len(scored) == 0 {
		return ListMemories(db, memType, limit)
	}

	// Normalize BM25 scores to 0-1
	var maxBM25 float64
	for _, s := range scored {
		if s.bm25Score > maxBM25 {
			maxBM25 = s.bm25Score
		}
	}
	if maxBM25 == 0 {
		maxBM25 = 1
	}

	// Compute composite scores and sort
	type ranked struct {
		Memory
		composite float64
	}
	var results []ranked
	for _, s := range scored {
		normBM25 := s.bm25Score / maxBM25
		recency := recencyDecay(s.CreatedAt)
		composite := 0.5*normBM25 + 0.3*recency + 0.2*s.Importance
		results = append(results, ranked{s.Memory, composite})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].composite > results[j].composite
	})

	if len(results) > limit {
		results = results[:limit]
	}
	memories := make([]Memory, len(results))
	for i, r := range results {
		memories[i] = r.Memory
	}
	return memories, nil
}

// compositeScore computes a blended relevance score.
func recencyDecay(createdAt string) float64 {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return 0.5
	}
	ageDays := time.Since(t).Hours() / 24
	return math.Pow(0.5, ageDays/30) // half-life = 30 days
}

// sortByComposite sorts memories by importance * recency (for no-query listing).
func sortByComposite(memories []Memory) {
	sort.Slice(memories, func(i, j int) bool {
		ri := recencyDecay(memories[i].CreatedAt) * memories[i].Importance
		rj := recencyDecay(memories[j].CreatedAt) * memories[j].Importance
		return ri > rj
	})
}

// sanitizeFTSQuery strips FTS5 operators and joins words with OR.
func sanitizeFTSQuery(q string) string {
	// Remove FTS5 special characters
	var cleaned []string
	for _, word := range strings.Fields(q) {
		// Strip leading/trailing punctuation and FTS operators
		word = strings.Trim(word, `"'()*+-:^~{}[]<>!@#$%&`)
		if word == "" || strings.EqualFold(word, "AND") || strings.EqualFold(word, "OR") || strings.EqualFold(word, "NOT") || strings.EqualFold(word, "NEAR") {
			continue
		}
		cleaned = append(cleaned, word)
	}
	return strings.Join(cleaned, " OR ")
}

// GetChannelSummary returns the current summary for a channel.
func GetChannelSummary(db *sql.DB, channelID string) (*ChannelSummary, error) {
	var s ChannelSummary
	err := db.QueryRow(
		"SELECT channel_id, summary, message_count, updated_at FROM brain_channel_summaries WHERE channel_id = ?",
		channelID,
	).Scan(&s.ChannelID, &s.Summary, &s.MessageCount, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveChannelSummary updates (upsert) the summary for a channel.
func SaveChannelSummary(db *sql.DB, channelID, summary, lastMessageID string, messageCount int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO brain_channel_summaries (channel_id, summary, message_count, last_message_id, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET summary = ?, message_count = ?, last_message_id = ?, updated_at = ?`,
		channelID, summary, messageCount, lastMessageID, now,
		summary, messageCount, lastMessageID, now,
	)
	return err
}

// BuildMemoryContext creates a text block of memories for inclusion in system prompt.
// Only includes current memories (not superseded). Shows participants and dates.
func BuildMemoryContext(db *sql.DB, query string) string {
	var memories []Memory
	var err error
	if query != "" {
		memories, err = SearchMemories(db, query, 20)
	} else {
		memories, err = ListMemories(db, "", 50)
	}
	if err != nil || len(memories) == 0 {
		return ""
	}

	// Filter out superseded memories
	var current []Memory
	for _, m := range memories {
		if m.SupersededBy == "" && m.ValidUntil == "" {
			current = append(current, m)
		}
	}
	if len(current) == 0 {
		return ""
	}

	// Batch-resolve channel names
	channelNames := map[string]string{}
	var channelIDs []string
	for _, m := range current {
		if m.SourceChannel != "" {
			channelIDs = append(channelIDs, m.SourceChannel)
		}
	}
	if len(channelIDs) > 0 {
		placeholders := make([]string, len(channelIDs))
		args := make([]any, len(channelIDs))
		for i, cid := range channelIDs {
			placeholders[i] = "?"
			args[i] = cid
		}
		chRows, err := db.Query("SELECT id, name FROM channels WHERE id IN ("+strings.Join(placeholders, ",")+")", args...)
		if err == nil {
			defer chRows.Close()
			for chRows.Next() {
				var cid, cname string
				if chRows.Scan(&cid, &cname) == nil {
					channelNames[cid] = cname
				}
			}
		}
	}

	var parts []string
	parts = append(parts, "# Organizational Memory\n")

	byType := map[string][]Memory{}
	for _, m := range current {
		byType[m.Type] = append(byType[m.Type], m)
	}

	typeLabels := map[string]string{
		MemoryTypeDecision:   "Decisions",
		MemoryTypeCommitment: "Commitments",
		MemoryTypePolicy:     "Policies",
		MemoryTypePerson:     "People",
		MemoryTypeInsight:    "Insights",
		MemoryTypeFact:       "Facts",
	}

	for _, t := range []string{MemoryTypeDecision, MemoryTypeCommitment, MemoryTypePolicy, MemoryTypePerson, MemoryTypeInsight, MemoryTypeFact} {
		mems := byType[t]
		if len(mems) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("## %s", typeLabels[t]))
		for _, m := range mems {
			line := fmt.Sprintf("- **%s**", m.Content)
			// Add participants, channel, and date
			var meta []string
			if m.Participants != "" {
				meta = append(meta, m.Participants)
			}
			if m.SourceChannel != "" {
				if cname, ok := channelNames[m.SourceChannel]; ok {
					meta = append(meta, "in #"+cname)
				}
			}
			if t, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
				meta = append(meta, t.Format("Jan 2"))
			}
			if len(meta) > 0 {
				line += " — " + strings.Join(meta, ", ")
			}
			parts = append(parts, line)
		}
		parts = append(parts, "")
	}

	return strings.Join(parts, "\n")
}

// BuildChannelContext creates a text block of channel summaries.
func BuildChannelContext(db *sql.DB) string {
	rows, err := db.Query("SELECT channel_id, summary FROM brain_channel_summaries WHERE summary != '' ORDER BY updated_at DESC LIMIT 10")
	if err != nil {
		return ""
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var chID, summary string
		if rows.Scan(&chID, &summary) == nil {
			parts = append(parts, fmt.Sprintf("Channel %s: %s", chID, summary))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return "# Channel Summaries\n\n" + strings.Join(parts, "\n\n")
}

// SummarizationPrompt instructs the LLM to produce a rolling channel summary.
const SummarizationPrompt = `You are a conversation summarizer. Given recent chat messages (and optionally a previous summary), produce a concise rolling summary of the conversation.

Rules:
- If a previous summary is provided, merge the new messages into it — update, extend, or replace stale information
- Keep the summary under 500 words
- Focus on: key topics discussed, decisions made, questions asked, action items, and important context
- Use present tense for ongoing topics, past tense for concluded ones
- Skip greetings, small talk, and trivial messages
- Output ONLY the summary text, no JSON or formatting markers`

// SummaryMessage represents a message for summarization.
type SummaryMessage struct {
	ID      string
	Name    string
	Content string
}

// GetMessagesSinceSummary fetches messages created after the last summarized message.
func GetMessagesSinceSummary(db *sql.DB, channelID string, limit int) ([]SummaryMessage, error) {
	// Get the last_message_id from the summary
	var lastMsgID string
	_ = db.QueryRow("SELECT COALESCE(last_message_id, '') FROM brain_channel_summaries WHERE channel_id = ?", channelID).Scan(&lastMsgID)

	var rows *sql.Rows
	var err error
	if lastMsgID != "" {
		// Get the created_at of the last summarized message
		var lastAt string
		if db.QueryRow("SELECT created_at FROM messages WHERE id = ?", lastMsgID).Scan(&lastAt) == nil {
			rows, err = db.Query(`
				SELECT m.id, COALESCE(mem.display_name, m.sender_id), m.content
				FROM messages m
				LEFT JOIN members mem ON mem.id = m.sender_id
				WHERE m.channel_id = ? AND m.deleted = FALSE AND m.created_at > ?
				ORDER BY m.created_at ASC
				LIMIT ?
			`, channelID, lastAt, limit)
		}
	}
	if rows == nil && err == nil {
		// No previous summary or lookup failed — get recent messages
		rows, err = db.Query(`
			SELECT m.id, COALESCE(mem.display_name, m.sender_id), m.content
			FROM messages m
			LEFT JOIN members mem ON mem.id = m.sender_id
			WHERE m.channel_id = ? AND m.deleted = FALSE
			ORDER BY m.created_at DESC
			LIMIT ?
		`, channelID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []SummaryMessage
	for rows.Next() {
		var m SummaryMessage
		if rows.Scan(&m.ID, &m.Name, &m.Content) == nil {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

// BuildSingleChannelContext returns a formatted summary for the active channel.
func BuildSingleChannelContext(db *sql.DB, channelID string) string {
	s, err := GetChannelSummary(db, channelID)
	if err != nil || s.Summary == "" {
		return ""
	}
	return fmt.Sprintf("# Conversation History\nSummary of %d earlier messages in this channel:\n%s", s.MessageCount, s.Summary)
}

// BuildCrossChannelContext returns truncated summaries from other channels (for Brain only).
func BuildCrossChannelContext(db *sql.DB, excludeChannelID string) string {
	rows, err := db.Query(`
		SELECT s.channel_id, COALESCE(c.name, s.channel_id), s.summary
		FROM brain_channel_summaries s
		LEFT JOIN channels c ON c.id = s.channel_id
		WHERE s.channel_id != ? AND s.summary != ''
		ORDER BY s.updated_at DESC
		LIMIT 5
	`, excludeChannelID)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var chID, chName, summary string
		if rows.Scan(&chID, &chName, &summary) == nil {
			// Truncate to 200 chars
			if len(summary) > 200 {
				summary = summary[:197] + "..."
			}
			parts = append(parts, fmt.Sprintf("**#%s**: %s", chName, summary))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "# Activity in Other Channels\n\n" + strings.Join(parts, "\n\n")
}

// ExtractionPrompt returns the system prompt for memory extraction.
const ExtractionPrompt = `You are an organizational memory system. Extract ONLY information someone would search for weeks or months from now.

EXTRACT (use these types):
- "decision": A choice the team made. Include WHO decided, WHAT was decided, WHY.
- "commitment": Something someone promised to do. Include WHO, WHAT, WHEN (deadline if mentioned).
- "policy": A rule, standard, or recurring practice the team follows. Include SCOPE.
- "person": A lasting fact about a member's role, expertise, or responsibility.

DISCARD everything else — be extremely selective:
- Creative content, brainstorming ideas, stylistic text, ad copy, image descriptions
- Casual conversation, greetings, jokes, reactions, emoji-only messages
- Temporary context, debugging output, live status updates
- Image generation prompts, visual compositions, design mockups
- Questions without answers, incomplete thoughts, speculation
- URLs, dates, and dollar amounts that lack organizational significance
- Content that is only relevant in the moment, not months later

Output ONLY a JSON array. Each object must have:
- "type": one of "decision", "commitment", "policy", "person"
- "content": Full standalone statement with context (one or two sentences)
- "summary": One-line summary, max 100 characters
- "confidence": 0.0 to 1.0 (1.0 = explicit clear statement, 0.8 = strong implication, 0.5 = ambiguous)
- "participants": array of names involved (e.g. ["Nico", "Maria"])

Confidence guide:
- 1.0: "We decided to use Stripe" — explicit, clear
- 0.8: "I think we should go with Stripe" + agreement — strong implication
- 0.5: mentioned in passing, no clear conclusion — DO NOT INCLUDE

Critical test: "Would someone search for this 3 months from now?" If no → discard.
If nothing worth extracting, return: []
Return ONLY valid JSON, no other text.`

// ConsolidationPrompt instructs the LLM to merge, supersede, and generate insights from memories.
const ConsolidationPrompt = `You are an organizational memory consolidation system. Given a set of recent memories, identify:

1. DUPLICATES: memories that say the same thing differently. Return the ID to keep and IDs to supersede.
2. SUPERSEDED: memories where a newer one replaces an older fact/decision. Mark the old one with valid_until.
3. INSIGHTS: patterns you notice across multiple memories. Generate new "insight" type memories.
4. CONFIDENCE UPGRADES: if a memory was later confirmed by another, upgrade its confidence.

Output a JSON object:
{
  "supersede": [{"old_id": "...", "new_id": "...", "reason": "..."}],
  "insights": [{"content": "...", "summary": "...", "based_on": ["id1", "id2"]}],
  "upgrade_confidence": [{"id": "...", "new_confidence": 0.9}]
}

Rules:
- Only supersede when the newer memory clearly replaces the older one
- Only generate insights when a genuine pattern exists across 3+ memories
- Keep it minimal — quality over quantity
- If nothing to consolidate, return: {"supersede": [], "insights": [], "upgrade_confidence": []}`

// RecentMemories fetches memories from the last N days.
func RecentMemories(db *sql.DB, days int, limit int) ([]Memory, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	query := `SELECT id, type, content, COALESCE(source,'llm'), COALESCE(importance, 0.5),
		COALESCE(summary,''), COALESCE(confidence, 0.5), COALESCE(participants,''),
		COALESCE(metadata,'{}'), COALESCE(valid_until,''), COALESCE(superseded_by,''),
		COALESCE(source_channel,''), COALESCE(source_message_id,''), created_at
		FROM brain_memories
		WHERE created_at > ? AND valid_until IS NULL AND superseded_by IS NULL OR superseded_by = ''
		ORDER BY created_at DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := db.Query(query, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, &m.Source, &m.Importance,
			&m.Summary, &m.Confidence, &m.Participants, &m.Metadata,
			&m.ValidUntil, &m.SupersededBy,
			&m.SourceChannel, &m.SourceMessageID, &m.CreatedAt); err != nil {
			continue
		}
		memories = append(memories, m)
	}
	return memories, nil
}

// SupersedeMemory marks a memory as superseded by another.
func SupersedeMemory(db *sql.DB, oldID, newID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec("UPDATE brain_memories SET superseded_by = ?, valid_until = ? WHERE id = ?", newID, now, oldID)
	return err
}

// UpdateConfidence updates the confidence score of a memory.
func UpdateConfidence(db *sql.DB, id string, confidence float64) error {
	_, err := db.Exec("UPDATE brain_memories SET confidence = ? WHERE id = ?", confidence, id)
	return err
}
