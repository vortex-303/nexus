package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
)

type folderInfo struct {
	ID        string `json:"id"`
	ParentID  string `json:"parent_id"`
	Name      string `json:"name"`
	CreatedBy string `json:"created_by"`
	IsPrivate bool   `json:"is_private"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type fileInfoExtended struct {
	ID          string `json:"id"`
	ChannelID   string `json:"channel_id"`
	UploaderID  string `json:"uploader_id"`
	Name        string `json:"name"`
	Mime        string `json:"mime"`
	Size        int64  `json:"size"`
	Hash        string `json:"hash"`
	FolderID    string `json:"folder_id"`
	IsPrivate   bool   `json:"is_private"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	URL         string `json:"url"`
}

// handleCreateFolder creates a new folder.
func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var body struct {
		Name      string `json:"name"`
		ParentID  string `json:"parent_id"`
		IsPrivate bool   `json:"is_private"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	folder := folderInfo{
		ID:        id.New(),
		ParentID:  body.ParentID,
		Name:      body.Name,
		CreatedBy: claims.UserID,
		IsPrivate: body.IsPrivate,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	_, err = wdb.DB.Exec(
		"INSERT INTO folders (id, parent_id, name, created_by, is_private, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		folder.ID, nilIfEmpty(folder.ParentID), folder.Name, folder.CreatedBy, boolToInt(folder.IsPrivate), folder.CreatedAt, folder.UpdatedAt,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create folder")
		return
	}

	writeJSON(w, http.StatusCreated, folder)
}

// handleListFolders lists folders, optionally filtered by parent_id.
func (s *Server) handleListFolders(w http.ResponseWriter, r *http.Request) {
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

	parentID := r.URL.Query().Get("parent_id")

	var query string
	var args []any
	if parentID == "" {
		// Root folders
		query = "SELECT id, COALESCE(parent_id,''), name, created_by, is_private, created_at, updated_at FROM folders WHERE parent_id IS NULL AND (is_private = 0 OR created_by = ?) ORDER BY name"
		args = []any{claims.UserID}
	} else {
		query = "SELECT id, COALESCE(parent_id,''), name, created_by, is_private, created_at, updated_at FROM folders WHERE parent_id = ? AND (is_private = 0 OR created_by = ?) ORDER BY name"
		args = []any{parentID, claims.UserID}
	}

	rows, err := wdb.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	folders := []folderInfo{}
	for rows.Next() {
		var f folderInfo
		var priv int
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.CreatedBy, &priv, &f.CreatedAt, &f.UpdatedAt); err != nil {
			continue
		}
		f.IsPrivate = priv != 0
		folders = append(folders, f)
	}

	// Also list files in this folder
	var filesQuery string
	var filesArgs []any
	if parentID == "" {
		filesQuery = "SELECT id, COALESCE(channel_id,''), uploader_id, name, mime, size, hash, COALESCE(folder_id,''), is_private, COALESCE(description,''), created_at FROM files WHERE (folder_id IS NULL OR folder_id = '') AND (is_private = 0 OR uploader_id = ?) ORDER BY created_at DESC"
		filesArgs = []any{claims.UserID}
	} else {
		filesQuery = "SELECT id, COALESCE(channel_id,''), uploader_id, name, mime, size, hash, COALESCE(folder_id,''), is_private, COALESCE(description,''), created_at FROM files WHERE folder_id = ? AND (is_private = 0 OR uploader_id = ?) ORDER BY created_at DESC"
		filesArgs = []any{parentID, claims.UserID}
	}

	fileRows, err := wdb.DB.Query(filesQuery, filesArgs...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "file query failed")
		return
	}
	defer fileRows.Close()

	files := []fileInfoExtended{}
	for fileRows.Next() {
		var fi fileInfoExtended
		var priv int
		if err := fileRows.Scan(&fi.ID, &fi.ChannelID, &fi.UploaderID, &fi.Name, &fi.Mime, &fi.Size, &fi.Hash, &fi.FolderID, &priv, &fi.Description, &fi.CreatedAt); err != nil {
			continue
		}
		fi.IsPrivate = priv != 0
		fi.URL = fmt.Sprintf("/api/workspaces/%s/files/%s", slug, fi.Hash)
		files = append(files, fi)
	}

	// Also list documents in this folder
	var docsQuery string
	var docsArgs []any
	if parentID == "" {
		docsQuery = "SELECT id, title, content, created_by, COALESCE(updated_by,''), sharing, COALESCE(channel_id,''), COALESCE(folder_id,''), created_at, updated_at FROM documents WHERE (folder_id IS NULL OR folder_id = '') ORDER BY updated_at DESC"
	} else {
		docsQuery = "SELECT id, title, content, created_by, COALESCE(updated_by,''), sharing, COALESCE(channel_id,''), COALESCE(folder_id,''), created_at, updated_at FROM documents WHERE folder_id = ? ORDER BY updated_at DESC"
		docsArgs = []any{parentID}
	}

	docRows, err := wdb.DB.Query(docsQuery, docsArgs...)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"folders": folders, "files": files, "documents": []any{}})
		return
	}
	defer docRows.Close()

	type docInfo struct {
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

	documents := []docInfo{}
	for docRows.Next() {
		var d docInfo
		if err := docRows.Scan(&d.ID, &d.Title, &d.Content, &d.CreatedBy, &d.UpdatedBy, &d.Sharing, &d.ChannelID, &d.FolderID, &d.CreatedAt, &d.UpdatedAt); err != nil {
			continue
		}
		documents = append(documents, d)
	}

	writeJSON(w, http.StatusOK, map[string]any{"folders": folders, "files": files, "documents": documents})
}

// handleUpdateFolder renames, moves, or toggles privacy of a folder.
func (s *Server) handleUpdateFolder(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	folderID := r.PathValue("folderID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var body struct {
		Name      *string `json:"name"`
		ParentID  *string `json:"parent_id"`
		IsPrivate *bool   `json:"is_private"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if body.Name != nil {
		wdb.DB.Exec("UPDATE folders SET name = ?, updated_at = ? WHERE id = ?", *body.Name, now, folderID)
	}
	if body.ParentID != nil {
		wdb.DB.Exec("UPDATE folders SET parent_id = ?, updated_at = ? WHERE id = ?", nilIfEmpty(*body.ParentID), now, folderID)
	}
	if body.IsPrivate != nil {
		wdb.DB.Exec("UPDATE folders SET is_private = ?, updated_at = ? WHERE id = ?", boolToInt(*body.IsPrivate), now, folderID)
	}

	var f folderInfo
	var priv int
	err = wdb.DB.QueryRow("SELECT id, COALESCE(parent_id,''), name, created_by, is_private, created_at, updated_at FROM folders WHERE id = ?", folderID).
		Scan(&f.ID, &f.ParentID, &f.Name, &f.CreatedBy, &priv, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "folder not found")
		return
	}
	f.IsPrivate = priv != 0

	writeJSON(w, http.StatusOK, f)
}

