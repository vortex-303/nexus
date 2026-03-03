package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"nhooyr.io/websocket"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/roles"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
	maxMsgSize = 64 * 1024 // 64KB
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Authenticate via query param (WebSocket can't set headers easily)
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		tokenStr = auth.FromRequest(r)
	}
	if tokenStr == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	claims, err := s.jwt.Validate(tokenStr)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	if claims.WorkspaceSlug == "" {
		http.Error(w, "token not bound to workspace", http.StatusBadRequest)
		return
	}

	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow any origin in dev
	})
	if err != nil {
		log.Printf("websocket accept: %v", err)
		return
	}
	wsConn.SetReadLimit(maxMsgSize)

	h := s.hubs.Get(claims.WorkspaceSlug)
	conn := hub.NewConn(id.New(), claims.UserID, claims.DisplayName, claims.WorkspaceSlug, claims.Role)

	h.Register(conn)

	// Broadcast presence online
	h.BroadcastAll(hub.MakeEnvelope(hub.TypePresence, hub.PresencePayload{
		UserID:      conn.UserID,
		DisplayName: conn.DisplayName,
		Status:      "online",
	}), conn.ID)

	// Auto-subscribe to all channels the user is a member of
	s.autoSubscribe(conn, claims.WorkspaceSlug)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Writer goroutine
	go s.wsWriter(ctx, wsConn, conn)

	// Reader loop (blocking)
	s.wsReader(ctx, wsConn, conn, h)

	// Cleanup
	h.Unregister(conn)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypePresence, hub.PresencePayload{
		UserID:      conn.UserID,
		DisplayName: conn.DisplayName,
		Status:      "offline",
	}), "")
	wsConn.Close(websocket.StatusNormalClosure, "")
}

func (s *Server) autoSubscribe(conn *hub.Conn, slug string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}
	// Subscribe to all public channels + channels user is in
	rows, err := wdb.DB.Query("SELECT id FROM channels WHERE archived = FALSE")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var chID string
		if err := rows.Scan(&chID); err == nil {
			conn.Subscribe(chID)
		}
	}
}

func (s *Server) wsReader(ctx context.Context, wsConn *websocket.Conn, conn *hub.Conn, h *hub.Hub) {
	for {
		_, data, err := wsConn.Read(ctx)
		if err != nil {
			return
		}

		var env hub.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			s.sendError(conn, "invalid message format")
			continue
		}

		s.handleWSMessage(conn, h, &env)
	}
}

func (s *Server) wsWriter(ctx context.Context, wsConn *websocket.Conn, conn *hub.Conn) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-conn.Send:
			if !ok {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeWait)
			err := wsConn.Write(writeCtx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, writeWait)
			err := wsConn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) wsChecker(conn *hub.Conn) *roles.Checker {
	wdb, err := s.ws.Open(conn.WorkspaceSlug)
	if err != nil {
		return nil
	}
	return roles.NewChecker(wdb.DB)
}

func (s *Server) wsHasPerm(conn *hub.Conn, perm roles.Permission) bool {
	checker := s.wsChecker(conn)
	if checker == nil {
		return false
	}
	return checker.Has(conn.UserID, perm)
}

func (s *Server) handleWSMessage(conn *hub.Conn, h *hub.Hub, env *hub.Envelope) {
	switch env.Type {
	case hub.TypeMessageSend:
		if !s.wsHasPerm(conn, roles.PermChatSend) {
			s.sendError(conn, "no permission to send messages")
			return
		}
		s.handleWSSendMessage(conn, h, env.Payload)
	case hub.TypeMessageEdit:
		if !s.wsHasPerm(conn, roles.PermChatEdit) {
			s.sendError(conn, "no permission to edit messages")
			return
		}
		s.handleWSEditMessage(conn, h, env.Payload)
	case hub.TypeMessageDelete:
		if !s.wsHasPerm(conn, roles.PermChatDelete) && !s.wsHasPerm(conn, roles.PermChatDeleteAny) {
			s.sendError(conn, "no permission to delete messages")
			return
		}
		s.handleWSDeleteMessage(conn, h, env.Payload)
	case hub.TypeReactionAdd:
		if !s.wsHasPerm(conn, roles.PermChatReact) {
			s.sendError(conn, "no permission to react")
			return
		}
		s.handleWSReaction(conn, h, env.Payload, true)
	case hub.TypeReactionRemove:
		s.handleWSReaction(conn, h, env.Payload, false)
	case hub.TypeTypingStart:
		s.handleWSTyping(conn, h, env.Payload)
	case hub.TypeChannelJoin:
		var p hub.ChannelJoinPayload
		if json.Unmarshal(env.Payload, &p) == nil {
			conn.Subscribe(p.ChannelID)
		}
	case hub.TypeChannelLeave:
		var p hub.ChannelJoinPayload
		if json.Unmarshal(env.Payload, &p) == nil {
			conn.Unsubscribe(p.ChannelID)
		}
	case hub.TypeChannelClear:
		if conn.Role != "admin" {
			s.sendError(conn, "admin only")
			return
		}
		s.handleWSClearChannel(conn, h, env.Payload)
	default:
		s.sendError(conn, "unknown message type: "+env.Type)
	}
}

