// Package pgtest runs a test body against a real PostgreSQL server.
package pgtest

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/primandproper/sqlc-gen-unison/internal/containers"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver.
	"github.com/shoenig/test/must"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// Image is the server the suite runs against.
	Image = "postgres:17-alpine"

	// DriverName is the database/sql driver. pgx rather than lib/pq because it
	// is what platform-go uses, and because a generated package's claim is that
	// it works through database/sql whichever driver is underneath.
	DriverName = "pgx"

	credential = "unisontest"

	// startupDeadline has to cover an image pull plus initdb on a busy host.
	startupDeadline = 2 * time.Minute

	// readyLog fires twice: once for the bootstrap server that runs the init
	// pass, once for the real one. Waiting for the second is what skips past
	// the server that is about to be shut down and restarted.
	readyLog           = "database system is ready to accept connections"
	readyLogOccurrence = 2
)

// Run starts a Postgres container, opens a pool against it, and hands the pool
// to fn. It skips when containers were not asked for.
func Run(tb testing.TB, fn func(ctx context.Context, db *sql.DB)) {
	tb.Helper()

	containers.Run(tb,
		func(ctx context.Context) (*postgrescontainer.PostgresContainer, error) {
			return postgrescontainer.Run(ctx, Image,
				postgrescontainer.WithDatabase(credential),
				postgrescontainer.WithUsername(credential),
				postgrescontainer.WithPassword(credential),
				testcontainers.WithWaitStrategy(
					wait.ForLog(readyLog).
						WithOccurrence(readyLogOccurrence).
						WithStartupTimeout(startupDeadline),
				),
			)
		},
		func(ctx context.Context, container *postgrescontainer.PostgresContainer) {
			dsn, err := container.ConnectionString(ctx, "sslmode=disable")
			must.NoError(tb, err)

			db, err := sql.Open(DriverName, dsn)
			must.NoError(tb, err)

			tb.Cleanup(func() {
				if closeErr := db.Close(); closeErr != nil {
					tb.Logf("closing the pool: %v", closeErr)
				}
			})

			containers.PingUntilReady(tb, ctx, db.PingContext)

			fn(ctx, db)
		},
	)
}
