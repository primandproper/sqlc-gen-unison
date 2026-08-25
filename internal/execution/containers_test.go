package execution_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/containers/mysqltest"
	"github.com/primandproper/sqlc-gen-unison/internal/containers/pgtest"
	"github.com/primandproper/sqlc-gen-unison/testdata/golden/identitydb"
)

// TestQuerier_Postgres runs the suite against a real PostgreSQL server.
func TestQuerier_Postgres(t *testing.T) {
	t.Parallel()

	pgtest.Run(t, func(ctx context.Context, db *sql.DB) {
		runQuerierSuite(t, ctx, env{db: db, name: "postgresql", dialect: identitydb.DialectPostgreSQL})
	})
}

// TestQuerier_MySQL runs the suite against a real MySQL server.
//
// This is the run that matters most. MySQL is the only engine of the three that
// repeats a placeholder per occurrence of a named argument rather than
// deduplicating, and the only one whose reported parameter numbers are not
// placeholder positions. Everything else in the repository would pass with those
// arguments in the wrong order.
func TestQuerier_MySQL(t *testing.T) {
	t.Parallel()

	mysqltest.Run(t, func(ctx context.Context, db *sql.DB) {
		runQuerierSuite(t, ctx, env{db: db, name: "mysql", dialect: identitydb.DialectMySQL})
	})
}
