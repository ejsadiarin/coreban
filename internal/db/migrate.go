// Package db owns coreban's schema: the goose migrations under migrations/,
// applied to the coreban schema of the shared database.
package db

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies every pending migration through conn. conn's search_path
// must already be the coreban schema (MIGRATION_URL's options form, as the
// goose CLI uses in `make migrate-up`), so goose's version table and every
// object land there and nowhere else.
func Migrate(ctx context.Context, conn *sql.DB) error {
	dir, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, dir)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
