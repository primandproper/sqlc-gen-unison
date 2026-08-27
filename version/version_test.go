package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		stamped  string
		module   string
		expected string
	}{
		// scripts/build.sh is handed the tag; the module system at best
		// rediscovers it, so the linker always wins.
		"the linker wins":                 {stamped: "v1.2.3", module: "v0.1.0", expected: "v1.2.3"},
		"the linker wins over a checkout": {stamped: "v1.2.3", module: devel, expected: "v1.2.3"},
		"the linker wins over nothing":    {stamped: "v1.2.3", module: "", expected: "v1.2.3"},

		"a module install names its tag":  {stamped: development, module: "v0.1.0", expected: "v0.1.0"},
		"a pinned commit names its own":   {stamped: development, module: "v0.0.0-20260101120000-abcdef123456", expected: "v0.0.0-20260101120000-abcdef123456"},
		"a checkout stays dev":            {stamped: development, module: devel, expected: development},
		"no build information stays dev":  {stamped: development, module: "", expected: development},
		"an unversioned module stays dev": {stamped: development, module: "unknown", expected: development},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			test.Eq(t, testCase.expected, resolve(testCase.stamped, testCase.module))
		})
	}
}

// TestRecordedVersionIgnoresACheckout pins the trap this fallback has to step
// around.
//
// `go build` inside a git checkout does not record "(devel)" — it synthesizes a
// pseudo-version from HEAD and appends "+dirty" when the tree is modified. The
// Main.Version values below are transcribed from the real toolchain: an install
// resolved through the module system carries no vcs settings, and a checkout
// build carries four.
func TestRecordedVersionIgnoresACheckout(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		expected string
		info     debug.BuildInfo
	}{
		"a module install": {
			info:     debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}},
			expected: "v0.1.0",
		},
		"a source tree with no repository": {
			info:     debug.BuildInfo{Main: debug.Module{Version: devel}},
			expected: devel,
		},
		"a clean checkout": {
			info: debug.BuildInfo{
				Main: debug.Module{Version: "v0.2.1-0.20260826171751-e7cde45ce3df"},
				Settings: []debug.BuildSetting{
					{Key: "vcs", Value: "git"},
					{Key: "vcs.revision", Value: "e7cde45ce3dfd522389d6da34dbbc8aa9435788a"},
					{Key: "vcs.time", Value: "2026-08-26T17:17:51Z"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			expected: "",
		},
		"a dirty checkout": {
			info: debug.BuildInfo{
				Main: debug.Module{Version: "v0.2.1-0.20260826171751-e7cde45ce3df+dirty"},
				Settings: []debug.BuildSetting{
					{Key: "vcs", Value: "git"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			expected: "",
		},
		"settings that are not about version control": {
			info: debug.BuildInfo{
				Main:     debug.Module{Version: "v0.1.0"},
				Settings: []debug.BuildSetting{{Key: "-trimpath", Value: "true"}, {Key: "GOARCH", Value: "arm64"}},
			},
			expected: "v0.1.0",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			test.Eq(t, testCase.expected, recordedVersion(&testCase.info))
		})
	}
}

// TestVersionReportsDevFromACheckout pins the half of the fallback that must not
// fire.
//
// Version is the one build variable that reaches generated code, and the golden
// corpus is committed with `unison: dev` in its header. Every test run and every
// bare `go build` happens in a checkout, so a fallback that took whatever the
// toolchain recorded would rewrite that line — in this repository's goldens and
// in every consumer's committed output — on each new commit.
func TestVersionReportsDevFromACheckout(t *testing.T) {
	t.Parallel()

	test.Eq(t, development, Version)
}

// TestVersionSurvivesTheLinker pins the symbol scripts/build.sh writes to, by
// running a binary that was actually linked with it.
//
// `go build -ldflags -X` on a name that does not exist is silently ignored, so
// renaming or moving this variable would not break any build — it would ship
// releases that quietly call themselves "dev" and stamp that into consumers'
// generated files. The unstamped case is the same build the test harness makes,
// and proves the fallback leaves it alone.
func TestVersionSurvivesTheLinker(t *testing.T) {
	t.Parallel()

	const (
		commandPackage = "github.com/primandproper/sqlc-gen-unison/cmd/unison"
		versionPackage = "github.com/primandproper/sqlc-gen-unison/version"
	)

	cases := map[string]struct {
		ldflags  string
		expected string
	}{
		"a stamped release": {ldflags: "-X " + versionPackage + ".Version=v9.9.9", expected: "v9.9.9"},
		"an ordinary build": {ldflags: "", expected: development},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			binary := filepath.Join(t.TempDir(), "unison")

			build := exec.CommandContext(t.Context(),
				"go", "build", "-ldflags", testCase.ldflags, "-o", binary, commandPackage)

			output, buildErr := build.CombinedOutput()
			must.NoError(t, buildErr, must.Sprintf("building the CLI: %s", output))

			run := exec.CommandContext(t.Context(), binary, "version")
			run.Stderr = os.Stderr

			reported, runErr := run.Output()
			must.NoError(t, runErr)

			test.StrContains(t, string(reported), "version:     "+testCase.expected+"\n")

			// The toolchain's own placeholder is not a version, and must never
			// reach a consumer's generated header.
			test.False(t, strings.Contains(string(reported), devel))
		})
	}
}
