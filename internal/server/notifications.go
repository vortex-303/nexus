package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/db"
)

type notificationResp struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Link      string `json:"link"`
	SourceID  string `json:"source_id"`
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
	Read      bool   `json:"read"`
	CreatedAt string `json:"created_at"`
}

// createNotification inserts a notification and broadcasts it via WebSocket.
func (s *Server) createNotification(wdb *db.WorkspaceDB, slug, recipientID, nType, title, body, link, actorID, actorName, sourceID string) {
	nID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := wdb.DB.Exec(
		`INSERT INTO notifications (id, recipient_id, type, title, body, link, source_id, actor_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nID, recipientID, nType, title, body, link, sourceID, actorID, now,
	)
	if err != nil {
		return
	}

	h := s.hubs.Get(slug)
	if h != nil {
		h.SendToUser(recipientID, hub.MakeEnvelope(hub.TypeNotification, hub.NotificationPayload{
			ID:        nID,
			Type:      nType,
			Title:     title,
			Body:      body,
			Link:      link,
			ActorID:   actorID,
			ActorName: actorName,
			SourceID:  sourceID,
			CreatedAt: now,
		}))
	}
}

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	query := `SELECT n.id, n.type, n.title, n.body, n.link, n.source_id, n.actor_id, COALESCE(m.display_name, ''), n.read, n.created_at
		FROM notifications n LEFT JOIN members m ON n.actor_id = m.id
		WHERE n.recipient_id = ?`
	args := []any{claims.UserID}

	if r.URL.Query().Get("unread_only") == "true" {
		query += " AND n.read = FALSE"
	}
	if nType := r.URL.Query().Get("type"); nType != "" {
		query += " AND n.type = ?"
		args = append(args, nType)
	}

	query += " ORDER BY n.created_at DESC LIMIT 100"

	rows, err := wdb.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	var notifs []notificationResp
	for rows.Next() {
		var n notificationResp
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &n.Body, &n.Link, &n.SourceID, &n.ActorID, &n.ActorName, &n.Read, &n.CreatedAt); err != nil {
			continue
		}
		notifs = append(notifs, n)
	}
	if notifs == nil {
		notifs = []notificationResp{}
	}
	writeJSON(w, http.StatusOK, notifs)
}

func (s *Server) handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	notifID := r.PathValue("id")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	_, err = wdb.DB.Exec("UPDATE notifications SET read = TRUE WHERE id = ? AND recipient_id = ?", notifID, claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	_, err = wdb.DB.Exec("UPDATE notifications SET read = TRUE WHERE recipient_id = ? AND read = FALSE", claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleNotificationCount(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	var count int
	err = wdb.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE recipient_id = ? AND read = FALSE", claims.UserID).Scan(&count)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

// truncateBody returns at most maxLen characters of s.
func truncateBody(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "…"
	}
	return s
}
