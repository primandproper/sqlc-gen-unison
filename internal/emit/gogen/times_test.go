package gogen

import (
	"maps"
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"
	"github.com/primandproper/sqlc-gen-unison/internal/options"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// timeLayout is what converge hands the emitter for an engine that keeps
// timestamps as text.
const timeLayout = "2006-01-02 15:04:05"

// timeFixture is a package whose parameters are times, in every shape one can
// arrive in.
//
// It is separate from fixture because the question it asks is separate: fixture
// covers the annotations, and this covers the one argument whose value cannot
// reach a text-timestamp engine as it stands. The corpus proves the nullable
// pointer against real analysis, which leaves the non-null one, the
// database/sql one, the list, and the type_override with nothing behind them.
func timeFixture(nullAs options.NullStyle, dialect string) *ir.Package {
	id := ir.Field{Name: "id", Type: ir.Type{Kind: ir.KindString}}
	since := ir.Field{Name: "since", Type: ir.Type{Kind: ir.KindTime}}
	until := ir.Field{Name: "until", Type: ir.Type{Kind: ir.KindTime, Nullable: true}}
	// A consumer's own type over a time column. unison does not know it holds a
	// time, and a consumer who named it has said they render it themselves.
	asOf := ir.Field{Name: "as_of", Type: ir.Type{Kind: ir.KindTime, Override: "int64"}}
	stamps := ir.Field{Name: "stamps", Type: ir.Type{Kind: ir.KindTime, Slice: true}}

	queries := []ir.Query{
		{Name: "ThingsBetween", Command: ir.CommandMany, Params: []ir.Field{since, until, asOf}, Columns: []ir.Field{id}},
		{Name: "TouchThing", Command: ir.CommandExec, Params: []ir.Field{id, since}},
		// A list of times beside a scalar, which is the only shape where the
		// formatter has to reach an element rather than a field — and, on the
		// engines that expand, the shape whose elements are appended one at a
		// time.
		{Name: "PickStamps", Command: ir.CommandMany, Params: []ir.Field{id, stamps}, Columns: []ir.Field{id}},
	}

	// SQLite expands a list into one placeholder per element; Postgres binds it
	// as one array, which is Expand left empty.
	statements := make([]ir.Statement, 0, len(queries))

	for i := range queries {
		sql := "SELECT id FROM things"
		args := make([]ir.Arg, 0, len(queries[i].Params))

		for j := range queries[i].Params {
			arg := ir.Arg{Name: queries[i].Params[j].Name}

			if queries[i].Params[j].Type.Slice && dialect == "sqlite" {
				arg.Expand, arg.Placeholder = "/*SLICE:stamps*/?", "?"
				sql += " WHERE stamp IN (" + arg.Expand + ")"
			}

			args = append(args, arg)
		}

		statements = append(statements, ir.Statement{Name: queries[i].Name, SQL: sql, Args: args})
	}

	layout := ""
	if dialect == "sqlite" {
		layout = timeLayout
	}

	return &ir.Package{
		Name:          "timefixturedb",
		Dialect:       dialect,
		Roster:        []string{"postgresql", "sqlite"},
		SQLCVersion:   "v1.31.1",
		UnisonVersion: "test",
		NullAs:        string(nullAs),
		TimeLayout:    layout,
		Queries:       queries,
		Statements:    statements,
	}
}

// TestEmitTimeArguments is the claim: on an engine that compares timestamps as
// text, every time argument is spelled by the generated code rather than by
// whatever the driver decides, so a caller's zone cannot move the comparison.
func TestEmitTimeArguments(t *testing.T) {
	t.Parallel()

	files := emitted(t, timeFixture(options.NullPointer, "sqlite"))
	queries := files["queries_sqlite_generated.go"]

	// The formatter itself: UTC first, then the stored shape.
	test.StrContains(t, queries, "func timeText(t time.Time) string {")
	test.StrContains(t, queries, `return t.UTC().Format("2006-01-02 15:04:05")`)

	// A nullable argument keeps its nil, or a column the caller left unset
	// would bind the zero time rather than NULL.
	test.StrContains(t, queries, "func timeTextPtr(t *time.Time) any {")
	test.StrContains(t, queries, "if t == nil {\n\t\treturn nil\n\t}")

	// Both scalar shapes wrapped at the bind site, and the type_override
	// untouched beside them.
	test.StrContains(t, queries, "q.thingsBetween,\n\t\ttimeText(arg.Since),\n\t\ttimeTextPtr(arg.Until),\n\t\targ.AsOf,\n\t)")

	// The same argument on a query that is not a :many, so the wrapping is a
	// property of the argument rather than of one command.
	test.StrContains(t, queries, "q.touchThing,\n\t\targ.ID,\n\t\ttimeText(arg.Since),\n\t)")

	// A list is bound one element at a time, so the formatter reaches the
	// element. The scope ahead of it is untouched.
	test.StrContains(t, queries, "args = append(args, arg.ID)")
	test.StrContains(t, queries, "for _, v := range arg.Stamps {\n\t\targs = append(args, timeText(v))\n\t}")

	// The database/sql nullable style has its own form, and it is the only one
	// emitted beside the value form.
	sqlStyle := emitted(t, timeFixture(options.NullSQL, "sqlite"))["queries_sqlite_generated.go"]
	test.StrContains(t, sqlStyle, "func timeTextNull(t sql.NullTime) any {")
	test.StrContains(t, sqlStyle, "if !t.Valid {\n\t\treturn nil\n\t}")
	test.StrContains(t, sqlStyle, "timeTextNull(arg.Until)")
	test.StrNotContains(t, sqlStyle, "func timeTextPtr(")
}

// TestEmitTimeArgumentsOnlyWhereTheEngineNeedsThem pins the other half: an
// engine with a timestamp type emits none of this, and the shared files do not
// move whether it is emitted or not.
func TestEmitTimeArgumentsOnlyWhereTheEngineNeedsThem(t *testing.T) {
	t.Parallel()

	text := emitted(t, timeFixture(options.NullPointer, "sqlite"))
	typed := emitted(t, timeFixture(options.NullPointer, "postgresql"))

	queries := typed["queries_postgresql_generated.go"]

	test.StrNotContains(t, queries, "func timeText(")
	test.StrNotContains(t, queries, "timeText(")
	test.StrContains(t, queries, "q.thingsBetween,\n\t\targ.Since,\n\t\targ.Until,\n\t\targ.AsOf,\n\t)")

	// A list Postgres binds whole is bound whole: there is no element to reach,
	// and wrapping the slice would not compile.
	test.StrContains(t, queries, "q.pickStamps,\n\t\targ.ID,\n\t\targ.Stamps,\n\t)")

	// The formatter is one dialect's business, so the shared files are still
	// byte-identical — which is the invariant every emitted file rests on.
	must.Eq(t, typed["db_generated.go"], text["db_generated.go"])
	must.Eq(t, typed["querier_generated.go"], text["querier_generated.go"])
	must.Eq(t, typed["types_generated.go"], text["types_generated.go"])
}

// TestEmittedTimeFixtureCompiles type-checks the formatter and its call sites.
//
// A helper that is emitted but never called, or called but never emitted, parses
// perfectly well either way.
func TestEmittedTimeFixtureCompiles(t *testing.T) {
	t.Parallel()

	for _, style := range []options.NullStyle{options.NullPointer, options.NullSQL} {
		t.Run(string(style), func(t *testing.T) {
			t.Parallel()

			files := map[string]string{}

			for _, dialect := range timeFixture(style, "").Roster {
				maps.Copy(files, emitted(t, timeFixture(style, dialect)))
			}

			compile(t, files)
		})
	}
}

// TestTimeBindersIgnoreWhatIsNotATime covers the two rules that decide nothing
// gets wrapped, from the side where they are decided.
func TestTimeBindersIgnoreWhatIsNotATime(t *testing.T) {
	t.Parallel()

	pkg := timeFixture(options.NullPointer, "sqlite")

	binders := timeBinders(pkg, &pkg.Queries[0])

	test.Eq(t, timeBinder{Value: "timeText"}, binders["since"])
	test.Eq(t, timeBinder{Value: "timeTextPtr"}, binders["until"])

	// A type_override is the consumer's own type over a time column. unison
	// does not know what it holds, and formatting it would be a guess.
	test.MapNotContainsKey(t, binders, "as_of")

	// A string parameter is not a time however the engine stores its dates.
	test.MapNotContainsKey(t, timeBinders(pkg, &pkg.Queries[1]), "id")
}
