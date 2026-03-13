package server

import (
	"net/http"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/search"
)

type docResp struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by,omitempty"`
	Sharing   string `json:"sharing"`
	ChannelID string `json:"channel_id,omitempty"`
	FolderID  string `json:"folder_id"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type createDocReq struct {
	Title    string `json:"title"`
	Content  string `json:"content"`
	FolderID string `json:"folder_id"`
}

type updateDocReq struct {
	Title    *string `json:"title"`
	Content  *string `json:"content"`
	FolderID *string `json:"folder_id"`
}

func (s *Server) handleCreateDoc(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req createDocReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		req.Title = "Untitled"
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	docID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = wdb.DB.Exec(
		"INSERT INTO documents (id, title, content, created_by, updated_by, folder_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		docID, req.Title, req.Content, claims.UserID, claims.UserID, req.FolderID, now, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create document")
		return
	}

	s.search.Index(slug, search.SearchDoc{
		ID: docID, Type: "document", Title: req.Title, Content: req.Content, CreatedAt: now,
	})

	doc := docResp{
		ID:        docID,
		Title:     req.Title,
		Content:   req.Content,
		CreatedBy: claims.UserID,
		UpdatedBy: claims.UserID,
		Sharing:   "workspace",
		FolderID:  req.FolderID,
		CreatedAt: now,
		UpdatedAt: now,
	}

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope("doc.created", doc), "")

	s.onPulse(slug, Pulse{
		Type: "document.created", ActorID: claims.UserID, ActorName: claims.DisplayName,
		EntityID: doc.ID, Summary: pulseSummary(claims.DisplayName, "created doc", doc.Title),
	})

	writeJSON(w, http.StatusCreated, doc)
}

func (s *Server) handleListDocs(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	folderID := r.URL.Query().Get("folder_id")
	var query string
	var args []any
	if folderID != "" {
		query = "SELECT id, title, content, created_by, COALESCE(updated_by,''), sharing, COALESCE(channel_id,''), COALESCE(folder_id,''), created_at, updated_at FROM documents WHERE folder_id = ? ORDER BY updated_at DESC"
		args = []any{folderID}
	} else {
		query = "SELECT id, title, content, created_by, COALESCE(updated_by,''), sharing, COALESCE(channel_id,''), COALESCE(folder_id,''), created_at, updated_at FROM documents ORDER BY updated_at DESC"
	}
	rows, err := wdb.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	docs := []docResp{}
	for rows.Next() {
		var d docResp
		if err := rows.Scan(&d.ID, &d.Title, &d.Content, &d.CreatedBy, &d.UpdatedBy, &d.Sharing, &d.ChannelID, &d.FolderID, &d.CreatedAt, &d.UpdatedAt); err != nil {
			continue
		}
		docs = append(docs, d)
	}

	writeJSON(w, http.StatusOK, map[string]any{"documents": docs})
}

func (s *Server) handleGetDoc(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	docID := r.PathValue("docID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	var d docResp
	err = wdb.DB.QueryRow(
		"SELECT id, title, content, created_by, COALESCE(updated_by,''), sharing, COALESCE(channel_id,''), COALESCE(folder_id,''), created_at, updated_at FROM documents WHERE id = ?",
		docID,
	).Scan(&d.ID, &d.Title, &d.Content, &d.CreatedBy, &d.UpdatedBy, &d.Sharing, &d.ChannelID, &d.FolderID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleUpdateDoc(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	docID := r.PathValue("docID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req updateDocReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	var sets []string
	var args []any

	if req.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *req.Title)
	}
	if req.Content != nil {
		sets = append(sets, "content = ?")
		args = append(args, *req.Content)
	}
	if req.FolderID != nil {
		sets = append(sets, "folder_id = ?")
		args = append(args, *req.FolderID)
	}

	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	sets = append(sets, "updated_by = ?", "updated_at = ?")
	args = append(args, claims.UserID, now)
	args = append(args, docID)

	query := "UPDATE documents SET " + joinStrings(sets, ", ") + " WHERE id = ?"
	res, err := wdb.DB.Exec(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update document")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	// Read back
	var d docResp
	_ = wdb.DB.QueryRow(
		"SELECT id, title, content, created_by, COALESCE(updated_by,''), sharing, COALESCE(channel_id,''), COALESCE(folder_id,''), created_at, updated_at FROM documents WHERE id = ?",
		docID,
	).Scan(&d.ID, &d.Title, &d.Content, &d.CreatedBy, &d.UpdatedBy, &d.Sharing, &d.ChannelID, &d.FolderID, &d.CreatedAt, &d.UpdatedAt)

	s.search.Index(slug, search.SearchDoc{
		ID: d.ID, Type: "document", Title: d.Title, Content: d.Content,
	})

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope("doc.updated", d), "")

	s.onPulse(slug, Pulse{
		Type: "document.updated", ActorID: claims.UserID, ActorName: claims.DisplayName,
		EntityID: d.ID, Summary: pulseSummary(claims.DisplayName, "updated doc", d.Title),
	})

	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeleteDoc(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	docID := r.PathValue("docID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	res, err := wdb.DB.Exec("DELETE FROM documents WHERE id = ?", docID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete document")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	s.search.Delete(slug, docID)

	h := s.hubs.Get(slug)
	h.BroadcastAll(hub.MakeEnvelope("doc.deleted", map[string]string{"id": docID}), "")

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func joinStrings(s []string, sep string) string {
	result := ""
	for i, v := range s {
		if i > 0 {
			result += sep
		}
		result += v
	}
	return result
}