func (s *Server) handleWSSendMessage(conn *hub.Conn, h *hub.Hub, payload json.RawMessage) {
	var p hub.MessageSendPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.sendError(conn, "invalid payload")
		return
	}
	if p.ChannelID == "" || p.Content == "" {
		s.sendError(conn, "channel_id and content required")
		return
	}

	// Auto-subscribe sender if not already subscribed (fixes DM bug)
	if !conn.IsSubscribed(p.ChannelID) {
		conn.Subscribe(p.ChannelID)
	}

	// Auto-subscribe other DM participants
	s.autoSubscribeChannel(conn, h, p.ChannelID)

	wdb, err := s.ws.Open(conn.WorkspaceSlug)
	if err != nil {
		s.sendError(conn, "workspace error")
		return
	}

	msgID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = wdb.DB.Exec(
		"INSERT INTO messages (id, channel_id, sender_id, content, created_at) VALUES (?, ?, ?, ?, ?)",
		msgID, p.ChannelID, conn.UserID, p.Content, now,
	)
	if err != nil {
		s.sendError(conn, "failed to save message")
		return
	}

	// Update last-read for sender
	_, _ = wdb.DB.Exec(
		`INSERT INTO channel_reads (channel_id, user_id, last_read_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(channel_id, user_id) DO UPDATE SET last_read_at = ?`,
		p.ChannelID, conn.UserID, now, now,
	)

	// Broadcast to all subscribers (including sender for confirmation)
	out := hub.MakeEnvelope(hub.TypeMessageNew, hub.MessageNewPayload{
		ID:         msgID,
		ChannelID:  p.ChannelID,
		SenderID:   conn.UserID,
		SenderName: conn.DisplayName,
		Content:    p.Content,
		CreatedAt:  now,
	})
	h.Broadcast(p.ChannelID, out, "") // Send to everyone including sender

	// Track for memory extraction
	s.trackMessageAndMaybeExtract(conn.WorkspaceSlug, p.ChannelID)

	// Check for @Brain mention or DM with Brain
	isBrainDM := false
	var chName string
	_ = wdb.DB.QueryRow("SELECT name FROM channels WHERE id = ? AND type = 'dm'", p.ChannelID).Scan(&chName)
	if chName != "" && strings.Contains(chName, brain.BrainMemberID) {
		isBrainDM = true
	}
	if brain.ContainsMention(p.Content) || isBrainDM {
		s.handleBrainMentionWithTools(conn.WorkspaceSlug, p.ChannelID, conn.DisplayName, p.Content)
	}

	// Check for @Agent mentions
	mentionedAgents := s.checkAgentMentions(conn.WorkspaceSlug, p.Content)
	for _, agent := range mentionedAgents {
		s.handleAgentMention(conn.WorkspaceSlug, p.ChannelID, conn.DisplayName, p.Content, agent)
	}

	// Build set of already-triggered agent IDs
	triggered := map[string]bool{}
	for _, a := range mentionedAgents {
		triggered[a.ID] = true
	}

	// Check DM with built-in agents (same pattern as Brain DMs)
	if chName != "" {
		for _, ba := range brain.BuiltinAgents {
			if strings.Contains(chName, ba.MemberID) && !triggered[ba.ID] {
				if agent := s.loadAgentByID(conn.WorkspaceSlug, ba.ID); agent != nil && agent.IsActive {
					triggered[ba.ID] = true
					s.handleAgentMention(conn.WorkspaceSlug, p.ChannelID, conn.DisplayName, p.Content, agent)
				}
			}
		}
	}

	// @all — trigger agents with trigger_type='all'
	if strings.Contains(strings.ToLower(p.Content), "@all") {
		allAgents := s.checkAllAgents(conn.WorkspaceSlug, p.ChannelID)
		for _, agent := range allAgents {
			if !triggered[agent.ID] {
				triggered[agent.ID] = true
				s.handleAgentMention(conn.WorkspaceSlug, p.ChannelID, conn.DisplayName, p.Content, agent)
			}
		}
	}

	// always — trigger agents with trigger_type='always'
	alwaysAgents := s.checkAlwaysAgents(conn.WorkspaceSlug, p.ChannelID)
	for _, agent := range alwaysAgents {
		if !triggered[agent.ID] {
			triggered[agent.ID] = true
			s.handleAgentMention(conn.WorkspaceSlug, p.ChannelID, conn.DisplayName, p.Content, agent)
		}
	}
}

