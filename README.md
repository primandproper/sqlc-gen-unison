# sqlc-gen-unison

A [sqlc](https://sqlc.dev) codegen plugin and orchestrator that generates **one**
set of Go types and **N** dialects' SQL from one logical query set.

A store supporting Postgres, MySQL, and SQLite ships a single `CreateUserParams`,
a single row struct, and a single method signature — with the query text and
argument order selected by dialect at construction, and every correspondence
between SQL and Go emitted by a generator instead of maintained by hand.

sqlc remains the analyzer. unison replaces only the **emission**.

## Status

Pre-release, under active development. See `docs/prd.md` for the design document.

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

## Using it

unison needs sqlc on PATH — it does not analyze SQL, sqlc does — and one
`unison.yaml` at the root of the project that consumes the generated package:

```yaml
sqlc_version: 1.31.1
package: identitydb
out: internal/identitydb

# The keys of `schemas:` are the dialect roster. There is deliberately no
# separate `dialects:` list: two places to say the same thing is one place to
# say it differently.
schemas:
  postgresql: migrations/postgres.sql
  mysql: migrations/mysql.sql
  sqlite: migrations/sqlite.sql

queries:
  postgresql: queries/postgres/
  mysql: queries/mysql/
  sqlite: queries/sqlite/

options:
  # Rewrite table identifiers to {{prefix}}, substituted once at construction.
  table_prefix_var: true

  # Where an analyzer resolves no type — SQLite often, Postgres for a COALESCE'd
  # LIMIT — say what it is. An override is the final Go type, nullability
  # included; write *int64 for a pointer.
  type_overrides:
    - {column: "*.result_limit", go_type: int64}
    - {column: "*.scope", go_type: github.com/example/tenancy.Scope}

  # MySQL accepts only a bare placeholder in LIMIT and names it `limit`;
  # Postgres rejects `limit` as an argument name because it is reserved. No
  # spelling satisfies all three, so the name converges here.
  rename_params:
    mysql:
      limit: result_limit
```

Then:

```bash
unison generate   # render a sqlc config per dialect, run sqlc once for each
unison check      # statically check every dialect's SQL, generate nothing
```

Queries carry the same name and annotation in every dialect's file — `-- name:
CreateUser :one` in all three — and the query name is the join key.

**Do not use `RETURNING`.** Postgres can write a create as `INSERT ... RETURNING`
and analyze it as `:one`; MySQL has no `RETURNING`, and sqlc analyzes one
statement per query, so there is no way to spell insert-then-read-back as a
single `:one` there. unison does not bridge that — doing so would mean pairing
two queries by convention and emitting a method that makes two round trips,
which is reconciling a divergent shape, and reconciliation is the one thing this
tool refuses. Write creates as `:exec` and read back with a separate query. A
`RETURNING` create on one dialect and not another is a divergence, and fails to
compile like any other.

**Spell a variable-length `IN` list each engine's own way, and put its clause
last.** There is no shared spelling: Postgres binds one array —
`WHERE id = ANY(sqlc.arg(ids)::TEXT[])` — while MySQL and SQLite take
`WHERE id IN (sqlc.slice(ids))`. Under one query name they converge onto one
`[]T` parameter, and each dialect's method binds it the way that engine needs:
Postgres passes the slice through, the other two expand it to one placeholder
per element before binding. An empty list matches nothing on all three, so a
caller does not have to guard the call — but its negation does not converge, so
test membership rather than `NOT IN`. The list has to bind the statement's last
placeholder, because SQLite numbers each expanded `?` one past the highest index
it has seen and would otherwise bind an element where the next parameter
belongs; unison refuses that ordering at generate time rather than emitting it.

**Render the per-dialect schema files from a single source.** Each invocation sees
only its own dialect's catalog, so drift a query touches is caught — a missing
column fails that dialect's analysis, a differently-typed one diverges the shape
and fails compilation — but drift no query touches is invisible. Generating the
three schema files from one definition removes the class entirely; the corpus in
`testdata/` is produced that way.

## What it emits

Into one directory, from N runs:

| file | written by | contents |
| --- | --- | --- |
| `db_generated.go` | every dialect | `DBTX`, the `Dialect` enum, `New` |
| `types_generated.go` | every dialect | one params and one row struct per query |
| `querier_generated.go` | every dialect | the `Querier` interface |
| `queries_<dialect>_generated.go` | that dialect | its SQL, its argument order, its methods |

```go
q, err := identitydb.New(identitydb.DialectPostgreSQL, "tenant_")
user, err := q.GetUser(ctx, db, identitydb.GetUserParams{ID: id, Scope: scope})
```

The dialect is an emitted enum rather than a string, so a typo is a compile
error in the one tool whose thesis is moving errors earlier.

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
make format              # imports (gci), field/tag alignment, gofmt -s
make lint                # golangci-lint (Docker) + shellcheck (Docker)
make test                # the whole suite, containers included
make test_no_containers  # the same, minus the tests that execute SQL
make build               # compile all packages + build the binary
```

`make test` runs the generated package against real PostgreSQL and MySQL servers via
testcontainers, and against SQLite with no container at all. Those are the only tests
that execute a statement: compilation, convergence, and the golden files all pass on a
package whose arguments are bound in the wrong order, and unison is what generates that
order. SQLite needs no Docker and always runs.

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
