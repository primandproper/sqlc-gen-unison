// Package generate is the plugin's entry point: one dialect's analysis in, the
// files sqlc should write out.
//
// This is the protocol skeleton. It proves the round trip — sqlc analyzes a
// dialect, hands us a CodeGenRequest, and writes back what we return — by
// emitting a manifest of what the request contained. The convergence core and
// the Go emitter replace the body of Files; the seam does not move.
package generate

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"
)

// ManifestFilename names the skeleton's output. It is deliberately not a .go
// file: nothing should compile against a manifest, and a consumer who finds one
// in their output directory has caught unison mid-bootstrap.
const ManifestFilename = "unison_manifest.txt"

// Files renders the response for one dialect's analysis.
func Files(_ context.Context, logger *slog.Logger, request *pb.GenerateRequest) ([]*pb.File, error) {
	if request.GetSettings() == nil {
		return nil, fmt.Errorf("unison: the request carries no settings, so there is no engine to generate for")
	}

	engine := request.GetSettings().GetEngine()

	logger.Info("analyzed",
		slog.String("engine", engine),
		slog.Int("queries", len(request.GetQueries())),
		slog.String("sqlc_version", request.GetSqlcVersion()),
	)

	return []*pb.File{{
		Name:     ManifestFilename,
		Contents: []byte(manifest(request)),
	}}, nil
}

// manifest describes a request in the same deterministic order every time.
// Determinism is load-bearing everywhere in unison, so the skeleton practices it
// too: queries are sorted by name, and nothing here reports a time or a path
// from the machine that ran it.
func manifest(request *pb.GenerateRequest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "engine: %s\n", request.GetSettings().GetEngine())
	fmt.Fprintf(&b, "sqlc: %s\n", request.GetSqlcVersion())
	fmt.Fprintf(&b, "options: %s\n", request.GetPluginOptions())

	fmt.Fprintf(&b, "\ntables:\n")

	for _, name := range tableNames(request.GetCatalog()) {
		fmt.Fprintf(&b, "  %s\n", name)
	}

	fmt.Fprintf(&b, "\nqueries:\n")

	queries := make([]*pb.Query, len(request.GetQueries()))
	copy(queries, request.GetQueries())
	sort.Slice(queries, func(i, j int) bool { return queries[i].GetName() < queries[j].GetName() })

	for _, q := range queries {
		fmt.Fprintf(&b, "  %s %s params=%d columns=%d\n",
			q.GetName(), q.GetCmd(), len(q.GetParams()), len(q.GetColumns()))

		for _, p := range q.GetParams() {
			fmt.Fprintf(&b, "    param %d %s %s not_null=%t\n",
				p.GetNumber(), paramName(p), columnType(p.GetColumn()), p.GetColumn().GetNotNull())
		}

		for _, c := range q.GetColumns() {
			fmt.Fprintf(&b, "    column %s %s not_null=%t\n",
				c.GetName(), columnType(c), c.GetNotNull())
		}
	}

	return b.String()
}

// tableNames lists every table the catalog knows, sorted. These are the names
// §9's prefix markers are placed from, so seeing them is half the point of the
// skeleton.
func tableNames(catalog *pb.Catalog) []string {
	var names []string

	for _, schema := range catalog.GetSchemas() {
		for _, table := range schema.GetTables() {
			names = append(names, qualify(schema.GetName(), table.GetRel().GetName()))
		}
	}

	sort.Strings(names)

	return names
}

// qualify joins a schema and table name, omitting the schema when the catalog
// left it empty — which is what SQLite and MySQL do.
func qualify(schema, table string) string {
	if schema == "" {
		return table
	}

	return schema + "." + table
}

// paramName reports the name sqlc resolved for a parameter, or "?" when it
// resolved none. An unnamed parameter is not a defect in the request: MySQL's
// bare LIMIT placeholder is one, and converging it is specified work.
func paramName(p *pb.Parameter) string {
	if name := p.GetColumn().GetName(); name != "" {
		return name
	}

	return "?"
}

// columnType renders a column's database type as the catalog spells it.
func columnType(c *pb.Column) string {
	t := c.GetType().GetName()
	if t == "" {
		t = "<untyped>"
	}

	if c.GetIsArray() {
		t += "[]"
	}

	return t
}
