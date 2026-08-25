package cli

import (
	"context"
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

	return protocol.Serve(ctx, stdin, stdout, func(ctx context.Context, request *pb.GenerateRequest) ([]*pb.File, error) {
		return generate.Files(ctx, logger, request)
	})
}
