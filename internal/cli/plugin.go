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
	level, err := parseLevel(os.Getenv(pluginenv.LogLevelEnvVar))
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
