package brain

import (
	"database/sql"
	"regexp"
	"strings"

	"github.com/nexus-chat/nexus/internal/id"
)

var (
	decisionRe = regexp.MustCompile(`(?i)\b(we decided|let'?s go with|agreed on|going with|we'll use|decision:|we chose|decision is to|decided to|let'?s buy|let'?s start|let'?s use)\b`)
	commitRe   = regexp.MustCompile(`(?i)\b(i'll|i will|by (?:friday|monday|tuesday|wednesday|thursday|saturday|sunday|end of (?:day|week|month))|promise to|committed to|action item:)\b`)
	personRe   = regexp.MustCompile(`(?i)@(\w+)\s+(?:is|handles|works on|manages|leads)\b`)
)

// RunRuleExtraction extracts memories from a message using pattern matching (no LLM).
// Returns the number of memories saved.
func RunRuleExtraction(db *sql.DB, channelID, messageID, content, senderName string) int {
	if len(content) < 10 {
		return 0
	}

	saved := 0

	// Decisions
	if locs := decisionRe.FindStringIndex(content); locs != nil {
		snippet := extractSentence(content, locs[0], locs[1])
		if snippet != "" && !MemorySimilarExists(db, MemoryTypeDecision, snippet) {
			if SaveMemoryFull(db, id.New(), MemoryTypeDecision, snippet, "rule", channelID, messageID, 0.8, "", 0.8, senderName) == nil {
				saved++
			}
		}
	}

	// Commitments
	if locs := commitRe.FindStringIndex(content); locs != nil {
		snippet := extractSentence(content, locs[0], locs[1])
		if snippet != "" && !MemorySimilarExists(db, MemoryTypeCommitment, snippet) {
			if SaveMemoryFull(db, id.New(), MemoryTypeCommitment, snippet, "rule", channelID, messageID, 0.7, "", 0.8, senderName) == nil {
				saved++
			}
		}
	}

	// People
	if match := personRe.FindStringSubmatch(content); match != nil {
		locs := personRe.FindStringIndex(content)
		snippet := extractSentence(content, locs[0], locs[1])
		if snippet != "" && !MemorySimilarExists(db, MemoryTypePerson, snippet) {
			if SaveMemoryFull(db, id.New(), MemoryTypePerson, snippet, "rule", channelID, messageID, 0.6, "", 0.8, senderName) == nil {
				saved++
			}
		}
	}

	return saved
}

// extractSentence extracts the sentence containing the match, capped at 300 chars.
func extractSentence(text string, matchStart, matchEnd int) string {
	// Find sentence start (look back for . ! ? or start of text)
	start := 0
	for i := matchStart - 1; i >= 0; i-- {
		if text[i] == '.' || text[i] == '!' || text[i] == '?' || text[i] == '\n' {
			start = i + 1
			break
		}
	}

	// Find sentence end (look forward for . ! ? or end of text)
	end := len(text)
	for i := matchEnd; i < len(text); i++ {
		if text[i] == '.' || text[i] == '!' || text[i] == '?' || text[i] == '\n' {
			end = i + 1
			break
		}
	}

	sentence := strings.TrimSpace(text[start:end])
	if len(sentence) > 300 {
		sentence = sentence[:297] + "..."
	}
	return sentence
}
