#!/usr/bin/env bash
set -euo pipefail

# Emit every Go file this module owns, NUL-separated, for the formatters to
# consume with `read -r -d ''`.
# Usage: go_files.sh [project_root]
#
# The list comes from the Go toolchain rather than from a find(1) exclusion
# list, because the toolchain already knows the answer. `./...` does not
# descend into vendor/ or testdata/, nor into any directory whose name begins
# with _ or . — which is why every wildcard-driven target here (test, lint,
# go fix, fieldalignment, tagalign) needs no exclusions at all, and only the
# tools that walk the filesystem ever did.
#
# Those tools used to answer the question themselves, three different ways:
# format_golang.sh and format_imports.sh each carried their own
# `-not -path '*/vendor/*'`, the formatting workflow filtered gofmt's output
# with `grep -Ev '^vendor\/'`, and goimports.sh ran `goimports -w .` with no
# exclusion whatsoever — which rewrote every vendored file in the tree. One
# question deserves one answer, and this is it.
#
# -e keeps a package that does not compile in the list. Formatting a file is
# most useful exactly when it is still broken, so a syntax error somewhere in
# the module must not empty the whole list.
#
# -maxdepth 1 because what go list names are package directories, and a package
# is entitled to a testdata directory of its own.

PROJECT_ROOT="${1:-$(pwd)}"

cd "${PROJECT_ROOT}"

# go list is asked for the directories separately from walking them, so that a
# failure to list is a failure of this script rather than an empty answer.
# `go list` exits non-zero for module-level problems that -e does not cover — an
# out-of-sync vendor/modules.txt being the one to expect — and a formatter that
# quietly formats nothing, or a CI check that quietly checks nothing, is a worse
# outcome than either a wrong file list or a loud stop.
package_dirs="$(go list -e -f '{{.Dir}}' ./...)"

if [ -z "${package_dirs}" ]; then
  echo "go_files.sh: go list named no packages under ${PROJECT_ROOT}" >&2
  exit 1
fi

printf '%s\n' "${package_dirs}" | while IFS= read -r dir; do
  # A package with no directory on disk is not something to walk; -e means the
  # list can carry entries the loader could not resolve.
  [ -n "${dir}" ] && [ -d "${dir}" ] || continue

  find "${dir}" -maxdepth 1 -type f -name '*.go' -print0
done
