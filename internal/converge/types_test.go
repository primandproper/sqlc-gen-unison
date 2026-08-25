package converge

import (
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// noOverride stands in for a config with no type_overrides.
func noOverride(string, string) (string, bool) { return "", false }

// TestMapTypeConverges is §8's table, exercised on the spellings the three
// engines actually report for the corpus's columns. Each group must land on one
// kind or the shared struct cannot exist.
func TestMapTypeConverges(t *testing.T) {
	t.Parallel()

	groups := map[ir.Kind][]string{
		ir.KindString: {"text", "varchar", "TEXT", "VARCHAR(255)", "character varying", "pg_catalog.text"},
		ir.KindTime:   {"timestamptz", "datetime", "DATETIME", "DATETIME(6)", "timestamp", "pg_catalog.timestamptz"},
		ir.KindBool:   {"bool", "boolean", "BOOLEAN", "tinyint", "pg_catalog.bool"},
		ir.KindInt64:  {"bigint", "integer", "INTEGER", "int8", "pg_catalog.int8", "smallint"},
		ir.KindBytes:  {"blob", "bytea", "BLOB"},
	}

	for kind, spellings := range groups {
		for _, spelling := range spellings {
			t.Run(kind.String()+"/"+spelling, func(t *testing.T) {
				t.Parallel()

				got, err := mapType("identity_users", "c", spelling, true, noOverride)

				must.NoError(t, err)
				test.Eq(t, kind, got.Kind)
				test.False(t, got.Nullable)
			})
		}
	}
}

// TestMapTypeReportsAnUnresolvedType checks that the error names the column and
// says how to fix it, because `any` is the common case on SQLite and reaches
// consumers.
func TestMapTypeReportsAnUnresolvedType(t *testing.T) {
	t.Parallel()

	_, err := mapType("", "result_limit", "any", false, noOverride)

	must.Error(t, err)
	test.StrContains(t, err.Error(), "result_limit")
	test.StrContains(t, err.Error(), "type_override")
}

// TestMapTypeOverrideReplacesNullabilityToo pins the decision that keeps a
// LIMIT argument convergent: an override is the final type, not a type the
// analyzer's nullability still modifies.
func TestMapTypeOverrideReplacesNullabilityToo(t *testing.T) {
	t.Parallel()

	override := func(string, string) (string, bool) { return "int64", true }

	// Nullable on Postgres and SQLite, NOT NULL on MySQL's bare placeholder —
	// the same override has to produce the same field from both.
	nullable, err := mapType("", "result_limit", "any", false, override)
	must.NoError(t, err)

	notNull, err := mapType("", "result_limit", "integer", true, override)
	must.NoError(t, err)

	must.Eq(t, nullable, notNull)
	test.Eq(t, "int64", nullable.Override)
	test.False(t, nullable.Nullable)
}
