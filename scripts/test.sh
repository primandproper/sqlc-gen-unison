#!/usr/bin/env bash
set -euo pipefail

# Run tests.
# Usage: test.sh [run_container_tests]
#   test.sh          # with containers (the default)
#   test.sh false    # without, for a host with no Docker daemon
#
# Containers are on by default because the tests that need them are the only
# ones that execute a generated statement. Everything else in this suite proves
# the emitted code compiles, converges, and regenerates byte-identically — all
# of which a package with its arguments bound in the wrong order also does.
#
# RUN_CONTAINER_TESTS is spelled the way platform-go spells it, so one export
# governs both repositories.

RUN_CONTAINER_TESTS="${1:-true}"

if [ "${RUN_CONTAINER_TESTS}" = "true" ]; then
  "$(dirname "$0")/pull_test_containers.sh"
fi

# shellcheck disable=SC2046
CGO_ENABLED=1 RUN_CONTAINER_TESTS="${RUN_CONTAINER_TESTS}" \
  go test -shuffle=on -race -vet=all -failfast \
  $(go list github.com/primandproper/sqlc-gen-unison/... | grep -Ev '(cmd)')
