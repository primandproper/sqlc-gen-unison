// Package containers is the shared lifecycle for unison's container-backed
// tests: the gate that keeps a bare `go test` from needing a Docker daemon, the
// retry policies that absorb a cold start, and one Run that owns a container so
// a test body only has to say what to do with a live database.
//
// It exists because unison generates argument order, and an argument-order bug
// is invisible to everything else the suite does. Generated code that binds
// sixteen MySQL placeholders from eight fields in the wrong order compiles,
// regenerates byte-identically, and matches its golden file. Only executing it
// against a real server disagrees.
//
// The shape here follows platform-go's testutils/containers, deliberately: an
// engineer who knows that harness should recognize this one. It is much smaller
// — unison has no per-test schema isolation, no template cloning, and no
// database client abstraction to bridge — but the parts that are here mean what
// they mean there.
package containers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shoenig/test/must"
	"github.com/testcontainers/testcontainers-go"
)

const (
	// startAttempts and startInitialDelay absorb a cold start: a Docker daemon
	// waking up, an image pull, a port collision.
	startAttempts     = 5
	startInitialDelay = time.Second

	// readyAttempts and readyInitialDelay are deliberately not the startup
	// policy. A server that has already logged its readiness line is usually a
	// few hundred milliseconds from accepting connections, not a second, and
	// this ladder adds up to roughly ten seconds of patience for the
	// stragglers.
	readyAttempts     = 10
	readyInitialDelay = 100 * time.Millisecond
	readyMaxDelay     = 2 * time.Second

	// ShutdownTimeout bounds termination. It runs on a context detached from
	// the test's, so a test that has already blown its own deadline still gets
	// its container reaped.
	ShutdownTimeout = 30 * time.Second
)

// EnvVar names the variable that opens the gate.
//
// It is spelled the way platform-go spells it so that one export in a shell
// governs both repositories, which is the whole ergonomic benefit of matching
// somebody else's convention.
const EnvVar = "RUN_CONTAINER_TESTS"

// Running reports whether container-backed tests were asked for. It is read
// once, at package init, so every test in a run agrees.
//
//nolint:gochecknoglobals // read-only, set once from the environment.
var Running = strings.TrimSpace(strings.ToLower(os.Getenv(EnvVar))) == "true"

// SkipIfNotRunning skips unless containers were asked for.
//
// -short skips even when the variable is set, and deliberately: -short is the
// caller saying they want a fast answer, and standing up two databases is the
// slowest thing this repository does.
func SkipIfNotRunning(tb testing.TB) {
	tb.Helper()

	if !Running {
		tb.Skipf("%s is not true; skipping the container-backed run", EnvVar)
	}

	if testing.Short() {
		tb.Skip("-short; skipping the container-backed run")
	}
}

// Terminable is the teardown half of the testcontainers API — the only part Run
// needs in order to own a container's life.
type Terminable interface {
	Terminate(ctx context.Context, opts ...testcontainers.TerminateOption) error
}

// Run starts a container, hands it to fn, and terminates it afterwards.
//
// Termination is registered with tb.Cleanup rather than deferred until fn
// returns, and the distinction is load-bearing: a closure that registers
// parallel subtests returns before those subtests execute, and a deferred
// Terminate would pull the container out from under them.
//
// The flip side is that the container lives until the end of tb rather than the
// end of fn, so call Run from the narrowest test that needs it.
func Run[C Terminable](tb testing.TB, start func(ctx context.Context) (C, error), fn func(ctx context.Context, container C)) {
	tb.Helper()

	SkipIfNotRunning(tb)

	ctx := tb.Context()

	container, err := StartWithRetry(ctx, start)
	must.NoError(tb, err)

	tb.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
		defer cancel()

		if terminateErr := container.Terminate(shutdownCtx); terminateErr != nil {
			tb.Logf("containers: terminating: %v", terminateErr)
		}
	})

	fn(ctx, container)
}

// StartWithRetry invokes start with exponential backoff.
func StartWithRetry[C any](ctx context.Context, start func(context.Context) (C, error)) (C, error) {
	var (
		container C
		err       error
	)

	delay := startInitialDelay

	for attempt := range startAttempts {
		if container, err = start(ctx); err == nil {
			return container, nil
		}

		if attempt == startAttempts-1 {
			break
		}

		if waitErr := sleep(ctx, delay); waitErr != nil {
			return container, waitErr
		}

		delay *= 2
	}

	return container, err
}

// PingUntilReady calls ping until it succeeds, failing tb if it never does.
//
// A container's readiness log is not the same event as its server accepting
// connections. Both the Postgres and MySQL images run an init pass against a
// temporary server and then restart, so a wait strategy matching log lines can
// release the test against a socket that is about to close. Downstream that
// looks like "bad connection" or "unexpected EOF" from the very first
// statement, on a container that is perfectly healthy a second later.
//
// Retrying the first ping is the fix that does not depend on reading logs: no
// pool is handed to a test until a query has actually round-tripped.
func PingUntilReady(tb testing.TB, ctx context.Context, ping func(context.Context) error) {
	tb.Helper()

	var err error

	delay := readyInitialDelay

	for attempt := range readyAttempts {
		if err = ping(ctx); err == nil {
			return
		}

		if attempt == readyAttempts-1 {
			break
		}

		must.NoError(tb, sleep(ctx, delay))

		if delay *= 2; delay > readyMaxDelay {
			delay = readyMaxDelay
		}
	}

	must.NoError(tb, err, must.Sprint("the container never accepted a connection"))
}

// sleep waits, or gives up early when the context is done.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
