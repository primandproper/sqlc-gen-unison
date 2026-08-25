package generate_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/generate"
	"github.com/primandproper/sqlc-gen-unison/internal/sqlcdriver"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// dialects is the corpus roster, and the roster is the schemas' keys — the same
// rule the orchestrator follows.
var dialects = []string{"mysql", "postgresql", "sqlite"}

// TestPluginRoundTrip drives the real, pinned sqlc over the real corpus and
// asserts that unison was invoked as a plugin and its files were written.
//
// This is the proof that matters for the protocol: a hand-built CodeGenRequest
// proves only that we can decode what we encoded. Every shape decision unison
// makes is downstream of what sqlc's analyzer actually reports, so the analyzer
// has to be in the loop.
func TestPluginRoundTrip(t *testing.T) {
	t.Parallel()

	binary := buildUnison(t)
	sqlc := findSQLC(t)

	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			t.Parallel()

			dir := stageCorpus(t, dialect, binary, nil)

			_, err := sqlcdriver.Run(t.Context(), sqlc, dir, "generate")
			must.NoError(t, err)

			manifest, err := os.ReadFile(filepath.Join(dir, "out", generate.ManifestFilename))
			must.NoError(t, err)

			contents := string(manifest)

			test.StrContains(t, contents, "engine: "+dialect)
			test.StrContains(t, contents, "sqlc: "+sqlcdriver.PinnedVersion)

			// The roster reaches the plugin through options, which is the
			// mechanism convergent emission is built on. If it did not arrive,
			// nothing downstream can be convergent.
			test.StrContains(t, contents, `"roster":["mysql","postgresql","sqlite"]`)

			// Every corpus query should have been analyzed and reported.
			for _, name := range []string{
				"ArchiveAccount", "ArchiveUser", "CreateAccount", "CreateInvitation",
				"CreateUser", "GetAccount", "GetInvitation", "GetUser",
				"ListAccounts", "ListInvitations", "ListUsers",
				"UpdateAccount", "UpdateUser",
			} {
				test.StrContains(t, contents, "  "+name+" :")
			}

			// The catalog knows the tables, which is what §9's prefix markers
			// are placed from.
			test.StrContains(t, contents, "identity_users")
		})
	}
}

// TestPluginRoundTripIsDeterministic runs the same generation twice into
// separate directories and requires the bytes to match.
//
// Determinism is not a nicety here: consumers gate CI on a clean tree after
// regeneration, and convergent emission relies on N invocations writing
// byte-identical shared files. A generator that sorted a map differently on
// alternate runs would break both, so the suite asserts it from the start
// rather than after the first flake.
func TestPluginRoundTripIsDeterministic(t *testing.T) {
	t.Parallel()

	binary := buildUnison(t)
	sqlc := findSQLC(t)

	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			t.Parallel()

			first := generateOnce(t, sqlc, binary, dialect)
			second := generateOnce(t, sqlc, binary, dialect)

			must.Eq(t, first, second)
		})
	}
}

// generateOnce stages the corpus in a fresh directory, generates, and returns
// the emitted file contents keyed by name.
func generateOnce(t *testing.T, sqlc, binary, dialect string) map[string]string {
	t.Helper()

	dir := stageCorpus(t, dialect, binary, nil)

	_, err := sqlcdriver.Run(t.Context(), sqlc, dir, "generate")
	must.NoError(t, err)

	out := filepath.Join(dir, "out")

	entries, err := os.ReadDir(out)
	must.NoError(t, err)

	files := make(map[string]string, len(entries))

	for _, entry := range entries {
		contents, readErr := os.ReadFile(filepath.Join(out, entry.Name()))
		must.NoError(t, readErr)

		files[entry.Name()] = string(contents)
	}

	must.MapNotEmpty(t, files)

	return files
}

// stageCorpus copies one dialect's schema and queries into a temp directory
// beside a rendered sqlc config, and returns the directory.
//
// The corpus is copied rather than referenced because sqlc resolves every path
// in a config relative to that config's own directory, and joins rather than
// honors an absolute one. Staging keeps every path in the rendered config
// relative and the whole run hermetic.
func stageCorpus(t *testing.T, dialect, binary string, extraOptions map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	for _, part := range []string{"schema", "queries"} {
		source := filepath.Join("..", "..", "testdata", "identity", part, dialect+".sql")

		contents, err := os.ReadFile(source)
		must.NoError(t, err)

		must.NoError(t, os.WriteFile(filepath.Join(dir, part+".sql"), contents, 0o600))
	}

	var options strings.Builder

	options.WriteString("          package: identitydb\n")
	options.WriteString("          roster: [mysql, postgresql, sqlite]\n")

	for _, key := range sortedKeys(extraOptions) {
		options.WriteString("          " + key + ": " + extraOptions[key] + "\n")
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
        out: out
        options:
` + options.String()

	must.NoError(t, os.WriteFile(filepath.Join(dir, "sqlc.yaml"), []byte(config), 0o600))

	return dir
}

// sortedKeys is the module's habit rather than this test's: iterating a map in
// range order is how nondeterminism gets into generated output, so nothing here
// does it, not even a test fixture.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

var (
	buildOnce   sync.Once
	builtBinary string
	errBuild    error
)

//nolint:gochecknoglobals // one compile per test binary, shared by every subtest.

// buildUnison compiles the CLI once per test binary and returns its path. Tests
// run the real executable because that is what sqlc will run.
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

// findSQLC locates the pinned sqlc, skipping the test when it is absent so a
// clone without it still runs the rest of the suite.
func findSQLC(t *testing.T) string {
	t.Helper()

	binary, err := exec.LookPath("sqlc")
	if err != nil {
		t.Skip("sqlc is not on PATH; skipping the round trip against the real analyzer")
	}

	version, err := sqlcdriver.Version(context.Background(), binary)
	must.NoError(t, err)

	if version != sqlcdriver.PinnedVersion {
		t.Skipf("sqlc on PATH is %s, not the pinned %s", version, sqlcdriver.PinnedVersion)
	}

	return binary
}
