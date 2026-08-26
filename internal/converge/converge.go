// Package converge turns one dialect's sqlc analysis into unison's IR.
//
// It is the adapter, and the only package besides the protocol that reads the
// plugin's protobuf. Everything it produces is language-neutral, and everything
// it decides it decides the same way on every dialect — which is what makes the
// shared files byte-identical when the dialects agree, and different when they
// do not.
//
// There is no reconciliation here, and adding some would defeat the design. When
// two dialects disagree about a shape, both emit what they saw, the last one
// wins the shared files, and the other dialect's query file names a symbol that
// is no longer declared. The Go compiler is the check.
package converge

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"
	"github.com/primandproper/sqlc-gen-unison/internal/options"

	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"
)

// Package converges one CodeGenRequest into the IR an emitter renders.
func Package(logger *slog.Logger, request *pb.GenerateRequest, opts *options.Options, unisonVersion string) (*ir.Package, error) {
	dialect := request.GetSettings().GetEngine()
	if dialect == "" {
		return nil, fmt.Errorf("unison: the request names no engine")
	}

	if !slices.Contains(opts.Roster, dialect) {
		return nil, fmt.Errorf(
			"unison: sqlc is generating for %q but the roster is %v. "+
				"The roster is the keys of unison.yaml's `schemas:` map, and every dialect being generated must be in it",
			dialect, opts.Roster)
	}

	tables := userTables(request.GetCatalog())

	pkg := &ir.Package{
		Name:          opts.Package,
		Dialect:       dialect,
		Roster:        slices.Clone(opts.Roster),
		SQLCVersion:   request.GetSqlcVersion(),
		UnisonVersion: unisonVersion,
		NullAs:        string(opts.NullAs),
		TablePrefix:   opts.TablePrefixVar,
	}

	queries := slices.Clone(request.GetQueries())
	slices.SortFunc(queries, func(a, b *pb.Query) int { return strings.Compare(a.GetName(), b.GetName()) })

	renames := opts.Renames(dialect)

	for _, query := range queries {
		shared, statement, err := convergeQuery(logger, query, opts, renames, tables)
		if err != nil {
			return nil, err
		}

		pkg.Queries = append(pkg.Queries, shared)
		pkg.Statements = append(pkg.Statements, statement)
	}

	if len(pkg.Queries) == 0 {
		return nil, fmt.Errorf("unison: %s analyzed no queries; check the `queries:` path for that dialect", dialect)
	}

	return pkg, nil
}

// convergeQuery produces one query's shared shape and this dialect's statement.
func convergeQuery(logger *slog.Logger, query *pb.Query, opts *options.Options, renames map[string]string, tables []string) (ir.Query, ir.Statement, error) {
	name := query.GetName()

	command, err := parseCommand(query.GetCmd())
	if err != nil {
		return ir.Query{}, ir.Statement{}, fmt.Errorf("unison: query %s: %w", name, err)
	}

	params, args, err := convergeParams(query, opts, renames)
	if err != nil {
		return ir.Query{}, ir.Statement{}, fmt.Errorf("unison: query %s: %w", name, err)
	}

	columns, err := convergeColumns(query, opts, command)
	if err != nil {
		return ir.Query{}, ir.Statement{}, fmt.Errorf("unison: query %s: %w", name, err)
	}

	text := query.GetText()
	if opts.TablePrefixVar {
		text = markPrefixes(logger, name, text, tables)
	}

	// Checked against the marked text rather than the analyzer's, because the
	// marked text is what the generated const holds and what the generated
	// replacement will run against.
	if err = checkExpansions(text, args); err != nil {
		return ir.Query{}, ir.Statement{}, fmt.Errorf("unison: query %s: %w", name, err)
	}

	return ir.Query{
		Name:    name,
		Command: command,
		Params:  params,
		Columns: columns,
	}, ir.Statement{
		Name: name,
		SQL:  text,
		Args: args,
	}, nil
}

