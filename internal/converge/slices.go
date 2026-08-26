package converge

import (
	"fmt"
	"strings"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"

	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"
)

// listKind says how a dialect binds a variable-length list.
//
// This is §7's remaining row, and it is the one divergence where the *generated
// code* differs rather than only the statement text. The two static forms sqlc
// blesses are structurally different: Postgres binds one array parameter, and
// MySQL and SQLite have no array to bind, so sqlc leaves an expansion site in
// the text for a generator to turn into one placeholder per element.
//
// Both still converge onto one shared []T field, which is why this is a
// convergence and not a reconciliation: nothing here pairs two queries, merges
// two shapes, or picks a winner. Each dialect emits the binding its own analysis
// described, against a params struct all three compute identically.
type listKind uint8

const (
	// notAList is an ordinary scalar parameter.
	notAList listKind = iota

	// boundList is Postgres's `= ANY($1::text[])`: the whole slice is one bound
	// array parameter, and the driver renders it.
	boundList

	// expandedList is MySQL's and SQLite's `IN (sqlc.slice(ids))`: the text
	// carries an expansion site, and the generated code replaces it with one
	// placeholder per element before it binds them.
	expandedList
)

// slicePlaceholder is what one element's placeholder looks like on the engines
// that expand.
//
// Both of them accept a bare `?` — it is the only form MySQL has, and SQLite
// numbers one implicitly — which is also why an expansion has to come last; see
// checkExpansions.
const slicePlaceholder = "?"

// sliceMarker spells the expansion site sqlc leaves in an analyzed statement.
//
// The spelling is sqlc's, not unison's: this is the text already sitting in the
// statement, reconstructed here so the emitter can name it in a replacement.
// Reconstructing it rather than scanning for it is deliberate — unison does not
// parse SQL — and checkExpansions confirms the reconstruction found its mark
// rather than trusting it.
//
// The name is the one the analyzer reported, before any rename_params, because
// the marker was written from the analyzer's own name.
func sliceMarker(reportedName string) string {
	return "/*SLICE:" + reportedName + "*/" + slicePlaceholder
}

// listOf reports how this dialect binds a parameter, and refuses the shapes that
// have no converged spelling.
func listOf(column *pb.Column) (listKind, error) {
	switch {
	case column.GetIsSqlcSlice():
		return expandedList, nil

	case column.GetIsArray():
		// An array of arrays is a Postgres type with no counterpart on the
		// other two, so there is no shared field that could be honest about it.
		if dims := column.GetArrayDims(); dims > 1 {
			return notAList, fmt.Errorf(
				"is a %d-dimensional array, and only a flat list converges. "+
					"Only Postgres has arrays at all; the other engines reach a list through placeholder expansion, "+
					"which has one dimension", dims)
		}

		return boundList, nil

	default:
		return notAList, nil
	}
}

// elementTypeName reports the database type one element of a parameter carries.
//
// It is deliberately not columnTypeName, which marks an array by suffixing `[]`
// so that a *projection* of one can be refused by name. A list parameter is not
// refused — it is the shape this converges — and what its shared field holds is
// the element type, which is what the other two engines report for the same
// list.
func elementTypeName(column *pb.Column) string {
	if column == nil {
		return ""
	}

	return column.GetType().GetName()
}

// checkExpansions refuses the two ways an expansion site would silently bind the
// wrong values.
//
// The first is a marker that is not there, or is there twice. Expansion is one
// string replacement against a token this package reconstructed rather than
// found, so the reconstruction is checked: a statement that does not carry
// exactly one of its own markers would run with an unexpanded `?` and bind a
// slice to it.
//
// The second is the trap, and it is silent on SQLite. Every placeholder in an
// expansion is a bare `?`, which SQLite numbers as one past the highest index it
// has seen — so an expansion followed by another parameter collides with it:
//
//	WHERE id IN (?,?) AND scope = ?2
//
// binds the second id where scope belongs, returns no rows, and reports no
// error. MySQL, whose placeholders are all positional, is unharmed either way.
// Rather than emit code that is correct on one expanding engine and wrong on the
// other, unison requires the list to bind the last placeholder — which is where
// an `IN` clause naturally reads anyway, and which the shared parameter order
// then forces on every other dialect.
func checkExpansions(text string, args []ir.Arg) error {
	for i := range args {
		arg := &args[i]

		if arg.Expand == "" {
			continue
		}

		if found := strings.Count(text, arg.Expand); found != 1 {
			return fmt.Errorf(
				"list parameter %q should have left exactly one expansion site in the statement, but the analyzed text carries %d. "+
					"unison reads the analysis rather than parsing SQL, so it cannot place one itself",
				arg.Name, found)
		}

		if i != len(args)-1 {
			return fmt.Errorf(
				"list parameter %q binds placeholder %d of %d, and a list has to bind the last one. "+
					"SQLite numbers each expanded placeholder one past the highest it has seen, so a parameter after the list "+
					"collides with an element of it and silently matches nothing. Move the list's clause to the end of the statement",
				arg.Name, i+1, len(args))
		}
	}

	return nil
}
