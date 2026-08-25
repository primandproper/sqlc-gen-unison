// Package sqlcdriver runs the pinned sqlc.
//
// unison does not analyze SQL. sqlc does, and this package is the whole of
// unison's relationship with it: render a config, run the binary, report what it
// said. The orchestrator uses it to generate; the test suite uses it to prove
// the plugin round trip against a real analyzer rather than a hand-built
// protobuf.
package sqlcdriver

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// PinnedVersion is the sqlc release unison is built and tested against.
//
// It is a pin rather than a floor because the CodeGenRequest protobuf is a
// moving target and the analysis it carries is the input to every shape decision
// here. A consumer whose sqlc differs is generating from a different analyzer
// than the one the golden files were produced by.
const PinnedVersion = "v1.31.1"

// Run invokes sqlc with the given arguments in dir.
//
// stdout and stderr are captured rather than inherited: sqlc reports analysis
// errors on stdout, and a caller wants them in the error it returns rather than
// interleaved into whatever the process is already writing.
func Run(ctx context.Context, binary, dir string, args ...string) (string, error) {
	// #nosec G204 -- running a named binary with caller-supplied arguments is
	// this package's entire purpose. The binary is the sqlc a consumer pinned
	// and the arguments are unison's own; neither is attacker-controlled input.
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := strings.TrimSpace(stdout.String() + stderr.String())

	if err != nil {
		return output, fmt.Errorf("sqlc %s: %w: %s", strings.Join(args, " "), err, output)
	}

	return output, nil
}

// Version reports the version of the sqlc at binary.
func Version(ctx context.Context, binary string) (string, error) {
	out, err := Run(ctx, binary, "", "version")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}
