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

	// Resolve sender display names for messages
	wdb, _ := s.ws.Open(slug)
	if wdb != nil {
		for i, r := range results {
			if r.Type == "message" && r.Sender != "" {
				var name string
				_ = wdb.DB.QueryRow("SELECT display_name FROM members WHERE id = ?", r.Sender).Scan(&name)
				if name != "" {
					results[i].Sender = name
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
