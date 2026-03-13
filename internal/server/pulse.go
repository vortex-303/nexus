package server

import (
	"fmt"

	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// Pulse represents a workspace activity event.
type Pulse struct {
	Type      string // e.g. "message.sent", "task.created"
	ChannelID string
	ActorID   string
	ActorName string
	Source    string // "user", "brain", "agent", "system"
	EntityID  string
	Summary   string
	Detail    string
}

type activityEntry struct {
	ID        string `json:"id"`
	PulseType string `json:"pulse_type"`
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
	ChannelID string `json:"channel_id"`
	EntityID  string `json:"entity_id"`
	Summary   string `json:"summary"`
	Detail    string `json:"detail"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) onPulse(slug string, p Pulse) {
	go s.recordActivity(slug, p)
}

func (s *Server) recordActivity(slug string, p Pulse) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Str("workspace", slug).Msg("pulse: workspace error")
		return
	}

	source := p.Source
	if source == "" {
		source = "user"
	}

	// Batch message and agent pulses: merge with recent entry from same actor+channel within 10 min
	if p.Type == "message.sent" || p.Type == "agent.responded" {
		var existingID string
		var existingCount int
		err := wdb.DB.QueryRow(
			`SELECT id, CAST(COALESCE(detail, '1') AS INTEGER) FROM activity_stream
			 WHERE pulse_type = ? AND actor_id = ? AND channel_id = ?
			   AND created_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-10 minutes')
			 ORDER BY created_at DESC LIMIT 1`,
			p.Type, p.ActorID, p.ChannelID,
		).Scan(&existingID, &existingCount)
		if err == nil && existingID != "" {
			// Merge: update count and timestamp
			newCount := existingCount + 1
			var verb string
			if p.Type == "agent.responded" {
				verb = "sent"
			} else {
				verb = "sent"
			}
			newSummary := fmt.Sprintf("%s %s %d messages", p.ActorName, verb, newCount)
			if p.ChannelID != "" {
				newSummary += " in a channel"
			}
			_, _ = wdb.DB.Exec(
				`UPDATE activity_stream SET summary = ?, detail = ?, created_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
				 WHERE id = ?`,
				newSummary, fmt.Sprintf("%d", newCount), existingID,
			)

			var createdAt string
			_ = wdb.DB.QueryRow("SELECT created_at FROM activity_stream WHERE id = ?", existingID).Scan(&createdAt)

			entry := activityEntry{
				ID:        existingID,
				PulseType: p.Type,
				ActorID:   p.ActorID,
				ActorName: p.ActorName,
				ChannelID: p.ChannelID,
				EntityID:  p.EntityID,
				Summary:   newSummary,
				Detail:    fmt.Sprintf("%d", newCount),
				Source:    source,
				CreatedAt: createdAt,
			}
			h := s.hubs.Get(slug)
			if h != nil {
				h.BroadcastAll(hub.MakeEnvelope(hub.TypeActivityNew, entry), "")
			}
			return
		}
	}

	actID := id.New()
	detail := p.Detail
	// For message types, store count in detail
	if (p.Type == "message.sent" || p.Type == "agent.responded") && detail == "" {
		detail = "1"
	}

	_, err = wdb.DB.Exec(
		`INSERT INTO activity_stream (id, pulse_type, actor_id, actor_name, channel_id, entity_id, summary, detail, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		actID, p.Type, p.ActorID, p.ActorName, p.ChannelID, p.EntityID, p.Summary, detail, source,
	)
	if err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Str("workspace", slug).Msg("pulse: failed to record activity")
		return
	}

	// Read back created_at
	var createdAt string
	_ = wdb.DB.QueryRow("SELECT created_at FROM activity_stream WHERE id = ?", actID).Scan(&createdAt)

	entry := activityEntry{
		ID:        actID,
		PulseType: p.Type,
		ActorID:   p.ActorID,
		ActorName: p.ActorName,
		ChannelID: p.ChannelID,
		EntityID:  p.EntityID,
		Summary:   p.Summary,
		Detail:    detail,
		Source:    source,
		CreatedAt: createdAt,
	}

	h := s.hubs.Get(slug)
	if h != nil {
		h.BroadcastAll(hub.MakeEnvelope(hub.TypeActivityNew, entry), "")
	}
}

// pulseSummary builds a summary string like "{actor} created task "title""
func pulseSummary(actor, verb, entity string) string {
	if entity != "" {
		return fmt.Sprintf("%s %s \"%s\"", actor, verb, entity)
	}
	return fmt.Sprintf("%s %s", actor, verb)
}
