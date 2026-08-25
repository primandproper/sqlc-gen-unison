// Package generate is the plugin's entry point: one dialect's analysis in, the
// files sqlc should write out.
//
// It is deliberately thin. Reading the analysis is converge's job, rendering it
// is an emitter's, and this package is the wiring between them plus the options
// both need. Keeping it thin is what makes the IR seam real rather than
// nominal.
package generate

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/primandproper/sqlc-gen-unison/internal/converge"
	"github.com/primandproper/sqlc-gen-unison/internal/emit/gogen"
	"github.com/primandproper/sqlc-gen-unison/internal/options"
	"github.com/primandproper/sqlc-gen-unison/version"

	pb "github.com/sqlc-dev/plugin-sdk-go/plugin"
)

// Files renders the response for one dialect's analysis.
func Files(_ context.Context, logger *slog.Logger, request *pb.GenerateRequest) ([]*pb.File, error) {
	if request.GetSettings() == nil {
		return nil, fmt.Errorf("unison: the request carries no settings, so there is no engine to generate for")
	}

	opts, err := options.Parse(request.GetPluginOptions())
	if err != nil {
		return nil, err
	}

	pkg, err := converge.Package(logger, request, opts, version.Version)
	if err != nil {
		return nil, err
	}

	logger.Info("converged",
		slog.String("dialect", pkg.Dialect),
		slog.Any("roster", pkg.Roster),
		slog.Int("queries", len(pkg.Queries)),
	)

	emitted, err := gogen.Emit(pkg)
	if err != nil {
		return nil, err
	}

	files := make([]*pb.File, 0, len(emitted))
	for i := range emitted {
		files = append(files, &pb.File{Name: emitted[i].Name, Contents: emitted[i].Contents})
	}

	return files, nil
}
