// Package cli wires unison's command-line interface together.
//
// unison is a sqlc codegen plugin and the orchestrator that drives it. The two
// modes share one binary, so this package owns the one decision that separates
// them and the logging setup they both use.
//
// Logging is stdlib slog to stderr, and that is not a style preference. In
// plugin mode sqlc hands the process a CodeGenRequest on stdin and reads a
// CodeGenResponse protobuf back from stdout, so stdout is a protocol channel:
// anything else written there corrupts the response. Nothing in this module may
// print to stdout outside of a command that owns its own output.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
)

// CommandName is the name the CLI reports for itself. It is the tool's name
// rather than the module's, because that is what a consumer types and what
// .unison-version pins.
const CommandName = "unison"

// application holds the process-wide dependencies constructed during startup.
type application struct {
	logger *slog.Logger
}

// Execute builds the root command and runs it.
func Execute(ctx context.Context) error {
	app := &application{logger: slog.New(slog.DiscardHandler)}

	return app.newRootCommand().ExecuteContext(ctx)
}

// newRootCommand constructs the cobra root command and registers subcommands.
func (a *application) newRootCommand() *cobra.Command {
	var logLevel string

	rootCmd := &cobra.Command{
		Use:          CommandName,
		Short:        "Generate one set of Go types and N dialects' SQL from one logical query set.",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return a.bootstrap(cmd.ErrOrStderr(), logLevel)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level: debug, info, warn, or error")

	rootCmd.AddCommand(a.newVersionCommand())

	return rootCmd
}

// bootstrap installs the logger every command shares. It writes to the stream it
// is given — stderr in practice — because stdout belongs to the plugin protocol.
func (a *application) bootstrap(stderr io.Writer, logLevel string) error {
	level, err := parseLevel(logLevel)
	if err != nil {
		return err
	}

	a.logger = slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	return nil
}

// log returns the application logger, or a logger that discards when bootstrap
// has not run.
func (a *application) log() *slog.Logger {
	if a.logger == nil {
		return slog.New(slog.DiscardHandler)
	}

	return a.logger
}

// parseLevel maps the --log-level flag onto a slog level, naming the offending
// value rather than silently defaulting.
func parseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q: want debug, info, warn, or error", name)
	}
}
