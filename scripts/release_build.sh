#!/usr/bin/env bash
set -euo pipefail

# Build the release binaries for every platform a consumer or their CI runs on,
# and record their checksums.
#
# Usage: release_build.sh <output_dir> [version]
#
# The version reaches the binary through scripts/build.sh, which writes it into
# version.Version — the one build variable that ends up inside generated files.
# That is why this script refuses to guess it: a release built as "dev" would
# stamp "dev" into every consumer's committed output, and consumers gate CI on a
# clean tree, so the mistake would surface as a diff in somebody else's
# repository rather than here.

OUT_DIR="${1:?missing output directory}"
VERSION="${2:-${UNISON_VERSION:-}}"

if [[ -z "${VERSION}" ]]; then
	echo "error: no version given, and UNISON_VERSION is unset." >&2
	echo "       Pass the tag: release_build.sh <output_dir> v1.2.3" >&2
	exit 1
fi

if [[ ! "${VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
	echo "error: version '${VERSION}' is not a v-prefixed semantic version." >&2
	exit 1
fi

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CMD_PACKAGE="github.com/primandproper/sqlc-gen-unison/cmd/main"
BINARY_NAME="unison"

# The platforms consumers and their CI actually run. Pure Go, so CGO is off and
# the result is a static binary that does not care what libc the host has.
PLATFORMS=(
	"linux/amd64"
	"linux/arm64"
	"darwin/amd64"
	"darwin/arm64"
)

mkdir -p "${OUT_DIR}"
OUT_DIR="$(cd "${OUT_DIR}" && pwd)"

for platform in "${PLATFORMS[@]}"; do
	goos="${platform%/*}"
	goarch="${platform#*/}"
	artifact="${BINARY_NAME}_${VERSION}_${goos}_${goarch}"

	echo "Building ${artifact}..."

	CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" UNISON_VERSION="${VERSION}" \
		"${PROJECT_ROOT}/scripts/build.sh" -o "${OUT_DIR}/${artifact}" "${CMD_PACKAGE}"
done

# Checksums are written with bare filenames so that `sha256sum -c checksums.txt`
# works from inside the directory the assets were downloaded into, which is what
# a consumer's ensure-tool-installed script has on hand.
cd "${OUT_DIR}"

if command -v sha256sum &>/dev/null; then
	sha256sum "${BINARY_NAME}_${VERSION}"_* >checksums.txt
elif command -v shasum &>/dev/null; then
	shasum -a 256 "${BINARY_NAME}_${VERSION}"_* >checksums.txt
else
	echo "error: neither sha256sum nor shasum is available to checksum the release." >&2
	exit 1
fi

echo
echo "Release artifacts in ${OUT_DIR}:"
cat checksums.txt
