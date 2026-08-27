# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`github.com/primandproper/sqlc-gen-unison` — a [sqlc](https://sqlc.dev) codegen plugin and
orchestrator that generates one set of Go types and N dialects' SQL from one logical query set.
Go 1.27. The design document is `docs/prd.md`; it is authoritative, and its §14 open questions are
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

§7's divergence table is settled as follows. The MySQL `LIMIT` row is handled by `rename_params`,
because no argument name satisfies all three engines. The reserved-word and `CURRENT_TIMESTAMP`
rows are sqlc's to catch, per dialect. The rows-changed row is emitted as a note above `Querier`,
since every dialect returns an `int64` and the compiler cannot reach it. **The `RETURNING` row is
resolved as "do not use `RETURNING`"** — creates are `:exec` plus a separate read-back query.
Bridging it would mean pairing two queries by convention and emitting two round trips, which is
reconciliation. `TestReturningIsNotAConvergentShape` pins that.

**The variable-length `IN` list is the one row where generated *code* diverges, and it still
converges.** Postgres binds the list as one array (`= ANY(sqlc.arg(x)::T[])`); MySQL and SQLite have
no array, so sqlc leaves a `/*SLICE:name*/?` expansion site in the analyzed text and the emitted
method replaces it with one placeholder per element before binding. Both reach one shared `[]T`
field, so nothing is paired, merged, or promoted. `internal/converge/slices.go` decides it. Two
rules go with it: an **empty list matches nothing on every dialect** (`IN (NULL)` on the expanding
two, an empty array on Postgres) — but its *negation* does not, which is why an emitted note above
`Querier` says so — and **the list must bind the last placeholder**, refused at generate time for
the reason two sections down.

## Two modes, one binary

- **Plugin mode** — the no-args root behavior. sqlc writes a `CodeGenRequest` protobuf to stdin and
  reads a `CodeGenResponse` back from stdout.
- **`unison generate`** — the orchestrator. Reads `unison.yaml`, renders a per-dialect sqlc config,
  and shells out to the pinned sqlc once per dialect, each run pointing `out:` at the same directory.
- **`unison check`** — compiles all dialects, generates nothing.

## Things that will cost you a day if you rediscover them

- **SQLite numbers a bare `?` one past the highest index it has seen.** sqlc spells SQLite's
  ordinary parameters `?1`, `?2`, but an expanded `sqlc.slice()` is bare `?`s — so a parameter
  *after* the list collides with an element of it. `WHERE id IN (?,?) AND scope = ?2` binds the
  second id where scope belongs, returns no rows, and reports no error. That is why
  `checkExpansions` requires a list to bind the statement's last placeholder, and why the corpus
  puts the `IN` clause at the end. MySQL, whose placeholders are all positional, is unharmed either
  way — which is exactly what makes it invisible if you only test there.
- **`Parameter.Number` is not placeholder order.** On MySQL the bare `?` that LIMIT requires is
  numbered 1 while appearing last in the text. Read parameters in **list order**; sqlc's own Go
  generator does. Sorting by `Number` transposes arguments silently.
- **MySQL does not deduplicate parameters.** A named argument used three times arrives as three
  parameters with the same name, so the corpus's list queries bind 16 positions from 8 fields.
- **sqlc joins absolute paths in a config with the config's own directory**, so an absolute
  `schema:` does not work. The orchestrator writes relative paths into a staging directory.
- **sqlc's `codegen:` block has no `env:` field in 1.31.1 — but the `plugins:` block does**, and
  the difference matters: sqlc does not hand a process plugin the environment it was run with. It
  builds a fresh one holding `SQLC_VERSION` plus only the keys that `plugins:` list names, so a
  variable missing from it never reaches plugin mode however faithfully the plugin reads it. That
  is why `renderConfig` emits `env: [UNISON_LOG_LEVEL]`, and
  `TestLogLevelReachesPluginMode` pins it. sqlc also discards a plugin's stderr on success,
  surfacing it only inside the error when a plugin fails.
- **Postgres reports built-ins as `pg_catalog.*`**; the type mapper strips that prefix.
- **The Postgres catalog carries all of `information_schema` and `pg_catalog`** — several hundred
  tables, including ones named `columns` and `tables`. Prefix marking filters to the catalog's
  default schema.
- **`fieldalignment` reorders struct fields and drops their comments.** Document fields in the
  type's doc comment, not beside them.

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

- `cmd/unison/main.go` — thin entrypoint: signal-cancellable context → `cli.Execute`.
- `internal/cli/` — cobra root command and subcommands. Owns the plugin-mode/stdout rule.
  `internal/cli/pluginenv` names the one environment variable, shared with the orchestrator.
- `internal/protocol/` — the sqlc plugin wire format, and nowhere else. Decides plugin mode.
- `internal/options/` — `unison.yaml`'s options, one definition serving both the orchestrator
  (which writes them into the rendered sqlc config) and the plugin (which parses them back).
- `internal/converge/` — the convergence core: sqlc's analysis → IR. The type mapper (§8) and
  the prefix markers (§9) live here. The only package besides `protocol` that imports plugin-sdk-go.
- `internal/ir/` — the language-neutral IR (§5). Imports no protobuf and names no Go type.
- `internal/emit/gogen/` — the one emitter: IR → Go source.
- `internal/orchestrator/` — `unison.yaml`, per-dialect sqlc config rendering, `generate` and `check`.
- `internal/sqlcdriver/` — runs the pinned sqlc, and holds the pin in `sqlc-version`.
- `internal/containers/` — the container lifecycle for the execution suite, plus `pgtest` and
  `mysqltest`. Shaped after platform-go's `testutils/containers` so the harness is recognizable.
- `internal/execution/` — runs the generated package against real servers. Test files only.
- `testdata/golden/identitydb/` — the emitted package, committed. Importable by its full path while
  invisible to `./...`, so the formatters never rewrite it and the execution suite can import it.
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
- **`make test` runs containers by default**; `make test_no_containers` is the escape hatch. The
  gate is `RUN_CONTAINER_TESTS=true`, spelled the way platform-go spells it so one export governs
  both repositories. There is no build tag.
- The execution suite is one `runQuerierSuite(t, ctx, env)` called from three entrypoints, so a case
  added for one dialect is a case added for all three. Its subtests are deliberately sequential —
  each builds on the rows the last one left.

### Why the execution suite exists

Compilation, convergence, byte-identical shared files, and the golden diff **all pass** on a
generated package whose arguments are bound in the wrong order. That was verified by mutation: a
one-line swap of the last two entries in `converge`'s `args` slice leaves the shared shape identical
and every non-container test green, and all three execution runs fail. unison generates the argument
order, so an argument-order bug is wrong in every consumer at once. That specific hole is what these
tests cover — not consumer semantics, which stay in consumers per §11.

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
