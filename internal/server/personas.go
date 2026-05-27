package server

import (
	"net/http"

	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/logger"
)

// handleListPersonas returns every persona in the workspace.
// Phase 1: read-only. CRUD endpoints land in Phase 2 with the Creator UI.
//
// Also triggers ensureBrainMember to backfill seeded built-ins on
// workspaces that existed before migration v62 ran. Cheap and idempotent.
func (s *Server) handleListPersonas(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	_ = s.ensureBrainMember(slug)
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	personas, err := brain.LoadPersonas(wdb.DB)
	if err != nil {
		logger.WithCategory(logger.CatBrain).Error().Str("workspace", slug).Err(err).Msg("LoadPersonas failed")
		writeError(w, http.StatusInternalServerError, "failed to load personas")
		return
	}
	if personas == nil {
		personas = []brain.Persona{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"personas": personas})
}

// handleGetPersona returns a single persona by slug.
func (s *Server) handleGetPersona(w http.ResponseWriter, r *http.Request) {
	wsSlug := r.PathValue("slug")
	personaSlug := r.PathValue("personaSlug")
	wdb, err := s.ws.Open(wsSlug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	p, err := brain.LoadPersonaBySlug(wdb.DB, personaSlug)
	if err != nil {
		writeError(w, http.StatusNotFound, "persona not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}
