package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/primandproper/sqlc-gen-unison/internal/generate"
	"github.com/primandproper/sqlc-gen-unison/internal/protocol"

	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"
)

// LogLevelEnvVar names the one environment variable unison reads. Plugin mode
// has no flags to take a log level from — sqlc chooses the arguments — so this
// is how a consumer turns on debug logging for a plugin run. It is passed
// through by the orchestrator, which puts it in the rendered sqlc config's
// `env:` list.
const LogLevelEnvVar = "UNISON_LOG_LEVEL"

// runPlugin serves the one request sqlc sends and returns.
//
// It does not go through cobra. sqlc passes the gRPC method to invoke as the
// first argument, which cobra would read as a subcommand, and there is no
// command here for it to find.
func runPlugin(ctx context.Context, stdin, stdout, stderr *os.File) error {
	level, err := parseLevel(os.Getenv(LogLevelEnvVar))
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	err = protocol.Serve(ctx, stdin, stdout, func(ctx context.Context, request *pb.GenerateRequest) ([]*pb.File, error) {
		return generate.Files(ctx, logger, request)
	})
	if err != nil {
		// Plugin mode does not go through cobra, so nothing else would print
		// this. sqlc reports only that the command failed — it does not relay
		// the plugin's stderr — so an unprinted error reaches the consumer as
		// "error running command" and nothing else, which is the difference
		// between a type they need to override and a mystery.
		//nolint:errcheck // Reporting is best effort; the error is returned either way.
		fmt.Fprintln(stderr, err)

		return err
	}

	return nil
}
