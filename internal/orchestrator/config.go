// Package orchestrator is `unison generate` and `unison check`: it reads
// unison.yaml, renders a sqlc config per dialect, and runs the pinned sqlc.
//
// It exists so a consumer calls one tool and cannot get the ordering or the sqlc
// version wrong. Every run points `out:` at the same directory, which is what
// makes the shared files converge.
package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/primandproper/sqlc-gen-unison/internal/options"

	"go.yaml.in/yaml/v3"
)

// ConfigFilename is the file `unison generate` looks for by default.
const ConfigFilename = "unison.yaml"

// engines are the dialects sqlc can analyze, and therefore the only ones unison
// supports. §3 is explicit that there is no plugin seam for engines and that we
// do not want one: if sqlc cannot analyze a dialect, unison does not support it.
var engines = []string{"mysql", "postgresql", "sqlite"}

// Config is unison.yaml.
//
// Field documentation is here rather than beside each field because this
// repository's formatter reorders struct fields.
//
//   - SQLCVersion is the sqlc release this project generates with, and it is
//     checked against the sqlc actually on PATH before anything runs.
//
//   - Package names the Go package the emitted files declare.
//
//   - Out is the directory every dialect's run writes to. One directory is the
//     design, not a convenience: the shared files are written once per dialect
//     and are byte-identical when the dialects agree.
//
//   - Schemas maps each dialect to its DDL. Its keys are the roster — there is
//     deliberately no separate `dialects:` list, because two places to say the
//     same thing is one place to say it differently.
//
//   - Queries maps each dialect to its query file or directory, and must have
//     exactly the same keys as Schemas.
//
//   - Options is everything the plugin needs that is not derived from the
//     above. It shares its definition with the plugin's own options, so the two
//     ends cannot drift.
type Config struct {
	Schemas     map[string]string `yaml:"schemas"`
	Queries     map[string]string `yaml:"queries"`
	SQLCVersion string            `yaml:"sqlc_version"`
	Package     string            `yaml:"package"`
	Out         string            `yaml:"out"`
	dir         string
	Options     options.Options `yaml:"options"`
}

// Load reads and validates a unison.yaml.
func Load(path string) (*Config, error) {
	contents, err := os.ReadFile(path) // #nosec G304 -- the path is the consumer's own config, named on their command line.
	if err != nil {
		return nil, fmt.Errorf("unison: reading %s: %w", path, err)
	}

	var cfg Config

	decoder := yaml.NewDecoder(strings.NewReader(string(contents)))
	decoder.KnownFields(true)

	if err = decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("unison: reading %s: %w", path, err)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("unison: resolving %s: %w", path, err)
	}

	cfg.dir = filepath.Dir(absolute)

	if err = cfg.validate(); err != nil {
		return nil, fmt.Errorf("unison: %s: %w", path, err)
	}

	return &cfg, nil
}

// Roster is every dialect in the run, sorted.
//
// It is the keys of `schemas:` and nothing else. A roster that could disagree
// with the schemas it is generated from would be a second source of truth for
// the one fact every invocation depends on.
func (c *Config) Roster() []string {
	roster := make([]string, 0, len(c.Schemas))
	for dialect := range c.Schemas {
		roster = append(roster, dialect)
	}

	slices.Sort(roster)

	return roster
}

// validate rejects a config that would produce output nobody wants.
func (c *Config) validate() error {
	if strings.TrimSpace(c.Package) == "" {
		return fmt.Errorf("`package` is required; it names the Go package the emitted files declare")
	}

	if strings.TrimSpace(c.Out) == "" {
		return fmt.Errorf("`out` is required; it is the directory every dialect generates into")
	}

	if len(c.Schemas) == 0 {
		return fmt.Errorf("`schemas` is required; its keys are the dialect roster")
	}

	for _, dialect := range c.Roster() {
		if !slices.Contains(engines, dialect) {
			return fmt.Errorf("`schemas` names %q, which sqlc cannot analyze; the engines are %s",
				dialect, strings.Join(engines, ", "))
		}

		if _, ok := c.Queries[dialect]; !ok {
			return fmt.Errorf("`schemas` names %q but `queries` does not; every dialect needs both", dialect)
		}
	}

	// A dialect with queries and no schema would be analyzed against nothing,
	// but more to the point it would not be in the roster — so its queries would
	// silently never be generated.
	for dialect := range c.Queries {
		if _, ok := c.Schemas[dialect]; !ok {
			return fmt.Errorf("`queries` names %q but `schemas` does not, so it is not in the roster and would never be generated", dialect)
		}
	}

	c.Options.Package = c.Package
	c.Options.Roster = c.Roster()

	if err := c.Options.Normalize(); err != nil {
		return err
	}

	return nil
}

// PluginOptions returns the options for one run, which are the same for every
// dialect. That sameness is the whole mechanism: the shared files are a function
// of the roster and the options, so identical inputs produce identical bytes.
func (c *Config) PluginOptions() options.Options {
	return c.Options
}
