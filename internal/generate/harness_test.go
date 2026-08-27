package generate_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/sqlcdriver"

	"github.com/shoenig/test/must"
)

// dialects is the corpus roster. The roster is the keys of unison.yaml's
// `schemas:` map, which is why it is sorted and why nothing else defines it.
var dialects = []string{"mysql", "postgresql", "sqlite"}

// corpusOptions is the options block the identity corpus needs, and it is worth
// reading as documentation of what a real three-dialect query set costs.
//
// Two type_overrides and one rename, for thirteen queries across three engines.
// Both overrides exist because an analyzer resolved no type at all: SQLite is
// the weak engine §8 warns about, but Postgres types a COALESCE'd LIMIT as `any`
// too. The rename exists because MySQL accepts only a bare placeholder in LIMIT
// and names it `limit`, while Postgres rejects `limit` as an argument name — the
// one divergence in §7's table with no authoring-side fix.
const corpusOptions = `          table_prefix_var: true
          rename_params:
            mysql:
              limit: result_limit
          type_overrides:
            - {column: "*.result_limit", go_type: int64}
            - {column: "*.include_archived", go_type: bool}
`

// corpus describes one generation run.
type corpus struct {
	// mutate rewrites one dialect's queries before sqlc sees them, so that a
	// test can introduce a divergence the corpus does not have.
	mutate func(dialect, sql string) string

	// mutateSchema does the same to one dialect's DDL, which is where a type
	// divergence really comes from.
	mutateSchema func(dialect, ddl string) string

	// options is the YAML options block, already indented.
	options string

	// dialects to generate, all into the same directory.
	dialects []string
}

// generate runs sqlc once per dialect, every run pointing at the same output
// directory, and returns that directory.
//
// Once per dialect into one directory is not an implementation detail — it is
// the design. Every invocation writes the shared files, so when the dialects
// agree the overwrites are byte-identical no-ops, and when they disagree the
// last one wins and another dialect's query file no longer compiles.
func (c corpus) generate(t *testing.T) string {
	t.Helper()

	binary := buildUnison(t)
	sqlc := findSQLC(t)

	root := t.TempDir()
	out := filepath.Join(root, "out")

	must.NoError(t, os.MkdirAll(out, 0o750))

	list := c.dialects
	if len(list) == 0 {
		list = dialects
	}

	options := c.options
	if options == "" {
		options = corpusOptions
	}

	for _, dialect := range list {
		dir := c.stage(t, root, dialect, binary, options)

		_, err := sqlcdriver.Run(t.Context(), sqlc, dir, "generate")
		must.NoError(t, err)
	}

	return out
}

// generateExpectingFailure is generate for the cases where the point is that
// unison refuses. It returns the first failure rather than failing the test.
func (c corpus) generateExpectingFailure(t *testing.T) (string, error) {
	t.Helper()

	binary := buildUnison(t)
	sqlc := findSQLC(t)

	root := t.TempDir()
	out := filepath.Join(root, "out")

	must.NoError(t, os.MkdirAll(out, 0o750))

	list := c.dialects
	if len(list) == 0 {
		list = dialects
	}

	options := c.options
	if options == "" {
		options = corpusOptions
	}

	for _, dialect := range list {
		dir := c.stage(t, root, dialect, binary, options)

		if _, err := sqlcdriver.Run(t.Context(), sqlc, dir, "generate"); err != nil {
			return out, err
		}
	}

	return out, nil
}