// convergeParams reduces a query's parameters to the distinct set the shared
// shape declares, and records which of them each placeholder binds.
//
// Two things about the input are traps.
//
// First, the parameters arrive in placeholder order but their Number field is
// not that order. On MySQL a bare `?` — the only form LIMIT accepts — is
// numbered 1 while appearing last in the text, so sorting by Number produces a
// statement whose arguments are silently transposed. sqlc's own Go generator
// reads them in list order, and so does this. Number is not consulted anywhere.
//
// Second, MySQL does not deduplicate: a named argument used three times is three
// parameters with the same name. The shared shape is the distinct set in order
// of first appearance, and Args repeats a name as often as the engine repeats
// its placeholder. That repetition is the hand-written []any this tool exists to
// stop anyone from writing.
func convergeParams(query *pb.Query, opts *options.Options, renames map[string]string) ([]ir.Field, []ir.Arg, error) {
	var (
		params []ir.Field
		args   []ir.Arg
		seen   = make(map[string]ir.Type, len(query.GetParams()))
	)

	for _, param := range query.GetParams() {
		column := param.GetColumn()

		reported := column.GetName()
		if reported == "" {
			return nil, nil, fmt.Errorf(
				"parameter %d has no name, so it cannot be converged onto a shared field. "+
					"Name it with sqlc.arg() or sqlc.narg()", param.GetNumber())
		}

		name := reported
		if renamed, ok := renames[reported]; ok {
			name = renamed
		}

		table := column.GetTable().GetName()

		list, err := listOf(column)
		if err != nil {
			return nil, nil, fmt.Errorf("parameter %q: %w", name, err)
		}

		fieldType, err := mapType(table, name, elementTypeName(column), column.GetNotNull(), opts.OverrideFor)
		if err != nil {
			return nil, nil, err
		}

		fieldType.Slice = list != notAList

		if fieldType.Slice && fieldType.Nullable {
			// A nullable list has two ways to be absent — a nil slice and a
			// NULL — and no engine agrees about which the placeholder means.
			// Name it with sqlc.arg() rather than sqlc.narg(); an empty list
			// already matches nothing on all three.
			return nil, nil, fmt.Errorf(
				"parameter %q is a nullable list, which has no converged spelling. "+
					"Bind it with sqlc.arg(); an empty list already matches nothing on every dialect", name)
		}

		if previous, ok := seen[name]; ok {
			// The same argument bound twice must mean the same thing both
			// times. When it does not, the analyzer disagrees with itself and
			// no shared field can be honest about both.
			if previous != fieldType {
				return nil, nil, fmt.Errorf(
					"parameter %q appears more than once with different types (%s then %s)",
					name, describe(previous), describe(fieldType))
			}

			if fieldType.Slice {
				// One expansion site is replaced once, and sqlc marks each
				// occurrence with the same token, so a list bound twice would
				// expand the first and leave the second a single placeholder.
				return nil, nil, fmt.Errorf(
					"list parameter %q is bound more than once, and only the first would expand. "+
						"Bind it once, or give each occurrence its own argument", name)
			}
		} else {
			seen[name] = fieldType
			params = append(params, ir.Field{Name: name, Type: fieldType})
		}

		arg := ir.Arg{Name: name}
		if list == expandedList {
			arg.Expand, arg.Placeholder = sliceMarker(reported), slicePlaceholder
		}

		args = append(args, arg)
	}

	return params, args, nil
}

// convergeColumns reduces a query's projection to the shared row shape.
func convergeColumns(query *pb.Query, opts *options.Options, command ir.Command) ([]ir.Field, error) {
	if !command.ReturnsRows() {
		return nil, nil
	}

	columns := query.GetColumns()
	if len(columns) == 0 {
		return nil, fmt.Errorf("%s returns rows but the analyzer resolved no columns", command)
	}

	fields := make([]ir.Field, 0, len(columns))
	seen := make(map[string]int, len(columns))

	for i, column := range columns {
		name := column.GetName()
		if name == "" {
			return nil, fmt.Errorf("column %d of the projection has no name; give it one with AS", i+1)
		}

		// A projection that names the same column twice cannot become a struct
		// with two fields of that name. Say so here rather than emitting Go
		// that will not compile for a reason the consumer has to reverse
		// engineer.
		if first, ok := seen[name]; ok {
			return nil, fmt.Errorf(
				"the projection names %q twice, at positions %d and %d; "+
					"give one of them a distinct alias with AS", name, first+1, i+1)
		}

		seen[name] = i

		fieldType, err := mapType(column.GetTable().GetName(), name, columnTypeName(column), column.GetNotNull(), opts.OverrideFor)
		if err != nil {
			return nil, err
		}

		fields = append(fields, ir.Field{Name: name, Type: fieldType})
	}

	return fields, nil
}

// columnTypeName reports the database type a column carries, spelled as the
// analyzer spelled it.
func columnTypeName(column *pb.Column) string {
	if column == nil {
		return ""
	}

	// An array is not a converged type in v1: only Postgres has them, so a
	// column that is one cannot have a shared field that is honest on the other
	// engines. Reporting the array spelling lets the error name it.
	name := column.GetType().GetName()
	if column.GetIsArray() {
		return name + "[]"
	}

	return name
}

// parseCommand maps a sqlc annotation onto the IR's command set.
func parseCommand(cmd string) (ir.Command, error) {
	switch cmd {
	case ":exec":
		return ir.CommandExec, nil
	case ":one":
		return ir.CommandOne, nil
	case ":many":
		return ir.CommandMany, nil
	case ":execrows":
		return ir.CommandExecRows, nil
	case ":execresult":
		return ir.CommandExecResult, nil
	case ":copyfrom":
		return 0, fmt.Errorf(
			"%s is Postgres-only and therefore shape-divergent by nature; unison does not generate it. "+
				"Keep it as a hand-written statement under the consumer's container tests", cmd)
	default:
		return 0, fmt.Errorf("unison does not generate %s; the supported annotations are :exec, :one, :many, :execrows, and :execresult", cmd)
	}
}

// describe renders a type for an error message.
func describe(t ir.Type) string {
	name := t.Kind.String()
	if t.Override != "" {
		name = t.Override
	}

	if t.Nullable {
		name = "nullable " + name
	}

	if t.Slice {
		return "a list of " + name
	}

	return name
}
