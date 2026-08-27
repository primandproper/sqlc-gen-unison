# ENVIRONMENT
PWD      := $(shell pwd)
MYSELF   := $(shell id -u)
MY_GROUP := $(shell id -g)

# PATHS
THIS          := github.com/primandproper/sqlc-gen-unison
BINARY_NAME   := unison
CMD_PACKAGE   := $(THIS)/cmd/unison
ARTIFACTS_DIR := artifacts
SCRIPTS_DIR   := scripts
COVERAGE_OUT  := $(ARTIFACTS_DIR)/coverage.out

# COMPUTED
TOTAL_PACKAGE_LIST := `go list $(THIS)/...`

# CONTAINER VERSIONS
LINTER_IMAGE     := golangci/golangci-lint:v2.13.1
SHELLCHECK_IMAGE := koalaman/shellcheck:stable

# COMMANDS
CONTAINER_RUNNER      := docker
RUN_CONTAINER         := $(CONTAINER_RUNNER) run --rm --volume $(PWD):$(PWD) --workdir=$(PWD) --network=host
RUN_CONTAINER_AS_USER := $(RUN_CONTAINER) --user $(MYSELF):$(MY_GROUP)
LINTER                := $(RUN_CONTAINER) $(LINTER_IMAGE) golangci-lint

## non-PHONY folders/files

$(ARTIFACTS_DIR):
	@mkdir -p $(ARTIFACTS_DIR)

## PREREQUISITES

# setup prepares a fresh clone: creates the artifacts dir and downloads the
# module cache. This module does not vendor; builds and tests run against the
# module cache.
.PHONY: setup
setup: $(ARTIFACTS_DIR)
	go mod download

## FORMATTING

.PHONY: format_imports
format_imports:
	$(SCRIPTS_DIR)/format_imports.sh $(THIS) $(PWD)

.PHONY: format_go_fieldalignment
format_go_fieldalignment:
	@$(SCRIPTS_DIR)/format_go_fieldalignment.sh

.PHONY: format_go_tag_alignment
format_go_tag_alignment:
	@$(SCRIPTS_DIR)/format_go_tag_alignment.sh

.PHONY: go_fix
go_fix:
	go fix ./...

.PHONY: goimports
goimports:
	$(SCRIPTS_DIR)/goimports.sh

.PHONY: format_golang
format_golang: go_fix goimports format_imports format_go_fieldalignment format_go_tag_alignment
	@$(SCRIPTS_DIR)/format_golang.sh $(PWD)

.PHONY: format
format: format_golang

.PHONY: fmt
fmt: format

## LINTING

.PHONY: golang_lint
golang_lint:
	@$(SCRIPTS_DIR)/golang_lint.sh $(CONTAINER_RUNNER) $(LINTER_IMAGE) "$(LINTER)"

.PHONY: shellcheck
shellcheck:
	@$(SCRIPTS_DIR)/shellcheck.sh $(CONTAINER_RUNNER) $(SHELLCHECK_IMAGE) $(SCRIPTS_DIR)

.PHONY: lint
lint: golang_lint shellcheck

## EXECUTION

# build compiles every package (fast failure on breakage) and then produces the
# binary with version metadata injected via ldflags.
.PHONY: build
build: $(ARTIFACTS_DIR)
	go build $(THIS)/...
	$(SCRIPTS_DIR)/build.sh -o $(ARTIFACTS_DIR)/$(BINARY_NAME) $(CMD_PACKAGE)

# run builds and runs the binary; pass args with `make run ARGS="version"`.
.PHONY: run
run:
	go run $(CMD_PACKAGE) $(ARGS)

# test runs everything, containers included. The container-backed tests are the
# only ones that execute a generated statement, so they are on by default.
.PHONY: test
test: $(ARTIFACTS_DIR)
	$(SCRIPTS_DIR)/test.sh

# release_build cross-compiles the release artifacts and checksums them, the
# same way the release workflow does. VERSION must be the tag: the binary stamps
# it into every file it generates, so this refuses to guess one.
.PHONY: release_build
release_build: $(ARTIFACTS_DIR)
	$(SCRIPTS_DIR)/release_build.sh $(ARTIFACTS_DIR)/release $(VERSION)

# test_no_containers is the escape hatch for a host with no Docker daemon. It
# skips exactly the tests that would catch an argument bound in the wrong
# position, so it is a convenience, not a substitute.
.PHONY: test_no_containers
test_no_containers: $(ARTIFACTS_DIR)
	$(SCRIPTS_DIR)/test.sh false