func (s *Server) handleWSEditMessage(conn *hub.Conn, h *hub.Hub, payload json.RawMessage) {
	var p hub.MessageEditPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.sendError(conn, "invalid payload")
		return
	}

	wdb, err := s.ws.Open(conn.WorkspaceSlug)
	if err != nil {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Only allow editing own messages
	res, err := wdb.DB.Exec(
		"UPDATE messages SET content = ?, edited_at = ? WHERE id = ? AND sender_id = ?",
		p.Content, now, p.MessageID, conn.UserID,
	)
	if err != nil {
		s.sendError(conn, "failed to edit message")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.sendError(conn, "message not found or not yours")
		return
	}

	h.Broadcast(p.ChannelID, hub.MakeEnvelope(hub.TypeMessageEdited, hub.MessageEditedPayload{
		MessageID: p.MessageID,
		ChannelID: p.ChannelID,
		Content:   p.Content,
		EditedAt:  now,
	}), "")
}

func (s *Server) handleWSDeleteMessage(conn *hub.Conn, h *hub.Hub, payload json.RawMessage) {
	var p hub.MessageDeletePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.sendError(conn, "invalid payload")
		return
	}

	wdb, err := s.ws.Open(conn.WorkspaceSlug)
	if err != nil {
		return
	}

	// Allow deleting own messages or admin
	query := "UPDATE messages SET deleted = TRUE WHERE id = ? AND sender_id = ?"
	args := []any{p.MessageID, conn.UserID}
	if conn.Role == "admin" {
		query = "UPDATE messages SET deleted = TRUE WHERE id = ?"
		args = []any{p.MessageID}
	}

	res, err := wdb.DB.Exec(query, args...)
	if err != nil {
		s.sendError(conn, "failed to delete message")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.sendError(conn, "message not found or not yours")
		return
	}

	h.Broadcast(p.ChannelID, hub.MakeEnvelope(hub.TypeMessageDeleted, hub.MessageDeletedPayload{
		MessageID: p.MessageID,
		ChannelID: p.ChannelID,
	}), "")
}

func (s *Server) handleWSReaction(conn *hub.Conn, h *hub.Hub, payload json.RawMessage, add bool) {
	var p hub.ReactionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.sendError(conn, "invalid payload")
		return
	}

	wdb, err := s.ws.Open(conn.WorkspaceSlug)
	if err != nil {
		return
	}

	if add {
		_, _ = wdb.DB.Exec(
			"INSERT OR IGNORE INTO reactions (message_id, user_id, emoji) VALUES (?, ?, ?)",
			p.MessageID, conn.UserID, p.Emoji,
		)
	} else {
		_, _ = wdb.DB.Exec(
			"DELETE FROM reactions WHERE message_id = ? AND user_id = ? AND emoji = ?",
			p.MessageID, conn.UserID, p.Emoji,
		)
	}

	p.UserID = conn.UserID
	p.UserName = conn.DisplayName

	msgType := hub.TypeReactionAdded
	if !add {
		msgType = hub.TypeReactionRemoved
	}
	h.Broadcast(p.ChannelID, hub.MakeEnvelope(msgType, p), "")
}

func (s *Server) handleWSTyping(conn *hub.Conn, h *hub.Hub, payload json.RawMessage) {
	var p hub.TypingPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return
	}
	p.UserID = conn.UserID
	p.DisplayName = conn.DisplayName
	h.Broadcast(p.ChannelID, hub.MakeEnvelope(hub.TypeTyping, p), conn.ID)
}

func (s *Server) sendError(conn *hub.Conn, msg string) {
	data := hub.MakeEnvelope(hub.TypeError, hub.ErrorPayload{Message: msg})
	select {
	case conn.Send <- data:
	default:
	}
}

func (s *Server) handleWSClearChannel(conn *hub.Conn, h *hub.Hub, payload json.RawMessage) {
	var p hub.ChannelClearPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.sendError(conn, "invalid payload")
		return
	}
	if p.ChannelID == "" {
		s.sendError(conn, "channel_id required")
		return
	}

	wdb, err := s.ws.Open(conn.WorkspaceSlug)
	if err != nil {
		s.sendError(conn, "workspace error")
		return
	}

	// Soft-delete all messages in the channel
	_, err = wdb.DB.Exec("UPDATE messages SET deleted = TRUE WHERE channel_id = ?", p.ChannelID)
	if err != nil {
		s.sendError(conn, "failed to clear channel")
		return
	}

	// Clear the channel summary too
	_, _ = wdb.DB.Exec("DELETE FROM brain_channel_summaries WHERE channel_id = ?", p.ChannelID)

	// Broadcast to all subscribers
	h.Broadcast(p.ChannelID, hub.MakeEnvelope(hub.TypeChannelCleared, hub.ChannelClearedPayload{
		ChannelID: p.ChannelID,
	}), "")
}

// autoSubscribeChannel ensures all workspace connections are subscribed to a channel.
// This fixes the bug where channels created after WebSocket connect (like DMs) don't receive messages.
func (s *Server) autoSubscribeChannel(conn *hub.Conn, h *hub.Hub, channelID string) {
	h.SubscribeAll(channelID)
}
