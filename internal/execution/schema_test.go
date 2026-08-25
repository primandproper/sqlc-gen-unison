package execution_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shoenig/test/must"
)

// applySchema renders the corpus DDL at a table prefix and executes it.
//
// Prefixing is how subtests are isolated from each other on one shared server,
// which is the cheap isolation platform-go uses too — and here it does double
// duty, because the prefix is also the feature under test. The generated
// statements carry §9's {{prefix}} marker, so a suite that ran at the empty
// prefix would never find out whether the marker is replaced or whether it was
// placed on the right identifiers.
func applySchema(t *testing.T, ctx context.Context, db *sql.DB, dialect, prefix string) {
	t.Helper()

	path := filepath.Join("..", "..", "testdata", "identity", "schema", dialect+".sql")

	ddl, err := os.ReadFile(path)
	must.NoError(t, err)

	// Every identifier in the corpus schema begins with identity_ — the tables
	// and the indexes both — and index names have to move with their tables or
	// a second prefix collides with the first on the shared server.
	rendered := strings.ReplaceAll(string(ddl), "identity_", prefix+"identity_")

	for _, statement := range splitStatements(rendered) {
		_, execErr := db.ExecContext(ctx, statement)
		must.NoError(t, execErr, must.Sprintf("executing %q", statement))
	}
}

// splitStatements breaks DDL on statement boundaries, ignoring semicolons
// inside string literals.
//
// One statement per Exec rather than a multi-statement DSN, so all three
// dialects take the same path: pgx sends through the extended protocol, which
// refuses more than one statement per round trip, and a harness that took two
// different paths would be testing two different things.
func splitStatements(ddl string) []string {
	var (
		statements []string
		current    strings.Builder
		inLiteral  bool
	)

	for i := range len(ddl) {
		c := ddl[i]

		switch {
		case c == '\'':
			// A doubled quote is an escaped quote, and flipping twice leaves
			// the state where it started, so it needs no special case.
			inLiteral = !inLiteral
		case c == ';' && !inLiteral:
			if statement := strings.TrimSpace(current.String()); statement != "" {
				statements = append(statements, statement)
			}

			current.Reset()

			continue
		}

		current.WriteByte(c)
	}

	if statement := strings.TrimSpace(current.String()); statement != "" {
		statements = append(statements, statement)
	}

	return statements
}
