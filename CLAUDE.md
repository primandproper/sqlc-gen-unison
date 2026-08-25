# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`github.com/primandproper/sqlc-gen-unison` — a [sqlc](https://sqlc.dev) codegen plugin and
orchestrator that generates one set of Go types and N dialects' SQL from one logical query set.
Go 1.27. The design document is `prd.md`; it is authoritative, and its §14 open questions are
settled (the name is `unison`; `New()` takes an emitted dialect enum; `unison check` is in scope).

sqlc remains the analyzer — unison replaces only the emission. The repository was created from
`primandproper/template-go` and keeps its toolchain while shedding its application layer; the
de-template commit is the record of what this tool deliberately does not carry. **`platform-go`
must never appear in `go.mod`**: unison versions against sqlc's plugin protocol, and platform
pins `.unison-version`, so depending back on platform is a cross-repo version loop.

## The core invariant: convergent emission

sqlc invokes a plugin once per `sql:` block, and each block has one engine, so no invocation ever
sees more than one dialect's analysis. Every invocation therefore receives the **full dialect
roster** via options and emits the shared files (types, querier interface, `New` constructor) as a
**pure, deterministic function of (roster, query shapes, options)**, all to the same paths.

- When dialects agree, the overwrites are byte-identical no-ops.
- When they diverge, last-write-wins the shared files and another dialect's query file now names a
  symbol that no longer exists — the **Go compiler** reports the divergence, naming the query.

There is deliberately **no merge step, no hash, and no promotion pass**. When something is hard,
the fix is never to add one. Shape divergence is *refused, not reconciled* (§7); the fix happens in
the `.sql`.

## Two modes, one binary

- **Plugin mode** — the no-args root behavior. sqlc writes a `CodeGenRequest` protobuf to stdin and
  reads a `CodeGenResponse` back from stdout.
- **`unison generate`** — the orchestrator. Reads `unison.yaml`, renders a per-dialect sqlc config,
  and shells out to the pinned sqlc once per dialect, each run pointing `out:` at the same directory.
- **`unison check`** — compiles all dialects, generates nothing.

## Hard constraints

- **stdout is the protocol channel in plugin mode.** All logging is stdlib `slog` to **stderr**.
  Nothing else may ever print to stdout in that mode.
- **Determinism is load-bearing.** Generating twice must be byte-identical: sorted iteration
  everywhere, no timestamps, no absolute paths in output. The test suite asserts double-generation
  equality. Generated headers carry the sqlc version and unison version only — no dates, no
  randomness.
- **No SQL parsing of our own.** If sqlc cannot analyze it, unison does not support it. The only
  query-text rewriting is the catalog-informed prefix markers of §9.
- **One output language.** The convergence core is language-neutral (the IR of §5); the Go emitter
  sits behind that seam. A second emitter is out of scope.
- **No platform-go conventions baked in.** Anything platform-flavored arrives via plugin options in
  the consumer's config.

## Layout

- `cmd/main/main.go` — thin entrypoint: signal-cancellable context → `cli.Execute`.
- `internal/cli/` — cobra root command and subcommands. Owns the plugin-mode/stdout rule.
- `version/` — build metadata (`CommitHash`/`BuildTime`/`CommitTime`), injected via `-ldflags` by
  `scripts/build.sh`.
- `testdata/` — the golden corpus: platform-go's identity store, vendored frozen. It is a fixture,
  not a dependency; its README records the source commit.

## Common Commands

```bash
make setup          # Create artifacts dir + download the module cache
make build          # Compile all packages, then build artifacts/unison with version metadata
make run ARGS="version"   # go run the CLI with arguments
make format         # Format all Go code (imports, field alignment, tag alignment, gofmt)
make lint           # Run golangci-lint (Docker) + shellcheck
make test           # Run tests (race detector, shuffle, failfast); excludes cmd packages
```

Run a single test:
```bash
go test -run TestName ./internal/...
```

Linting runs in Docker (`golangci/golangci-lint` image). Formatting runs locally via `go tool` with
`gci`, `goimports`, `fieldalignment`, `tagalign`, and `gofmt` (declared in the `tool` block of go.mod).

This module does **not** vendor dependencies; builds and tests run against the module cache.

`scripts/go_files.sh` is the one place that decides which Go files the formatters see, and
`format_golang.sh`, `format_imports.sh`, `goimports.sh`, and the `gofmt` check in
`.github/workflows/formatting.yaml` all take their list from it. It asks the Go toolchain
(`go list -e -f '{{.Dir}}' ./...`, then `find -maxdepth 1`) rather than writing out an exclusion
list, so `vendor/`, `testdata/`, and any `_`- or `.`-prefixed directory are skipped for the same
reason `go test ./...` skips them. Point new filesystem-walking tooling at it rather than adding a
fourth spelling of the same exclusion.

Two things about it are load-bearing. It **fails loudly rather than emitting an empty list** — an
out-of-sync `vendor/modules.txt` makes `go list` exit non-zero, and a formatter that quietly formats
nothing (or a CI check that quietly checks nothing) is worse than a stop. And its callers read it
**through a file, not `< <(...)`**, because process substitution discards the exit status of what it
runs, which is exactly how that empty list would go unnoticed.

Note that `testdata/` being invisible to the formatters is what lets the golden corpus and the
emitted golden files live there without the toolchain rewriting them.

## Import Ordering

Import ordering uses `gci` with four sections, separated by blank lines:

1. Standard library
2. `github.com/primandproper/sqlc-gen-unison` (this module)
3. `github.com/primandproper` (org-level packages)
4. Everything else (third-party)

The Makefile `THIS` variable must be the full module path
(`github.com/primandproper/sqlc-gen-unison`) because `format_imports.sh` runs `dirname` on it to
derive the org-level prefix.

## Testing

- Tests use `shoenig/test`: `test` for non-fatal assertions, `must` for fatal ones. Both take
  `(t, expected, actual)` and annotate failures via `test.Sprintf` / `must.Sprintf` settings rather
  than `...f` variants. **No testify.**
- Interfaces are faked with `moq`, not hand-written mocks.
- Tests call `t.Parallel()` by default.
- `make test` excludes `cmd` packages, so keep testable logic in `internal/` and `version/`.
- Test command: `CGO_ENABLED=1 go test -shuffle=on -race -vet=all -failfast`.

## Conventions worth knowing

- The `--log-level` persistent flag defaults to `info`. There is no environment-variable config
  machinery: unison's configuration is the consumer's `unison.yaml` plus flags.
- `version` prints its data to stdout and emits nothing at the default `info` level, so
  `unison version` stays machine-parseable.

## Linting

- ~46 linters enabled via `.golangci.yml` (golangci-lint v2 format).
- Formatters: `gci` and `gofmt` (configured in the `formatters:` section).
- Notable strictness: `errcheck` (with `check-blank` + `check-type-assertions`), `errorlint`,
  `gosec`, `forcetypeassert`, `unconvert`, `unparam`. Many are relaxed for `_test.go` files.
