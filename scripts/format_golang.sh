#!/usr/bin/env bash
set -euo pipefail

# Format all Go files using gofmt
# Usage: format_golang.sh <project_root> <gofmt_command>
#
# Which files those are is go_files.sh's question, not this one's.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="${1:-$(pwd)}"

# Through a file rather than `< <(go_files.sh)`: process substitution discards
# the exit status of what it runs, so a failure to produce the list would read
# here as a list of no files, and formatting nothing would look like success.
file_list="$(mktemp)"
trap 'rm -f "${file_list}"' EXIT

"${SCRIPT_DIR}/go_files.sh" "${PROJECT_ROOT}" >"${file_list}"

while IFS= read -r -d '' file; do
  # GO_FORMAT contains a command with arguments, so we use eval
  # shellcheck disable=SC2086
  eval "gofmt -s -w \"${file}\""
done <"${file_list}"
