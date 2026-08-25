package cli

import (
	"fmt"
	"runtime"

	"github.com/primandproper/sqlc-gen-unison/version"

	"github.com/spf13/cobra"
)

// newVersionCommand returns the `version` subcommand, which reports the build
// metadata injected at link time along with the Go toolchain and platform.
func (a *application) newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build and version information.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a.log().Debug("printing version information")

			// Version data goes to stdout (cobra's Print* default to stderr) so
			// `unison version` stays machine-parseable.
			_, err := fmt.Fprintf(cmd.OutOrStdout(),
				"version:     %s\ncommit:      %s\nbuilt:       %s\ncommit time: %s\ngo:          %s\nplatform:    %s/%s\n",
				version.Version, version.CommitHash, version.BuildTime, version.CommitTime,
				runtime.Version(), runtime.GOOS, runtime.GOARCH,
			)

			return err
		},
	}
}
