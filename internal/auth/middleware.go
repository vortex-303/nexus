package auth

import (
	"context"
	"encoding/json"
	"net/http"
)

type contextKey string

const claimsKey contextKey = "claims"

// Middleware validates JWT and injects claims into context.
func Middleware(jm *JWTManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := FromRequest(r)
			if tokenStr == "" {
				writeError(w, http.StatusUnauthorized, "missing token")
				return
			}
			claims, err := jm.Validate(tokenStr)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetClaims retrieves claims from request context.
func GetClaims(r *http.Request) *Claims {
	if c, ok := r.Context().Value(claimsKey).(*Claims); ok {
		return c
	}
	return nil
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
