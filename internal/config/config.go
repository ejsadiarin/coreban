// Package config reads coreban's settings from the environment.
package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/ejsadiarin/coreban/internal/attachment"
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

	// Bucket is where attachments live (ADR-0004); attachments are off
	// while its Endpoint (S3_ENDPOINT) is empty.
	Bucket attachment.BucketConfig
	// AttachmentQuota is how many bytes one owner may store
	// (ATTACHMENT_QUOTA, e.g. 5GB); 0 means it could not be read.
	AttachmentQuota int64
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
		Bucket: attachment.BucketConfig{
			Endpoint:       os.Getenv("S3_ENDPOINT"),
			PublicEndpoint: os.Getenv("S3_PUBLIC_ENDPOINT"),
			Bucket:         os.Getenv("S3_BUCKET"),
			AccessKeyID:    os.Getenv("S3_ACCESS_KEY_ID"),
			SecretKey:      os.Getenv("S3_SECRET_ACCESS_KEY"),
			Region:         getEnv("S3_REGION", "auto"),
		},
		AttachmentQuota: byteSize(getEnv("ATTACHMENT_QUOTA", "5GB")),
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
	if c.Bucket.Endpoint != "" && c.AttachmentQuota == 0 {
		return fmt.Errorf("ATTACHMENT_QUOTA must be a size such as 5GB or 500MB")
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

// byteSize reads a size such as 5GB, 500MB, 64KB or a plain number of
// bytes (binary units), or returns 0 if it is none of those.
func byteSize(s string) int64 {
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	unit := int64(1)
	for suffix, u := range map[string]int64{"GB": 1 << 30, "MB": 1 << 20, "KB": 1 << 10} {
		if strings.HasSuffix(s, suffix) {
			s, unit = strings.TrimSuffix(s, suffix), u
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 || n > math.MaxInt64/unit {
		return 0
	}
	return n * unit
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
