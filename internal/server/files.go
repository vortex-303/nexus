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
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/hub"
	"github.com/nexus-chat/nexus/internal/id"
)

const maxUploadSize = 50 * 1024 * 1024 // 50MB

type fileInfo struct {
	ID         string `json:"id"`
	ChannelID  string `json:"channel_id"`
	UploaderID string `json:"uploader_id"`
	Name       string `json:"name"`
	Mime       string `json:"mime"`
	Size       int64  `json:"size"`
	Hash       string `json:"hash"`
	CreatedAt  string `json:"created_at"`
	URL        string `json:"url"`
}

// handleUploadFile handles multipart file upload to a channel.
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	channelID := r.PathValue("channelID")
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

	// Read file into memory and compute hash
	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read file")
		return
	}

	hashBytes := sha256.Sum256(data)
	hash := hex.EncodeToString(hashBytes[:])

	// Content-addressed storage: use first 2 chars as prefix dir
	blobsDir := s.ws.BlobsDir(slug)
	prefix := hash[:2]
	prefixDir := filepath.Join(blobsDir, prefix)
	if err := os.MkdirAll(prefixDir, 0700); err != nil {
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}

	blobPath := filepath.Join(prefixDir, hash)
	// Only write if not already stored (dedup)
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		if err := os.WriteFile(blobPath, data, 0600); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store file")
			return
		}
	}

	// Detect MIME type
	mime := header.Header.Get("Content-Type")
	if mime == "" || mime == "application/octet-stream" {
		mime = http.DetectContentType(data[:min(512, len(data))])
	}

	// Record in DB
	fileID := id.New()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = wdb.DB.Exec(
		"INSERT INTO files (id, channel_id, uploader_id, name, mime, size, hash, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		fileID, channelID, claims.UserID, header.Filename, mime, len(data), hash, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record file")
		return
	}

	fi := fileInfo{
		ID:         fileID,
		ChannelID:  channelID,
		UploaderID: claims.UserID,
		Name:       header.Filename,
		Mime:       mime,
		Size:       int64(len(data)),
		Hash:       hash,
		CreatedAt:  now,
		URL:        fmt.Sprintf("/api/workspaces/%s/files/%s", slug, hash),
	}

	// Broadcast file event to channel subscribers
	h := s.hubs.Get(slug)
	h.Broadcast(channelID, hub.MakeEnvelope("file.new", fi), "")

	writeJSON(w, http.StatusCreated, fi)
}

// handleDownloadFile serves a file by its hash.
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	hash := r.PathValue("hash")

	// Validate hash format
	if len(hash) != 64 {
		writeError(w, http.StatusBadRequest, "invalid hash")
		return
	}

	blobsDir := s.ws.BlobsDir(slug)
	blobPath := filepath.Join(blobsDir, hash[:2], hash)

	f, err := os.Open(blobPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	defer f.Close()

	// Look up metadata
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	var name, mime string
	err = wdb.DB.QueryRow("SELECT name, mime FROM files WHERE hash = ? LIMIT 1", hash).Scan(&name, &mime)
	if err != nil {
		// Fallback if no DB record
		name = hash
		mime = "application/octet-stream"
	}

	// Determine if inline or attachment
	inline := strings.HasPrefix(mime, "image/") || mime == "application/pdf"
	if inline {
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", name))
	} else {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

	io.Copy(w, f)
}

// handleListFiles lists files in a channel or all files in a workspace.
func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
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

	channelID := r.URL.Query().Get("channel_id")

	var query string
	var args []any
	if channelID != "" {
		query = "SELECT id, channel_id, uploader_id, name, mime, size, hash, created_at FROM files WHERE channel_id = ? ORDER BY created_at DESC"
		args = []any{channelID}
	} else {
		query = "SELECT id, channel_id, uploader_id, name, mime, size, hash, created_at FROM files ORDER BY created_at DESC"
	}

	rows, err := wdb.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	files := []fileInfo{}
	for rows.Next() {
		var fi fileInfo
		if err := rows.Scan(&fi.ID, &fi.ChannelID, &fi.UploaderID, &fi.Name, &fi.Mime, &fi.Size, &fi.Hash, &fi.CreatedAt); err != nil {
			continue
		}
		fi.URL = fmt.Sprintf("/api/workspaces/%s/files/%s", slug, fi.Hash)
		files = append(files, fi)
	}

	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// marshalFileInfo converts fileInfo to JSON for WebSocket broadcast.
func marshalFileInfo(fi fileInfo) json.RawMessage {
	data, _ := json.Marshal(fi)
	return data
}
