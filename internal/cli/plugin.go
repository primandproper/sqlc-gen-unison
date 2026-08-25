package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/primandproper/sqlc-gen-unison/internal/cli/pluginenv"
	"github.com/primandproper/sqlc-gen-unison/internal/generate"
	"github.com/primandproper/sqlc-gen-unison/internal/protocol"

	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"
)

// runPlugin serves the one request sqlc sends and returns.
//
// It does not go through cobra. sqlc passes the gRPC method to invoke as the
// first argument, which cobra would read as a subcommand, and there is no
// command here for it to find.
func runPlugin(ctx context.Context, stdin, stdout, stderr *os.File) error {
	err := servePlugin(ctx, stdin, stdout, stderr)
	if err != nil {
		// Plugin mode does not go through cobra, so nothing else would print
		// this. sqlc relays a failing plugin's stderr and nothing else, so an
		// unprinted error reaches the consumer as "error running command" and
		// nothing else, which is the difference between a type they need to
		// override and a mystery.
		//
		// Every failure gets here, not just the ones from generation. A level
		// this process could not parse used to exit non-zero without a word,
		// which is the same mystery arriving one step earlier.
		//nolint:errcheck // Reporting is best effort; the error is returned either way.
		fmt.Fprintln(stderr, err)

		return err
	}

	return nil
}

// servePlugin is runPlugin without the reporting, so that reporting has exactly
// one place to happen.
func servePlugin(ctx context.Context, stdin, stdout, stderr *os.File) error {
	level, err := parseLevel(os.Getenv(pluginenv.LogLevelEnvVar))
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	return protocol.Serve(ctx, stdin, stdout, func(ctx context.Context, request *pb.GenerateRequest) ([]*pb.File, error) {
		return generate.Files(ctx, logger, request)
	})
}