// stage writes one dialect's schema, queries, and sqlc config into its own
// directory under root, with `out:` pointing at root/out.
//
// The corpus is copied rather than referenced because sqlc resolves every path
// in a config relative to that config's own directory, and joins rather than
// honors an absolute one.
func (c corpus) stage(t *testing.T, root, dialect, binary, options string) string {
	t.Helper()

	dir := filepath.Join(root, dialect)

	must.NoError(t, os.MkdirAll(dir, 0o750))

	for _, part := range []string{"schema", "queries"} {
		contents, err := os.ReadFile(filepath.Join("..", "..", "testdata", "identity", part, dialect+".sql"))
		must.NoError(t, err)

		text := string(contents)

		if part == "queries" && c.mutate != nil {
			text = c.mutate(dialect, text)
		}

		if part == "schema" && c.mutateSchema != nil {
			text = c.mutateSchema(dialect, text)
		}

		must.NoError(t, os.WriteFile(filepath.Join(dir, part+".sql"), []byte(text), 0o600))
	}

	config := `version: "2"
plugins:
  - name: unison
    process:
      cmd: ` + binary + `
sql:
  - engine: ` + dialect + `
    schema: schema.sql
    queries: queries.sql
    codegen:
      - plugin: unison
        out: ../out
        options:
          package: identitydb
          roster: [mysql, postgresql, sqlite]
` + options

	must.NoError(t, os.WriteFile(filepath.Join(dir, "sqlc.yaml"), []byte(config), 0o600))

	return dir
}

// readAll returns every emitted file's contents, keyed by name.
func readAll(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	must.NoError(t, err)

	files := make(map[string]string, len(entries))

	for _, entry := range entries {
		contents, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		must.NoError(t, readErr)

		files[entry.Name()] = string(contents)
	}

	return files
}

// compilePackage builds the emitted package and returns the compiler's output.
//
// This is the assertion the whole design rests on: unison does not check that
// the dialects agree, it arranges for the Go compiler to do it. A test that only
// compared strings would be checking unison against itself.
func compilePackage(t *testing.T, dir string) (string, error) {
	t.Helper()

	must.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module unisoncompilecheck\n\ngo 1.27\n"), 0o600))

	cmd := exec.CommandContext(t.Context(), "go", "build", "./...")
	cmd.Dir = dir
	// A module of its own, built against the module cache, with the parent
	// module's workspace deliberately out of the picture.
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")

	output, err := cmd.CombinedOutput()

	return string(output), err
}

var (
	buildOnce   sync.Once
	builtBinary string
	errBuild    error
)

// buildUnison compiles the CLI once per test binary. Tests run the real
// executable because that is what sqlc will run.
func buildUnison(t *testing.T) string {
	t.Helper()

	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "unison-build-")
		if err != nil {
			errBuild = err

			return
		}

		builtBinary = filepath.Join(dir, "unison")

		cmd := exec.CommandContext(context.Background(), "go", "build", "-o", builtBinary, "../../cmd/unison")
		if output, outputErr := cmd.CombinedOutput(); outputErr != nil {
			errBuild = fmt.Errorf("building the CLI: %w: %s", outputErr, output)
		}
	})

	must.NoError(t, errBuild)

	return builtBinary
}

// findSQLC locates the pinned sqlc.
//
// Locally it skips when sqlc is absent or the wrong version, so a fresh clone
// still runs the rest of the suite. In CI, where UNISON_REQUIRE_SQLC is set, the
// same conditions fail instead: these are the tests that drive the real
// analyzer, and a suite that silently skips them has checked nothing.
func findSQLC(t *testing.T) string {
	t.Helper()

	required := os.Getenv(sqlcdriver.RequireEnvVar) != ""

	stop := t.Skipf
	if required {
		stop = t.Fatalf
	}

	binary, err := exec.LookPath("sqlc")
	if err != nil {
		stop("sqlc is not on PATH, and the pinned %s is needed to drive the real analyzer", sqlcdriver.PinnedVersion)

		return ""
	}

	version, err := sqlcdriver.Version(context.Background(), binary)
	must.NoError(t, err)

	if version != sqlcdriver.PinnedVersion {
		stop("sqlc on PATH is %s, not the pinned %s", version, sqlcdriver.PinnedVersion)

		return ""
	}

	return binary
}

// swapColumns exchanges two adjacent lines of a projection, which is the paste
// error §1 opens with.
func swapColumns(sql, first, second string) string {
	firstLine := "\t" + first + ",\n"
	secondLine := "\t" + second + ",\n"

	if !strings.Contains(sql, firstLine) || !strings.Contains(sql, secondLine) {
		panic("swapColumns: the corpus does not contain " + first + " and " + second)
	}

	sql = strings.ReplaceAll(sql, firstLine, "\x00")
	sql = strings.ReplaceAll(sql, secondLine, firstLine)

	return strings.ReplaceAll(sql, "\x00", secondLine)
}
