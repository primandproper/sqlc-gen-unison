package converge

import (
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"
)

// TestListOfRecognizesBothStaticForms is the convergence claim of this feature,
// stated at the level it is decided.
//
// The three engines report a list two structurally different ways — Postgres an
// array column, MySQL and SQLite a slice marked for expansion — and both have to
// arrive here as a list, because both produce the same []T field. The spellings
// below are what sqlc v1.31.1 actually sends; they were read off a dumped
// CodeGenRequest rather than guessed.
func TestListOfRecognizesBothStaticForms(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		column *pb.Column
		want   listKind
	}{
		"postgres binds an array": {
			column: &pb.Column{IsArray: true, ArrayDims: 1, Type: &pb.Identifier{Name: "text"}},
			want:   boundList,
		},
		"mysql expands placeholders": {
			column: &pb.Column{IsSqlcSlice: true, Type: &pb.Identifier{Name: "varchar"}},
			want:   expandedList,
		},
		"sqlite expands placeholders": {
			column: &pb.Column{IsSqlcSlice: true, Type: &pb.Identifier{Name: "TEXT"}},
			want:   expandedList,
		},
		"an ordinary parameter is not a list": {
			column: &pb.Column{Type: &pb.Identifier{Name: "text"}},
			want:   notAList,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := listOf(testCase.column)

			must.NoError(t, err)
			test.Eq(t, testCase.want, got)

			// Whichever form it arrived in, the shared field holds the element
			// type — which is the type the other two engines report for the
			// same list, and therefore the reason one field can serve all three.
			test.Eq(t, testCase.column.GetType().GetName(), elementTypeName(testCase.column))
		})
	}
}

// TestListOfRefusesNestedArrays covers the one array shape that cannot converge:
// only Postgres has arrays at all, and the other engines reach a list through
// placeholder expansion, which has exactly one dimension.
func TestListOfRefusesNestedArrays(t *testing.T) {
	t.Parallel()

	_, err := listOf(&pb.Column{IsArray: true, ArrayDims: 2, Type: &pb.Identifier{Name: "text"}})

	must.Error(t, err)
	test.StrContains(t, err.Error(), "2-dimensional")
}

// TestSliceMarkerMatchesTheAnalyzedText pins the reconstruction against the real
// thing.
//
// unison does not parse SQL, so it spells sqlc's expansion site rather than
// finding it. The statement below is sqlc's own output for
// `IN (sqlc.slice(user_ids))`, and if the two ever stop agreeing this is where
// it shows up — rather than as a statement that runs with an unexpanded
// placeholder.
func TestSliceMarkerMatchesTheAnalyzedText(t *testing.T) {
	t.Parallel()

	const analyzed = "SELECT role FROM identity_user_roles\nWHERE user_id IN (/*SLICE:user_ids*/?)"

	marker := sliceMarker("user_ids")

	test.StrContains(t, analyzed, marker)
	must.NoError(t, checkExpansions(analyzed, []ir.Arg{
		{Name: "user_ids", Expand: marker, Placeholder: slicePlaceholder},
	}))
}

// TestCheckExpansionsRefusesAMissingSite is the guard on that reconstruction.
//
// Expansion is one string replacement against a token this package spelled
// rather than found. A statement that does not carry exactly one of them would
// otherwise run with a single `?` bound to a whole slice.
func TestCheckExpansionsRefusesAMissingSite(t *testing.T) {
	t.Parallel()

	err := checkExpansions("SELECT role FROM identity_user_roles WHERE user_id IN (?)", []ir.Arg{
		{Name: "user_ids", Expand: sliceMarker("user_ids"), Placeholder: slicePlaceholder},
	})

	must.Error(t, err)
	test.StrContains(t, err.Error(), "user_ids")
	test.StrContains(t, err.Error(), "expansion site")
}

// TestCheckExpansionsRequiresTheListToBindLast is the trap this feature would
// otherwise ship, and it is silent on SQLite.
//
// Every placeholder an expansion produces is a bare `?`, which SQLite numbers as
// one past the highest index it has seen. So a parameter after the list collides
// with an element of it, matches nothing, and reports no error — verified
// against a real SQLite before this check was written. Refusing at generate time
// is the only place the divergence can still be named.
func TestCheckExpansionsRequiresTheListToBindLast(t *testing.T) {
	t.Parallel()

	const analyzed = "SELECT role FROM identity_user_roles\nWHERE user_id IN (/*SLICE:user_ids*/?) AND scope = ?2"

	err := checkExpansions(analyzed, []ir.Arg{
		{Name: "user_ids", Expand: sliceMarker("user_ids"), Placeholder: slicePlaceholder},
		{Name: "scope"},
	})

	must.Error(t, err)
	test.StrContains(t, err.Error(), "user_ids")
	test.StrContains(t, err.Error(), "last one")
}

// TestCheckExpansionsAcceptsAListAfterOtherParameters is the ordering that
// works, and the one the corpus uses: the scalar predicates first, the list's
// clause at the end.
func TestCheckExpansionsAcceptsAListAfterOtherParameters(t *testing.T) {
	t.Parallel()

	const analyzed = "SELECT role FROM identity_user_roles\nWHERE scope = ?1 AND user_id IN (/*SLICE:user_ids*/?)"

	must.NoError(t, checkExpansions(analyzed, []ir.Arg{
		{Name: "scope"},
		{Name: "user_ids", Expand: sliceMarker("user_ids"), Placeholder: slicePlaceholder},
	}))
}

// TestCheckExpansionsIgnoresStatementsWithoutLists keeps the check off the path
// every other query takes: Postgres never expands, and neither does a query
// whose parameters are all scalars.
func TestCheckExpansionsIgnoresStatementsWithoutLists(t *testing.T) {
	t.Parallel()

	must.NoError(t, checkExpansions("SELECT role FROM identity_user_roles WHERE user_id = ANY($1) AND scope = $2",
		[]ir.Arg{{Name: "user_ids"}, {Name: "scope"}}))
}
