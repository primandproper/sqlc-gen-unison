package gogen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"
	"github.com/primandproper/sqlc-gen-unison/internal/options"
)

// goType renders an IR type as Go source, recording any import it needs.
func goType(t ir.Type, nullAs string, imports *importSet) (string, error) {
	// A list is one field holding many values, and the nullable question does
	// not arise: a nil slice and an empty one both match nothing, on every
	// dialect. So the slice wraps the element type and nothing else changes —
	// which is what makes the shared field the same []T whether the dialect
	// underneath binds an array or expands placeholders.
	if t.Slice {
		element := t
		element.Slice = false

		rendered, err := goType(element, nullAs, imports)
		if err != nil {
			return "", err
		}

		return "[]" + rendered, nil
	}

	if t.Override != "" {
		return renderOverride(t.Override, imports)
	}

	// A byte slice is already nil-able, so wrapping it would add a second way
	// to say the same thing.
	if t.Kind == ir.KindBytes {
		return "[]byte", nil
	}

	if !t.Nullable {
		return plainType(t.Kind, imports)
	}

	if options.NullStyle(nullAs) == options.NullSQL {
		imports.add("database/sql")

		return sqlNullType(t.Kind)
	}

	plain, err := plainType(t.Kind, imports)
	if err != nil {
		return "", err
	}

	return "*" + plain, nil
}

// plainType renders a non-null kind.
func plainType(kind ir.Kind, imports *importSet) (string, error) {
	switch kind {
	case ir.KindString:
		return "string", nil
	case ir.KindInt64:
		return "int64", nil
	case ir.KindFloat64:
		return "float64", nil
	case ir.KindBool:
		return "bool", nil
	case ir.KindTime:
		imports.add("time")

		return "time.Time", nil
	case ir.KindBytes:
		return "[]byte", nil
	case ir.KindInvalid:
		return "", fmt.Errorf("unison: a field reached the emitter with no type")
	default:
		return "", fmt.Errorf("unison: a field reached the emitter with an unknown type %d", kind)
	}
}

// sqlNullType renders the database/sql wrapper for a kind.
func sqlNullType(kind ir.Kind) (string, error) {
	switch kind {
	case ir.KindString:
		return "sql.NullString", nil
	case ir.KindInt64:
		return "sql.NullInt64", nil
	case ir.KindFloat64:
		return "sql.NullFloat64", nil
	case ir.KindBool:
		return "sql.NullBool", nil
	case ir.KindTime:
		return "sql.NullTime", nil
	case ir.KindBytes, ir.KindInvalid:
		return "", fmt.Errorf("unison: %s has no database/sql null wrapper", kind)
	default:
		return "", fmt.Errorf("unison: %s has no database/sql null wrapper", kind)
	}
}

// renderOverride turns a configured type into Go source and records its import.
//
// A path-qualified type — github.com/org/mod/tenancy.Scope — becomes
// tenancy.Scope with github.com/org/mod/tenancy imported. Anything without a
// slash is taken as written, which covers the builtins. A dotted name with no
// path is refused rather than guessed at: there is no way to know which module
// a bare `tenancy.Scope` means, and importing the wrong one produces a compile
// error a consumer would have to trace back to their config.
func renderOverride(override string, imports *importSet) (string, error) {
	override = strings.TrimSpace(override)

	// A pointer override is the consumer asking for a nullable field, and the
	// star travels with the type rather than through it.
	if pointee, ok := strings.CutPrefix(override, "*"); ok {
		rendered, err := renderOverride(pointee, imports)
		if err != nil {
			return "", err
		}

		return "*" + rendered, nil
	}

	if !strings.Contains(override, "/") {
		if strings.Contains(override, ".") {
			return "", fmt.Errorf(
				"unison: the type_override %q names a package but no import path. "+
					"Write it in full, as in github.com/org/module/tenancy.Scope", override)
		}

		return override, nil
	}

	dot := strings.LastIndex(override, ".")
	if dot < strings.LastIndex(override, "/") {
		return "", fmt.Errorf(
			"unison: the type_override %q looks like an import path with no type on the end. "+
				"Write it as <import path>.<type>", override)
	}

	importPath := override[:dot]
	typeName := override[dot+1:]

	pkg := importPath[strings.LastIndex(importPath, "/")+1:]

	imports.add(importPath)

	return pkg + "." + typeName, nil
}

// importSet collects import paths and renders them in one deterministic order.
type importSet struct {
	paths map[string]struct{}
}

// newImportSet returns an empty set.
func newImportSet() *importSet {
	return &importSet{paths: map[string]struct{}{}}
}

// add records an import path.
func (s *importSet) add(path string) {
	s.paths[path] = struct{}{}
}

// render writes the import block, standard library first and everything else
// after, each group sorted. Iterating the map directly would be the shortest
// way to make regeneration produce different bytes on different runs.
func (s *importSet) render() string {
	if len(s.paths) == 0 {
		return ""
	}

	var std, other []string

	for path := range s.paths {
		if isStdlib(path) {
			std = append(std, path)

			continue
		}

		other = append(other, path)
	}

	sort.Strings(std)
	sort.Strings(other)

	var b strings.Builder

	b.WriteString("import (\n")

	for _, path := range std {
		fmt.Fprintf(&b, "\t%q\n", path)
	}

	if len(std) > 0 && len(other) > 0 {
		b.WriteString("\n")
	}

	for _, path := range other {
		fmt.Fprintf(&b, "\t%q\n", path)
	}

	b.WriteString(")\n\n")

	return b.String()
}

// isStdlib reports whether an import path belongs to the standard library. A
// path with no dot in its first element is the rule the toolchain itself uses.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")

	return !strings.Contains(first, ".")
}
