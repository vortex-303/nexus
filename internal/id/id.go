package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// New generates a ULID-like sortable ID: timestamp (ms) + random bytes, hex-encoded.
// Format: 12 hex chars (6 bytes time) + 16 hex chars (8 bytes random) = 28 chars.
func New() string {
	ts := time.Now().UnixMilli()
	timePart := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		timePart[i] = byte(ts & 0xFF)
		ts >>= 8
	}
	randPart := make([]byte, 8)
	rand.Read(randPart)
	return hex.EncodeToString(timePart) + hex.EncodeToString(randPart)
}

// Slug generates a short random workspace slug (6 chars, alphanumeric lowercase).
func Slug() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

// Token generates a random token (32 bytes, hex-encoded = 64 chars).
func Token() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Short generates a shorter random token (16 bytes, hex-encoded = 32 chars).
func Short() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// InviteCode generates a short human-friendly invite code like "NX-A7B3".
// 4 alphanumeric chars = 1.6M combinations, safe for 24h expiry.
func InviteCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O/1/I to avoid confusion
	b := make([]byte, 4)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return "NX-" + string(b)
}
