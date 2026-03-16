package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// publicKeyHex is the Ed25519 public key used to verify license signatures.
// Generate a keypair with: go run ./cmd/licensegen/ -email test@test.com -plan pro -max-members 10
// The first run prints the public key hex to embed here.
const publicKeyHex = "78176d047b88a7c528e1a783eda20abe1c1ff62038be33d78c032b5a2a19b78a"

const freePlanMemberLimit = 5

// License represents a verified signed license.
type License struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Plan       string `json:"plan"`
	MaxMembers int    `json:"max_members"`
	ExpiresAt  string `json:"expires_at"`
	IssuedAt   string `json:"issued_at"`
}

// envelope is the wire format: base64-encoded JSON containing payload + signature.
type envelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// Parse decodes and verifies a signed license key string.
// Returns nil and an error if the key is invalid, has a bad signature, or is expired.
func Parse(raw string) (*License, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty license key")
	}

	// Decode outer base64 envelope
	envelopeBytes, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid license encoding: %w", err)
	}

	var env envelope
	if err := json.Unmarshal(envelopeBytes, &env); err != nil {
		return nil, fmt.Errorf("invalid license format: %w", err)
	}

	// Decode payload and signature
	payloadBytes, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload encoding: %w", err)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return nil, fmt.Errorf("invalid signature encoding: %w", err)
	}

	// Verify signature
	pubKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid embedded public key (run licensegen to generate one)")
	}
	pubKey := ed25519.PublicKey(pubKeyBytes)

	if !ed25519.Verify(pubKey, payloadBytes, sigBytes) {
		return nil, fmt.Errorf("invalid license signature")
	}

	// Unmarshal license
	var lic License
	if err := json.Unmarshal(payloadBytes, &lic); err != nil {
		return nil, fmt.Errorf("invalid license payload: %w", err)
	}

	// Check expiry
	if lic.ExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, lic.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("invalid expiry date: %w", err)
		}
		if time.Now().After(exp) {
			return nil, fmt.Errorf("license expired on %s", lic.ExpiresAt)
		}
	}

	return &lic, nil
}

// MemberLimit returns the maximum number of members allowed by this license.
// Returns freePlanMemberLimit if MaxMembers is zero or invalid.
func (l *License) MemberLimit() int {
	if l.MaxMembers < 0 {
		return -1 // unlimited
	}
	if l.MaxMembers == 0 {
		return freePlanMemberLimit
	}
	return l.MaxMembers
}
