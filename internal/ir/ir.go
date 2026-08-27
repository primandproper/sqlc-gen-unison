// Package ir is unison's language-neutral intermediate representation.
//
// It is the seam §5 asks for. The hard half of this tool — roster handling,
// convergent shape computation, divergence policy, prefix markers — has nothing
// to do with Go, so it produces one of these and an emitter renders it. v1 ships
// exactly one emitter and a second language is out of scope, but keeping the
// core honest about what it knows is what makes the emitter replaceable rather
// than entangled.
//
// Nothing here imports the plugin protocol, and nothing here names a Go type.
// The one exception is Type.Override, which is deliberately opaque: it carries a
// string the consumer wrote in their config for an emitter to interpret.
package ir

// Kind is a type as unison understands it, independent of both the engine that
// reported it and the language that will render it.
//
// The set is small on purpose. It is the intersection worth converging: the
// types the three engines agree about often enough that a shared struct is
// honest. Anything outside it is a type_override or an error, never a guess.
type Kind uint8

const (
	// KindInvalid is the zero value and never appears in a valid Package.
	KindInvalid Kind = iota
	KindString
	KindInt64
	KindFloat64
	KindBool
	KindTime
	KindBytes
)

// String names the kind for diagnostics.
func (k Kind) String() string {
	switch k {
	case KindString:
		return "string"
	case KindInt64:
		return "int64"
	case KindFloat64:
		return "float64"
	case KindBool:
		return "bool"
	case KindTime:
		return "time"
	case KindBytes:
		return "bytes"
	case KindInvalid:
		return "invalid"
	default:
		return "invalid"
	}
}

// Type is a field's type.
type Type struct {
	// Override, when set, replaces Kind entirely. It is whatever the consumer
	// wrote in a type_override — for the Go emitter, a type name or a full
	// import path and type name.
	Override string

	// Kind is the converged type, meaningful only when Override is empty.
	Kind Kind

	// Nullable reports that the analyzer could not rule out NULL.
	Nullable bool

	// Slice reports that the field holds a list of Kind rather than one of it,
	// bound against a set the statement tests membership in.
	//
	// It is the one shape whose *generated code* differs by dialect rather than
	// only its text: Postgres binds the list as one array parameter, and the
	// other two engines have no array to bind, so their statements carry an
	// expansion site that becomes one placeholder per element at query time.
	// Which of the two a dialect does is recorded on Arg, not here — this half
	// is the shared shape, and the shared shape is a single []T field either
	// way.
	Slice bool
}

// Field is one named, typed member of a params or row shape.
type Field struct {
	// Name is the canonical name, spelled as the SQL spells it — snake_case,
	// as it arrives from the analyzer. Turning it into an identifier is the
	// emitter's job, because the rules differ by language.
	Name string

	// Type is what it holds.
	Type Type
}

// Command is a query's sqlc annotation.
type Command uint8

const (
	// CommandInvalid is the zero value and never appears in a valid Package.
	CommandInvalid Command = iota
	// CommandExec is `:exec` — run it, report only failure.
	CommandExec
	// CommandOne is `:one` — exactly one row.
	CommandOne
	// CommandMany is `:many` — zero or more rows.
	CommandMany
	// CommandExecRows is `:execrows` — the number of rows affected.
	CommandExecRows
	// CommandExecResult is `:execresult` — the driver's own result.
	CommandExecResult
)

// String names the command as its annotation is spelled.
func (c Command) String() string {
	switch c {
	case CommandExec:
		return ":exec"
	case CommandOne:
		return ":one"
	case CommandMany:
		return ":many"
	case CommandExecRows:
		return ":execrows"
	case CommandExecResult:
		return ":execresult"
	case CommandInvalid:
		return ":invalid"
	default:
		return ":invalid"
	}
}

// ReturnsRows reports whether the command produces a row shape.
func (c Command) ReturnsRows() bool {
	return c == CommandOne || c == CommandMany
}

// Query is one query's shared shape: what every dialect must agree on.
//
// It is computed from a single dialect's analysis, because a single dialect's
// analysis is all any invocation has. Convergence is not checked here — it is
// the consequence of every invocation computing this the same way from the same
// query set and writing the result to the same path.
type Query struct {
	// Name is the query's name from its `-- name:` annotation, and the join key
	// across dialects.
	Name string

	// Params are the distinct parameters, in the order the placeholders for
	// them first appear. Distinct is the important word: MySQL repeats a
	// placeholder for every occurrence of a named argument, so its statement
	// can bind sixteen positions from these eight fields.
	Params []Field

	// Columns is the projection, empty unless Command.ReturnsRows.
	Columns []Field

	// Command is the annotation.
	Command Command
}

// Statement is one dialect's realization of a Query.
//
// It is the half that is allowed to differ. The SQL is that engine's text, and
// Args names which parameter each placeholder binds — the list that is written
// by hand today, in the order of a column list a few lines up, and is wrong on
// two engines out of three when it is wrong at all.
type Statement struct {
	// Name matches the Query it realizes.
	Name string

	// SQL is the analyzed statement text, carrying {{prefix}} markers when the
	// consumer asked for them.
	SQL string

	// Args names the parameter each placeholder binds, in placeholder order.
	// Names repeat when the engine repeats a placeholder.
	Args []Arg
}

// Arg is one placeholder's binding: which parameter it takes its value from,
// and how.
type Arg struct {
	// Name is the shared parameter this placeholder binds.
	Name string

	// Expand, when set, is the literal text in SQL that stands where this
	// dialect needs one placeholder per element of Name's list. It is replaced
	// at query time — by generated, compiled code — with Placeholder repeated
	// once per element, and every element is bound in this position.
	//
	// Empty is the ordinary case, and it is also what Postgres uses for a list:
	// there the whole slice is one bound array, so there is nothing to expand.
	Expand string

	// Placeholder is one element's placeholder, meaningful only when Expand is
	// set. It is spelled here rather than assumed by the emitter, which knows
	// nothing about which engines number their placeholders.
	Placeholder string
}

// Package is everything an emitter needs for one invocation.
//
// TimeLayout is the one field that needs explaining. It is empty for an engine
// that has a timestamp type, and set to the text layout the engine stores and
// compares timestamps in for an engine that does not. SQLite is that engine: a
// DATETIME column holds text, and comparing two of them compares two strings,
// so a bound time has to arrive already spelled the way the stored ones are.
// Which engine that is belongs here rather than in an emitter, for the same
// reason Arg.Placeholder does — an emitter renders shapes and knows nothing
// about engines.
type Package struct {
	Name          string
	Dialect       string
	SQLCVersion   string
	UnisonVersion string
	NullAs        string
	TimeLayout    string
	Roster        []string
	Queries       []Query
	Statements    []Statement
	TablePrefix   bool
}

// PrefixMarker is the token §9's rewriting places before a table name, and the
// one the generated constructor replaces once per statement at construction.
//
// It is deliberately not valid SQL. A marker that happened to parse would let a
// statement that was never given a prefix reach the database and fail there,
// rather than failing where it was written.
const PrefixMarker = "{{prefix}}"
