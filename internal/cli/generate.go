package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/primandproper/sqlc-gen-unison/internal/orchestrator"

	"github.com/spf13/cobra"
)

// newGenerateCommand returns the `generate` subcommand: the orchestrator that
// consumers call from `make generate`.
func (a *application) newGenerateCommand() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate the Go package from unison.yaml.",
		Long: `Generate reads unison.yaml, renders a sqlc config per dialect, and runs the
pinned sqlc once for each — every run writing to the same directory.

Each run emits that dialect's query file plus the files shared by all of them.
When the dialects agree those shared writes are byte-identical; when they do not,
the generated package does not compile, and the compiler names what moved.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, cfg, err := a.orchestrate(cmd.Context(), configPath)
			if err != nil {
				return err
			}

			return runner.Generate(cmd.Context(), cfg)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", orchestrator.ConfigFilename, "path to unison.yaml")

	return cmd
}

// newCheckCommand returns the `check` subcommand.
func (a *application) newCheckCommand() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Statically check every dialect's SQL against its schema, generating nothing.",
		Long: `Check runs sqlc's static analysis over every dialect in unison.yaml and writes
nothing.

It is here so a project runs one tool for both tiers: the statements that go
through generation, and the ones that are still hand-written but should still be
checked against the schema they run against.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, cfg, err := a.orchestrate(cmd.Context(), configPath)
			if err != nil {
				return err
			}

			return runner.Check(cmd.Context(), cfg)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", orchestrator.ConfigFilename, "path to unison.yaml")

	return cmd
}

// orchestrate assembles what both subcommands need.
func (a *application) orchestrate(_ context.Context, configPath string) (*orchestrator.Runner, *orchestrator.Config, error) {
	cfg, err := orchestrator.Load(configPath)
	if err != nil {
		return nil, nil, err
	}

	sqlc, err := exec.LookPath("sqlc")
	if err != nil {
		return nil, nil, fmt.Errorf(
			"unison: sqlc is not on PATH. unison does not analyze SQL — sqlc does — so it has to be installed: %w", err)
	}

	self, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("unison: locating this binary, which sqlc must run as the plugin: %w", err)
	}

	return &orchestrator.Runner{Logger: a.log(), SQLC: sqlc, Self: self}, cfg, nil
}
