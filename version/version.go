// Package version exposes build metadata about the compiled binary.
//
// The exported variables are populated via -ldflags -X by scripts/build.sh.
// When the binary is built without them, the commit metadata reports "unknown"
// and Version falls back to the version the module system resolved, so a
// `go install ...@v0.1.0` names the tag rather than claiming to be a development
// build. See resolve for the rule.
package version

import (
	"runtime/debug"
	"strings"
)

// unknown is the placeholder reported when build metadata was not injected.
const unknown = "unknown"

// development is what Version reports for any binary that was not stamped by
// scripts/build.sh and did not come from the module system.
const development = "dev"

// devel is what runtime/debug reports for the main module of a binary built
// outside a version control checkout. It is not a version anyone can install, so
// it is not one unison will print.
const devel = "(devel)"

// vcsSetting is the build setting the Go toolchain records when it builds a main
// package from a version control checkout, along with the "vcs."-prefixed
// details that accompany it.
const vcsSetting = "vcs"

// Version is the release this binary claims to be, and the only version string
// that reaches generated code.
//
// It is deliberately not CommitHash. Generated files carry the generator's
// version so a consumer can tell what produced them, and a commit hash would
// change that line on every build — turning every unrelated rebuild into a diff
// for consumers who gate CI on a clean tree after regeneration. A release moves
// this; a build does not.
//
// -ldflags -X writes here directly, which is why the fallback below runs in init
// rather than in this variable's initializer: the linker's value has to be in
// place before anything can decide whether it needs replacing.
var Version = development

// Build metadata, injected at link time by scripts/build.sh.
var (
	// CommitHash is the git commit the binary was built from.
	CommitHash = unknown
	// BuildTime is the UTC timestamp the binary was built at.
	BuildTime = unknown
	// CommitTime is the committer timestamp of CommitHash.
	CommitTime = unknown
)

func init() {
	Version = resolve(Version, moduleVersion())
}

// resolve picks between the version the linker stamped and the one the module
// system recorded.
//
// The linker wins whenever it said anything, because scripts/build.sh is told
// the tag and the module system at best rediscovers it. The module version is
// the fallback for the install path that never runs that script:
// `go install github.com/primandproper/sqlc-gen-unison/cmd/unison@v0.1.0` links
// no ldflags at all, and reporting "dev" there stamps "dev" into every file the
// install generates.
func resolve(stamped, module string) string {
	if stamped != development {
		return stamped
	}

	if isModuleVersion(module) {
		return module
	}

	return development
}

// isModuleVersion reports whether the recorded main-module version is one the
// module system resolved, rather than one the toolchain derived from a checkout.
//
// Everything the module system resolves is v-prefixed and immutable — a tag, or
// a pseudo-version naming one commit. "(devel)" is neither, and is the
// placeholder for a binary built outside a checkout.
func isModuleVersion(module string) bool {
	return module != devel && strings.HasPrefix(module, "v")
}

// moduleVersion reports the version the module system resolved for this binary,
// or the empty string when there is no build information to read.
func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	return recordedVersion(info)
}

// recordedVersion reports the main module's version when the module system
// resolved it, and the empty string when the Go toolchain derived it from a
// version control checkout instead.
//
// The distinction is the whole reason this is not a one-liner. `go build` in a
// checkout does not report "(devel)": it synthesizes a pseudo-version from the
// commit and appends "+dirty" for a modified tree. Taking that would rewrite the
// version line of every generated file on every commit — and on every
// uncommitted edit — which is exactly the churn Version's own documentation
// exists to prevent. The toolchain records a "vcs" build setting precisely when
// it derived the version that way, and records none for a module the proxy
// resolved, so that setting is the discriminator.
func recordedVersion(info *debug.BuildInfo) string {
	for i := range info.Settings {
		if key := info.Settings[i].Key; key == vcsSetting || strings.HasPrefix(key, vcsSetting+".") {
			return ""
		}
	}

	return info.Main.Version
}
