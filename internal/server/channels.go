package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/search"
)

type createChannelReq struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`           // public, private, dm, group
	Classification string   `json:"classification"` // public, internal, confidential, restricted
	Members        []string `json:"members"`        // member IDs for group channels
}

type channelResp struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Classification string `json:"classification"`
	CreatedBy      string `json:"created_by"`
	CreatedAt      string `json:"created_at"`
	Archived       bool   `json:"archived"`
	Unread         int    `json:"unread"`
	IsFavorite     bool   `json:"is_favorite"`
	FavoritedAt    string `json:"favorited_at,omitempty"`
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}

	var req createChannelReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if req.Type == "" {
		req.Type = "public"
	}
	if req.Classification == "" {
		req.Classification = "public"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Dedup: if a DM channel with this name already exists, return it
	if req.Type == "dm" {
		var existingID string
		err := wdb.DB.QueryRow("SELECT id FROM channels WHERE name = ? AND type = 'dm'", req.Name).Scan(&existingID)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]string{"id": existingID, "name": req.Name, "type": "dm"})
			return
		}
	}

	// For group channels, auto-generate name from member display names if not provided
	if req.Type == "group" && len(req.Members) > 0 {
		// Ensure creator is included
		hasCreator := false
		for _, m := range req.Members {
			if m == claims.UserID {
				hasCreator = true
				break
			}
		}
		if !hasCreator {
			req.Members = append(req.Members, claims.UserID)
		}

		if req.Name == "" {
			var names []string
			for _, mid := range req.Members {
				var dn string
				if wdb.DB.QueryRow("SELECT display_name FROM members WHERE id = ?", mid).Scan(&dn) == nil {
					names = append(names, dn)
				}
			}
			if len(names) > 0 {
				req.Name = strings.Join(names, ", ")
			} else {
				req.Name = "Group"
			}
		}
	}

	chID := id.New()
	_, err = wdb.DB.Exec(
		"INSERT INTO channels (id, name, type, classification, created_by) VALUES (?, ?, ?, ?, ?)",
		chID, req.Name, req.Type, req.Classification, claims.UserID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create channel")
		return
	}

	// For group channels, insert channel_members and subscribe only those users
	if req.Type == "group" && len(req.Members) > 0 {
		for _, mid := range req.Members {
			_, _ = wdb.DB.Exec("INSERT OR IGNORE INTO channel_members (channel_id, member_id) VALUES (?, ?)", chID, mid)
		}
		h := s.hubs.Get(slug)
		if h != nil {
			h.SubscribeUsersByID(chID, req.Members)
		}
	} else {
		// Subscribe all current WebSocket connections to the new channel
		h := s.hubs.Get(slug)
		if h != nil {
			h.SubscribeAll(chID)
		}
	}

	// Index channel for search
	s.search.Index(slug, search.SearchDoc{
		ID: chID, Type: "channel", Title: req.Name,
	})

	if req.Type != "dm" {
		s.onPulse(slug, Pulse{
			Type: "channel.created", ActorID: claims.UserID, ActorName: claims.DisplayName,
			EntityID: chID, Summary: pulseSummary(claims.DisplayName, "created #"+req.Name, ""),
		})
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"id":   chID,
		"name": req.Name,
		"type": req.Type,
	})
}

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	rows, err := wdb.DB.Query(
		`SELECT c.id, c.name, c.type, c.classification, COALESCE(c.created_by, ''), c.created_at, c.archived,
			COALESCE((SELECT COUNT(*) FROM messages m
				WHERE m.channel_id = c.id AND m.deleted = FALSE
				AND m.created_at > COALESCE((SELECT last_read_at FROM channel_reads WHERE channel_id = c.id AND user_id = ?), '')
				AND m.sender_id != ?
			), 0) AS unread,
			COALESCE((SELECT cr.is_favorite FROM channel_reads cr WHERE cr.channel_id = c.id AND cr.user_id = ?), FALSE) AS is_favorite,
			COALESCE((SELECT cr.favorited_at FROM channel_reads cr WHERE cr.channel_id = c.id AND cr.user_id = ?), '') AS favorited_at
		FROM channels c WHERE c.archived = FALSE
			AND (c.type NOT IN ('dm', 'group') OR c.name LIKE '%' || ? || '%'
				OR (c.type = 'group' AND c.id IN (SELECT channel_id FROM channel_members WHERE member_id = ?)))
		ORDER BY c.created_at`,
		claims.UserID, claims.UserID, claims.UserID, claims.UserID, claims.UserID, claims.UserID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query error")
		return
	}
	defer rows.Close()

	channels := make([]channelResp, 0)
	for rows.Next() {
		var ch channelResp
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Type, &ch.Classification, &ch.CreatedBy, &ch.CreatedAt, &ch.Archived, &ch.Unread, &ch.IsFavorite, &ch.FavoritedAt); err != nil {
			continue
		}
		channels = append(channels, ch)
	}

	writeJSON(w, http.StatusOK, channels)
}

type messageHistoryResp struct {
	Messages []messageResp `json:"messages"`
	HasMore  bool          `json:"has_more"`
}

type messageResp struct {
	ID            string         `json:"id"`
	ChannelID     string         `json:"channel_id"`
	SenderID      string         `json:"sender_id"`
	SenderName    string         `json:"sender_name"`
	Content       string         `json:"content"`
	EditedAt      *string        `json:"edited_at,omitempty"`
	CreatedAt     string         `json:"created_at"`
	Reactions     []reactionResp `json:"reactions,omitempty"`
	ToolsUsed     []string       `json:"tools_used,omitempty"`
	ParentID      *string        `json:"parent_id,omitempty"`
	ReplyCount    int            `json:"reply_count,omitempty"`
	LatestReplyAt *string        `json:"latest_reply_at,omitempty"`
}

type reactionResp struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Users []string `json:"users"`
}

func (s *Server) handleMessageHistory(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	channelID := r.PathValue("channelID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// DM privacy: only participants can read DM message history
	var chType, chName string
	if err := wdb.DB.QueryRow("SELECT type, name FROM channels WHERE id = ?", channelID).Scan(&chType, &chName); err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if chType == "dm" && !strings.Contains(chName, claims.UserID) {
		writeError(w, http.StatusForbidden, "not a participant")
		return
	}
	if chType == "group" {
		var isMember int
		_ = wdb.DB.QueryRow("SELECT COUNT(*) FROM channel_members WHERE channel_id = ? AND member_id = ?", channelID, claims.UserID).Scan(&isMember)
		if isMember == 0 {
			writeError(w, http.StatusForbidden, "not a member of this group")
			return
		}
	}

	// Cursor-based pagination: before=<message_id>, limit=N
	before := r.URL.Query().Get("before")
	limit := 50

	var rows_ interface{ Close() error }
	var query string
	var args []any

	if before != "" {
		query = `SELECT m.id, m.channel_id, m.sender_id, COALESCE(mb.display_name, 'Unknown'), m.content, m.edited_at, m.created_at, m.metadata,
				(SELECT COUNT(*) FROM messages r WHERE r.parent_id = m.id AND r.deleted = FALSE) AS reply_count,
				(SELECT MAX(r.created_at) FROM messages r WHERE r.parent_id = m.id AND r.deleted = FALSE) AS latest_reply_at
			FROM messages m
			LEFT JOIN members mb ON m.sender_id = mb.id
			WHERE m.channel_id = ? AND m.deleted = FALSE AND m.parent_id IS NULL AND m.created_at < (SELECT created_at FROM messages WHERE id = ?)
			ORDER BY m.created_at DESC LIMIT ?`
		args = []any{channelID, before, limit + 1}
	} else {
		query = `SELECT m.id, m.channel_id, m.sender_id, COALESCE(mb.display_name, 'Unknown'), m.content, m.edited_at, m.created_at, m.metadata,
				(SELECT COUNT(*) FROM messages r WHERE r.parent_id = m.id AND r.deleted = FALSE) AS reply_count,
				(SELECT MAX(r.created_at) FROM messages r WHERE r.parent_id = m.id AND r.deleted = FALSE) AS latest_reply_at
			FROM messages m
			LEFT JOIN members mb ON m.sender_id = mb.id
			WHERE m.channel_id = ? AND m.deleted = FALSE AND m.parent_id IS NULL
			ORDER BY m.created_at DESC LIMIT ?`
		args = []any{channelID, limit + 1}
	}

	dbRows, err := wdb.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query error")
		return
	}
	rows_ = dbRows
	defer rows_.Close()

	messages := make([]messageResp, 0)
	for dbRows.Next() {
		var m messageResp
		var metadata string
		if err := dbRows.Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.SenderName, &m.Content, &m.EditedAt, &m.CreatedAt, &metadata, &m.ReplyCount, &m.LatestReplyAt); err != nil {
			continue
		}
		if metadata != "" && metadata != "{}" {
			var meta struct {
				ToolsUsed []string `json:"tools_used"`
			}
			if json.Unmarshal([]byte(metadata), &meta) == nil {
				m.ToolsUsed = meta.ToolsUsed
			}
		}
		messages = append(messages, m)
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}

	// Reverse so oldest first
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	// Load reactions
	for i := range messages {
		messages[i].Reactions = s.loadReactions(wdb, messages[i].ID)
	}

	writeJSON(w, http.StatusOK, messageHistoryResp{
		Messages: messages,
		HasMore:  hasMore,
	})
}

func (s *Server) loadReactions(wdb *db.WorkspaceDB, msgID string) []reactionResp {
	rows, err := wdb.DB.Query(
		`SELECT emoji, COUNT(*) as cnt, GROUP_CONCAT(user_id) as users
		 FROM reactions WHERE message_id = ? GROUP BY emoji`,
		msgID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var reactions []reactionResp
	for rows.Next() {
		var r reactionResp
		var users string
		if err := rows.Scan(&r.Emoji, &r.Count, &users); err != nil {
			continue
		}
		r.Users = strings.Split(users, ",")
		reactions = append(reactions, r)
	}
	return reactions
}

func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	channelID := r.PathValue("channelID")
	messageID := r.PathValue("messageID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Get root message + all replies
	dbRows, err := wdb.DB.Query(
		`SELECT m.id, m.channel_id, m.sender_id, COALESCE(mb.display_name, 'Unknown'), m.content, m.edited_at, m.created_at, m.metadata, m.parent_id
		FROM messages m
		LEFT JOIN members mb ON m.sender_id = mb.id
		WHERE m.channel_id = ? AND m.deleted = FALSE AND (m.id = ? OR m.parent_id = ?)
		ORDER BY m.created_at ASC`,
		channelID, messageID, messageID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query error")
		return
	}
	defer dbRows.Close()

	msgs := make([]messageResp, 0)
	for dbRows.Next() {
		var m messageResp
		var metadata string
		if err := dbRows.Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.SenderName, &m.Content, &m.EditedAt, &m.CreatedAt, &metadata, &m.ParentID); err != nil {
			continue
		}
		if metadata != "" && metadata != "{}" {
			var meta struct {
				ToolsUsed []string `json:"tools_used"`
			}
			if json.Unmarshal([]byte(metadata), &meta) == nil {
				m.ToolsUsed = meta.ToolsUsed
			}
		}
		m.Reactions = s.loadReactions(wdb, m.ID)
		msgs = append(msgs, m)
	}

	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

func (s *Server) handleToggleFavorite(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	channelID := r.PathValue("channelID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Upsert channel_reads row and toggle is_favorite
	now := strings.Replace(time.Now().UTC().Format(time.RFC3339), "+00:00", "Z", 1)
	_, err = wdb.DB.Exec(
		`INSERT INTO channel_reads (channel_id, user_id, last_read_at, is_favorite, favorited_at)
		 VALUES (?, ?, ?, TRUE, ?)
		 ON CONFLICT(channel_id, user_id) DO UPDATE SET
		   is_favorite = NOT is_favorite,
		   favorited_at = CASE WHEN NOT is_favorite THEN ? ELSE '' END`,
		channelID, claims.UserID, now, now, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to toggle favorite")
		return
	}

	// Return new state
	var isFav bool
	var favAt string
	_ = wdb.DB.QueryRow("SELECT is_favorite, favorited_at FROM channel_reads WHERE channel_id = ? AND user_id = ?", channelID, claims.UserID).Scan(&isFav, &favAt)
	writeJSON(w, http.StatusOK, map[string]any{"is_favorite": isFav, "favorited_at": favAt})
}

// handleDeleteChannel archives a channel (soft-delete).
// Admin can delete any channel. Regular users can only delete their own DM channels.
func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	channelID := r.PathValue("channelID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Check channel type and ownership
	var chType, chName string
	err = wdb.DB.QueryRow("SELECT type, name FROM channels WHERE id = ? AND archived = FALSE", channelID).Scan(&chType, &chName)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	// Non-admins can only delete DM channels they belong to
	if claims.Role != "admin" {
		if chType != "dm" {
			writeError(w, http.StatusForbidden, "only admins can delete channels")
			return
		}
		// Verify user is part of this DM
		if !strings.Contains(chName, claims.UserID) {
			writeError(w, http.StatusForbidden, "not your conversation")
			return
		}
	}

	// Archive the channel
	_, err = wdb.DB.Exec("UPDATE channels SET archived = TRUE WHERE id = ?", channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete channel")
		return
	}

	// Broadcast to all connected clients
	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope(hub.TypeChannelArchived, map[string]string{
		"channel_id": channelID,
	}), "")

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleOnlineMembers(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}
	h := s.hubs.Get(slug)
	writeJSON(w, http.StatusOK, h.OnlineMembers())
}

// handleKickChannelMember removes a member from a group channel.
func (s *Server) handleKickChannelMember(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	channelID := r.PathValue("channelID")
	memberID := r.PathValue("memberID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.WorkspaceSlug != slug {
		writeError(w, http.StatusForbidden, "not a member")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Verify channel is group type
	var chType string
	if err := wdb.DB.QueryRow("SELECT type FROM channels WHERE id = ? AND archived = FALSE", channelID).Scan(&chType); err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if chType != "group" {
		writeError(w, http.StatusBadRequest, "can only kick from group channels")
		return
	}

	// Can't kick yourself
	if memberID == claims.UserID {
		writeError(w, http.StatusBadRequest, "cannot kick yourself")
		return
	}

	// Remove from channel_members
	res, err := wdb.DB.Exec("DELETE FROM channel_members WHERE channel_id = ? AND member_id = ?", channelID, memberID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove member")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "member not in channel")
		return
	}

	// Unsubscribe from hub
	h := s.hubs.Get(slug)
	h.UnsubscribeUsersByID(channelID, []string{memberID})

	// Broadcast removal event
	h.Broadcast(channelID, hub.MakeEnvelope(hub.TypeChannelMemberRemoved, hub.ChannelMemberRemovedPayload{
		ChannelID: channelID,
		MemberID:  memberID,
	}), "")

	// Also notify the kicked user directly
	h.SendToUser(memberID, hub.MakeEnvelope(hub.TypeChannelMemberRemoved, hub.ChannelMemberRemovedPayload{
		ChannelID: channelID,
		MemberID:  memberID,
	}))

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
