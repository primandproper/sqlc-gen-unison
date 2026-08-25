package generate_test

import (
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// TestUnderTypedColumnIsRefusedWithAnActionableError is §8's weak-engine
// problem, stated as behavior.
//
// SQLite's analyzer resolves types loosely — `include_archived` comes back as
// `any` there — and Postgres does the same for a COALESCE'd LIMIT. unison does
// not guess. Guessing would put a type in a shared struct that no engine
// actually reported, and the error it caused would surface as a scan failure in
// a consumer's container test rather than here.
//
// What it does instead is refuse, name the column, and name the option that
// fixes it. That message is the whole ergonomics of the weak-engine problem, so
// it is tested rather than left to chance.
func TestUnderTypedColumnIsRefusedWithAnActionableError(t *testing.T) {
	t.Parallel()

	for _, dialect := range []string{"sqlite", "postgresql"} {
		t.Run(dialect, func(t *testing.T) {
			t.Parallel()

			// The corpus's own options minus the hints, so the only thing
			// missing is the type.
			_, err := corpus{
				dialects: []string{dialect},
				options: `          table_prefix_var: true
          rename_params:
            mysql:
              limit: result_limit
`,
			}.generateExpectingFailure(t)

			must.Error(t, err)

			message := err.Error()

			test.StrContains(t, message, "could not resolve a type")
			test.StrContains(t, message, "type_override")
		})
	}
}

// TestNullAsSQLConvergesAndCompiles walks the corpus through the other nullable
// style §8 offers.
//
// The corpus generates with pointers, so without this the sql.Null* path would
// ship as an option nothing had ever run over a real query set. It is checked
// end to end rather than in the emitter alone because the interesting failure is
// a missing import, which parses fine and does not compile.
func TestNullAsSQLConvergesAndCompiles(t *testing.T) {
	t.Parallel()

	out := corpus{options: corpusOptions + "          null_as: sql\n"}.generate(t)
	files := readAll(t, out)

	types := files["types.go"]

	test.StrContains(t, types, "sql.NullTime")
	test.StrContains(t, types, "sql.NullString")
	test.StrContains(t, types, `"database/sql"`)

	// A type_override is the final type either way, so null_as does not reach
	// it: result_limit is the only nullable integer in the corpus, and it was
	// overridden to a plain int64.
	test.StrContains(t, types, "ResultLimit")
	test.StrNotContains(t, types, "sql.NullInt64")

	output, err := compilePackage(t, out)
	must.NoError(t, err, must.Sprintf("the sql.Null* package did not compile:\n%s", output))
}

// TestHintsMakeSQLiteConverge is the other half: with the hints supplied, the
// weak engine produces the same shared shape as the strong ones.
//
// That equality is the exit criterion "the sqlite runs work". It is asserted
// against Postgres rather than against a golden file, because the claim is that
// they agree, not that either matches something written down.
func TestHintsMakeSQLiteConverge(t *testing.T) {
	t.Parallel()

	sqlite := readAll(t, corpus{dialects: []string{"sqlite"}}.generate(t))
	postgres := readAll(t, corpus{dialects: []string{"postgresql"}}.generate(t))

	for _, name := range []string{"types.go", "querier.go", "db.go"} {
		must.Eq(t, postgres[name], sqlite[name],
			must.Sprintf("%s differs between sqlite and postgresql", name))
	}
}
