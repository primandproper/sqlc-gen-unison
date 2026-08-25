#!/usr/bin/env bash
set -euo pipefail

# Pull the images the container-backed tests use, before the test run.
# Usage: pull_test_containers.sh
#
# Pulling here rather than letting testcontainers do it inline keeps a cold
# image out of the per-container startup budget, where it competes with a
# readiness deadline and reads as a flake rather than as a slow download.
#
# The list is kept in step with internal/containers/*/  by hand; there are two
# images and both are named in one const each.

IMAGES=(
  "postgres:17-alpine"
  "mysql:8.0"
)

if ! command -v docker >/dev/null 2>&1; then
  echo "pull_test_containers.sh: no docker on PATH; skipping the pull" >&2
  exit 0
fi

if ! docker info >/dev/null 2>&1; then
  echo "pull_test_containers.sh: no docker daemon; skipping the pull" >&2
  exit 0
fi

for image in "${IMAGES[@]}"; do
  echo "pulling ${image}"
  docker pull --quiet "${image}" &
done

wait

echo "test images ready"
