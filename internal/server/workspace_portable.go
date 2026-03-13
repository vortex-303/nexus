package server

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// handleExportWorkspace packages the workspace as a downloadable .nexus (zip) file.
// GET /api/workspaces/{slug}/export
func (s *Server) handleExportWorkspace(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	log := logger.WithCategory(logger.CatSystem)

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// Get workspace name for the filename
	var wsName string
	err = s.global.DB.QueryRow("SELECT name FROM workspaces WHERE slug = ?", slug).Scan(&wsName)
	if err != nil {
		wsName = slug
	}

	// Checkpoint WAL to ensure all data is in the main DB file
	_, _ = wdb.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

	wsDir := filepath.Join(s.cfg.DataDir, "workspaces", slug)
	dbPath := filepath.Join(wsDir, "workspace.db")
	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	blobsDir := s.ws.BlobsDir(slug)

	// Sanitize name for filename
	safeName := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, wsName)
	if safeName == "" {
		safeName = slug
	}

	filename := fmt.Sprintf("%s_%s.nexus", safeName, time.Now().UTC().Format("2006-01-02"))

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	zw := zip.NewWriter(w)
	defer zw.Close()

	// Add workspace.db
	if err := addFileToZip(zw, dbPath, "workspace.db"); err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("export: failed to add workspace.db")
		return
	}

	// Add brain definition files
	if entries, err := os.ReadDir(brainDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				if entry.Name() == "skills" {
					skillsDir := filepath.Join(brainDir, "skills")
					if skillEntries, err := os.ReadDir(skillsDir); err == nil {
						for _, se := range skillEntries {
							if !se.IsDir() {
								addFileToZip(zw, filepath.Join(skillsDir, se.Name()), "brain/skills/"+se.Name())
							}
						}
					}
				}
				continue
			}
			addFileToZip(zw, filepath.Join(brainDir, entry.Name()), "brain/"+entry.Name())
		}
	}

	// Add blobs
	if _, err := os.Stat(blobsDir); err == nil {
		filepath.Walk(blobsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(blobsDir, path)
			return addFileToZip(zw, path, "blobs/"+rel)
		})
	}

	// Add metadata
	meta := fmt.Sprintf(`{"name":%q,"slug":%q,"exported_at":%q,"version":1}`,
		wsName, slug, time.Now().UTC().Format(time.RFC3339))
	mf, _ := zw.Create("nexus_meta.json")
	mf.Write([]byte(meta))

	log.Info().Str("workspace", slug).Str("filename", filename).Msg("workspace exported")
}

// handleImportWorkspace creates a new workspace from a .nexus file.
// POST /api/workspaces/import
func (s *Server) handleImportWorkspace(w http.ResponseWriter, r *http.Request) {
	log := logger.WithCategory(logger.CatSystem)

	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "auth required")
		return
	}

	// Limit to 2GB
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()

	if !strings.HasSuffix(header.Filename, ".nexus") {
		writeError(w, http.StatusBadRequest, "file must be a .nexus archive")
		return
	}

	// Save to temp file for zip reading (zip needs ReadSeeker)
	tmpFile, err := os.CreateTemp("", "nexus-import-*.zip")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "temp file error")
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	size, err := io.Copy(tmpFile, file)
	tmpFile.Close()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "upload error")
		return
	}

	zr, err := zip.OpenReader(tmpPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid archive")
		return
	}
	defer zr.Close()

	// Verify it contains workspace.db
	hasDB := false
	for _, f := range zr.File {
		if f.Name == "workspace.db" {
			hasDB = true
			break
		}
	}
	if !hasDB {
		writeError(w, http.StatusBadRequest, "archive missing workspace.db")
		return
	}

	// Create new workspace with a fresh slug
	newSlug := id.Slug()
	wsName := strings.TrimSuffix(header.Filename, ".nexus")
	// Clean up date suffix if present (e.g., "MyWorkspace_2026-03-13")
	if idx := strings.LastIndex(wsName, "_20"); idx > 0 && len(wsName)-idx <= 11 {
		wsName = wsName[:idx]
	}
	wsName = strings.ReplaceAll(wsName, "_", " ")
	if wsName == "" {
		wsName = "Imported Workspace"
	}
	wsName += " (imported)"

	// Register in global DB
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	_, err = s.global.DB.Exec(`
		INSERT INTO workspaces (slug, name, created_by, created_at) VALUES (?, ?, ?, ?)
	`, newSlug, wsName, claims.AccountID, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace creation error")
		return
	}

	// Create workspace directories
	wsDir := filepath.Join(s.cfg.DataDir, "workspaces", newSlug)
	brainDir := filepath.Join(wsDir, "brain")
	blobsDir := filepath.Join(wsDir, "blobs")
	os.MkdirAll(brainDir, 0700)
	os.MkdirAll(blobsDir, 0700)

	// Extract files
	for _, f := range zr.File {
		// Security: skip paths with .. or absolute paths
		if strings.Contains(f.Name, "..") || filepath.IsAbs(f.Name) {
			continue
		}

		destPath := filepath.Join(wsDir, f.Name)

		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, 0700)
			continue
		}

		// Ensure parent directory exists
		os.MkdirAll(filepath.Dir(destPath), 0700)

		if err := extractZipFile(f, destPath); err != nil {
			log.Error().Err(err).Str("file", f.Name).Msg("import: extract error")
			continue
		}
	}

	// Open the imported workspace DB to verify and run migrations
	_, err = s.ws.Open(newSlug)
	if err != nil {
		// Clean up on failure
		s.global.DB.Exec("DELETE FROM workspaces WHERE slug = ?", newSlug)
		os.RemoveAll(wsDir)
		writeError(w, http.StatusInternalServerError, "imported database invalid")
		return
	}

	log.Info().Str("slug", newSlug).Str("name", wsName).Int64("size", size).Msg("workspace imported")

	writeJSON(w, http.StatusCreated, map[string]string{
		"slug": newSlug,
		"name": wsName,
	})
}

// handleDestroyWorkspace permanently deletes a workspace and all its data.
// DELETE /api/workspaces/{slug}/destroy
func (s *Server) handleDestroyWorkspace(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	log := logger.WithCategory(logger.CatSystem)

	// Require confirmation: workspace name must match
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	var wsName string
	err := s.global.DB.QueryRow("SELECT name FROM workspaces WHERE slug = ?", slug).Scan(&wsName)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	if req.Confirm != wsName {
		writeError(w, http.StatusBadRequest, "workspace name does not match")
		return
	}

	// Close the workspace DB connection
	s.ws.Close(slug)

	// Remove the workspace directory (DB + brain + blobs)
	wsDir := filepath.Join(s.cfg.DataDir, "workspaces", slug)
	if err := os.RemoveAll(wsDir); err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("destroy: failed to remove directory")
		writeError(w, http.StatusInternalServerError, "failed to remove workspace data")
		return
	}

	// Remove from global DB
	_, _ = s.global.DB.Exec("DELETE FROM workspaces WHERE slug = ?", slug)

	log.Info().Str("workspace", slug).Str("name", wsName).Msg("workspace destroyed")

	writeJSON(w, http.StatusOK, map[string]string{"status": "destroyed"})
}

// addFileToZip adds a file from disk to the zip archive.
func addFileToZip(zw *zip.Writer, srcPath, zipPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = zipPath
	header.Method = zip.Deflate

	dst, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(dst, src)
	return err
}

// extractZipFile extracts a single file from the zip to disk.
func extractZipFile(f *zip.File, destPath string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}
