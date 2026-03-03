package server

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/id"
)

// smtpSession accumulates envelope data for a single SMTP transaction.
type smtpSession struct {
	server *Server
	from   string
	to     []string
}

// smtpBackend implements a minimal SMTP server using raw net.Conn.
type smtpBackend struct {
	server *Server
}

// startSMTPServer starts a basic SMTP server on the configured listen address.
func (s *Server) startSMTPServer() {
	addr := s.cfg.SMTPListen
	if addr == "" {
		addr = ":2525"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("[smtp] failed to listen on %s: %v", addr, err)
		return
	}
	log.Printf("[smtp] listening on %s", addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("[smtp] accept error: %v", err)
			continue
		}
		go s.handleSMTPConn(conn)
	}
}

// handleSMTPConn handles a single SMTP connection with a minimal state machine.
func (s *Server) handleSMTPConn(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(60 * time.Second))

	write := func(msg string) {
		conn.Write([]byte(msg + "\r\n"))
	}

	write("220 nexus SMTP ready")

	session := &smtpSession{server: s}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	var dataMode bool
	var dataBuffer bytes.Buffer

	for {
		n, err := conn.Read(tmp)
		if err != nil {
			return
		}
		buf = append(buf, tmp[:n]...)

		for {
			idx := bytes.Index(buf, []byte("\r\n"))
			if idx < 0 {
				break
			}
			line := string(buf[:idx])
			buf = buf[idx+2:]

			if dataMode {
				if line == "." {
					dataMode = false
					session.handleData(dataBuffer.Bytes())
					write("250 OK")
					dataBuffer.Reset()
				} else {
					dataBuffer.WriteString(line + "\r\n")
				}
				continue
			}

			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "HELO") || strings.HasPrefix(upper, "EHLO"):
				write("250 Hello")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				addr := extractAngle(line[10:])
				session.from = addr
				write("250 OK")
			case strings.HasPrefix(upper, "RCPT TO:"):
				addr := extractAngle(line[8:])
				session.to = append(session.to, addr)
				write("250 OK")
			case upper == "DATA":
				dataMode = true
				write("354 Start mail input")
			case upper == "QUIT":
				write("221 Bye")
				return
			case upper == "RSET":
				session.from = ""
				session.to = nil
				write("250 OK")
			case upper == "NOOP":
				write("250 OK")
			default:
				write("500 Unrecognized command")
			}
		}
	}
}

// extractAngle extracts an email address from SMTP angle brackets.
func extractAngle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "<"); i >= 0 {
		if j := strings.Index(s, ">"); j > i {
			return s[i+1 : j]
		}
	}
	return s
}

// handleData processes the DATA portion of an SMTP message.
func (sess *smtpSession) handleData(data []byte) {
	s := sess.server

	// Parse the email
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		log.Printf("[smtp] failed to parse email: %v", err)
		return
	}

	// Extract slug from To address: brain-{slug}@domain
	var slug string
	for _, to := range sess.to {
		local := strings.Split(to, "@")[0]
		if strings.HasPrefix(local, "brain-") {
			slug = strings.TrimPrefix(local, "brain-")
			break
		}
	}
	if slug == "" {
		log.Printf("[smtp] no brain-{slug}@ recipient found in %v", sess.to)
		return
	}

	// Check email_enabled
	if s.getBrainSetting(slug, "email_enabled") != "true" {
		log.Printf("[smtp] email not enabled for workspace %s", slug)
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		log.Printf("[smtp] workspace error for %s: %v", slug, err)
		return
	}

	subject := msg.Header.Get("Subject")
	fromAddr := sess.from
	fromName := fromAddr
	if addrs, err := msg.Header.AddressList("From"); err == nil && len(addrs) > 0 {
		fromAddr = addrs[0].Address
		if addrs[0].Name != "" {
			fromName = addrs[0].Name
		}
	}
	messageID := msg.Header.Get("Message-Id")
	inReplyTo := msg.Header.Get("In-Reply-To")

	// Extract body text
	body := extractEmailBody(msg)

	// Check reply scope
	scope := s.getBrainSetting(slug, "email_reply_scope")
	if scope == "" {
		scope = "anyone"
	}
	// For now, "anyone" accepts all. Other scopes can be added later.

	// Find or create channel
	var channelID string

	// 1. Check In-Reply-To → email_threads for existing channel
	if inReplyTo != "" {
		_ = wdb.DB.QueryRow(
			"SELECT channel_id FROM email_threads WHERE message_id = ?", inReplyTo,
		).Scan(&channelID)
	}

	// 2. Check channel_integrations for sender
	if channelID == "" {
		_ = wdb.DB.QueryRow(
			"SELECT channel_id FROM channel_integrations WHERE source_type = 'email' AND source_key = ?",
			fromAddr,
		).Scan(&channelID)
	}

	// 3. Create new channel
	if channelID == "" {
		channelID = id.New()
		chName := "email: " + subject
		if len(chName) > 80 {
			chName = chName[:80]
		}
		_, err = wdb.DB.Exec(
			"INSERT INTO channels (id, name, type, created_by) VALUES (?, ?, 'public', 'system')",
			channelID, chName,
		)
		if err != nil {
			log.Printf("[smtp] failed to create channel: %v", err)
			return
		}

		// Link sender to channel
		_, _ = wdb.DB.Exec(
			"INSERT OR IGNORE INTO channel_integrations (id, channel_id, source_type, source_key, label) VALUES (?, ?, 'email', ?, ?)",
			id.New(), channelID, fromAddr, fromName,
		)
	}

	// Insert/update email_threads
	if messageID != "" {
		now := time.Now().UTC().Format(time.RFC3339)
		_, _ = wdb.DB.Exec(
			`INSERT INTO email_threads (id, message_id, channel_id, subject, last_reply_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(message_id) DO UPDATE SET last_reply_at = ?`,
			id.New(), messageID, channelID, subject, now, now,
		)
	}

	// Format content for channel
	content := fmt.Sprintf("**From:** %s\n**Subject:** %s\n\n%s", fromAddr, subject, body)

	// Sender ID
	senderID := "email:" + fromAddr

	// Get autonomy
	autonomy := s.getBrainSetting(slug, "email_autonomy")
	if autonomy == "" {
		autonomy = "draft"
	}

	// Build reply function
	replyFn := func(response string) {
		s.sendOutboundEmail(slug, fromAddr, "Re: "+subject, response, messageID)
	}

	s.ingestExternalMessage(slug, channelID, senderID, fromName, content, "email", autonomy, replyFn)
}

