package generate_test

import (
	"strings"
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// TestCorpusConverges is milestone 1's headline: three dialects, one output
// directory, and a package that compiles.
//
// It compiles the result rather than comparing it to a golden file, because
// compiling is the actual guarantee. The shared files were written three times,
// once by each dialect, and each dialect's query file pins the shape it
// analyzed; that the whole thing builds is the statement that all three agreed.
func TestCorpusConverges(t *testing.T) {
	t.Parallel()

	out := corpus{}.generate(t)
	files := readAll(t, out)

	// One shared set plus one file per dialect.
	for _, name := range []string{
		"db_generated.go", "types_generated.go", "querier_generated.go",
		"queries_mysql_generated.go", "queries_postgresql_generated.go", "queries_sqlite_generated.go",
	} {
		test.MapContainsKey(t, files, name)
	}

	output, err := compilePackage(t, out)
	must.NoError(t, err, must.Sprintf("the converged package did not compile:\n%s", output))
}

// TestSharedFilesAreByteIdenticalAcrossDialects is the invariant stated
// directly.
//
// Every invocation emits the shared files from the roster and the shapes it
// analyzed. When the dialects agree those writes are byte-identical, which is
// what makes last-write-wins a no-op rather than a race. Generating each dialect
// on its own and comparing is the only way to observe that; in a real run they
// overwrite each other and the equality is invisible by construction.
func TestSharedFilesAreByteIdenticalAcrossDialects(t *testing.T) {
	t.Parallel()

	shared := make(map[string]map[string]string, len(dialects))

	for _, dialect := range dialects {
		out := corpus{dialects: []string{dialect}}.generate(t)
		shared[dialect] = readAll(t, out)
	}

	reference := dialects[0]

	for _, name := range []string{"db_generated.go", "types_generated.go", "querier_generated.go"} {
		for _, dialect := range dialects[1:] {
			must.Eq(t, shared[reference][name], shared[dialect][name],
				must.Sprintf("%s differs between %s and %s, so the dialects do not converge",
					name, reference, dialect))
		}
	}
}

// TestRegenerationIsByteIdentical is the determinism gate.
//
// Consumers regenerate in CI and fail on a dirty tree, which is only meaningful
// if generating twice produces the same bytes. A map ranged over in the wrong
// place, or a timestamp in a header, would break that — so it is asserted from
// the start rather than after the first flake.
func TestRegenerationIsByteIdentical(t *testing.T) {
	t.Parallel()

	first := readAll(t, corpus{}.generate(t))
	second := readAll(t, corpus{}.generate(t))

	must.Eq(t, first, second)
}

// TestSwappedFieldIsCompileError is §1's opening paragraph, made into a test.
//
// Two same-typed columns swapped in one dialect's projection is the paste error
// that "compiles, lints, and silently transposes data". Here it cannot: the
// shared row struct is written by whichever dialect ran last, and every other
// dialect's file converts that struct to the shape it analyzed. Two string
// fields in the other order is a different struct type, and the conversion stops
// compiling.
func TestSwappedFieldIsCompileError(t *testing.T) {
	t.Parallel()

	out := corpus{
		mutate: func(dialect, sql string) string {
			if dialect != "postgresql" {
				return sql
			}

			return swapColumns(sql,
				"identity_users.first_name",
				"identity_users.last_name")
		},
	}.generate(t)

	output, err := compilePackage(t, out)

	must.Error(t, err, must.Sprintf("a swapped projection compiled, which is the failure this tool exists to prevent:\n%s", output))
	test.StrContains(t, output, "Row")
}

// TestDroppedQueryIsCompileError is the second exit criterion: a query that one
// dialect no longer has.
//
// The shared Querier interface still declares it, because two dialects still
// analyze it. MySQL's querier no longer implements it, and New returns that
// querier as a Querier — so the compiler reports the missing method, by name.
func TestDroppedQueryIsCompileError(t *testing.T) {
	t.Parallel()

	out := corpus{
		mutate: func(dialect, sql string) string {
			if dialect != "mysql" {
				return sql
			}

			return dropQuery(sql, "GetInvitation")
		},
	}.generate(t)

	output, err := compilePackage(t, out)

	must.Error(t, err, must.Sprintf("a query dropped from one dialect compiled:\n%s", output))
	test.StrContains(t, output, "GetInvitation")
}

// TestRetypedColumnIsCompileError covers the divergence the compiler would
// otherwise miss.
//
// Scan and Exec take ...any, so a column that is int64 on one dialect and string
// on another binds without complaint and fails at run time — or worse, does not.
// The shape assertions in each dialect's file are what turn it into a compile
// error, and this is the test that says so.
//
// The divergence is introduced in the schema rather than the query, because that
// is where this class of drift actually comes from: three migrations that were
// meant to agree and do not.
func TestRetypedColumnIsCompileError(t *testing.T) {
	t.Parallel()

	out := corpus{
		mutateSchema: func(dialect, ddl string) string {
			if dialect != "postgresql" {
				return ddl
			}

			// A TEXT column that is BIGINT on one engine only.
			return strings.Replace(ddl,
				"first_name                       TEXT NOT NULL DEFAULT ''",
				"first_name                       BIGINT NOT NULL DEFAULT 0", 1)
		},
	}.generate(t)

	output, err := compilePackage(t, out)

	must.Error(t, err, must.Sprintf("a column with a different type on one dialect compiled:\n%s", output))
	test.StrContains(t, output, "FirstName")
}

// TestReturningIsNotAConvergentShape is §7's first row, decided.
//
// Postgres can write a create as `INSERT ... RETURNING` and analyze it as :one.
// MySQL cannot: it has no RETURNING, and sqlc analyzes one statement per query,
// so there is no way to spell insert-then-read-back as a single :one there.
//
// unison does not bridge that. It could only do so by pairing two queries under
// a naming convention and emitting a method that makes two round trips, which is
// a reconciliation — and reconciling divergent shapes is the thing this tool
// refuses. So the rule is the authoring one: write creates as :exec and read
// back with a separate query, which is what the corpus does.
//
// This test is what makes that a rule rather than a preference. A projection
// that exists on one dialect and not another is a divergence, and it fails to
// compile like every other divergence — no silent fallback, as §7 ends.
func TestReturningIsNotAConvergentShape(t *testing.T) {
	t.Parallel()

	out, err := corpus{
		mutate: func(dialect, sql string) string {
			if dialect != "postgresql" {
				return sql
			}

			// The create that every dialect writes as :exec, rewritten the way
			// a Postgres author reaching for RETURNING would write it: a real
			// projection Postgres analyzes happily and MySQL cannot express.
			sql = strings.Replace(sql,
				"-- name: CreateUser :exec",
				"-- name: CreateUser :one", 1)

			return strings.Replace(sql,
				");\n\n-- name: GetUser",
				") RETURNING *;\n\n-- name: GetUser", 1)
		},
	}.generateExpectingFailure(t)

	// It may fail at generation — Postgres reports a projection the others do
	// not have — or at compilation, when the shared files disagree. Either is
	// the refusal working; what must not happen is a package that builds.
	if err != nil {
		test.StrContains(t, err.Error(), "CreateUser")

		return
	}

	output, compileErr := compilePackage(t, out)
	must.Error(t, compileErr,
		must.Sprintf("a RETURNING-shaped create on one dialect compiled:\n%s", output))
	test.StrContains(t, output, "CreateUser")
}

// dropQuery removes a named query and its statement from a query file.
func dropQuery(sql, name string) string {
	marker := "-- name: " + name + " "

	start := strings.Index(sql, marker)
	if start < 0 {
		panic("dropQuery: the corpus has no query named " + name)
	}

	rest := sql[start+len(marker):]

	next := strings.Index(rest, "-- name: ")
	if next < 0 {
		return sql[:start]
	}

	return sql[:start] + rest[next:]
}
