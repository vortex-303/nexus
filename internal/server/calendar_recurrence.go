package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/teambition/rrule-go"
)

// expandRecurring generates occurrences of a recurring event within a time range.
// Each occurrence gets a synthetic ID: "{parentID}_{unix_timestamp}".
func expandRecurring(event hub.EventPayload, rangeStart, rangeEnd time.Time) []hub.EventPayload {
	if event.RecurrenceRule == "" {
		return []hub.EventPayload{event}
	}

	eventStart, err := time.Parse(time.RFC3339, event.StartTime)
	if err != nil {
		return []hub.EventPayload{event}
	}
	eventEnd, err := time.Parse(time.RFC3339, event.EndTime)
	if err != nil {
		return []hub.EventPayload{event}
	}
	duration := eventEnd.Sub(eventStart)

	rule, err := rrule.StrToRRule(event.RecurrenceRule)
	if err != nil {
		return []hub.EventPayload{event}
	}
	rule.DTStart(eventStart)

	occurrences := rule.Between(rangeStart.Add(-duration), rangeEnd, true)

	// Cap at 366 occurrences to prevent runaway expansion
	if len(occurrences) > 366 {
		occurrences = occurrences[:366]
	}

	var result []hub.EventPayload
	for _, occ := range occurrences {
		occEnd := occ.Add(duration)
		e := hub.EventPayload{
			ID:                 fmt.Sprintf("%s_%d", event.ID, occ.Unix()),
			Title:              event.Title,
			Description:        event.Description,
			Location:           event.Location,
			StartTime:          occ.UTC().Format(time.RFC3339),
			EndTime:            occEnd.UTC().Format(time.RFC3339),
			AllDay:             event.AllDay,
			RecurrenceRule:     event.RecurrenceRule,
			RecurrenceParentID: event.ID,
			Color:              event.Color,
			Calendar:           event.Calendar,
			CreatedBy:          event.CreatedBy,
			Attendees:          json.RawMessage(event.Attendees),
			Reminders:          json.RawMessage(event.Reminders),
			ChannelID:          event.ChannelID,
			Status:             event.Status,
			CreatedAt:          event.CreatedAt,
			UpdatedAt:          event.UpdatedAt,
		}
		result = append(result, e)
	}
	return result
}
