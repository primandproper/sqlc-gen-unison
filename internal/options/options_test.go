package options_test

import (
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/options"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestParse(t *testing.T) {
	t.Parallel()

	opts, err := options.Parse([]byte(`{
		"package": "identitydb",
		"roster": ["sqlite", "mysql", "postgresql"],
		"table_prefix_var": true,
		"rename_params": {"mysql": {"limit": "result_limit"}},
		"type_overrides": [{"column": "*.result_limit", "go_type": "int64"}]
	}`))
	must.NoError(t, err)

	test.Eq(t, "identitydb", opts.Package)
	test.True(t, opts.TablePrefixVar)

	// The roster is sorted on the way in, so no emitter has to remember to do
	// it and every dialect's invocation sees the same order.
	test.Eq(t, []string{"mysql", "postgresql", "sqlite"}, opts.Roster)

	// Pointers are the default nullable representation.
	test.Eq(t, options.NullPointer, opts.NullAs)

	test.Eq(t, "result_limit", opts.Renames("mysql")["limit"])
	test.MapEmpty(t, opts.Renames("postgresql"))
}

// TestParseRejectsAnEmptyPayload pins the decision not to default.
//
// Every field that makes emission convergent arrives through options. A plugin
// that quietly generated for a roster of one because the options went missing
// would emit a package that compiles and is wrong, which is the outcome this
// tool exists to prevent.
func TestParseRejectsAnEmptyPayload(t *testing.T) {
	t.Parallel()

	_, err := options.Parse(nil)

	must.Error(t, err)
}

func TestParseRejectsBadInput(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"no package":        `{"roster": ["mysql"]}`,
		"no roster":         `{"package": "db"}`,
		"a repeated roster": `{"package": "db", "roster": ["mysql", "mysql"]}`,
		"an unknown null":   `{"package": "db", "roster": ["mysql"], "null_as": "maybe"}`,
		"an unknown field":  `{"package": "db", "roster": ["mysql"], "typo": 1}`,
		"a half override":   `{"package": "db", "roster": ["mysql"], "type_overrides": [{"column": "*.x"}]}`,
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := options.Parse([]byte(payload))

			must.Error(t, err)
		})
	}
}

// TestOverrideForPrefersTheSpecificRule lets a general rule be stated once and
// contradicted in the one place it is wrong.
func TestOverrideForPrefersTheSpecificRule(t *testing.T) {
	t.Parallel()

	opts, err := options.Parse([]byte(`{
		"package": "db",
		"roster": ["mysql"],
		"type_overrides": [
			{"column": "*.scope", "go_type": "tenancy.Scope"},
			{"column": "identity_users.scope", "go_type": "users.Scope"}
		]
	}`))
	must.NoError(t, err)

	specific, ok := opts.OverrideFor("identity_users", "scope")
	must.True(t, ok)
	test.Eq(t, "users.Scope", specific)

	wildcard, ok := opts.OverrideFor("identity_accounts", "scope")
	must.True(t, ok)
	test.Eq(t, "tenancy.Scope", wildcard)

	_, ok = opts.OverrideFor("identity_users", "username")
	test.False(t, ok)
}
