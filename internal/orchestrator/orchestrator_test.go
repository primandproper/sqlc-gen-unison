package orchestrator_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/cli/pluginenv"
	"github.com/primandproper/sqlc-gen-unison/internal/orchestrator"
	"github.com/primandproper/sqlc-gen-unison/internal/sqlcdriver"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// unisonYAML is the corpus's project file, and it is the shortest complete
// statement of what unison asks a consumer for.
//
// There is no `dialects:` key. The keys of `schemas:` are the roster, because
// two places to say the same thing is one place to say it differently.
const unisonYAML = `sqlc_version: 1.31.1
package: identitydb
out: identitydb
schemas:
  postgresql: schema/postgresql.sql
  mysql: schema/mysql.sql
  sqlite: schema/sqlite.sql
queries:
  postgresql: queries/postgresql.sql
  mysql: queries/mysql.sql
  sqlite: queries/sqlite.sql
options:
  table_prefix_var: true
  rename_params:
    mysql:
      limit: result_limit
  type_overrides:
    - {column: "*.result_limit", go_type: int64}
    - {column: "*.include_archived", go_type: bool}
`

// TestGenerate drives the orchestrator the way `make generate` would, and then
// compiles what it produced.
func TestGenerate(t *testing.T) {
	t.Parallel()

	project := stageProject(t)
	runner, cfg := runnerFor(t, project)

	must.NoError(t, runner.Generate(t.Context(), cfg))

	out := filepath.Join(project, "identitydb")

	for _, name := range []string{
		"db_generated.go", "types_generated.go", "querier_generated.go",
		"queries_mysql_generated.go", "queries_postgresql_generated.go", "queries_sqlite_generated.go",
	} {
		_, err := os.Stat(filepath.Join(out, name))
		test.NoError(t, err, test.Sprintf("%s was not written", name))
	}

	output, err := compile(t, out)
	must.NoError(t, err, must.Sprintf("the generated package did not compile:\n%s", output))

	// The staging directory is temporary, so a run leaves nothing behind
	// besides its output — which is what lets consumers gate CI on a clean tree.
	entries, err := os.ReadDir(project)
	must.NoError(t, err)

	for _, entry := range entries {
		test.StrNotContains(t, entry.Name(), "sqlc-")
	}
}

// TestGenerateIsIdempotent is the consumer-facing half of determinism: running
// `unison generate` twice leaves the tree exactly as the first run left it.
func TestGenerateIsIdempotent(t *testing.T) {
	t.Parallel()

	project := stageProject(t)
	runner, cfg := runnerFor(t, project)

	must.NoError(t, runner.Generate(t.Context(), cfg))
	first := snapshot(t, filepath.Join(project, "identitydb"))

	must.NoError(t, runner.Generate(t.Context(), cfg))
	second := snapshot(t, filepath.Join(project, "identitydb"))

	must.Eq(t, first, second)
}

// TestCheck runs the static analysis tier and asserts it writes nothing.
func TestCheck(t *testing.T) {
	t.Parallel()

	project := stageProject(t)
	runner, cfg := runnerFor(t, project)

	must.NoError(t, runner.Check(t.Context(), cfg))

	_, err := os.Stat(filepath.Join(project, "identitydb"))
	test.True(t, os.IsNotExist(err), test.Sprint("check generated files, and it must not"))
}

// TestCheckReportsBadSQL is the reason check exists: a statement that does not
// match its schema is caught without a database and without generating.
func TestCheckReportsBadSQL(t *testing.T) {
	t.Parallel()

	project := stageProject(t)

	path := filepath.Join(project, "queries", "postgresql.sql")

	contents, err := os.ReadFile(path)
	must.NoError(t, err)

	must.NoError(t, os.WriteFile(path,
		append(contents, "\n-- name: Nonsense :one\nSELECT no_such_column FROM identity_users;\n"...), 0o600))

	runner, cfg := runnerFor(t, project)

	err = runner.Check(t.Context(), cfg)

	must.Error(t, err)
	test.StrContains(t, err.Error(), "no_such_column")
}

// TestLoadRejectsAMismatchedRoster keeps the roster honest: a dialect with
// queries and no schema is not in the roster, so its queries would silently
// never be generated.
func TestLoadRejectsAMismatchedRoster(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "unison.yaml")

	must.NoError(t, os.WriteFile(path, []byte(`package: db
out: db
schemas:
  postgresql: schema/postgresql.sql
queries:
  postgresql: queries/postgresql.sql
  mysql: queries/mysql.sql
`), 0o600))

	_, err := orchestrator.Load(path)

	must.Error(t, err)
	test.StrContains(t, err.Error(), "mysql")
}

