// Package options carries the settings a consumer writes in unison.yaml through
// to the plugin.
//
// It exists as its own package because both ends need to agree: the orchestrator
// serializes these into each rendered sqlc config, and the plugin parses them
// back out of the CodeGenRequest. One definition means the two cannot drift.
//
// Everything here is part of the convergence contract. Every invocation receives
// the same options, so any decision made from them is made identically by every
// dialect — which is what lets N runs write byte-identical shared files.
package options

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// NullStyle selects how a nullable column reaches Go.
type NullStyle string

const (
	// NullPointer renders a nullable column as a pointer: *string, *time.Time.
	NullPointer NullStyle = "pointer"
	// NullSQL renders it as the database/sql wrapper: sql.NullString.
	NullSQL NullStyle = "sql"
)

// Options is the plugin's half of unison.yaml, and everything in it is part of
// the convergence contract: every invocation receives the same options, so any
// decision made from them is made identically by every dialect.
//
// The field documentation lives here rather than beside each field because this
// repository's formatter runs fieldalignment, which reorders struct fields and
// does not carry their comments along.
//
//   - Package names the Go package the emitted files declare.
//
//   - Roster is every dialect in the run, sorted, and it is the reason
//     convergent emission works: an invocation only ever analyzes one dialect,
//     but it emits a constructor that switches over all of them. The
//     orchestrator fills it from the keys of unison.yaml's `schemas:` map, so
//     the roster and the schemas cannot disagree. It is not settable from the
//     options block.
//
//   - TablePrefixVar asks for the {{prefix}} marker of §9.
//
//   - NullAs selects the nullable representation. Empty means NullPointer.
//
//   - TypeOverrides replace what the analyzer inferred, and serve two jobs that
//     turn out to be one: naming a domain type for a column whose database type
//     is merely its storage, and supplying a type where an analyzer resolved
//     none. SQLite is the weak engine §8 warns about, but it is not the only
//     one — Postgres types a COALESCE'd LIMIT as `any` too. An override is the
//     final type, nullability included; write *int64 to get a pointer.
//
//   - RenameParams maps, per dialect, the name an analyzer reported onto the
//     name the shared shape uses. It exists because one divergence in §7's
//     table has no authoring-side fix: MySQL accepts only a bare placeholder in
//     LIMIT — sqlc.arg() is a syntax error there — and names that parameter
//     `limit`, while Postgres rejects `limit` as an argument name because it is
//     reserved. No spelling satisfies all three engines, so the name converges
//     here instead of in the .sql. It is deliberately not a general reconciler:
//     it renames, and cannot change a type, add a parameter, or reorder
//     anything. A genuine shape divergence still diverges, and still fails to
//     compile.
type Options struct {
	RenameParams   map[string]map[string]string `json:"rename_params"    yaml:"rename_params"`
	Package        string                       `json:"package"          yaml:"-"`
	NullAs         NullStyle                    `json:"null_as"          yaml:"null_as"`
	Roster         []string                     `json:"roster"           yaml:"-"`
	TypeOverrides  []TypeOverride               `json:"type_overrides"   yaml:"type_overrides"`
	TablePrefixVar bool                         `json:"table_prefix_var" yaml:"table_prefix_var"`
}

// TypeOverride replaces the type unison would otherwise infer for a column.
//
// Column is matched as `table.column`, or `*.column` to match by name wherever
// it appears — which is the form a parameter needs, since a parameter built from
// an expression belongs to no table.
type TypeOverride struct {
	Column string `json:"column"  yaml:"column"`
	GoType string `json:"go_type" yaml:"go_type"`
}

// Normalize validates and canonicalizes options assembled by the orchestrator,
// which reads them from YAML rather than off the wire. Both paths land here, so
// a config that the orchestrator accepts is one the plugin will accept.
func (o *Options) Normalize() error {
	return o.normalize()
}

// Parse decodes the options sqlc forwarded from the consumer's config.
//
// An empty payload is an error rather than a default. Every field that makes
// emission convergent arrives this way, and a plugin that silently generated for
// a roster of one because the options were missing would produce code that
// compiles and is wrong.
func Parse(raw []byte) (*Options, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("unison: no plugin options were supplied; at minimum `package` and `roster` are required")
	}

	var opts Options

	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&opts); err != nil {
		return nil, fmt.Errorf("unison: reading plugin options: %w", err)
	}

	if err := opts.normalize(); err != nil {
		return nil, err
	}

	return &opts, nil
}

// normalize validates and puts every collection in a canonical order, so that
// nothing downstream has to remember to sort.
func (o *Options) normalize() error {
	if strings.TrimSpace(o.Package) == "" {
		return fmt.Errorf("unison: `package` is required; it names the Go package the emitted files declare")
	}

	if len(o.Roster) == 0 {
		return fmt.Errorf("unison: `roster` is required; it is every dialect in the run, and the shared files are emitted from it")
	}

	o.Roster = slices.Clone(o.Roster)
	slices.Sort(o.Roster)

	if len(slices.Compact(slices.Clone(o.Roster))) != len(o.Roster) {
		return fmt.Errorf("unison: `roster` names a dialect twice: %v", o.Roster)
	}

	switch o.NullAs {
	case "":
		o.NullAs = NullPointer
	case NullPointer, NullSQL:
	default:
		return fmt.Errorf("unison: unknown null_as %q: want %q or %q", o.NullAs, NullPointer, NullSQL)
	}

	slices.SortFunc(o.TypeOverrides, func(a, b TypeOverride) int {
		return strings.Compare(a.Column, b.Column)
	})

	for i := range o.TypeOverrides {
		override := &o.TypeOverrides[i]

		if override.Column == "" || override.GoType == "" {
			return fmt.Errorf("unison: a type_override needs both `column` and `go_type`, got %+v", override)
		}
	}

	return nil
}

// Renames reports the rename map for one dialect.
func (o *Options) Renames(dialect string) map[string]string {
	return o.RenameParams[dialect]
}

// OverrideFor reports the Go type configured for a column, if any.
//
// A `table.column` match wins over a `*.column` match, so a general rule can be
// stated once and contradicted in the one place it is wrong.
func (o *Options) OverrideFor(table, column string) (string, bool) {
	var wildcard string

	for i := range o.TypeOverrides {
		override := &o.TypeOverrides[i]

		switch override.Column {
		case table + "." + column:
			return override.GoType, true
		case "*." + column:
			wildcard = override.GoType
		}
	}

	if wildcard != "" {
		return wildcard, true
	}

	return "", false
}
