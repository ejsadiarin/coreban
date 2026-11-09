// Package testharness gives tests a disposable Postgres with coreban's
// migrations applied, and the production router driven by an injected
// verified identity instead of a gateway token.
package testharness

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ejsadiarin/coreban/internal/auth"
	"github.com/ejsadiarin/coreban/internal/db"
	"github.com/ejsadiarin/coreban/internal/server"
)

const schema = "coreban"

var (
	once    sync.Once
	pool    *pgxpool.Pool
	initErr error
)

// Postgres returns a pool on a disposable Postgres shared by every test in
// the binary, scoped to the coreban schema with migrations applied. It
// skips under -short. Tests share the database, so each must create its own
// owner (uuid.New()) rather than assume an empty one.
func Postgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("docker-backed integration test")
	}
	once.Do(func() { pool, initErr = start(context.Background()) })
	if initErr != nil {
		t.Fatalf("testharness: start postgres: %v", initErr)
	}
	return pool
}

func start(ctx context.Context) (*pgxpool.Pool, error) {
	ctr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("coredb"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		return nil, err
	}
	base, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, err
	}

	// Production shape (internal/db/bootstrap.sql): the owner role owns the
	// coreban schema and runs migrations; the API connects as the DML-only
	// coreban_role, scoped by that role's search_path. A slice that forgets
	// a grant fails here, not in production.
	super, err := sql.Open("pgx", base)
	if err != nil {
		return nil, err
	}
	defer super.Close()
	if _, err := super.ExecContext(ctx, "CREATE ROLE neondb_owner LOGIN PASSWORD 'owner'"); err != nil {
		return nil, fmt.Errorf("create owner role: %w", err)
	}
	bootstrap := strings.Replace(db.Bootstrap, "'REPLACE_ME'", "'runtime'", 1)
	if _, err := super.ExecContext(ctx, bootstrap); err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}

	migrationURL, err := as(base, "neondb_owner", "owner")
	if err != nil {
		return nil, err
	}
	migrator, err := sql.Open("pgx", migrationURL+"&options="+url.QueryEscape("-c search_path="+schema))
	if err != nil {
		return nil, err
	}
	defer migrator.Close()
	if err := db.Migrate(ctx, migrator); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	databaseURL, err := as(base, "coreban_role", "runtime")
	if err != nil {
		return nil, err
	}
	p, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return p, p.Ping(ctx)
}

// as rewrites a connection string to log in as another role.
func as(connStr, user, password string) (string, error) {
	u, err := url.Parse(connStr)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword(user, password)
	return u.String(), nil
}

// Identity is the verified gateway identity a test request carries. The
// zero value carries none, so the request reaches handlers unauthenticated.
type Identity struct {
	Owner  uuid.UUID
	Scopes []string
	// ActAs is the signed targeted-read target (uuid.Nil for none).
	ActAs uuid.UUID
}

// Router returns the production router on the harness Postgres, with id
// injected exactly where the token verifier would establish it.
func Router(t *testing.T, id Identity) http.Handler {
	t.Helper()
	return server.New(Postgres(t), inject(id)).Routes()
}

// inject is a stand-in for the verifier middleware that establishes id
// without a token.
func inject(id Identity) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id.Owner == uuid.Nil {
				next.ServeHTTP(w, r)
				return
			}
			ctx := auth.WithUserID(r.Context(), id.Owner, id.Scopes)
			if id.ActAs != uuid.Nil {
				ctx = auth.WithActAs(ctx, id.ActAs)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