// TestLoadRejectsAnUnsupportedEngine holds §3's line: if sqlc cannot analyze a
// dialect, unison does not support it.
func TestLoadRejectsAnUnsupportedEngine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "unison.yaml")

	must.NoError(t, os.WriteFile(path, []byte(`package: db
out: db
schemas:
  oracle: schema/oracle.sql
queries:
  oracle: queries/oracle.sql
`), 0o600))

	_, err := orchestrator.Load(path)

	must.Error(t, err)
	test.StrContains(t, err.Error(), "oracle")
}

// TestRosterIsTheSchemaKeys states the decision directly.
func TestRosterIsTheSchemaKeys(t *testing.T) {
	t.Parallel()

	cfg, err := orchestrator.Load(filepath.Join(stageProject(t), "unison.yaml"))
	must.NoError(t, err)

	test.Eq(t, []string{"mysql", "postgresql", "sqlite"}, cfg.Roster())
	test.Eq(t, []string{"mysql", "postgresql", "sqlite"}, cfg.PluginOptions().Roster)
	test.Eq(t, "identitydb", cfg.PluginOptions().Package)
}

// stageProject lays out a consumer-shaped project around the frozen corpus.
func stageProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	for _, part := range []string{"schema", "queries"} {
		must.NoError(t, os.MkdirAll(filepath.Join(dir, part), 0o750))

		for _, dialect := range []string{"mysql", "postgresql", "sqlite"} {
			contents, err := os.ReadFile(filepath.Join("..", "..", "testdata", "identity", part, dialect+".sql"))
			must.NoError(t, err)

			must.NoError(t, os.WriteFile(filepath.Join(dir, part, dialect+".sql"), contents, 0o600))
		}
	}

	must.NoError(t, os.WriteFile(filepath.Join(dir, "unison.yaml"), []byte(unisonYAML), 0o600))

	return dir
}

// runnerFor builds a Runner pointed at the real sqlc and the real binary.
func runnerFor(t *testing.T, project string) (*orchestrator.Runner, *orchestrator.Config) {
	t.Helper()

	cfg, err := orchestrator.Load(filepath.Join(project, "unison.yaml"))
	must.NoError(t, err)

	return &orchestrator.Runner{
		Logger: slog.New(slog.DiscardHandler),
		SQLC:   findSQLC(t),
		Self:   buildUnison(t),
	}, cfg
}

// snapshot reads a directory into a map for comparison.
func snapshot(t *testing.T, dir string) map[string]string {
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

// compile builds the generated package on its own.
func compile(t *testing.T, dir string) (string, error) {
	t.Helper()

	must.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module unisoncompilecheck\n\ngo 1.27\n"), 0o600))

	cmd := exec.CommandContext(t.Context(), "go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")

	output, err := cmd.CombinedOutput()

	return string(output), err
}

var (
	buildOnce   sync.Once
	builtBinary string
	errBuild    error
)

// buildUnison compiles the CLI once per test binary.
func buildUnison(t *testing.T) string {
	t.Helper()

	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "unison-build-")
		if err != nil {
			errBuild = err

			return
		}

		builtBinary = filepath.Join(dir, "unison")

		cmd := exec.CommandContext(context.Background(), "go", "build", "-o", builtBinary, "../../cmd/main")
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

// TestLogLevelReachesPluginMode proves that UNISON_LOG_LEVEL crosses the
// process boundary into the plugin, which is not something rendering the config
// correctly is enough to guarantee.
//
// sqlc does not pass a process plugin the environment it was itself run with.
// It builds a fresh one holding SQLC_VERSION plus only the keys the `plugins:`
// block's `env:` list names, so a variable missing from that list never arrives,
// however faithfully plugin mode reads it. Rendering the key is therefore load
// bearing, and asserting on the rendered YAML would only restate the code.
//
// So this drives the whole chain and observes the far end. An unparseable level
// is refused by plugin mode at startup, and a failing plugin is the one case
// where sqlc relays the plugin's stderr instead of discarding it — so the
// message coming back out is proof the value arrived. Drop the env line from
// renderConfig and generation succeeds instead, which is the failure this test
// exists to catch.
func TestLogLevelReachesPluginMode(t *testing.T) {
	t.Setenv(pluginenv.LogLevelEnvVar, "nonsense")

	project := stageProject(t)
	runner, cfg := runnerFor(t, project)

	err := runner.Generate(t.Context(), cfg)

	must.Error(t, err)
	test.StrContains(t, err.Error(), `unknown log level "nonsense"`,
		test.Sprint("the plugin should have refused the level it was handed"))
}
