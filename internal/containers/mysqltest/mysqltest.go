// Package mysqltest runs a test body against a real MySQL server.
//
// MySQL is the dialect this whole harness exists for. It is the one engine that
// does not deduplicate repeated named arguments — the corpus's list queries bind
// sixteen placeholders from eight fields there — and the one whose analysis
// reports a parameter number that is not its placeholder position. Both are
// invisible to a compiler and to a golden file.
package mysqltest

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/primandproper/sqlc-gen-unison/internal/containers"

	_ "github.com/go-sql-driver/mysql" // registers the "mysql" driver.
	"github.com/shoenig/test/must"
	"github.com/testcontainers/testcontainers-go"
	mysqlcontainer "github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// Image is the server the suite runs against. sqlc's `mysql` engine targets
	// MySQL proper, so that is what the emitted SQL is checked against.
	Image = "mysql:8.0"

	// DriverName is the database/sql driver.
	DriverName = "mysql"

	credential = "unisontest"

	// startupDeadline has to cover an image pull plus the init run.
	startupDeadline = 2 * time.Minute

	// readyLog identifies the real server by the port it announces rather than
	// by counting readiness lines, because the count is not what it looks like:
	// the bootstrap server that runs the init scripts and then shuts down logs
	// "ready for connections" twice on its own.
	//
	// The trailing space is load-bearing. Without it this also matches the X
	// plugin's "port: 33060", which is logged a line before the real server is
	// ready.
	readyLog = "port: 3306 "

	// parseTime keeps DATETIME(6) round-tripping as time.Time rather than
	// []byte, which is the whole claim the shared row struct makes about times.
	// multiStatements is not set: the DDL is executed one statement at a time so
	// that every dialect takes the same path.
	dsnParams = "parseTime=true"
)

// Run starts a MySQL container, opens a pool against it, and hands the pool to
// fn. It skips when containers were not asked for.
func Run(tb testing.TB, fn func(ctx context.Context, db *sql.DB)) {
	tb.Helper()

	containers.Run(tb,
		func(ctx context.Context) (*mysqlcontainer.MySQLContainer, error) {
			return mysqlcontainer.Run(ctx, Image,
				mysqlcontainer.WithDatabase(credential),
				mysqlcontainer.WithUsername(credential),
				mysqlcontainer.WithPassword(credential),
				testcontainers.WithWaitStrategy(
					wait.ForLog(readyLog).WithStartupTimeout(startupDeadline),
				),
			)
		},
		func(ctx context.Context, container *mysqlcontainer.MySQLContainer) {
			dsn, err := container.ConnectionString(ctx, dsnParams)
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
