package gogen

import (
	"fmt"
	"strings"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"
)

// emitQueries renders one dialect's file: its statement text, its constructor,
// and its implementation of every method in Querier.
//
// This is the half of the output that is allowed to differ between dialects, and
// it is also the half that pins the shared files by name. Every method here
// spells out the params type, the row type, and each field it binds or scans, so
// a shared file that no longer declares one of them fails to compile with the
// symbol in the message.
func emitQueries(pkg *ir.Package) (string, error) {
	imports := newImportSet()

	imports.add("context")

	if pkg.TablePrefix {
		imports.add("strings")
	}

	var b strings.Builder

	statements := make(map[string]ir.Statement, len(pkg.Statements))
	for i := range pkg.Statements {
		statements[pkg.Statements[i].Name] = pkg.Statements[i]
	}

	writeStatementConsts(&b, pkg)

	receiver := dialectReceiver(pkg.Dialect)

	fmt.Fprintf(&b, `// %s answers every query in Querier against %s.
type %s struct {
`, receiver, pkg.Dialect, receiver)

	for i := range pkg.Queries {
		fmt.Fprintf(&b, "\t%s string\n", unexportedName(pkg.Queries[i].Name))
	}

	b.WriteString("}\n\n")

	writeConstructor(&b, pkg, receiver)

	writeTimeHelpers(&b, pkg, imports)

	for i := range pkg.Queries {
		query := &pkg.Queries[i]

		statement, ok := statements[query.Name]
		if !ok {
			return "", fmt.Errorf("query %s has no statement for %s", query.Name, pkg.Dialect)
		}

		if err := writeMethod(&b, pkg, receiver, query, &statement, imports); err != nil {
			return "", err
		}
	}

	if err := writeShapeAssertions(&b, pkg, imports); err != nil {
		return "", err
	}

	return header(pkg) + imports.render() + b.String(), nil
}

// writeShapeAssertions pins every shared type against the shape this dialect
// analyzed.
//
// Without these, a divergence in types or in field order is the one class the
// compiler misses. Scan and Exec take ...any, so binding a *string where the
// other dialect emitted an *int64 type-checks, and two same-typed fields in the
// wrong order type-checks too — which is exactly the silent transposition this
// tool exists to make impossible.
//
// A conversion between struct types compiles only when the fields match by name,
// by type, and in order, so each line below is that check. The shared files are
// written by whichever dialect ran last; these are written by this one, and
// disagreement is a compile error that names the type.
func writeShapeAssertions(b *strings.Builder, pkg *ir.Package, imports *importSet) error {
	b.WriteString(`// Shape assertions.
//
// Each conversion below compiles only if the shared type still has exactly
// these fields, with these types, in this order — which is what makes a
// disagreement between dialects a compile error here rather than a
// transposition at run time.
var (
`)

	for i := range pkg.Queries {
		query := &pkg.Queries[i]

		if len(query.Params) > 0 {
			if err := writeShapeAssertion(b, query.Name+"Params", query.Params, pkg.NullAs, imports); err != nil {
				return err
			}
		}

		if query.Command.ReturnsRows() {
			if err := writeShapeAssertion(b, query.Name+"Row", query.Columns, pkg.NullAs, imports); err != nil {
				return err
			}
		}
	}

	b.WriteString(")\n\n")

	return nil
}

// writeShapeAssertion renders one type's assertion.
func writeShapeAssertion(b *strings.Builder, typeName string, fields []ir.Field, nullAs string, imports *importSet) error {
	b.WriteString("\t_ = struct {\n")

	for i := range fields {
		field := &fields[i]

		rendered, err := goType(field.Type, nullAs, imports)
		if err != nil {
			return fmt.Errorf("%s: %w", typeName, err)
		}

		fmt.Fprintf(b, "\t\t%s %s\n", exportedName(field.Name), rendered)
	}

	fmt.Fprintf(b, "\t}(%s{})\n", typeName)

	return nil
}

// writeStatementConsts renders one const per query, holding that dialect's text.
func writeStatementConsts(b *strings.Builder, pkg *ir.Package) {
	for i := range pkg.Statements {
		statement := &pkg.Statements[i]

		name := statementConstName(statement.Name, pkg.Dialect)

		fmt.Fprintf(b, "const %s = `%s`\n\n", name, escapeBackticks(statement.SQL))
	}
}

