package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nexus-chat/nexus/internal/license"
)

func main() {
	email := flag.String("email", "", "license holder email (required)")
	plan := flag.String("plan", "pro", "plan tier: pro, enterprise")
	maxMembers := flag.Int("max-members", -1, "max members (-1 = unlimited)")
	expires := flag.String("expires", "", "expiry date (YYYY-MM-DD), empty = never")
	keyPath := flag.String("key", defaultKeyPath(), "path to Ed25519 private key PEM")
	flag.Parse()

	if *email == "" {
		fmt.Fprintln(os.Stderr, "usage: licensegen -email user@co.com [-plan pro] [-max-members 50] [-expires 2027-01-01]")
		os.Exit(1)
	}

	privKey := loadOrGenerateKey(*keyPath)

	// Build license payload
	lic := license.License{
		ID:         fmt.Sprintf("lic_%d", time.Now().UnixMilli()),
		Email:      *email,
		Plan:       *plan,
		MaxMembers: *maxMembers,
		IssuedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if *expires != "" {
		t, err := time.Parse("2006-01-02", *expires)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid expiry date: %v\n", err)
			os.Exit(1)
		}
		lic.ExpiresAt = t.UTC().Format(time.RFC3339)
	}

	payloadBytes, _ := json.Marshal(lic)

	// Sign
	sig := ed25519.Sign(privKey, payloadBytes)

	// Encode envelope
	env := struct {
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}{
		Payload:   base64.StdEncoding.EncodeToString(payloadBytes),
		Signature: base64.StdEncoding.EncodeToString(sig),
	}
	envBytes, _ := json.Marshal(env)
	licenseKey := base64.StdEncoding.EncodeToString(envBytes)

	fmt.Fprintln(os.Stderr, "License generated successfully.")
	fmt.Fprintf(os.Stderr, "  Email:       %s\n", lic.Email)
	fmt.Fprintf(os.Stderr, "  Plan:        %s\n", lic.Plan)
	fmt.Fprintf(os.Stderr, "  MaxMembers:  %d\n", lic.MaxMembers)
	if lic.ExpiresAt != "" {
		fmt.Fprintf(os.Stderr, "  Expires:     %s\n", lic.ExpiresAt)
	} else {
		fmt.Fprintf(os.Stderr, "  Expires:     never\n")
	}
	fmt.Fprintf(os.Stderr, "\nPublic key (hex) — embed in internal/license/license.go:\n")
	fmt.Fprintf(os.Stderr, "  %s\n\n", hex.EncodeToString(privKey.Public().(ed25519.PublicKey)))
	fmt.Fprintf(os.Stderr, "License key:\n")
	fmt.Println(licenseKey)
}

func defaultKeyPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".nexus", "license-key.pem")
}

func loadOrGenerateKey(path string) ed25519.PrivateKey {
	// Try to load existing key
	data, err := os.ReadFile(path)
	if err == nil {
		keyBytes, err := hex.DecodeString(string(data))
		if err == nil && len(keyBytes) == ed25519.PrivateKeySize {
			fmt.Fprintf(os.Stderr, "Loaded signing key from %s\n", path)
			return ed25519.PrivateKey(keyBytes)
		}
	}

	// Generate new keypair
	_, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate key: %v\n", err)
		os.Exit(1)
	}

	// Save private key
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not create key directory: %v\n", err)
	} else {
		if err := os.WriteFile(path, []byte(hex.EncodeToString(privKey)), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save key: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Generated new signing key at %s\n", path)
		}
	}

	return privKey
}
