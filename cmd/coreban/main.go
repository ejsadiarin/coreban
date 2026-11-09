// Command coreban-api serves coreban's API behind the gateway. Run with the
// argument `migrate` to apply pending migrations to MIGRATION_URL and exit.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ejsadiarin/coreban/internal/auth"
	"github.com/ejsadiarin/coreban/internal/config"
	"github.com/ejsadiarin/coreban/internal/db"
	"github.com/ejsadiarin/coreban/internal/server"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()

	run := serve
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		run = migrate
	}
	if err := run(ctx, cfg); err != nil {
		slog.Error("coreban-api: exiting", "error", err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, cfg *config.Config) error {
	if err := cfg.ValidateServe(); err != nil {
		return err
	}

	// Fail closed before touching anything else: without the gateway's
	// signing keys nothing can be served.
	verifier, err := auth.NewVerifier(auth.Config{
		Issuer:   cfg.JWTIssuer,
		Audience: cfg.JWTAudience,
		JWKSURL:  cfg.JWTJWKSURL,
	})
	if err != nil {
		return fmt.Errorf("establish JWT verification: %w", err)
	}

	pool, err := openPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server.New(pool, verifier.Middleware).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       time.Minute,
	}
	slog.Info("coreban-api listening", "port", cfg.Port)
	return srv.ListenAndServe()
}

func openPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid DATABASE_URL: %w", err)
	}
	pcfg.MaxConns = 20
	pcfg.MinConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func migrate(ctx context.Context, cfg *config.Config) error {
	if err := cfg.ValidateMigrate(); err != nil {
		return err
	}
	conn, err := sql.Open("pgx", cfg.MigrationURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	slog.Info("coreban-api: migrations applied")
	return nil
}
