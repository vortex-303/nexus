package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/smtp"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/logger"
)

// generateICS creates an iCalendar (RFC 5545) VEVENT string.
func generateICS(event hub.EventPayload, method string) string {
	var b strings.Builder

	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//Nexus//Calendar//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	b.WriteString(fmt.Sprintf("METHOD:%s\r\n", method))
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString(fmt.Sprintf("UID:%s@nexus\r\n", event.ID))
	b.WriteString(fmt.Sprintf("DTSTAMP:%s\r\n", formatICSTime(event.CreatedAt)))
	b.WriteString(fmt.Sprintf("DTSTART:%s\r\n", formatICSTime(event.StartTime)))
	b.WriteString(fmt.Sprintf("DTEND:%s\r\n", formatICSTime(event.EndTime)))
	b.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", escapeICS(event.Title)))

	if event.Description != "" {
		b.WriteString(fmt.Sprintf("DESCRIPTION:%s\r\n", escapeICS(event.Description)))
	}
	if event.Location != "" {
		b.WriteString(fmt.Sprintf("LOCATION:%s\r\n", escapeICS(event.Location)))
	}
	if event.RecurrenceRule != "" {
		b.WriteString(fmt.Sprintf("RRULE:%s\r\n", event.RecurrenceRule))
	}

	// Status mapping
	switch event.Status {
	case "confirmed":
		b.WriteString("STATUS:CONFIRMED\r\n")
	case "tentative":
		b.WriteString("STATUS:TENTATIVE\r\n")
	case "cancelled":
		b.WriteString("STATUS:CANCELLED\r\n")
	}

	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")

	return b.String()
}

// formatICSTime converts ISO 8601 to iCalendar datetime format.
func formatICSTime(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.UTC().Format("20060102T150405Z")
}

// escapeICS escapes special characters for iCalendar text.
func escapeICS(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

// sendEventInvite sends an email with an .ics attachment.
func (s *Server) sendEventInvite(slug string, event hub.EventPayload, recipientEmail, method string) {
	host := s.getBrainSetting(slug, "email_outbound_host")
	port := s.getBrainSetting(slug, "email_outbound_port")
	user := s.getBrainSetting(slug, "email_outbound_user")
	pass := s.getBrainSetting(slug, "email_outbound_pass")

	if host == "" {
		logger.WithCategory(logger.CatCalendar).Warn().Str("workspace", slug).Msg("no outbound SMTP configured, skipping invite")
		return
	}
	if port == "" {
		port = "587"
	}

	from := user
	if from == "" {
		from = fmt.Sprintf("calendar@%s", host)
	}

	ics := generateICS(event, method)
	boundary := fmt.Sprintf("nexus-cal-%d", time.Now().UnixNano())

	subject := "Invitation: " + event.Title
	if method == "CANCEL" {
		subject = "Cancelled: " + event.Title
	}

	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", recipientEmail))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary))
	msg.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z)))
	msg.WriteString("\r\n")

	// Text part
	msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	msg.WriteString(fmt.Sprintf("Event: %s\r\n", event.Title))
	msg.WriteString(fmt.Sprintf("When: %s - %s\r\n", event.StartTime, event.EndTime))
	if event.Location != "" {
		msg.WriteString(fmt.Sprintf("Where: %s\r\n", event.Location))
	}
	if event.Description != "" {
		msg.WriteString(fmt.Sprintf("\r\n%s\r\n", event.Description))
	}
	msg.WriteString("\r\n")

	// ICS attachment
	msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	msg.WriteString(fmt.Sprintf("Content-Type: text/calendar; charset=utf-8; method=%s\r\n", method))
	msg.WriteString("Content-Disposition: attachment; filename=\"invite.ics\"\r\n\r\n")
	msg.WriteString(ics)
	msg.WriteString("\r\n")
	msg.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	addr := fmt.Sprintf("%s:%s", host, port)
	var a smtp.Auth
	if user != "" && pass != "" {
		a = smtp.PlainAuth("", user, pass, host)
	}

	if err := smtp.SendMail(addr, a, from, []string{recipientEmail}, msg.Bytes()); err != nil {
		logger.WithCategory(logger.CatCalendar).Error().Err(err).Str("workspace", slug).Str("recipient", recipientEmail).Msg("failed to send invite")
	} else {
		logger.WithCategory(logger.CatCalendar).Info().Str("workspace", slug).Str("recipient", recipientEmail).Str("event", event.Title).Msg("sent invite")
	}
}

// sendEventInvites sends invites to all attendees with email addresses.
func (s *Server) sendEventInvites(slug string, event hub.EventPayload, method string) {
	var attendees []struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(event.Attendees, &attendees); err != nil {
		return
	}

	for _, a := range attendees {
		if a.Email != "" {
			go s.sendEventInvite(slug, event, a.Email, method)
		}
	}
}
