package converge

import (
	"log/slog"
	"slices"
	"strings"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"

	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"
)

// userTables lists the tables a consumer actually declared, sorted longest name
// first so that rewriting never matches a prefix of a longer name.
//
// The catalog carries far more than the consumer's schema. Postgres reports all
// of information_schema and pg_catalog, several hundred tables including ones
// named `columns`, `tables`, and `parameters` — words that appear in ordinary
// SQL and in ordinary string literals. Restricting to the catalog's default
// schema is what keeps §9's rewriting from turning a query into nonsense.
func userTables(catalog *pb.Catalog) []string {
	def := catalog.GetDefaultSchema()

	var names []string

	for _, schema := range catalog.GetSchemas() {
		// SQLite calls it "main", Postgres and MySQL "public"; whatever the
		// catalog says is default is the consumer's.
		if schema.GetName() != def {
			continue
		}

		for _, table := range schema.GetTables() {
			if name := table.GetRel().GetName(); name != "" {
				names = append(names, name)
			}
		}
	}

	slices.SortFunc(names, func(a, b string) int {
		if d := len(b) - len(a); d != 0 {
			return d
		}

		return strings.Compare(a, b)
	})

	return names
}

// markPrefixes rewrites whole-word table identifiers in a statement to carry
// §9's marker.
//
// This is the blocker that kept sqlc out of platform-go, and the resolution is
// that the plugin knows which tokens are tables because the analyzer told it.
// The generated constructor then does one replacement per statement at
// construction. That is not runtime SQL surgery: the marker is placed once, at
// generate time, from the catalog, and what happens at construction is a string
// substitution into a position a generator chose.
//
// The sharp edge §9 documents is here: a string literal that spells a table name
// is indistinguishable from an identifier to a whole-word scan, and would be
// rewritten too. Rather than parse SQL — which unison does not do, and will not
// start doing for this — the literal is left alone and a warning names the
// query. The fix is renaming the literal, and the corpus is controlled.
func markPrefixes(logger *slog.Logger, query, sql string, tables []string) string {
	if len(tables) == 0 {
		return sql
	}

	literals := stringLiteralSpans(sql)

	var b strings.Builder

	b.Grow(len(sql) + len(tables)*len(ir.PrefixMarker))

	for i := 0; i < len(sql); {
		if !isIdentStart(sql[i]) {
			b.WriteByte(sql[i])
			i++

			continue
		}

		end := i
		for end < len(sql) && isIdentPart(sql[end]) {
			end++
		}

		word := sql[i:end]

		if slices.Contains(tables, word) {
			if inSpan(literals, i) {
				// Inside a literal the word is data, not an identifier. Leave
				// it, and say so — this is the one case where the catalog
				// cannot settle the question.
				logger.Warn("a string literal spells a table name and was left unprefixed",
					slog.String("query", query),
					slog.String("table", word),
					slog.String("fix", "rename the literal so it does not collide with a table name"),
				)
			} else if !precededByDot(sql, i) {
				// `identity_users.id` is a qualified column reference; the
				// table half was already marked when it was matched on its own.
				// A dot before the word means this is the column half.
				b.WriteString(ir.PrefixMarker)
			}
		}

		b.WriteString(word)

		i = end
	}

	return b.String()
}

// span is a half-open byte range.
type span struct {
	start int
	end   int
}

// stringLiteralSpans finds the single-quoted literals in a statement.
//
// It understands the one escape all three engines share — a doubled quote — and
// nothing else. This is not a SQL parser and must not become one; it exists only
// to decide whether a word that matched a table name is data, so that the
// warning above can be accurate.
func stringLiteralSpans(sql string) []span {
	var spans []span

	for i := 0; i < len(sql); i++ {
		if sql[i] != '\'' {
			continue
		}

		start := i

		for i++; i < len(sql); i++ {
			if sql[i] != '\'' {
				continue
			}

			// A doubled quote is an escaped quote, not the end.
			if i+1 < len(sql) && sql[i+1] == '\'' {
				i++

				continue
			}

			break
		}

		end := min(i+1, len(sql))

		spans = append(spans, span{start: start, end: end})
	}

	return spans
}

// inSpan reports whether an offset falls inside any span.
func inSpan(spans []span, offset int) bool {
	for _, s := range spans {
		if offset >= s.start && offset < s.end {
			return true
		}
	}

	return false
}

// precededByDot reports whether the identifier at offset is the right half of a
// qualified name.
func precededByDot(sql string, offset int) bool {
	for i := offset - 1; i >= 0; i-- {
		switch sql[i] {
		case ' ', '\t', '\n', '\r':
			continue
		case '.':
			return true
		default:
			return false
		}
	}

	return false
}

// isIdentStart reports whether c can begin a SQL identifier.
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentPart reports whether c can continue one.
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
