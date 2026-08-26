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

// TestListParamConvergesAcrossItsTwoSpellings is §7's last row, resolved.
//
// A variable-length IN list is the one shape where the *generated code* differs
// by dialect rather than only the statement text: Postgres binds one array,
// MySQL and SQLite have no array to bind and expand placeholders instead. The
// corpus spells it both ways under one query name, and this is the assertion
// that both arrive at the same method with the same []string parameter.
//
// It is a convergence and not a reconciliation: nothing pairs two queries or
// merges two shapes. Each dialect emits the binding its own analysis described,
// against a params struct all three computed identically — and the shared files
// being byte-identical, which the test above checks, is the proof they did.
func TestListParamConvergesAcrossItsTwoSpellings(t *testing.T) {
	t.Parallel()

	files := readAll(t, corpus{}.generate(t))

	// One shared []string field, once, in the shared types.
	test.StrContains(t, files["types_generated.go"], "UserIDs []string")

	// One signature, and it says nothing about which dialect answers it.
	test.StrContains(t, files["querier_generated.go"],
		"ListRolesForUsers(ctx context.Context, db DBTX, arg ListRolesForUsersParams) ([]ListRolesForUsersRow, error)")

	// Postgres binds the slice as it stands: one array parameter, no rewriting.
	postgres := files["queries_postgresql_generated.go"]
	test.StrContains(t, postgres, "q.listRolesForUsers,\n\t\targ.Scope,\n\t\targ.UserIDs,")
	test.StrNotContains(t, postgres, "slicePlaceholders(")

	// The other two expand, and the elements are appended after the scalar they
	// follow in the statement rather than before it.
	for _, dialect := range []string{"mysql", "sqlite"} {
		expanding := files["queries_"+dialect+"_generated.go"]

		test.StrContains(t, expanding, `strings.Replace(query, "/*SLICE:user_ids*/?", slicePlaceholders("?", len(arg.UserIDs)), 1)`)
		test.StrContains(t, expanding, "args = append(args, arg.Scope)")
	}
}

// TestRetypedListParamIsCompileError is the milestone-1 exit criterion applied
// to lists: a parameter that is a list on two dialects and a scalar on the third
// must not compile.
//
// This is the divergence that would otherwise be invisible. Exec and Query take
// ...any, so binding a []string where the other dialects bind a string
// type-checks at the call, and the statement that receives it merely returns the
// wrong rows. The shape assertion in each dialect's file is what turns it into a
// compile error naming the field.
func TestRetypedListParamIsCompileError(t *testing.T) {
	t.Parallel()

	out, err := corpus{
		mutate: func(dialect, sql string) string {
			if dialect != "mysql" {
				return sql
			}

			// The same argument, bound one at a time — which is exactly the
			// hand-written escape hatch this feature exists to replace, left in
			// place on one dialect after the others moved.
			return strings.Replace(sql,
				"identity_user_roles.user_id IN (sqlc.slice(user_ids))",
				"identity_user_roles.user_id = sqlc.arg(user_ids)", 1)
		},
	}.generateExpectingFailure(t)

	must.NoError(t, err, must.Sprint("the corpus should still generate; the divergence is meant to reach the compiler"))

	output, compileErr := compilePackage(t, out)

	must.Error(t, compileErr,
		must.Sprintf("a list on two dialects and a scalar on the third compiled:\n%s", output))
	test.StrContains(t, output, "UserIDs")
}

// TestListMustBindTheLastPlaceholder is the trap this feature would otherwise
// have shipped, refused at generate time.
//
// Every placeholder an expansion produces is a bare `?`, which SQLite numbers as
// one past the highest index it has seen — so a parameter that follows the list
// collides with an element of it, matches nothing, and reports no error. There
// is no way to emit code that is right on both expanding engines for that
// ordering, so unison refuses it and says where to move the clause. The corpus
// query already ends with its list; this puts it in front of the scope to prove
// the refusal is real.
func TestListMustBindTheLastPlaceholder(t *testing.T) {
	t.Parallel()

	_, err := corpus{
		mutate: func(dialect, sql string) string {
			if dialect != "mysql" {
				return sql
			}

			return strings.Replace(sql,
				"\tAND identity_users.scope = sqlc.arg(scope)\n"+
					"\tAND identity_user_roles.user_id IN (sqlc.slice(user_ids))\n",
				"\tAND identity_user_roles.user_id IN (sqlc.slice(user_ids))\n"+
					"\tAND identity_users.scope = sqlc.arg(scope)\n", 1)
		},
	}.generateExpectingFailure(t)

	must.Error(t, err, must.Sprint("a list bound before another parameter generated, and SQLite would have silently matched nothing"))
	test.StrContains(t, err.Error(), "user_ids")
	test.StrContains(t, err.Error(), "ListRolesForUsers")
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
