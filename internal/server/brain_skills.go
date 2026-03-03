package server

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
)

// handleListSkills returns loaded skills for a workspace.
func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	// Auto-create default skills if directory doesn't exist yet
	brain.EnsureDefaultSkills(brainDir)
	skills := brain.LoadSkills(brainDir)
	if skills == nil {
		skills = []brain.Skill{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"skills": skills})
}

// handleGetSkill returns a single skill file content.
func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileName := r.PathValue("file")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	path := filepath.Join(brainDir, "skills", fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"file": fileName, "content": string(data)})
}

// handleUpdateSkill updates a skill file (admin only).
func (s *Server) handleUpdateSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileName := r.PathValue("file")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	skillsDir := filepath.Join(brainDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create skills dir")
		return
	}

	path := filepath.Join(skillsDir, fileName)
	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save skill")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleDeleteSkill deletes a skill file (admin only).
func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	fileName := r.PathValue("file")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	path := filepath.Join(brainDir, "skills", fileName)
	if err := os.Remove(path); err != nil {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
