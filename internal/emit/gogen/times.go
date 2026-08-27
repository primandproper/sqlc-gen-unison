package gogen

import (
	"fmt"
	"strings"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"
	"github.com/primandproper/sqlc-gen-unison/internal/options"
)

// The functions a time argument is wrapped in before it is bound, named here
// once so that the emission and the call sites cannot spell them differently.
const (
	timeTextFunc     = "timeText"
	timeTextPtrFunc  = "timeTextPtr"
	timeTextNullFunc = "timeTextNull"
)

// timeBinder says how one parameter's value reaches the driver.
//
// A scalar is wrapped whole and a list is wrapped an element at a time, and the
// two are separate fields rather than one so that neither call site has to
// re-derive which it is holding. Both empty is the ordinary case: the argument
// binds as it stands.
type timeBinder struct {
	// Value wraps the whole field, for a parameter that is one time.
	Value string

	// Element wraps one element, for a parameter that is a list of times. A
	// list is bound one element per placeholder, so there is nothing to wrap
	// whole.
	Element string
}

// timeBinders indexes a query's parameters by how each one's time has to be
// spelled for this dialect.
//
// Nothing is indexed at all when the dialect has a timestamp type, which is
// every dialect but SQLite — so this is empty on two engines out of three and
// the emitted code there is exactly what it was.
//
// A type_override is left alone deliberately. It is the consumer's own type,
// unison does not know that it holds a time, and a consumer who names one has
// said they will render it themselves.
func timeBinders(pkg *ir.Package, query *ir.Query) map[string]timeBinder {
	if pkg.TimeLayout == "" {
		return nil
	}

	binders := make(map[string]timeBinder, len(query.Params))

	for i := range query.Params {
		field := &query.Params[i]

		if field.Type.Override != "" || field.Type.Kind != ir.KindTime {
			continue
		}

		switch {
		case field.Type.Slice:
			// A list's elements are plain times: goType wraps the slice, never
			// its elements, because a nil slice and an empty one already say
			// everything a nullable list could.
			binders[field.Name] = timeBinder{Element: timeTextFunc}
		case !field.Type.Nullable:
			binders[field.Name] = timeBinder{Value: timeTextFunc}
		case options.NullStyle(pkg.NullAs) == options.NullSQL:
			binders[field.Name] = timeBinder{Value: timeTextNullFunc}
		default:
			binders[field.Name] = timeBinder{Value: timeTextPtrFunc}
		}
	}

	return binders
}

// bindValue renders what a placeholder binds: the field, or the field spelled
// as text.
func bindValue(field string, binder timeBinder) string {
	if binder.Value == "" {
		return field
	}

	return binder.Value + "(" + field + ")"
}

// bindElement renders what one placeholder of an expanded list binds.
func bindElement(element string, binder timeBinder) string {
	if binder.Element == "" {
		return element
	}

	return binder.Element + "(" + element + ")"
}

// writeTimeHelpers emits the formatter every time argument in this package goes
// through, and only the forms this package actually uses.
//
// It lives in the dialect's own file rather than in the shared ones because it
// is the half of the output that is allowed to differ: the dialects that have a
// timestamp type emit none of it, and the shared files stay byte-identical
// either way.
func writeTimeHelpers(b *strings.Builder, pkg *ir.Package, imports *importSet) {
	if pkg.TimeLayout == "" {
		return
	}

	var (
		value bool
		ptr   bool
		null  bool
	)

	for i := range pkg.Queries {
		binders := timeBinders(pkg, &pkg.Queries[i])

		for name := range binders {
			binder := binders[name]

			switch {
			case binder.Element != "" || binder.Value == timeTextFunc:
				value = true
			case binder.Value == timeTextPtrFunc:
				ptr = true
			case binder.Value == timeTextNullFunc:
				null = true
			}
		}
	}

	// The nullable forms are written in terms of the value form, so either one
	// pulls it in.
	value = value || ptr || null

	if !value {
		return
	}

	imports.add("time")

	fmt.Fprintf(b, `// %s renders a time argument as the text this engine stores timestamps as.
//
// This engine has no date type: a DATETIME column holds text, and a comparison
// between two of them compares two strings. What a driver writes for a bound
// time.Time is Go's own rendering of it, whose leading characters happen to be
// the stored shape — but only while the value is UTC. A time carrying any other
// zone puts its own wall clock in those leading characters, so every window
// comparison is off by that offset, silently, and only for the callers whose
// clock is not UTC.
//
// Converting here is what makes the bound text the stored shape by
// construction rather than by accident, whatever zone the caller's time
// carries. The layout is whole seconds, which is what CURRENT_TIMESTAMP writes,
// so a bound value and a stored one are one shape rather than two that sort
// alike — which does mean a sub-second time bound here is stored truncated to
// the second on this engine.
func %s(t time.Time) string {
	return t.UTC().Format(%q)
}

`, timeTextFunc, timeTextFunc, pkg.TimeLayout)

	if ptr {
		fmt.Fprintf(b, `// %s is %s for a nullable argument. It preserves nil, so a
// field the caller left unset still binds NULL rather than the zero time.
func %s(t *time.Time) any {
	if t == nil {
		return nil
	}

	return %s(*t)
}

`, timeTextPtrFunc, timeTextFunc, timeTextPtrFunc, timeTextFunc)
	}

	if null {
		imports.add("database/sql")

		fmt.Fprintf(b, `// %s is %s for a nullable argument under the database/sql
// nullable style. An invalid one binds NULL, which is what it says.
func %s(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}

	return %s(t.Time)
}

`, timeTextNullFunc, timeTextFunc, timeTextNullFunc, timeTextFunc)
	}
}
