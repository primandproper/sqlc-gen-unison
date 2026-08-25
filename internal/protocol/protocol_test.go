package protocol_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/primandproper/sqlc-gen-unison/internal/protocol"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"
	"google.golang.org/protobuf/proto"
)

func TestIsPluginInvocation(t *testing.T) {
	t.Parallel()

	pipe := pipeStdin(t)

	cases := map[string]struct {
		stdin    *os.File
		args     []string
		expected bool
	}{
		// sqlc v1.24.0 and later name the gRPC method to invoke.
		"sqlc names the method":  {args: []string{"/plugin.CodegenService/Generate"}, stdin: pipe, expected: true},
		"subcommand is not one":  {args: []string{"generate"}, stdin: pipe, expected: false},
		"check is not one":       {args: []string{"check"}, stdin: pipe, expected: false},
		"a flag is not one":      {args: []string{"--log-level", "debug"}, stdin: pipe, expected: false},
		"older sqlc pipes stdin": {args: nil, stdin: pipe, expected: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			test.Eq(t, tc.expected, protocol.IsPluginInvocation(tc.args, tc.stdin))
		})
	}
}

// TestServeRoundTrip drives the exchange in memory: a marshaled request in, a
// response carrying the generator's files out.
func TestServeRoundTrip(t *testing.T) {
	t.Parallel()

	request := &pb.GenerateRequest{
		SqlcVersion: "v1.31.1",
		Settings:    &pb.Settings{Engine: "postgresql"},
		Queries:     []*pb.Query{{Name: "GetUser", Cmd: ":one"}},
	}

	requestBytes, err := proto.Marshal(request)
	must.NoError(t, err)

	var stdout bytes.Buffer

	err = protocol.Serve(t.Context(), bytes.NewReader(requestBytes), &stdout,
		func(_ context.Context, got *pb.GenerateRequest) ([]*pb.File, error) {
			// The generator sees what sqlc sent.
			test.Eq(t, "postgresql", got.GetSettings().GetEngine())
			must.SliceLen(t, 1, got.GetQueries())
			test.Eq(t, "GetUser", got.GetQueries()[0].GetName())

			return []*pb.File{{Name: "types.go", Contents: []byte("package db\n")}}, nil
		})
	must.NoError(t, err)

	var response pb.GenerateResponse

	must.NoError(t, proto.Unmarshal(stdout.Bytes(), &response))
	must.SliceLen(t, 1, response.GetFiles())
	test.Eq(t, "types.go", response.GetFiles()[0].GetName())
	test.Eq(t, "package db\n", string(response.GetFiles()[0].GetContents()))
}

// TestServeWritesNothingOnGeneratorError pins the rule that makes a failed
// generation legible to sqlc: a partial or empty response on stdout would be
// decoded as a successful generation of no files, quietly deleting the
// consumer's package. Writing nothing means sqlc reports a plugin failure.
func TestServeWritesNothingOnGeneratorError(t *testing.T) {
	t.Parallel()

	requestBytes, err := proto.Marshal(&pb.GenerateRequest{Settings: &pb.Settings{Engine: "mysql"}})
	must.NoError(t, err)

	sentinel := errors.New("shapes diverge")

	var stdout bytes.Buffer

	err = protocol.Serve(t.Context(), bytes.NewReader(requestBytes), &stdout,
		func(context.Context, *pb.GenerateRequest) ([]*pb.File, error) { return nil, sentinel })

	must.ErrorIs(t, err, sentinel)
	test.SliceEmpty(t, stdout.Bytes())
}

// TestServeRejectsGarbage keeps a decode failure a decode failure, rather than
// an empty request the generator would treat as a schema with no queries.
func TestServeRejectsGarbage(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	called := false
	err := protocol.Serve(t.Context(), bytes.NewReader([]byte("this is not a protobuf")), &stdout,
		func(context.Context, *pb.GenerateRequest) ([]*pb.File, error) {
			called = true

			return nil, nil
		})

	must.Error(t, err)
	test.False(t, called)
	test.SliceEmpty(t, stdout.Bytes())
}

// pipeStdin returns a regular file standing in for a piped stdin: not a
// character device, which is the property IsPluginInvocation reads.
func pipeStdin(t *testing.T) *os.File {
	t.Helper()

	path := filepath.Join(t.TempDir(), "stdin")

	f, err := os.Create(path)
	must.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	return f
}