// extractEmailBody extracts the text body from an email message.
func extractEmailBody(msg *mail.Message) string {
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, _ := mime.ParseMediaType(contentType)

	// Multipart message
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary != "" {
			reader := multipart.NewReader(msg.Body, boundary)
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				partType := part.Header.Get("Content-Type")
				if strings.HasPrefix(partType, "text/plain") || partType == "" {
					body, _ := io.ReadAll(io.LimitReader(part, 64*1024))
					return string(body)
				}
			}
		}
	}

	// Simple text message
	body, _ := io.ReadAll(io.LimitReader(msg.Body, 64*1024))
	return string(body)
}

// sendOutboundEmail sends an email via the configured outbound SMTP server.
func (s *Server) sendOutboundEmail(slug, to, subject, body, inReplyTo string) {
	host := s.getBrainSetting(slug, "email_outbound_host")
	port := s.getBrainSetting(slug, "email_outbound_port")
	user := s.getBrainSetting(slug, "email_outbound_user")
	pass := s.getBrainSetting(slug, "email_outbound_pass")

	if host == "" {
		log.Printf("[email:%s] no outbound SMTP configured, skipping reply", slug)
		return
	}
	if port == "" {
		port = "587"
	}

	from := user
	if from == "" {
		from = fmt.Sprintf("brain-%s@%s", slug, host)
	}

	// Build email message
	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	if inReplyTo != "" {
		msg.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", inReplyTo))
		msg.WriteString(fmt.Sprintf("References: %s\r\n", inReplyTo))
	}
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	msg.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z)))
	msg.WriteString("\r\n")
	msg.WriteString(body)

	addr := fmt.Sprintf("%s:%s", host, port)

	var a smtp.Auth
	if user != "" && pass != "" {
		a = smtp.PlainAuth("", user, pass, host)
	}

	if err := smtp.SendMail(addr, a, from, []string{to}, msg.Bytes()); err != nil {
		log.Printf("[email:%s] failed to send reply to %s: %v", slug, to, err)
	} else {
		log.Printf("[email:%s] sent reply to %s: %s", slug, to, subject)
	}
}

// handleListEmailThreads returns email threads for a workspace.
// GET /api/workspaces/{slug}/brain/email/threads
func (s *Server) handleListEmailThreads(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	rows, err := wdb.DB.Query(
		"SELECT id, message_id, channel_id, subject, participants, last_reply_at, created_at FROM email_threads ORDER BY last_reply_at DESC LIMIT 50",
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	var threads []map[string]string
	for rows.Next() {
		var tid, msgID, chID, subject, participants, lastReply, createdAt string
		if rows.Scan(&tid, &msgID, &chID, &subject, &participants, &lastReply, &createdAt) == nil {
			threads = append(threads, map[string]string{
				"id":            tid,
				"message_id":    msgID,
				"channel_id":    chID,
				"subject":       subject,
				"participants":  participants,
				"last_reply_at": lastReply,
				"created_at":    createdAt,
			})
		}
	}

	if threads == nil {
		threads = []map[string]string{}
	}
	writeJSON(w, http.StatusOK, threads)
}

// handleDeleteEmailThread deletes an email thread record.
// DELETE /api/workspaces/{slug}/brain/email/threads/{id}
func (s *Server) handleDeleteEmailThread(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	threadID := r.PathValue("id")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	result, _ := wdb.DB.Exec("DELETE FROM email_threads WHERE id = ?", threadID)
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "thread not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
