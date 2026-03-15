package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Listen     string `toml:"listen"`
	DataDir    string `toml:"data_dir"`
	Domain     string `toml:"domain"`
	Dev        bool   `toml:"dev"`
	SMTPListen string `toml:"smtp_listen"`
	RedisURL   string `toml:"redis_url"`
	QdrantURL   string `toml:"qdrant_url"`
	ResendAPIKey string `toml:"brevo_api_key"`
	LicenseKey   string `toml:"license_key"`
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Listen:  ":8080",
		DataDir: filepath.Join(home, ".nexus"),
		Domain:  "",
		Dev:     false,
	}
}

func Load() (*Config, error) {
	cfg := defaults()

	// Layer 1: config file
	configPath := findConfigFile()
	if configPath != "" {
		if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", configPath, err)
		}
	}

	// Layer 2: CLI flags (override config file)
	fs := flag.NewFlagSet("nexus", flag.ContinueOnError)
	listen := fs.String("listen", cfg.Listen, "address to listen on")
	dataDir := fs.String("data-dir", cfg.DataDir, "data directory path")
	domain := fs.String("domain", cfg.Domain, "domain for auto-TLS")
	dev := fs.Bool("dev", cfg.Dev, "development mode (no TLS)")

	// Parse flags after the subcommand
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg.Listen = *listen
	cfg.DataDir = *dataDir
	cfg.Domain = *domain
	cfg.Dev = *dev

	// Layer 3: environment variables (override everything)
	if v := os.Getenv("LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("DOMAIN"); v != "" {
		cfg.Domain = v
	}
	if v := os.Getenv("SMTP_LISTEN"); v != "" {
		cfg.SMTPListen = v
	}
	if v := os.Getenv("REDIS_URL"); v != "" {
		cfg.RedisURL = v
	}
	if v := os.Getenv("QDRANT_URL"); v != "" {
		cfg.QdrantURL = v
	}
	if v := os.Getenv("RESEND_API_KEY"); v != "" {
		cfg.ResendAPIKey = v
	}
	if v := os.Getenv("NEXUS_LICENSE"); v != "" {
		cfg.LicenseKey = v
	}

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("creating data dir: %w", err)
	}

	return &cfg, nil
}

func findConfigFile() string {
	// Check working directory first, then data dir
	candidates := []string{
		"nexus.toml",
		filepath.Join(defaults().DataDir, "nexus.toml"),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