// statementConstName names the const holding one dialect's text for a query.
func statementConstName(query, dialect string) string {
	return unexportedName(query) + dialectName(dialect)
}

// escapeBackticks makes SQL safe inside a raw string literal.
//
// A backtick cannot appear in a raw literal at all, and MySQL uses them to quote
// identifiers, so a statement that contains one is spliced back together. The
// result is uglier than the alternative and correct, which is the right trade
// for generated code.
func escapeBackticks(sql string) string {
	return strings.ReplaceAll(sql, "`", "` + \"`\" + `")
}

// writeConstructor renders the per-dialect constructor.
//
// When the consumer asked for prefix markers this is where §9's one replacement
// per statement happens: at construction, once, into a field the methods then
// use as-is. Nothing rewrites a statement at query time.
func writeConstructor(b *strings.Builder, pkg *ir.Package, receiver string) {
	constructor := dialectConstructor(pkg.Dialect)

	if !pkg.TablePrefix {
		fmt.Fprintf(b, "// %s returns the %s querier.\nfunc %s() *%s {\n\treturn &%s{\n",
			constructor, pkg.Dialect, constructor, receiver, receiver)

		for i := range pkg.Queries {
			name := pkg.Queries[i].Name
			fmt.Fprintf(b, "\t\t%s: %s,\n", unexportedName(name), statementConstName(name, pkg.Dialect))
		}

		b.WriteString("\t}\n}\n\n")

		return
	}

	fmt.Fprintf(b, `// %s returns the %s querier with prefix substituted into every
// table name the analyzer identified.
func %s(prefix string) *%s {
	return &%s{
`, constructor, pkg.Dialect, constructor, receiver, receiver)

	for i := range pkg.Queries {
		name := pkg.Queries[i].Name
		fmt.Fprintf(b, "\t\t%s: strings.ReplaceAll(%s, prefixMarker, prefix),\n",
			unexportedName(name), statementConstName(name, pkg.Dialect))
	}

	b.WriteString("\t}\n}\n\n")
}

// writeMethod renders one query's implementation for this dialect.
func writeMethod(b *strings.Builder, pkg *ir.Package, receiver string, query *ir.Query, statement *ir.Statement, imports *importSet) error {
	signature, err := methodSignature(query, pkg.NullAs, imports)
	if err != nil {
		return err
	}

	fmt.Fprintf(b, "// %s runs the %s query against %s.\n", query.Name, query.Command, pkg.Dialect)
	fmt.Fprintf(b, "func (q *%s) %s {\n", receiver, signature)

	binders := timeBinders(pkg, query)

	statementExpr := "q." + unexportedName(query.Name)
	args := bindArgs(statement.Args, binders)

	if expands(statement) {
		imports.add("strings")

		b.WriteString(expansionPrelude(query, statement, binders))

		statementExpr, args = "query", ", args..."
	}

	switch query.Command {
	case ir.CommandExec:
		fmt.Fprintf(b, "\t_, err := db.ExecContext(ctx, %s%s)\n\n\treturn err\n", statementExpr, args)
	case ir.CommandExecRows:
		fmt.Fprintf(b, `	result, err := db.ExecContext(ctx, %s%s)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
`, statementExpr, args)
	case ir.CommandExecResult:
		fmt.Fprintf(b, "\treturn db.ExecContext(ctx, %s%s)\n", statementExpr, args)
	case ir.CommandOne:
		fmt.Fprintf(b, `	row := db.QueryRowContext(ctx, %s%s)

	var i %sRow

	err := row.Scan(%s)

	return i, err
`, statementExpr, args, query.Name, scanTargets(query.Columns))
	case ir.CommandMany:
		fmt.Fprintf(b, `	rows, err := db.QueryContext(ctx, %s%s)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var items []%sRow

	for rows.Next() {
		var i %sRow

		if err := rows.Scan(%s); err != nil {
			return nil, err
		}

		items = append(items, i)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
`, statementExpr, args, query.Name, query.Name, scanTargets(query.Columns))
	case ir.CommandInvalid:
		return fmt.Errorf("query %s has no command", query.Name)
	default:
		return fmt.Errorf("query %s has an unknown command", query.Name)
	}

	b.WriteString("}\n\n")

	return nil
}

