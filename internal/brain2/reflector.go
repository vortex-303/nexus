package brain2

import (
	"database/sql"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/id"
)

// ReflectorConfig holds what the reflector needs to analyze after a response.
type ReflectorConfig struct {
	DB            *sql.DB
	Client        LLMClient // cheap model for reflection
	Slug          string
	ChannelID     string
	SenderName    string
	SenderID      string
	UserMessage   string
	BrainResponse string
	ToolsUsed     []string
}

// RunReflector analyzes the interaction async. It detects feedback signals,
// updates user profiles, and creates self-memories. Runs in a goroutine — no user latency.
func RunReflector(cfg ReflectorConfig) {
	// 1. Detect feedback signals from the user's message
	detectFeedback(cfg)

	// 2. Update member profile (what they're working on, communication patterns)
	updateMemberProfile(cfg)

	// 3. Check if we should save any self-memories about Brain's behavior
	if cfg.Client != nil {
		reflectOnResponse(cfg)
	}
}

// detectFeedback checks the user's message for correction or confirmation patterns.
func detectFeedback(cfg ReflectorConfig) {
	lower := strings.ToLower(cfg.UserMessage)

	// Correction patterns — user is telling Brain what NOT to do
	correctionSignals := []string{
		"no, ", "no not", "don't ", "dont ", "stop ", "wrong", "that's not",
		"thats not", "actually,", "actually ", "i said ", "not what i",
		"i meant ", "please don't", "never ", "not that",
	}

	for _, signal := range correctionSignals {
		if strings.Contains(lower, signal) {
			saveFeedbackMemory(cfg.DB, cfg.SenderID, cfg.ChannelID, cfg.UserMessage, "correction")
			return
		}
	}

	// Confirmation patterns — user validates Brain's approach
	confirmationSignals := []string{
		"perfect", "exactly", "that's right", "thats right", "yes exactly",
		"great job", "well done", "nice work", "keep doing",
		"yes, that", "good call", "spot on",
	}

	for _, signal := range confirmationSignals {
		if strings.Contains(lower, signal) {
			saveFeedbackMemory(cfg.DB, cfg.SenderID, cfg.ChannelID, cfg.UserMessage, "confirmation")
			return
		}
	}
}

func saveFeedbackMemory(db *sql.DB, memberID, channelID, content, feedbackType string) {
	if db == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.Exec(
		`INSERT INTO brain_memories (id, type, content, confidence, source, source_channel, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id.New(), "feedback", content, 0.85, "reflector_"+feedbackType, channelID, now,
	)
}

// updateMemberProfile tracks what each member talks about and their communication style.
func updateMemberProfile(cfg ReflectorConfig) {
	if cfg.DB == nil || cfg.SenderID == "" {
		return
	}

	// Simple topic extraction — save what the user is working on
	lower := strings.ToLower(cfg.UserMessage)
	workSignals := []string{"working on", "building", "fixing", "implementing", "deploying", "launching"}

	for _, signal := range workSignals {
		idx := strings.Index(lower, signal)
		if idx >= 0 {
			topic := cfg.UserMessage[idx:]
			if len(topic) > 200 {
				topic = topic[:200]
			}
			now := time.Now().UTC().Format(time.RFC3339)
			_, _ = cfg.DB.Exec(
				`INSERT INTO brain_memories (id, type, content, confidence, source, source_channel, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				id.New(), "preference",
				cfg.SenderName+": "+topic,
				0.6, "reflector_profile", cfg.ChannelID, now,
			)
			return
		}
	}
}

// reflectOnResponse uses an LLM to analyze whether the response was good
// and if Brain should remember anything about its own behavior.
func reflectOnResponse(cfg ReflectorConfig) {
	if len(cfg.BrainResponse) < 50 {
		return // too short to analyze
	}

	reflectPrompt := `You are analyzing a conversation between a user and an AI assistant called Brain.
Determine if Brain should save any behavioral notes for self-improvement.

User said: ` + truncate(cfg.UserMessage, 500) + `
Brain responded: ` + truncate(cfg.BrainResponse, 1000) + `
Tools used: ` + strings.Join(cfg.ToolsUsed, ", ") + `

If there's something Brain should remember about how to handle similar requests in the future, respond with a single short sentence starting with "Remember:".
If the interaction was routine and nothing special to note, respond with just "OK".
Respond with ONLY "Remember: ..." or "OK". Nothing else.`

	messages := []brain.Message{{Role: "user", Content: reflectPrompt}}
	response, _, err := cfg.Client.Complete("You are a concise self-reflection analyzer.", messages)
	if err != nil {
		return
	}

	response = strings.TrimSpace(response)
	if strings.HasPrefix(response, "Remember:") {
		now := time.Now().UTC().Format(time.RFC3339)
		_, _ = cfg.DB.Exec(
			`INSERT INTO brain_memories (id, type, content, confidence, source, source_channel, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id.New(), "self", response, 0.7, "reflector_self", cfg.ChannelID, now,
		)
	}
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
