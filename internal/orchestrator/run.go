package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/primandproper/sqlc-gen-unison/internal/cli/pluginenv"
	"github.com/primandproper/sqlc-gen-unison/internal/sqlcdriver"

	"go.yaml.in/yaml/v3"
)

// PluginName is what the rendered sqlc config calls unison. It never reaches a
// consumer's own config, because the orchestrator writes the sqlc config.
const PluginName = "unison"

// Runner generates or checks a project.
type Runner struct {
	// Logger receives progress. It writes to stderr like everything else here.
	Logger *slog.Logger

	// SQLC is the path to the sqlc binary.
	SQLC string

	// Self is the path to this binary, which the rendered config names as the
	// plugin's command. Taking it from os.Executable rather than from a
	// consumer's config is what makes the plugin and the orchestrator the same
	// version by construction.
	Self string
}

// Generate runs sqlc once per dialect, every run writing to the same directory.
//
// The dialects run in roster order, and the order does not matter: whichever
// runs last wins the shared files, and if they disagree the package does not
// compile. Ordering would only matter if unison were merging, and it is not.
func (r *Runner) Generate(ctx context.Context, cfg *Config) error {
	return r.run(ctx, cfg, "generate")
}

// Check statically checks every dialect's SQL against its schema and generates
// nothing.
//
// This is `sqlc compile` per dialect, and it is here so a consumer runs one tool
// for both tiers: the statements that go through generation, and the ones that
// are still hand-written but should still be checked against the schema.
func (r *Runner) Check(ctx context.Context, cfg *Config) error {
	return r.run(ctx, cfg, "compile")
}

// run renders a config per dialect and invokes sqlc with the given subcommand.
func (r *Runner) run(ctx context.Context, cfg *Config, subcommand string) error {
	if err := r.checkSQLCVersion(ctx, cfg); err != nil {
		return err
	}

	out := filepath.Join(cfg.dir, cfg.Out)

	if subcommand == "generate" {
		if err := os.MkdirAll(out, 0o750); err != nil {
			return fmt.Errorf("unison: creating %s: %w", cfg.Out, err)
		}
	}

	// The rendered configs live in a temporary directory so that a run leaves
	// nothing behind in the consumer's tree — which matters because consumers
	// gate CI on a clean tree after regeneration. sqlc resolves every path in a
	// config relative to that config's own directory and does not honor an
	// absolute one, so the paths written into it are relative to the temporary
	// directory rather than copied across verbatim.
	staging, err := os.MkdirTemp("", "unison-sqlc-")
	if err != nil {
		return fmt.Errorf("unison: creating a staging directory: %w", err)
	}

	defer func() {
		if removeErr := os.RemoveAll(staging); removeErr != nil {
			r.Logger.Warn("could not remove the staging directory",
				slog.String("path", staging), slog.String("error", removeErr.Error()))
		}
	}()

	for _, dialect := range cfg.Roster() {
		configPath, renderErr := r.renderConfig(cfg, dialect, staging, out)
		if renderErr != nil {
			return renderErr
		}

		r.Logger.Info("running sqlc", slog.String("dialect", dialect), slog.String("command", subcommand))

		output, runErr := sqlcdriver.Run(ctx, r.SQLC, staging, subcommand, "-f", configPath)
		if runErr != nil {
			return fmt.Errorf("unison: %s: %w", dialect, runErr)
		}

		if output != "" {
			r.Logger.Info("sqlc", slog.String("dialect", dialect), slog.String("output", output))
		}
	}

	return nil
}

// checkSQLCVersion refuses to run against an sqlc the project did not pin.
//
// The CodeGenRequest protobuf is a moving target and the analysis it carries is
// the input to every shape decision unison makes, so generating with a different
// sqlc than the one a project pinned is generating from a different analyzer.
func (r *Runner) checkSQLCVersion(ctx context.Context, cfg *Config) error {
	if strings.TrimSpace(cfg.SQLCVersion) == "" {
		return nil
	}

	actual, err := sqlcdriver.Version(ctx, r.SQLC)
	if err != nil {
		return fmt.Errorf("unison: asking %s for its version: %w", r.SQLC, err)
	}

	if normalizeVersion(actual) != normalizeVersion(cfg.SQLCVersion) {
		return fmt.Errorf(
			"unison: this project pins sqlc %s but %s reports %s; "+
				"generating with a different analyzer produces different analysis",
			cfg.SQLCVersion, r.SQLC, actual)
	}

	return nil
}

