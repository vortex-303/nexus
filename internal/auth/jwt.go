package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid or expired token")

// Claims holds JWT payload data.
type Claims struct {
	UserID        string `json:"uid"`
	DisplayName   string `json:"name"`
	WorkspaceSlug string `json:"ws,omitempty"`
	Role          string `json:"role,omitempty"`
	AccountID     string `json:"aid,omitempty"`
	SuperAdmin    bool   `json:"sa,omitempty"`
	jwt.RegisteredClaims
}

// JWTManager handles token creation and validation.
type JWTManager struct {
	secret []byte
}

// NewJWTManager creates a manager with the given secret.
func NewJWTManager(secret []byte) *JWTManager {
	return &JWTManager{secret: secret}
}

// GenerateSecret creates a random 32-byte secret for JWT signing.
func GenerateSecret() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generating jwt secret: %w", err)
	}
	return b, nil
}

// SecretHex returns the secret as a hex string (for storage).
func (j *JWTManager) SecretHex() string {
	return hex.EncodeToString(j.secret)
}

// Issue creates a new JWT for a user.
func (j *JWTManager) Issue(userID, displayName, workspaceSlug, role, accountID string, opts ...func(*Claims)) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:        userID,
		DisplayName:   displayName,
		WorkspaceSlug: workspaceSlug,
		Role:          role,
		AccountID:     accountID,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(30 * 24 * time.Hour)), // 30 days
		},
	}
	for _, opt := range opts {
		opt(&claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

// WithSuperAdmin sets the superadmin flag on claims.
func WithSuperAdmin() func(*Claims) {
	return func(c *Claims) { c.SuperAdmin = true }
}

// Validate parses and validates a JWT, returning claims.
func (j *JWTManager) Validate(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return j.secret, nil
	})
	if err != nil {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// FromRequest extracts JWT from Authorization header or cookie.
func FromRequest(r *http.Request) string {
	// Try Authorization header first
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
	}
	// Try cookie
	if c, err := r.Cookie("nexus_token"); err == nil {
		return c.Value
	}
	// Try query parameter (for download links)
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	return ""
}