// expands reports whether this dialect has to rewrite the statement before it
// runs it.
func expands(statement *ir.Statement) bool {
	for i := range statement.Args {
		if statement.Args[i].Expand != "" {
			return true
		}
	}

	return false
}

// expansionPrelude renders the lines a method needs before it can run a
// statement whose list parameter has no single placeholder.
//
// This is the one place generated code assembles anything at query time, and it
// is worth being precise about what it is and is not. It is not dynamic SQL: the
// statement is the analyzed one, the token being replaced was placed by the
// analyzer, and what replaces it is a run of placeholders — never a value, never
// an identifier. Everything the caller supplied is still bound. What the
// generator could not know at generate time is only how many elements there
// would be, and that is exactly what this computes.
//
// The arguments are appended in placeholder order, one statement per argument in
// the order Args lists them, so an element of a list lands where the placeholder
// that expanded for it sits. That correspondence is the whole point: it is the
// hand-written []any that this tool exists to stop anyone from writing, and here
// it is written from the same list the replacement is.
func expansionPrelude(query *ir.Query, statement *ir.Statement, binders map[string]timeBinder) string {
	var b strings.Builder

	fmt.Fprintf(&b, "\tquery := q.%s\n\n", unexportedName(query.Name))
	fmt.Fprintf(&b, "\targs := make([]any, 0, %s)\n\n", capacity(statement.Args))

	for i := range statement.Args {
		arg := &statement.Args[i]

		field := "arg." + exportedName(arg.Name)

		if arg.Expand == "" {
			fmt.Fprintf(&b, "\targs = append(args, %s)\n\n", bindValue(field, binders[arg.Name]))

			continue
		}

		fmt.Fprintf(&b, "\tquery = strings.Replace(query, %q, slicePlaceholders(%q, len(%s)), 1)\n\n",
			arg.Expand, arg.Placeholder, field)

		fmt.Fprintf(&b, "\tfor _, v := range %s {\n\t\targs = append(args, %s)\n\t}\n\n",
			field, bindElement("v", binders[arg.Name]))
	}

	return b.String()
}

// capacity renders the exact length the argument slice will reach, so that the
// one allocation is the only one.
func capacity(args []ir.Arg) string {
	var (
		fixed int
		terms []string
	)

	for i := range args {
		if args[i].Expand == "" {
			fixed++

			continue
		}

		terms = append(terms, "len(arg."+exportedName(args[i].Name)+")")
	}

	if fixed > 0 || len(terms) == 0 {
		terms = append([]string{fmt.Sprintf("%d", fixed)}, terms...)
	}

	return strings.Join(terms, "+")
}

// bindArgs renders the argument list a statement's placeholders bind, in
// placeholder order.
//
// A name repeats here as often as the engine repeats its placeholder — MySQL
// binds sixteen positions from eight fields in the corpus's list queries — and
// that repetition is exactly the []any that is written by hand today, in the
// order of a column list a few lines up, and is correct on one engine and
// silently wrong on the others.
func bindArgs(args []ir.Arg, binders map[string]timeBinder) string {
	if len(args) == 0 {
		return ""
	}

	var b strings.Builder

	for i := range args {
		field := "arg." + exportedName(args[i].Name)

		fmt.Fprintf(&b, ",\n\t\t%s", bindValue(field, binders[args[i].Name]))
	}

	return b.String() + ",\n\t"
}

// scanTargets renders the destinations a row is scanned into, in projection
// order.
//
// Naming every field explicitly is what makes a swapped pair of same-typed
// columns a compile error somewhere rather than a silent transposition: the
// projection order and the scan order are emitted from the same list, so they
// cannot drift apart.
func scanTargets(columns []ir.Field) string {
	targets := make([]string, 0, len(columns))

	for i := range columns {
		targets = append(targets, "&i."+exportedName(columns[i].Name))
	}

	return "\n\t\t" + strings.Join(targets, ",\n\t\t") + ",\n\t"
}
