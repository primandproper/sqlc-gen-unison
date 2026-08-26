package gogen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"
	"github.com/primandproper/sqlc-gen-unison/internal/options"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// fixture is a package exercising every annotation unison supports, including
// the ones the identity corpus happens not to use.
//
// The corpus is the golden test's job. This one exists so that :execresult and
// the sql.Null* nullable style are not shipped as claims nothing has ever
// compiled — both are in §3's supported set and in §8's options, and neither
// appears anywhere in the corpus.
func fixture(nullAs options.NullStyle, dialect string) *ir.Package {
	id := ir.Field{Name: "id", Type: ir.Type{Kind: ir.KindString}}
	nickname := ir.Field{Name: "nickname", Type: ir.Type{Kind: ir.KindString, Nullable: true}}
	seenAt := ir.Field{Name: "seen_at", Type: ir.Type{Kind: ir.KindTime, Nullable: true}}
	hits := ir.Field{Name: "hits", Type: ir.Type{Kind: ir.KindInt64, Nullable: true}}
	active := ir.Field{Name: "active", Type: ir.Type{Kind: ir.KindBool, Nullable: true}}
	weight := ir.Field{Name: "weight", Type: ir.Type{Kind: ir.KindFloat64, Nullable: true}}
	blob := ir.Field{Name: "payload", Type: ir.Type{Kind: ir.KindBytes, Nullable: true}}

	row := []ir.Field{id, nickname, seenAt, hits, active, weight, blob}

	queries := []ir.Query{
		{Name: "InsertThing", Command: ir.CommandExec, Params: []ir.Field{id}},
		{Name: "GetThing", Command: ir.CommandOne, Params: []ir.Field{id}, Columns: row},
		{Name: "ListThings", Command: ir.CommandMany, Columns: row},
		{Name: "TouchThing", Command: ir.CommandExecRows, Params: []ir.Field{id}},
		{Name: "ReplaceThing", Command: ir.CommandExecResult, Params: []ir.Field{id, nickname}},
	}

	statements := make([]ir.Statement, 0, len(queries))
	for i := range queries {
		args := make([]string, 0, len(queries[i].Params))
		for j := range queries[i].Params {
			args = append(args, queries[i].Params[j].Name)
		}

		statements = append(statements, ir.Statement{
			Name: queries[i].Name,
			SQL:  "SELECT 1 FROM " + ir.PrefixMarker + "things",
			Args: args,
		})
	}

	return &ir.Package{
		Name:          "fixturedb",
		Dialect:       dialect,
		Roster:        []string{"mysql", "postgresql"},
		SQLCVersion:   "v1.31.1",
		UnisonVersion: "test",
		NullAs:        string(nullAs),
		TablePrefix:   true,
		Queries:       queries,
		Statements:    statements,
	}
}

// TestEmitEveryCommand covers the annotations §3 puts in scope, and asserts the
// return types each one produces.
func TestEmitEveryCommand(t *testing.T) {
	t.Parallel()

	files := emitted(t, fixture(options.NullPointer, "postgresql"))

	querier := collapse(files["querier_generated.go"])

	test.StrContains(t, querier, "InsertThing(ctx context.Context, db DBTX, arg InsertThingParams) error")
	test.StrContains(t, querier, "GetThing(ctx context.Context, db DBTX, arg GetThingParams) (GetThingRow, error)")
	test.StrContains(t, querier, "ListThings(ctx context.Context, db DBTX) ([]ListThingsRow, error)")
	test.StrContains(t, querier, "TouchThing(ctx context.Context, db DBTX, arg TouchThingParams) (int64, error)")

	// :execresult hands back the driver's own result, which is the whole reason
	// to choose it over :execrows.
	test.StrContains(t, querier, "ReplaceThing(ctx context.Context, db DBTX, arg ReplaceThingParams) (sql.Result, error)")
	test.StrContains(t, querier, `"database/sql"`)

	// A query with no parameters takes no arg struct rather than an empty one.
	test.StrNotContains(t, collapse(files["types_generated.go"]), "ListThingsParams")

	// The :execrows note is emitted because this package has one.
	test.StrContains(t, querier, "A note on the :execrows count")
}

// TestEmitNullPointer is the default nullable style.
func TestEmitNullPointer(t *testing.T) {
	t.Parallel()

	types := collapse(emitted(t, fixture(options.NullPointer, "postgresql"))["types_generated.go"])

	test.StrContains(t, types, "Nickname *string")
	test.StrContains(t, types, "SeenAt *time.Time")
	test.StrContains(t, types, "Hits *int64")
	test.StrContains(t, types, "Active *bool")
	test.StrContains(t, types, "Weight *float64")

	// A byte slice is already nil-able, so wrapping it would add a second way
	// to say the same thing.
	test.StrContains(t, types, "Payload []byte")
}

// TestEmitNullSQL covers the option §8 offers and the corpus never exercises.
func TestEmitNullSQL(t *testing.T) {
	t.Parallel()

	files := emitted(t, fixture(options.NullSQL, "postgresql"))
	types := collapse(files["types_generated.go"])

	test.StrContains(t, types, "Nickname sql.NullString")
	test.StrContains(t, types, "SeenAt sql.NullTime")
	test.StrContains(t, types, "Hits sql.NullInt64")
	test.StrContains(t, types, "Active sql.NullBool")
	test.StrContains(t, types, "Weight sql.NullFloat64")
	test.StrContains(t, types, "Payload []byte")

	// The import that makes those names resolve. A missing one is exactly the
	// defect an untested rendering path ships.
	test.StrContains(t, types, `"database/sql"`)
	test.StrNotContains(t, types, `"time"`)
}

// TestEmittedFixtureCompiles builds both nullable styles.
//
// Emit already runs go/format, so a parse error would surface without this.
// What this adds is the type checker: a missing import or a name that does not
// resolve parses perfectly well.
func TestEmittedFixtureCompiles(t *testing.T) {
	t.Parallel()

	for _, style := range []options.NullStyle{options.NullPointer, options.NullSQL} {
		t.Run(string(style), func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			// Every dialect in the roster, into one directory — which is what a
			// real run does, and what the shared files are written to assume.
			// One dialect's output alone deliberately does not compile: db_generated.go
			// names a constructor per dialect.
			for _, dialect := range fixture(style, "").Roster {
				for name, contents := range emitted(t, fixture(style, dialect)) {
					must.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600))
				}
			}

			must.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
				[]byte("module unisonfixture\n\ngo 1.27\n"), 0o600))

			cmd := exec.CommandContext(t.Context(), "go", "build", "./...")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")

			output, err := cmd.CombinedOutput()
			must.NoError(t, err, must.Sprintf("the fixture did not compile:\n%s", output))
		})
	}
}

// emitted renders a package and returns its files by name.
func emitted(t *testing.T, pkg *ir.Package) map[string]string {
	t.Helper()

	files, err := Emit(pkg)
	must.NoError(t, err)

	out := make(map[string]string, len(files))
	for i := range files {
		out[files[i].Name] = string(files[i].Contents)
	}

	return out
}

// collapse squeezes runs of spaces so an assertion can name a field and its
// type without also pinning the column gofmt aligned it to. Only assertions use
// it; what reaches disk is what the emitter produced.
func collapse(s string) string {
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}

	return s
}
