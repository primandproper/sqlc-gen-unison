package converge

import (
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// TestParseCommandAccepts covers the annotations §3 puts in scope.
func TestParseCommandAccepts(t *testing.T) {
	t.Parallel()

	cases := map[string]ir.Command{
		":exec":       ir.CommandExec,
		":one":        ir.CommandOne,
		":many":       ir.CommandMany,
		":execrows":   ir.CommandExecRows,
		":execresult": ir.CommandExecResult,
	}

	for annotation, expected := range cases {
		t.Run(annotation, func(t *testing.T) {
			t.Parallel()

			got, err := parseCommand(annotation)

			must.NoError(t, err)
			test.Eq(t, expected, got)
		})
	}
}

// TestParseCommandRefusesCopyFrom pins §3's v1 exclusion, and the reason.
//
// :copyfrom is Postgres-only, so a query using it cannot have the same shape on
// the other two engines — it is shape-divergent by construction rather than by
// accident, which is the one thing unison refuses outright. The error says where
// such a statement should live instead, because "unsupported" without a
// destination is not advice.
func TestParseCommandRefusesCopyFrom(t *testing.T) {
	t.Parallel()

	_, err := parseCommand(":copyfrom")

	must.Error(t, err)
	test.StrContains(t, err.Error(), "Postgres-only")
	test.StrContains(t, err.Error(), "hand-written")
}

// TestParseCommandRefusesBatchAndUnknown keeps the supported set closed.
//
// Batch annotations are the other half of §3's v1 exclusion. An unrecognized
// annotation is refused the same way rather than defaulting to :exec, which
// would silently discard a query's results.
func TestParseCommandRefusesBatchAndUnknown(t *testing.T) {
	t.Parallel()

	for _, annotation := range []string{":batchexec", ":batchmany", ":batchone", ":nonsense", ""} {
		t.Run(annotation, func(t *testing.T) {
			t.Parallel()

			_, err := parseCommand(annotation)

			must.Error(t, err)
			test.StrContains(t, err.Error(), ":execresult")
		})
	}
}
