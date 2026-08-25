#!/usr/bin/env bash
set -euo pipefail

# Format Go imports using goimports
# Usage: goimports.sh [project_root]
#
# This used to be `go tool goimports -w .`, which walks the filesystem from the
# working directory and so rewrote every vendored file in the tree whenever one
# was present. It takes its file list from go_files.sh now, like the other
# formatters.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="${1:-$(pwd)}"

# Through a file rather than `< <(go_files.sh)`: process substitution discards
# the exit status of what it runs, so a failure to produce the list would read
# here as a list of no files, and formatting nothing would look like success.
file_list="$(mktemp)"
trap 'rm -f "${file_list}"' EXIT

"${SCRIPT_DIR}/go_files.sh" "${PROJECT_ROOT}" >"${file_list}"

go_files=()
while IFS= read -r -d '' file; do
  go_files+=("${file}")
done <"${file_list}"

if [ ${#go_files[@]} -gt 0 ]; then
  go tool goimports -w "${go_files[@]}"
fi
