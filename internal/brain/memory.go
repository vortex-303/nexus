package brain

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Memory types
const (
	MemoryTypeFact       = "fact"
	MemoryTypeDecision   = "decision"
	MemoryTypeCommitment = "commitment"
	MemoryTypePerson     = "person"
)

// Memory represents an extracted memory.
type Memory struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	Content         string `json:"content"`
	SourceChannel   string `json:"source_channel,omitempty"`
	SourceMessageID string `json:"source_message_id,omitempty"`
	CreatedAt       string `json:"created_at"`
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
	query := "SELECT id, type, content, COALESCE(source_channel,''), COALESCE(source_message_id,''), created_at FROM brain_memories"
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
		if err := rows.Scan(&m.ID, &m.Type, &m.Content, &m.SourceChannel, &m.SourceMessageID, &m.CreatedAt); err != nil {
			continue
		}
		memories = append(memories, m)
	}
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
func SaveMemory(db *sql.DB, id, memType, content, sourceChannel, sourceMessageID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		"INSERT INTO brain_memories (id, type, content, source_channel, source_message_id, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, memType, content, sourceChannel, sourceMessageID, now,
	)
	return err
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
func BuildMemoryContext(db *sql.DB) string {
	memories, err := ListMemories(db, "", 50)
	if err != nil || len(memories) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, "# Active Memory\n\nHere are key facts and decisions you've extracted from conversations:\n")

	byType := map[string][]Memory{}
	for _, m := range memories {
		byType[m.Type] = append(byType[m.Type], m)
	}

	typeLabels := map[string]string{
		MemoryTypeDecision:   "Decisions",
		MemoryTypeCommitment: "Commitments",
		MemoryTypePerson:     "People",
		MemoryTypeFact:       "Facts",
	}

	for _, t := range []string{MemoryTypeDecision, MemoryTypeCommitment, MemoryTypePerson, MemoryTypeFact} {
		mems := byType[t]
		if len(mems) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("## %s", typeLabels[t]))
		for _, m := range mems {
			parts = append(parts, fmt.Sprintf("- %s", m.Content))
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
const ExtractionPrompt = `You are a memory extraction system. Given a batch of chat messages, extract key facts, decisions, commitments, and information about people.

Output ONLY a JSON array of objects. Each object must have:
- "type": one of "fact", "decision", "commitment", "person"
- "content": a concise, standalone statement (one sentence)

Types:
- "decision": A choice or conclusion the team made (e.g., "Team decided to use PostgreSQL for the backend")
- "commitment": Something someone promised to do (e.g., "Sarah will send the proposal by Friday")
- "person": Information about a team member (e.g., "Jake handles frontend development and design")
- "fact": Any other important information (e.g., "The project deadline is March 15th")

Rules:
- Only extract genuinely important, reusable information
- Skip small talk, greetings, and trivial messages
- Each fact should be self-contained and make sense without context
- If there's nothing worth extracting, return an empty array: []
- Return ONLY valid JSON, no other text`
