// Package version exposes build metadata about the compiled binary.
//
// The exported variables are intentionally left empty at compile time and are
// populated via -ldflags -X by scripts/build.sh. When the binary is built with
// `go build` directly (no ldflags), they report "unknown".
package version

// unknown is the placeholder reported when build metadata was not injected.
const unknown = "unknown"

// Version is the release this binary claims to be, and the only version string
// that reaches generated code.
//
// It is deliberately not CommitHash. Generated files carry the generator's
// version so a consumer can tell what produced them, and a commit hash would
// change that line on every build — turning every unrelated rebuild into a diff
// for consumers who gate CI on a clean tree after regeneration. A release moves
// this; a build does not.
var Version = "dev"

// Build metadata, injected at link time by scripts/build.sh.
var (
	// CommitHash is the git commit the binary was built from.
	CommitHash = unknown
	// BuildTime is the UTC timestamp the binary was built at.
	BuildTime = unknown
	// CommitTime is the committer timestamp of CommitHash.
	CommitTime = unknown
)
