# sqlc-gen-unison

A [sqlc](https://sqlc.dev) codegen plugin and orchestrator that generates **one**
set of Go types and **N** dialects' SQL from one logical query set.

A store supporting Postgres, MySQL, and SQLite ships a single `CreateUserParams`,
a single row struct, and a single method signature — with the query text and
argument order selected by dialect at construction, and every correspondence
between SQL and Go emitted by a generator instead of maintained by hand.

sqlc remains the analyzer. unison replaces only the **emission**.

## Status

Pre-release, under active development. See `prd.md` for the design document.

## The invariant

sqlc invokes a plugin once per `sql:` block, and each block has one engine — so
no single invocation ever sees more than one dialect's analysis. unison's answer
is **convergent emission**: every invocation receives the full dialect roster via
options, and emits the shared files (types, querier interface, constructor) as a
pure, deterministic function of roster and query shapes, all to the same paths.

When the dialects agree, the overwrites are byte-identical no-ops. When they
diverge, the last write wins the shared files and some other dialect's query file
now names a symbol that no longer exists — so the **Go compiler** reports the
divergence, in generated code, naming the query.

There is no merge step, no hash, no promotion pass. That is the point.

## Requirements

Requires **Go 1.27+** and a pinned **sqlc**. [Docker](https://www.docker.com/) is
used for linting and shellcheck.

```bash
make setup                  # create artifacts/ and download the module cache
make build                  # compile everything, produce artifacts/unison
./artifacts/unison version
```

## Common commands

```bash
make format     # imports (gci), field/tag alignment, gofmt -s
make lint       # golangci-lint (Docker) + shellcheck (Docker)
make test       # go test -shuffle -race -vet=all -failfast (excludes cmd)
make build      # compile all packages + build the binary with version metadata
```

## Layout

```
cmd/main/             # entrypoint: signal-cancellable context -> cli.Execute
internal/cli/         # cobra root command and subcommands
version/              # build metadata, injected via -ldflags by scripts/build.sh
scripts/              # build/format/lint/test/shellcheck helpers
.github/workflows/    # CI mirroring the make targets
```

## stdout is a protocol channel

In plugin mode sqlc writes a `CodeGenRequest` protobuf to this process's stdin
and reads a `CodeGenResponse` back from stdout. Anything else printed to stdout
corrupts the response, so all logging is stdlib `slog` to **stderr**. The only
command that writes to stdout is one that owns its own output, such as `version`.

## License

[AGPL-3.0](./LICENSE).