// normalizeVersion makes "1.31.1" and "v1.31.1" the same answer.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// sqlcConfig is the config unison writes for sqlc. It is rendered rather than
// asked for so that a consumer cannot point the plugin somewhere else, pass a
// roster that disagrees with their schemas, or forget a dialect.
type sqlcConfig struct {
	Version string          `yaml:"version"`
	Plugins []sqlcPlugin    `yaml:"plugins"`
	SQL     []sqlcSQLConfig `yaml:"sql"`
}

// sqlcPlugin is the `plugins:` entry naming unison.
//
// Env is not optional decoration. sqlc does not hand a process plugin the
// environment it was itself run with: it builds one containing SQLC_VERSION and
// then only the keys listed here. A variable this list omits does not reach
// plugin mode at all, however faithfully the plugin reads it.
//
// Note that this is the `plugins:` block. sqlc's `codegen:` block has no `env:`
// field in 1.31.1; they are different blocks, and only this one carries it.
type sqlcPlugin struct {
	Name    string      `yaml:"name"`
	Process sqlcProcess `yaml:"process"`
	Env     []string    `yaml:"env"`
}

type sqlcProcess struct {
	Cmd string `yaml:"cmd"`
}

type sqlcSQLConfig struct {
	Engine  string       `yaml:"engine"`
	Schema  string       `yaml:"schema"`
	Queries string       `yaml:"queries"`
	Codegen []sqlcCodgen `yaml:"codegen"`
}

type sqlcCodgen struct {
	Options map[string]any `yaml:"options"`
	Plugin  string         `yaml:"plugin"`
	Out     string         `yaml:"out"`
}

// renderConfig writes one dialect's sqlc config into staging and returns its
// path.
func (r *Runner) renderConfig(cfg *Config, dialect, staging, out string) (string, error) {
	schema, err := relativeTo(staging, filepath.Join(cfg.dir, cfg.Schemas[dialect]))
	if err != nil {
		return "", err
	}

	queries, err := relativeTo(staging, filepath.Join(cfg.dir, cfg.Queries[dialect]))
	if err != nil {
		return "", err
	}

	outRelative, err := relativeTo(staging, out)
	if err != nil {
		return "", err
	}

	pluginOptions, err := optionsMap(cfg)
	if err != nil {
		return "", err
	}

	rendered := sqlcConfig{
		Version: "2",
		Plugins: []sqlcPlugin{{
			Name:    PluginName,
			Env:     []string{pluginenv.LogLevelEnvVar},
			Process: sqlcProcess{Cmd: r.Self},
		}},
		SQL: []sqlcSQLConfig{{
			Engine:  dialect,
			Schema:  schema,
			Queries: queries,
			Codegen: []sqlcCodgen{{
				Plugin:  PluginName,
				Out:     outRelative,
				Options: pluginOptions,
			}},
		}},
	}

	contents, err := yaml.Marshal(rendered)
	if err != nil {
		return "", fmt.Errorf("unison: rendering the sqlc config for %s: %w", dialect, err)
	}

	path := filepath.Join(staging, "sqlc-"+dialect+".yaml")
	if err = os.WriteFile(path, contents, 0o600); err != nil {
		return "", fmt.Errorf("unison: writing the sqlc config for %s: %w", dialect, err)
	}

	return path, nil
}

// optionsMap renders the plugin options as the map sqlc will forward.
//
// It goes through JSON rather than YAML because JSON is what the plugin
// receives, so the keys are the ones Parse expects by construction rather than
// by a second set of tags that could drift.
func optionsMap(cfg *Config) (map[string]any, error) {
	opts := cfg.PluginOptions()

	encoded, err := json.Marshal(opts)
	if err != nil {
		return nil, fmt.Errorf("unison: encoding plugin options: %w", err)
	}

	var decoded map[string]any
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		return nil, fmt.Errorf("unison: encoding plugin options: %w", err)
	}

	return decoded, nil
}

// relativeTo expresses target relative to base, resolving symlinks first.
//
// The symlink step is not incidental: on macOS a temporary directory is under
// /var, which is a symlink to /private/var, and a relative path computed across
// that boundary points somewhere that does not exist.
func relativeTo(base, target string) (string, error) {
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		resolvedBase = base
	}

	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		// The target may not exist yet — an output directory on a first run —
		// so fall back to the path as given rather than failing here.
		resolvedTarget = target
	}

	relative, err := filepath.Rel(resolvedBase, resolvedTarget)
	if err != nil {
		return "", fmt.Errorf("unison: locating %s from %s: %w", target, base, err)
	}

	return relative, nil
}
