// Package config reads coreban's settings from the environment.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"

	"github.com/ejsadiarin/coreban/internal/reminder"
)

// Config holds coreban's environment-driven settings.
type Config struct {
	Port string

	// DatabaseURL is the runtime connection (pooler host, DML-only role whose
	// search_path is the coreban schema; see internal/db/bootstrap.sql).
	DatabaseURL string
	// MigrationURL is the owner connection on the direct host, scoped with
	// options=-c search_path=coreban. Only `coreban-api migrate` uses it.
	MigrationURL string

	// Gateway internal token verification. JWTAudience is coreban's own
	// name, never the gateway's comma-separated allow-list.
	JWTIssuer   string
	JWTAudience string
	JWTJWKSURL  string

	// ntfy delivery for reminders. Without NtfyURL nothing is sent.
	NtfyURL   string
	NtfyToken string

	// ReminderCap is how many reminders one owner may have (REMINDER_CAP);
	// 0 means the variable was not a positive whole number.
	ReminderCap int
}

// Load reads the environment, applying defaults for local development.
func Load() *Config {
	return &Config{
		Port:         getEnv("PORT", "6972"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		MigrationURL: os.Getenv("MIGRATION_URL"),
		JWTIssuer:    getEnv("JWT_ISSUER", "https://gateway.internal"),
		JWTAudience:  getEnv("JWT_AUDIENCE", "coreban"),
		JWTJWKSURL:   getEnv("JWT_JWKS_URL", "https://gateway.internal/.well-known/jwks.json"),
		NtfyURL:      os.Getenv("NTFY_URL"),
		NtfyToken:    os.Getenv("NTFY_TOKEN"),
		ReminderCap:  positiveInt(getEnv("REMINDER_CAP", strconv.Itoa(reminder.DefaultCap))),
	}
}

// ValidateServe reports settings the API cannot start without.
func (c *Config) ValidateServe() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.ReminderCap == 0 {
		return fmt.Errorf("REMINDER_CAP must be a positive whole number")
	}
	if c.NtfyURL != "" {
		if u, err := url.Parse(c.NtfyURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("NTFY_URL must be an http(s) URL")
		}
		// The server denies anonymous access (README "ntfy server").
		if c.NtfyToken == "" {
			return fmt.Errorf("NTFY_TOKEN is required when NTFY_URL is set")
		}
	}
	return nil
}

// ValidateMigrate reports settings `coreban-api migrate` cannot run without.
func (c *Config) ValidateMigrate() error {
	if c.MigrationURL == "" {
		return fmt.Errorf("MIGRATION_URL is required")
	}
	return nil
}

// positiveInt parses s, or returns 0 if it is not a positive whole number.
func positiveInt(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0
	}
	return n
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
