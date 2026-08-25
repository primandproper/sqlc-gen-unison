#!/usr/bin/env bash
set -euo pipefail

# Build packages. Two modes:
# 1) Single binary with VCS ldflags: build.sh -o <output_path> <package>
#    e.g. build.sh -o /server github.com/org/package
# 2) Build all packages (no VCS): build.sh <package_list>
#    e.g. build.sh "$(go list ./...)"

VERSION_PKG="github.com/primandproper/sqlc-gen-unison/version"

if [[ "${1:-}" == "-o" ]]; then
	OUT="${2:?missing output path after -o}"
	PACKAGE="${3:?missing package path}"
	# VCS/build vars for ldflags (fallback when git unavailable or shallow)
	COMMIT_HASH="${GITHUB_SHA:-${BUILDKITE_COMMIT:-}}"
	if [[ -z "$COMMIT_HASH" ]] && command -v git &>/dev/null; then
		COMMIT_HASH=$(git rev-parse HEAD 2>/dev/null || echo "unknown")
	fi
	[[ -z "$COMMIT_HASH" ]] && COMMIT_HASH="unknown"

	BUILD_TIME=""
	if command -v date &>/dev/null; then
		BUILD_TIME=$(date -u -Iseconds 2>/dev/null || true)
	fi
	[[ -z "$BUILD_TIME" ]] && BUILD_TIME="unknown"

	COMMIT_TIME=""
	if command -v git &>/dev/null; then
		COMMIT_TIME=$(git log -1 --format=%cI HEAD 2>/dev/null || true)
	fi
	[[ -z "$COMMIT_TIME" ]] && COMMIT_TIME="unknown"

	# VERSION is the only build metadata that reaches generated code, so it is
	# resolved differently from the rest: a release names itself, and everything
	# else says so plainly.
	#
	# It is deliberately not COMMIT_HASH. Generated files carry this string, and
	# consumers gate CI on a clean tree after regeneration — a hash would turn
	# every unrelated rebuild of unison into a diff in somebody else's
	# repository. A tag moves it; a commit does not.
	VERSION="${UNISON_VERSION:-}"
	if [[ -z "$VERSION" ]] && command -v git &>/dev/null; then
		VERSION=$(git describe --tags --exact-match 2>/dev/null || true)
	fi
	[[ -z "$VERSION" ]] && VERSION="dev"

	LDFLAGS="-s -w -X ${VERSION_PKG}.Version=${VERSION} -X ${VERSION_PKG}.CommitHash=${COMMIT_HASH} -X ${VERSION_PKG}.BuildTime=${BUILD_TIME} -X ${VERSION_PKG}.CommitTime=${COMMIT_TIME}"
	go build -trimpath -ldflags "$LDFLAGS" -o "$OUT" "$PACKAGE"
else
	PACKAGE_LIST="${1:?missing package list}"
	# shellcheck disable=SC2086
	go build ${PACKAGE_LIST}
fi
