// Package protocol carries sqlc's codegen plugin wire format across this
// module's boundary and no further.
//
// sqlc invokes a plugin as a subprocess: it writes a GenerateRequest protobuf to
// the process's stdin, and reads a GenerateResponse protobuf back from its
// stdout. That makes stdout a protocol channel rather than a place to print,
// which is why every other package here logs to stderr and why Serve takes its
// streams as arguments instead of reaching for os.Stdin and os.Stdout — a test
// can drive the whole exchange in memory.
//
// The wire framing stops here. Reading the analysis a request carries is the
// convergence core's job, and rendering it is an emitter's; this package only
// gets the bytes on and off the process boundary.
package protocol

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"
	"google.golang.org/protobuf/proto"
)

// Generator turns one dialect's analysis into the files sqlc should write.
//
// It is called once per sqlc `sql:` block, which is once per dialect — the
// constraint the whole design is arranged around. The returned files are
// written verbatim, relative to that block's `out:` directory.
type Generator func(context.Context, *pb.GenerateRequest) ([]*pb.File, error)

// IsPluginInvocation reports whether args (os.Args[1:]) and stdin describe a
// call from sqlc rather than a person at a shell.
//
// sqlc v1.24.0 and later pass the gRPC method to invoke as the first argument —
// "/plugin.CodegenService/Generate" — which is why a leading slash is the
// signal. Nothing a person would type as a subcommand starts with one.
//
// Older sqlc passed no arguments at all, which is indistinguishable from a
// person typing `unison` and expecting help. stdin settles it: sqlc pipes the
// request in, a terminal does not.
func IsPluginInvocation(args []string, stdin *os.File) bool {
	if len(args) > 0 {
		return strings.HasPrefix(args[0], "/")
	}

	return !isTerminal(stdin)
}

// isTerminal reports whether f is a character device, which is what a terminal
// is and a pipe is not. A stat failure is treated as "not a terminal" only when
// there is nothing to stat, so a closed stdin does not silently start a server.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}

	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// Serve reads one GenerateRequest from stdin, hands it to gen, and writes the
// GenerateResponse to stdout. It is the entire lifetime of a plugin process:
// sqlc starts one per block and reads exactly one response.
//
// An error from gen is returned rather than encoded, and nothing is written to
// stdout in that case. sqlc treats a non-zero exit with no response as a plugin
// failure and reports the process's stderr, which is where our diagnostics
// already are.
func Serve(ctx context.Context, stdin io.Reader, stdout io.Writer, gen Generator) error {
	requestBytes, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("reading the CodeGenRequest from stdin: %w", err)
	}

	var request pb.GenerateRequest
	if err = proto.Unmarshal(requestBytes, &request); err != nil {
		return fmt.Errorf("decoding the CodeGenRequest: %w", err)
	}

	files, err := gen(ctx, &request)
	if err != nil {
		return err
	}

	responseBytes, err := proto.Marshal(&pb.GenerateResponse{Files: files})
	if err != nil {
		return fmt.Errorf("encoding the CodeGenResponse: %w", err)
	}

	if _, err = stdout.Write(responseBytes); err != nil {
		return fmt.Errorf("writing the CodeGenResponse to stdout: %w", err)
	}

	return nil
}
