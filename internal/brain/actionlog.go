package brain

import (
	"database/sql"
	"encoding/json"
	"time"
)

// ActionLog represents a logged Brain action.
type ActionLog struct {
	ID           string   `json:"id"`
	ActionType   string   `json:"action_type"`
	ChannelID    string   `json:"channel_id,omitempty"`
	TriggerText  string   `json:"trigger_text"`
	ResponseText string   `json:"response_text"`
	ToolsUsed    []string `json:"tools_used"`
	Model        string   `json:"model"`
	CreatedAt    string   `json:"created_at"`
}

// Action types
const (
	ActionMention      = "mention"
	ActionExtraction   = "extraction"
	ActionHeartbeat    = "heartbeat"
	ActionConfigChange = "config_change"
)

// LogAction stores a brain action in the log.
func LogAction(db *sql.DB, id, actionType, channelID, triggerText, responseText, model string, toolsUsed []string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	tools := "[]"
	if len(toolsUsed) > 0 {
		b, _ := json.Marshal(toolsUsed)
		tools = string(b)
	}
	_, err := db.Exec(
		`INSERT INTO brain_action_log (id, action_type, channel_id, trigger_text, response_text, tools_used, model, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, actionType, channelID, triggerText, responseText, tools, model, now,
	)
	return err
}

// ListActions returns recent actions.
func ListActions(db *sql.DB, limit int) ([]ActionLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(
		"SELECT id, action_type, COALESCE(channel_id,''), trigger_text, response_text, tools_used, model, created_at FROM brain_action_log ORDER BY created_at DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var actions []ActionLog
	for rows.Next() {
		var a ActionLog
		var toolsJSON string
		if err := rows.Scan(&a.ID, &a.ActionType, &a.ChannelID, &a.TriggerText, &a.ResponseText, &toolsJSON, &a.Model, &a.CreatedAt); err != nil {
			continue
		}
		json.Unmarshal([]byte(toolsJSON), &a.ToolsUsed)
		if a.ToolsUsed == nil {
			a.ToolsUsed = []string{}
		}
		actions = append(actions, a)
	}
	return actions, nil
}

// CountActions returns total action count.
func CountActions(db *sql.DB) int {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM brain_action_log").Scan(&count)
	return count
}