// handleDeleteFolder deletes an empty folder.
func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	folderID := r.PathValue("folderID")
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

	// Check folder is empty
	var childCount int
	wdb.DB.QueryRow("SELECT COUNT(*) FROM folders WHERE parent_id = ?", folderID).Scan(&childCount)
	if childCount > 0 {
		writeError(w, http.StatusConflict, "folder contains subfolders")
		return
	}

	var fileCount int
	wdb.DB.QueryRow("SELECT COUNT(*) FROM files WHERE folder_id = ?", folderID).Scan(&fileCount)
	var docCount int
	wdb.DB.QueryRow("SELECT COUNT(*) FROM documents WHERE folder_id = ?", folderID).Scan(&docCount)
	if fileCount+docCount > 0 {
		writeError(w, http.StatusConflict, "folder contains files or documents")
		return
	}

	wdb.DB.Exec("DELETE FROM folders WHERE id = ?", folderID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleUploadToFolder uploads a file to a specific folder.
func (s *Server) handleUploadToFolder(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	folderID := r.PathValue("folderID")
	// _root means no folder (root level)
	if folderID == "_root" {
		folderID = ""
	}
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeError(w, http.StatusBadRequest, "file too large (max 50MB)")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read file")
		return
	}

	hashBytes := sha256.Sum256(data)
	hash := hex.EncodeToString(hashBytes[:])

	blobsDir := s.ws.BlobsDir(slug)
	prefix := hash[:2]
	prefixDir := filepath.Join(blobsDir, prefix)
	if err := os.MkdirAll(prefixDir, 0700); err != nil {
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}

	blobPath := filepath.Join(prefixDir, hash)
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		if err := os.WriteFile(blobPath, data, 0600); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store file")
			return
		}
	}

	mime := header.Header.Get("Content-Type")
	if mime == "" || mime == "application/octet-stream" {
		mime = http.DetectContentType(data[:min(512, len(data))])
	}

	fileID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = wdb.DB.Exec(
		"INSERT INTO files (id, channel_id, uploader_id, name, mime, size, hash, folder_id, created_at) VALUES (?, '', ?, ?, ?, ?, ?, ?, ?)",
		fileID, claims.UserID, header.Filename, mime, len(data), hash, nilIfEmpty(folderID), now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record file")
		return
	}

	fi := fileInfoExtended{
		ID:         fileID,
		UploaderID: claims.UserID,
		Name:       header.Filename,
		Mime:       mime,
		Size:       int64(len(data)),
		Hash:       hash,
		FolderID:   folderID,
		CreatedAt:  now,
		URL:        fmt.Sprintf("/api/workspaces/%s/files/%s", slug, hash),
	}

	writeJSON(w, http.StatusCreated, fi)
}

