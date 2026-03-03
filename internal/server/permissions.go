package server

import (
	"net/http"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/roles"
)

// requirePerm wraps a handler with a permission check.
// The user must be authenticated and have the specified permission in their workspace.
func (s *Server) requirePerm(perm roles.Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.GetClaims(r)
		if claims == nil {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}

		slug := r.PathValue("slug")
		if slug == "" {
			slug = claims.WorkspaceSlug
		}
		if claims.WorkspaceSlug != slug {
			writeError(w, http.StatusForbidden, "not a member of this workspace")
			return
		}

		wdb, err := s.ws.Open(slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "workspace error")
			return
		}

		checker := roles.NewChecker(wdb.DB)
		if !checker.Has(claims.UserID, perm) {
			writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}

		next(w, r)
	}
}

// requireAdmin is a shortcut — checks that the user is an admin.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.GetClaims(r)
		if claims == nil || claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin only")
			return
		}
		next(w, r)
	}
}

// requireSuperadmin checks that the user has the platform superadmin flag.
func (s *Server) requireSuperadmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.GetClaims(r)
		if claims == nil || !claims.SuperAdmin {
			writeError(w, http.StatusForbidden, "superadmin only")
			return
		}
		next(w, r)
	}
}
