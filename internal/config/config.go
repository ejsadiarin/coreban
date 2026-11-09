// Package config reads coreban's settings from the environment.
package config

import (
	"fmt"
	"os"
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

	// ntfy delivery for reminders.
	NtfyURL   string
	NtfyToken string
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
	}
}

// ValidateServe reports settings the API cannot start without.
func (c *Config) ValidateServe() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
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

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
