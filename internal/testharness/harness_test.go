package testharness_test

import (
	"context"
	"testing"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

func TestPostgresIsScopedToTheCorebanSchemaWithMigrationsApplied(t *testing.T) {
	pool := testharness.Postgres(t)
	ctx := context.Background()

	var schema string
	if err := pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	if schema != "coreban" {
		t.Errorf("current_schema = %q, want coreban", schema)
	}

	var applied int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM coreban.goose_db_version WHERE version_id > 0 AND is_applied").Scan(&applied); err != nil {
		t.Fatalf("read coreban.goose_db_version: %v", err)
	}
	if applied == 0 {
		t.Error("no migrations recorded in coreban.goose_db_version")
	}

	if _, err := pool.Exec(ctx, "CREATE TABLE harness_ddl_probe (id int)"); err == nil {
		t.Error("runtime role created a table; it must be DML-only like production's coreban_role")
	}
}
