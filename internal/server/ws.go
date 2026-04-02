package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"nhooyr.io/websocket"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
	"github.com/nexus-chat/nexus/internal/metrics"
	"github.com/nexus-chat/nexus/internal/roles"
	"github.com/nexus-chat/nexus/internal/search"
)

const (
	writeWait  = 15 * time.Second
	pongWait   = 90 * time.Second
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
		logger.WithCategory(logger.CatWebSocket).Error().Err(err).Msg("websocket accept failed")
		return
	}
	wsConn.SetReadLimit(maxMsgSize)

	h := s.hubs.Get(claims.WorkspaceSlug)
	conn := hub.NewConn(id.New(), claims.UserID, claims.DisplayName, claims.WorkspaceSlug, claims.Role)

	h.Register(conn)
	metrics.WSConnectionsActive.WithLabelValues(claims.WorkspaceSlug).Inc()

	// Broadcast presence online (low priority — droppable under pressure)
	h.BroadcastAllLowPriority(hub.MakeEnvelope(hub.TypePresence, hub.PresencePayload{
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
	metrics.WSConnectionsActive.WithLabelValues(claims.WorkspaceSlug).Dec()
	h.Unregister(conn)
	h.BroadcastAllLowPriority(hub.MakeEnvelope(hub.TypePresence, hub.PresencePayload{
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
	// Subscribe to channels the user is a member of (via channel_members)
	rows, err := wdb.DB.Query(`
		SELECT c.id FROM channels c
		JOIN channel_members cm ON cm.channel_id = c.id
		WHERE c.archived = FALSE AND cm.member_id = ?
	`, conn.UserID)
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
	// Also subscribe to DM channels (membership encoded in name)
	dmRows, err := wdb.DB.Query(`
		SELECT id FROM channels WHERE archived = FALSE AND type = 'dm' AND name LIKE '%' || ? || '%'
	`, conn.UserID)
	if err != nil {
		return
	}
	defer dmRows.Close()
	for dmRows.Next() {
		var chID string
		if err := dmRows.Scan(&chID); err == nil {
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
			s.sendErrorCode(conn, "no permission to send messages", "no_permission")
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
	case hub.TypeChannelRead:
		s.handleWSChannelRead(conn, h, env.Payload)
	case hub.TypeChannelClear:
		s.handleWSClearChannel(conn, h, env.Payload)
	default:
		s.sendError(conn, "unknown message type: "+env.Type)
	}
}

func (s *Server) handleWSSendMessage(conn *hub.Conn, h *hub.Hub, payload json.RawMessage) {
	// Rate limit: max one message per 200ms
	sendTime := time.Now()
	lastMsg := conn.LastMessageAt()
	if !lastMsg.IsZero() && sendTime.Sub(lastMsg) < 200*time.Millisecond {
		s.sendErrorCode(conn, "sending too fast", "rate_limited")
		return
	}
	conn.SetLastMessageAt(sendTime)

	var p hub.MessageSendPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.sendErrorCode(conn, "invalid payload", "invalid_payload")
		return
	}
	if p.ChannelID == "" || p.Content == "" {
		s.sendErrorCode(conn, "channel_id and content required", "invalid_payload")
		return
	}

	// Auto-subscribe sender if not already subscribed (fixes DM bug)
	if !conn.IsSubscribed(p.ChannelID) {
		conn.Subscribe(p.ChannelID)
	}

	// Auto-subscribe other DM participants
	s.autoSubscribeChannel(conn, h, p.ChannelID)

	// Check @mentions and auto-add mentioned users/bots to channel
	s.handleMentionToJoin(conn.WorkspaceSlug, p.ChannelID, p.Content, h)

	wdb, err := s.ws.Open(conn.WorkspaceSlug)
	if err != nil {
		s.sendError(conn, "workspace error")
		return
	}

	msgID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	// Validate parent_id if provided (must exist in same channel)
	if p.ParentID != "" {
		var parentChannel string
		err := wdb.DB.QueryRow("SELECT channel_id FROM messages WHERE id = ? AND deleted = FALSE", p.ParentID).Scan(&parentChannel)
		if err != nil || parentChannel != p.ChannelID {
			s.sendError(conn, "invalid parent_id")
			return
		}
	}

	if p.ParentID != "" {
		_, err = wdb.DB.Exec(
			"INSERT INTO messages (id, channel_id, sender_id, content, created_at, parent_id) VALUES (?, ?, ?, ?, ?, ?)",
			msgID, p.ChannelID, conn.UserID, p.Content, now, p.ParentID,
		)
	} else {
		_, err = wdb.DB.Exec(
			"INSERT INTO messages (id, channel_id, sender_id, content, created_at) VALUES (?, ?, ?, ?, ?)",
			msgID, p.ChannelID, conn.UserID, p.Content, now,
		)
	}
	if err != nil {
		s.sendError(conn, "failed to save message")
		return
	}

	s.search.Index(conn.WorkspaceSlug, search.SearchDoc{
		ID: msgID, Type: "message", Content: p.Content,
		Sender: conn.DisplayName, ChannelID: p.ChannelID, CreatedAt: now,
	})

	// Update last-read for sender
	_, _ = wdb.DB.Exec(
		`INSERT INTO channel_reads (channel_id, user_id, last_read_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(channel_id, user_id) DO UPDATE SET last_read_at = ?`,
		p.ChannelID, conn.UserID, now, now,
	)

	// Send confirmation to sender (with client_id for dedup), broadcast to others
	out := hub.MakeEnvelope(hub.TypeMessageNew, hub.MessageNewPayload{
		ID:         msgID,
		ChannelID:  p.ChannelID,
		SenderID:   conn.UserID,
		SenderName: conn.DisplayName,
		Content:    p.Content,
		CreatedAt:  now,
		ClientID:   p.ClientID,
		ParentID:   p.ParentID,
	})
	h.SendTo(conn.ID, out)
	h.Broadcast(p.ChannelID, out, conn.ID)

	metrics.MessagesTotal.WithLabelValues(conn.WorkspaceSlug).Inc()

	s.onPulse(conn.WorkspaceSlug, Pulse{
		Type: "message.sent", ActorID: conn.UserID, ActorName: conn.DisplayName,
		ChannelID: p.ChannelID, EntityID: msgID,
		Summary: pulseSummary(conn.DisplayName, "sent a message", ""),
	})

	// Track thread activity (lightweight UPDATE, no LLM)
	if p.ParentID != "" {
		s.updateThreadActivity(wdb, p.ChannelID, p.ParentID)
	}

	// Track for memory extraction
	s.trackMessageAndMaybeExtract(conn.WorkspaceSlug, p.ChannelID, msgID, p.Content, conn.DisplayName)

	// Look up DM channel name (used for both Brain and Agent DM detection)
	var chName string
	_ = wdb.DB.QueryRow("SELECT name FROM channels WHERE id = ? AND type = 'dm'", p.ChannelID).Scan(&chName)

	// Check for @Brain mention or DM with Brain
	// Skip server-side brain if sender's client is handling via WebLLM
	isBrainDM := chName != "" && strings.Contains(chName, brain.BrainMemberID)
	brainTriggered := false
	if !p.WebLLM {
		if brain.ContainsMention(p.Content) || isBrainDM {
			brainTriggered = true
			if s.getBrainSetting(conn.WorkspaceSlug, "brain_version") == "v2" {
				s.handleBrainV2(conn.WorkspaceSlug, p.ChannelID, p.ParentID, conn.DisplayName, p.Content, time.Now())
			} else {
				s.handleBrainMentionWithTools(conn.WorkspaceSlug, p.ChannelID, p.ParentID, conn.DisplayName, p.Content, time.Now())
			}
		}
	}

	// Check for @Agent mentions
	mentionedAgents := s.checkAgentMentions(conn.WorkspaceSlug, p.Content)
	for _, agent := range mentionedAgents {
		s.handleAgentMention(conn.WorkspaceSlug, p.ChannelID, p.ParentID, conn.DisplayName, p.Content, agent, time.Now())
	}

	// Build set of already-triggered agent IDs
	triggered := map[string]bool{}
	for _, a := range mentionedAgents {
		triggered[a.ID] = true
	}

	// Check DM with agents FIRST — DMs always use handleAgentMention (with tools).
	// This must come before conversation-following to prevent the toolless follow-up
	// handler from stealing the trigger in DM channels.
	if chName != "" {
		// Built-in agents
		for _, ba := range brain.BuiltinAgents {
			if strings.Contains(chName, ba.MemberID) && !triggered[ba.ID] {
				if agent := s.loadAgentByID(conn.WorkspaceSlug, ba.ID); agent != nil && agent.IsActive {
					triggered[ba.ID] = true
					s.handleAgentMention(conn.WorkspaceSlug, p.ChannelID, p.ParentID, conn.DisplayName, p.Content, agent, time.Now())
				}
			}
		}
		// Custom agents — check if channel name contains any agent's ID
		if customAgents := s.checkDMAgents(conn.WorkspaceSlug, chName); len(customAgents) > 0 {
			for _, agent := range customAgents {
				if !triggered[agent.ID] {
					triggered[agent.ID] = true
					s.handleAgentMention(conn.WorkspaceSlug, p.ChannelID, p.ParentID, conn.DisplayName, p.Content, agent, time.Now())
				}
			}
		}
	}


	// Notification triggers (async)
	go func() {
		// DM notification
		if chName != "" && strings.HasPrefix(chName, "dm-") {
			parts := strings.Split(strings.TrimPrefix(chName, "dm-"), "-")
			for _, pid := range parts {
				if pid != conn.UserID && pid != brain.BrainMemberID {
					s.createNotification(wdb, conn.WorkspaceSlug, pid, "dm",
						conn.DisplayName+" sent you a message",
						truncateBody(p.Content, 200),
						"/w/"+conn.WorkspaceSlug+"?c="+p.ChannelID+"&m="+msgID,
						conn.UserID, conn.DisplayName, msgID)
				}
			}
		} else {
			// @mention notifications (non-DM channels)
			lower := strings.ToLower(p.Content)
			rows, err := wdb.DB.Query("SELECT id, display_name FROM members")
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var memberID, displayName string
					if rows.Scan(&memberID, &displayName) != nil {
						continue
					}
					if memberID == conn.UserID {
						continue
					}
					mention := "@" + strings.ToLower(displayName)
					if strings.Contains(lower, mention) {
						var chDisplayName string
						_ = wdb.DB.QueryRow("SELECT name FROM channels WHERE id = ?", p.ChannelID).Scan(&chDisplayName)
						s.createNotification(wdb, conn.WorkspaceSlug, memberID, "mention",
							conn.DisplayName+" mentioned you in #"+chDisplayName,
							truncateBody(p.Content, 200),
							"/w/"+conn.WorkspaceSlug+"?c="+p.ChannelID+"&m="+msgID,
							conn.UserID, conn.DisplayName, msgID)
					}
				}
			}
		}
	}()

	// Brain auto-follow: if Brain posted in this thread, auto-respond
	// Thread attention decays after 30 minutes of inactivity — require explicit @Brain
	if p.ParentID != "" && !p.WebLLM && !brainTriggered && !isBrainDM {
		if !brain.ContainsMention(p.Content) {
			var brainInThread int
			_ = wdb.DB.QueryRow(
				"SELECT COUNT(*) FROM messages WHERE (id = ? OR parent_id = ?) AND sender_id = ? AND deleted = FALSE",
				p.ParentID, p.ParentID, brain.BrainMemberID,
			).Scan(&brainInThread)
			if brainInThread > 0 {
				// Check thread freshness — don't auto-follow stale threads
				threadStale := false
				var lastActivity string
				if wdb.DB.QueryRow(
					"SELECT last_activity_at FROM thread_context WHERE parent_id = ?", p.ParentID,
				).Scan(&lastActivity) == nil && lastActivity != "" {
					if t, err := time.Parse(time.RFC3339, lastActivity); err == nil {
						if time.Since(t) > 30*time.Minute {
							threadStale = true
						}
					}
				}
				if !threadStale {
					triggered[brain.BrainMemberID] = true
					s.handleBrainMentionWithTools(conn.WorkspaceSlug, p.ChannelID, p.ParentID, conn.DisplayName, p.Content, time.Now())
				}
			}
		}
	}

	// Auto-follow threads: if any agent posted in this thread, that agent auto-responds
	if p.ParentID != "" {
		rows, err := wdb.DB.Query(
			"SELECT DISTINCT sender_id FROM messages WHERE (id = ? OR parent_id = ?) AND sender_id != ? AND deleted = FALSE",
			p.ParentID, p.ParentID, conn.UserID,
		)
		if err == nil {
			for rows.Next() {
				var senderID string
				rows.Scan(&senderID)
				if triggered[senderID] || senderID == brain.BrainMemberID {
					continue
				}
				if agent := s.loadAgentByID(conn.WorkspaceSlug, senderID); agent != nil && agent.IsActive && agent.BehaviorConfig.AutoFollowThreads {
					triggered[senderID] = true
					s.handleAgentMention(conn.WorkspaceSlug, p.ChannelID, p.ParentID, conn.DisplayName, p.Content, agent, time.Now())
				}
			}
			rows.Close()
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

	s.search.Index(conn.WorkspaceSlug, search.SearchDoc{
		ID: p.MessageID, Type: "message", Content: p.Content, ChannelID: p.ChannelID,
	})

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

	s.search.Delete(conn.WorkspaceSlug, p.MessageID)

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
	h.BroadcastLowPriority(p.ChannelID, hub.MakeEnvelope(hub.TypeTyping, p), conn.ID)
}

func (s *Server) sendError(conn *hub.Conn, msg string) {
	s.sendErrorCode(conn, msg, "")
}

func (s *Server) sendErrorCode(conn *hub.Conn, msg, code string) {
	data := hub.MakeEnvelope(hub.TypeError, hub.ErrorPayload{Message: msg, Code: code})
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

	// Admins can clear any channel; regular users can only clear their own DMs
	if conn.Role != "admin" {
		var chType, chName string
		err := wdb.DB.QueryRow("SELECT type, name FROM channels WHERE id = ?", p.ChannelID).Scan(&chType, &chName)
		if err != nil || chType != "dm" || !strings.Contains(chName, conn.UserID) {
			s.sendError(conn, "permission denied")
			return
		}
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

func (s *Server) handleWSChannelRead(conn *hub.Conn, h *hub.Hub, payload json.RawMessage) {
	var p hub.ChannelReadPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.ChannelID == "" {
		return
	}

	wdb, err := s.ws.Open(conn.WorkspaceSlug)
	if err != nil {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = wdb.DB.Exec(
		`INSERT INTO channel_reads (channel_id, user_id, last_read_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(channel_id, user_id) DO UPDATE SET last_read_at = ?`,
		p.ChannelID, conn.UserID, now, now,
	)

	// Broadcast to all user's connections for cross-tab sync
	h.SendToUser(conn.UserID, hub.MakeEnvelope(hub.TypeUnreadUpdate, hub.UnreadUpdatePayload{
		ChannelID: p.ChannelID,
		Unread:    0,
	}))
}

// autoSubscribeChannel ensures relevant connections are subscribed to a channel.
// For DM channels, only the two participants are subscribed.
// For all other channels, only channel_members are subscribed.
func (s *Server) autoSubscribeChannel(conn *hub.Conn, h *hub.Hub, channelID string) {
	wdb, err := s.ws.Open(conn.WorkspaceSlug)
	if err != nil {
		return
	}

	var chType, chName string
	err = wdb.DB.QueryRow("SELECT type, name FROM channels WHERE id = ?", channelID).Scan(&chType, &chName)
	if err != nil {
		return
	}

	if chType == "dm" {
		// Parse DM name: "dm-{id1}-{id2}" — only subscribe participants
		trimmed := strings.TrimPrefix(chName, "dm-")
		parts := strings.SplitN(trimmed, "-", 2)
		if len(parts) == 2 {
			h.SubscribeUsersByID(channelID, parts)
			return
		}
	}

	// For all non-DM channels, subscribe only channel_members
	rows, err := wdb.DB.Query("SELECT member_id FROM channel_members WHERE channel_id = ?", channelID)
	if err == nil {
		defer rows.Close()
		var memberIDs []string
		for rows.Next() {
			var mid string
			if rows.Scan(&mid) == nil {
				memberIDs = append(memberIDs, mid)
			}
		}
		if len(memberIDs) > 0 {
			h.SubscribeUsersByID(channelID, memberIDs)
		}
	}
}
