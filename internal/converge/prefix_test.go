package converge

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// TestMarkPrefixes covers §9: the marker is placed from the catalog's knowledge
// of which tokens are tables, not from a regex guessing at query time.
func TestMarkPrefixes(t *testing.T) {
	t.Parallel()

	tables := []string{"identity_user_roles", "identity_users", "users"}

	cases := map[string]struct {
		sql      string
		expected string
	}{
		"a bare table name": {
			sql:      "SELECT id FROM identity_users",
			expected: "SELECT id FROM {{prefix}}identity_users",
		},
		// The qualifier half of a qualified column is the table, and needs the
		// marker; the column half does not.
		"a qualified column": {
			sql:      "SELECT identity_users.id FROM identity_users",
			expected: "SELECT {{prefix}}identity_users.id FROM {{prefix}}identity_users",
		},
		// identity_users is a prefix of identity_user_roles only by accident of
		// spelling; a whole-word match must not split the longer name.
		"a longer name that starts with a shorter one": {
			sql:      "SELECT * FROM identity_user_roles",
			expected: "SELECT * FROM {{prefix}}identity_user_roles",
		},
		"a word that merely contains a table name": {
			sql:      "SELECT archived_users_count FROM identity_users",
			expected: "SELECT archived_users_count FROM {{prefix}}identity_users",
		},
		"a column whose name is not a table": {
			sql:      "SELECT username FROM identity_users",
			expected: "SELECT username FROM {{prefix}}identity_users",
		},
		"nothing to mark": {
			sql:      "SELECT 1",
			expected: "SELECT 1",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			test.Eq(t, tc.expected, markPrefixes(quietLogger(), "Q", tc.sql, tables))
		})
	}
}

// TestMarkPrefixesLeavesStringLiteralsAloneAndWarns is §9's documented sharp
// edge.
//
// A string literal that spells a table name is indistinguishable from an
// identifier to a whole-word scan. unison does not parse SQL and will not start
// doing so for this, so the literal is left as written and the query is named in
// a warning. Rewriting it would corrupt data; silently skipping it would hide a
// real collision.
func TestMarkPrefixesLeavesStringLiteralsAloneAndWarns(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

	got := markPrefixes(logger, "AuditLookup",
		"SELECT id FROM identity_users WHERE table_name = 'identity_users'",
		[]string{"identity_users"})

	test.Eq(t, "SELECT id FROM {{prefix}}identity_users WHERE table_name = 'identity_users'", got)

	warning := logs.String()

	test.StrContains(t, warning, "AuditLookup")
	test.StrContains(t, warning, "identity_users")
}

// TestStringLiteralSpansHandlesDoubledQuotes checks the one escape all three
// engines share, so that a literal containing a quote does not swallow the rest
// of the statement.
func TestStringLiteralSpansHandlesDoubledQuotes(t *testing.T) {
	t.Parallel()

	sql := "SELECT 'it''s here' FROM identity_users"

	got := markPrefixes(quietLogger(), "Q", sql, []string{"identity_users"})

	must.Eq(t, "SELECT 'it''s here' FROM {{prefix}}identity_users", got)
}

// quietLogger discards, for the cases where the warning is not what is under
// test.
func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