// handleUpdateFile renames a file or updates its description.
func (s *Server) handleUpdateFile(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileID := r.PathValue("fileID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		IsPrivate   *bool   `json:"is_private"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	if body.Name != nil {
		wdb.DB.Exec("UPDATE files SET name = ? WHERE id = ?", *body.Name, fileID)
	}
	if body.Description != nil {
		wdb.DB.Exec("UPDATE files SET description = ? WHERE id = ?", *body.Description, fileID)
	}
	if body.IsPrivate != nil {
		wdb.DB.Exec("UPDATE files SET is_private = ? WHERE id = ?", boolToInt(*body.IsPrivate), fileID)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleMoveFile moves a file between folders.
func (s *Server) handleMoveFile(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileID := r.PathValue("fileID")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var body struct {
		FolderID string `json:"folder_id"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	_, err = wdb.DB.Exec("UPDATE files SET folder_id = ? WHERE id = ?", nilIfEmpty(body.FolderID), fileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to move file")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "moved"})
}

// handleDeleteFile deletes a file record (keeps blob for dedup).
func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileID := r.PathValue("fileID")
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

	_, err = wdb.DB.Exec("DELETE FROM files WHERE id = ?", fileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete file")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// handleDuplicateFile creates a copy of a file record (same blob, new ID).
func (s *Server) handleDuplicateFile(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileID := r.PathValue("fileID")
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

	// Read the original file
	var orig fileInfoExtended
	var priv int
	err = wdb.DB.QueryRow(
		"SELECT id, COALESCE(channel_id,''), uploader_id, name, mime, size, hash, COALESCE(folder_id,''), is_private, COALESCE(description,''), created_at FROM files WHERE id = ?",
		fileID,
	).Scan(&orig.ID, &orig.ChannelID, &orig.UploaderID, &orig.Name, &orig.Mime, &orig.Size, &orig.Hash, &orig.FolderID, &priv, &orig.Description, &orig.CreatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}

	newID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)
	copyName := "Copy of " + orig.Name

	_, err = wdb.DB.Exec(
		"INSERT INTO files (id, channel_id, uploader_id, name, mime, size, hash, folder_id, is_private, description, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		newID, nilIfEmpty(orig.ChannelID), claims.UserID, copyName, orig.Mime, orig.Size, orig.Hash, nilIfEmpty(orig.FolderID), priv, orig.Description, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to duplicate file")
		return
	}

	fi := fileInfoExtended{
		ID:          newID,
		ChannelID:   orig.ChannelID,
		UploaderID:  claims.UserID,
		Name:        copyName,
		Mime:        orig.Mime,
		Size:        orig.Size,
		Hash:        orig.Hash,
		FolderID:    orig.FolderID,
		IsPrivate:   priv != 0,
		Description: orig.Description,
		CreatedAt:   now,
		URL:         fmt.Sprintf("/api/workspaces/%s/files/%s", slug, orig.Hash),
	}

	writeJSON(w, http.StatusCreated, fi)
}

// Ensure unused imports don't cause compile errors
var _ = json.Marshal
var _ = hub.MakeEnvelope
