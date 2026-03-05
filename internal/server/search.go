package server

import (
	"net/http"
	"strings"

	"github.com/nexus-chat/nexus/internal/auth"
)

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "query required")
		return
	}

	typesStr := r.URL.Query().Get("type")
	var types []string
	if typesStr != "" {
		types = strings.Split(typesStr, ",")
	}

	limit := 20
	results, err := s.search.Search(slug, q, types, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
