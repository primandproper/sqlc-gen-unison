# sqlc-gen-unison

> **Status:** implemented through milestone 1 (§12) and building its own output.
> This is the founding design doc, kept current: where the code settled a
> question differently than the first draft imagined, the code won and this
> document was corrected. Sections marked **Settled** record a decision that is
> pinned by a test; §12 is the only forward-looking section left.

A sqlc codegen plugin and orchestrator that generates **one** set of Go types
and **N** dialects' SQL from one logical query set, so that a store supporting
Postgres, MySQL, and SQLite ships a single `CreateUserParams`, a single row
struct, and a single method signature — with the query text and argument order
selected by dialect at construction, and every correspondence between SQL and
Go emitted by a generator instead of maintained by hand.

---

## 1. Problem

platform-go promises three SQL dialects. Its stores today compose statements as
Go strings, and three correspondences are maintained by hand with nothing
checking them:

1. **Projection ↔ scan.** A column list in one file, a `Scan(&u.FirstName,
   &u.LastName, …)` in another. Swapping two same-typed fields in a paste
   compiles, lints, and silently transposes data.
2. **Placeholder ↔ argument position.** An `[]any` written out by hand in the
   order of a column list a few lines up. Reusing an index is correct on
   Postgres and silently wrong on MySQL and SQLite.
3. **Statement ↔ schema.** Verified only when a container test happens to
   execute the statement.

`database/querygen` plus the `sqlc compile` gate (platform-go PR #317) closed
correspondence 3 at build time and single-sourced the column lists — but the
binding is still by name *at runtime*, the scan pairing is still hand-written,
and the architecture that delivers it renders statements twice (once at
construction for execution, once at generate time for the checker), which makes
it hard to read and reason about.

`sqlc generate` with the stock generator closes 1 and 2 — but its generated
types are **nominal per package**, so three dialects means three unrelated
`CreateUserParams` types and a hand- or tool-written adapter layer mapping them
onto one interface: the drift surface reborn, plus an unsolved table-prefix
problem.

The gap is a generator that keeps sqlc's per-dialect analysis (the part nobody
should rewrite: three parsers and schema typecheckers) and replaces only the
**emission**, producing shared types and per-dialect SQL.

## 2. Goals

- One logical query file set → one Go package with **one** params/row type per
  query and per-dialect SQL constants and argument orderings.
- A copy/paste error in a query or its bindings is a **generate-time or
  compile-time** failure, never a runtime scan error.
- Cross-dialect drift — a query whose shape differs between dialects — fails
  generation or compilation loudly, naming the query.
- Deterministic output: regeneration on any machine is byte-identical
  (prerequisite for a generated-files CI gate).
- Table-prefix support (`{{PREFIX}}`-style shared-database deployments) without
  runtime SQL surgery.
- Generic tool: no platform-go conventions baked in. Anything platform-flavored
  (a scope column mapping to `tenancy.Scope`) arrives via plugin options in the
  consumer's config. The tool is usable by any repo with sqlc-parseable
  queries, one dialect or several.

## 3. Non-goals

- **Not a SQL frontend.** Parsing, catalog, and type analysis remain sqlc's.
  There is no plugin seam for engines and we do not want one; if sqlc cannot
  analyze a dialect, unison does not support it.
- **Not a query builder or ORM.** Input is SQL written by people (or emitted by
  `database/querygen`); output is boring Go.
- **Not a reconciler of divergent shapes.** A query that cannot be expressed
  with the same params and projection on every configured dialect is refused;
  the fix happens in the `.sql` (see §7).
- **v1 skips** `:copyfrom` (Postgres-only by nature — inherently
  shape-divergent) and batch annotations. `:one`, `:many`, `:exec`,
  `:execrows`, `:execresult` are in scope.
- **Replacing `database/querygen`'s consumer-facing runtime mode.** `Bound`
  statements remain the "sqlc optional" path for consumers who want runtime
  rendering. Unison is for stores that commit generated code.

## 4. Background: why this seam

sqlc's extension point is the **codegen plugin protocol**: sqlc invokes a
separate binary (process or WASM), passes a `CodeGenRequest` protobuf on stdin
containing the fully analyzed catalog and queries — resolved columns, types,
parameters — and reads generated files back on stdout. Plugins import
`github.com/sqlc-dev/plugin-sdk-go`; sqlc's own compiler is `internal/` and
cannot be imported. Two consequences shape everything below:

- The plugin runs **once per `sql:` block**, and each block has one engine. No
  single invocation ever sees more than one dialect's analysis.
- Type mapping from database types to Go types is the *plugin's* job (the stock
  generator's `overrides:` feature is implemented by that plugin, not by sqlc
  core). Unison therefore owns a convergent type mapper (§8).

### Prior art

There is none to reuse, but the problem has upstream history: sqlc discussion
#465 ("generate compatible interfaces for exchangeable sql dialects", 2020) is
this exact ask. The maintainer sketched two paths — a strict cross-engine SQL
subset (dismissed as impractical, especially for SQLite) and protobuf-style
upfront shape definitions validated to produce identical types across engines —
and named the fundamental blocker: "the user structs for MySQL and PostgreSQL
aren't the same; the ID columns are different types." Nothing was built, and
the discussion has been dormant since April 2020. Unison is the second path,
made buildable by the plugin protocol (it needn't live in sqlc core), the
convergent-emission design in §6 (shape validation degenerates into
byte-identical writes plus the Go compiler), and owning the type mapper in §8
(the answer to the ID-columns blocker). No existing plugin merges dialects
into shared types; `sqlc-gen-typescript` supports all three engines but
generates per block like every other plugin.

## 5. Design overview

One module, one binary, two modes:

- **Plugin mode** (invoked by sqlc, request on stdin): emits files for one
  dialect — plus the shared files, convergently (§6). In this mode **stdout is
  the protocol channel** — the `CodeGenResponse` protobuf — so all logging goes
  to stderr (stdlib `slog`; see milestone 0). Anything printed to stdout
  corrupts the response.
- **`unison generate`** (the orchestrator, invoked by `make generate`): reads
  `unison.yaml`, shells out to the **pinned** sqlc once per dialect (each run's
  config points `out:` at the same directory), and reports failures. It exists
  so consumers call one tool and cannot get the ordering or the sqlc version
  wrong.

### Internal layout: converge, then emit

The plugin protocol carries no target-language context — the plugin *is* the
language decision, and sqlc writes back whatever files it returns. Unison's
hard half (roster handling, convergent shape computation, divergence policy,
prefix markers) is language-neutral, so the implementation keeps it that way:
a convergence core produces an internal representation of the shared shapes
and per-dialect statements, and an **emitter** renders it. §8's mapper is two
composed steps — engine type → neutral type (the convergence step), neutral
type → language type. v1 ships exactly one emitter, Go. A second language is
deliberately out of scope — no consumer in this org speaks SQL from another
language; clients go through the API — but the seam means adding one later is
an emitter, not a redesign.

### Configuration

`unison.yaml` (consumed by the orchestrator, which renders per-dialect sqlc
configs):

```yaml
sqlc_version: 1.31.1
package: identitydb
out: internal/identitydb
# The dialect roster (§6) is the set of keys below — omitting a dialect means
# it is not analyzed and not emitted; there is no separate list to drift.
schemas:
  postgresql: migrations/postgres.sql
  mysql: migrations/mysql.sql
  sqlite: migrations/sqlite.sql
queries:
  postgresql: queries/postgres/
  mysql: queries/mysql/
  sqlite: queries/sqlite/
options:
  table_prefix_var: true                # emit the {{prefix}} marker (§9)
  null_as: pointer                      # or `sql`, for sql.Null*; pointer is the default
  type_overrides:
    - column: "*.scope"
      go_type: github.com/primandproper/platform-go/v13/tenancy.Scope
  rename_params:                        # per dialect; see §7's LIMIT row
    mysql:
      limit: result_limit
```

`sqlc_version` is enforced, not documentation: the orchestrator asks the sqlc on
PATH for its version before anything runs and refuses to proceed on a mismatch.
The CodeGenRequest protobuf is a moving target and the analysis it carries is the
input to every shape decision here, so generating with a different sqlc is
generating from a different analyzer.

Queries carry the same **name and annotation** across dialects (`-- name:
CreateUser :one` in all three files); the query name is the join key.

Each invocation receives only its own dialect's analyzed catalog; nothing
verifies the N schema documents describe the same logical schema. Drift a
query observes is caught (a missing column fails that dialect's analysis; a
differently-typed one diverges the shape and fails compilation — `SELECT *`
included, since sqlc expands the star per catalog), but drift no query touches
is invisible. Consumers should therefore render the per-dialect schema files
from a **single source** — as platform-go does via `migrations.SQL(dialect,
"")` — rather than maintaining N siblings by hand.

## 6. Convergent emission — the core trick

Every invocation receives, via options, the **full dialect roster**, not just
its own dialect. The emitted output splits into:

- **Shared files** — `types.go` (params and row structs), `querier.go` (the
  interface and the `New(...)` constructor that switches over the roster),
  `db.go` (DBTX, prefix plumbing). These are a **pure function of (roster,
  query shapes, options)**: deterministically ordered, no dialect-specific
  strings, no timestamps. Because every invocation computes them from the same
  roster and — when the dialects agree — the same shapes, **all N invocations
  write byte-identical shared files to the same paths.** The overwrites are
  idempotent. We deliberately eat the redundant emission; it costs
  microseconds and removes an entire merge/promote/hash subsystem.
- **Per-dialect files** — `queries_postgres.go`, `queries_mysql.go`,
  `queries_sqlite.go`: SQL constants, argument marshaling in that dialect's
  order, and that dialect's implementation of the querier interface. All
  compile unconditionally; a consumer instantiates one.

### How divergence surfaces

If dialect B's analysis disagrees with dialect A's — a missing query, an extra
param, a different projection — then B's run overwrites the shared files with
*its* shape, and A's `queries_postgres.go` now references a field or method the
shared files don't declare. **The Go compiler reports the divergence**, in
generated code, naming the symbol. Last-write-wins is acceptable precisely
because the per-dialect files pin their expectations by name.

Residual risk, accepted for v1: a divergence in *name-and-type-identical*
shapes with different semantics (same field, different meaning per dialect)
compiles. That class is small, is exactly what the consumer's three-dialect
container tests exist for, and a `unison verify` mode (re-run generation,
`git diff --exit-code`) can be added later without changing the architecture.

### Sketch of the output

```go
// types.go (shared, emitted identically by every dialect's run)
type CreateUserParams struct {
    ID           string
    Scope        tenancy.Scope
    Username     string
    EmailAddress string
}

// querier.go (shared)
type Querier interface {
    CreateUser(ctx context.Context, db DBTX, arg CreateUserParams) error
    GetUser(ctx context.Context, db DBTX, arg GetUserParams) (GetUserRow, error)
    // ...
}

// db.go (shared) — the dialect is an emitted enum, not a string, so naming one
// that was not generated is a compile error rather than an error value at
// startup. Dialects() lists the roster in a stable order.
type Dialect string

const (
    DialectMySQL      Dialect = "mysql"
    DialectPostgreSQL Dialect = "postgresql"
    DialectSQLite     Dialect = "sqlite"
)

func New(dialect Dialect, prefix string) (Querier, error) {
    switch dialect { // the roster, known to every invocation via options
    case DialectMySQL:      return newMySQL(prefix), nil
    case DialectPostgreSQL: return newPostgreSQL(prefix), nil
    case DialectSQLite:     return newSQLite(prefix), nil
    default:
        return nil, fmt.Errorf("identitydb: unknown dialect %q", dialect)
    }
}

// queries_mysql.go (per-dialect)
const createUserMySQL = "INSERT INTO {{prefix}}identity_users (...) VALUES (?, ?, ?, ?)"

func (q *mysqlQueries) CreateUser(ctx context.Context, db DBTX, arg CreateUserParams) error {
    // args marshaled by the generator in MySQL's positional order.
}
```

**Settled:** `New` takes the emitted enum. The prefix is substituted into each
statement once, here, at construction — nothing rewrites SQL at query time.

## 7. Shape-divergence policy

**Refused, not reconciled.** The known catalog of three-dialect divergence
(measured while porting platform's identity store) is finite and each entry has
an authoring-side fix that converges the shape:

| divergence | convergent authoring | settled by |
| --- | --- | --- |
| MySQL has no `RETURNING` | **do not use `RETURNING`.** Creates are `:exec` plus a separate read-back query | `TestReturningIsNotAConvergentShape` |
| MySQL `LIMIT` takes only a bare placeholder | the `rename_params` option, per dialect | `internal/options` |
| Reserved words differ (`cursor` in MySQL) | canonical argument names avoid the union of reserved words | sqlc, per dialect |
| `CURRENT_TIMESTAMP` granularity | schema convention (`DATETIME(6)`) — a consumer schema concern | sqlc, against that schema |
| rows-changed vs rows-matched | an emitted note above `Querier`; gate on a discriminating predicate, or set `clientFoundRows=true` in the MySQL DSN | `internal/emit/gogen` |

Three of these rows moved during implementation, and how they moved is the
policy working rather than bending:

**`RETURNING` is refused outright.** The first draft imagined the MySQL query
doing exec-then-read-back while the generated method still presented a `:one`
result. That is reconciliation — it means pairing two queries by convention and
emitting a method that makes two round trips, which is precisely the one thing
§3 says this tool does not do. sqlc analyzes one statement per query, so there
is no honest way to spell insert-then-read-back as a single `:one` on MySQL.
Creates are `:exec` and the read-back is its own query. A `RETURNING` create on
one dialect and not another is a divergence, and fails to compile like any
other.

**The `LIMIT` row needed an option, not a generator trick.** MySQL accepts only
a bare placeholder there — `sqlc.arg()` is a syntax error — and names that
parameter `limit`; Postgres rejects `limit` as an argument name because it is
reserved. No spelling satisfies all three engines, so the name converges in
`rename_params` instead of in the `.sql`. It is deliberately not a general
reconciler: it renames, and cannot change a type, add a parameter, or reorder
anything. A genuine shape divergence still diverges and still fails to compile.

**The rows-changed row is emitted, not documented.** Every dialect returns an
`int64`, so the compiler cannot reach this one — which makes it the single
divergence in the table that no mechanism here catches. It is emitted as a note
above `Querier` so a reader meets it where the decision gets made.

A query that genuinely cannot converge does not go through unison — it lives as
a per-dialect hand-written statement under the consumer's existing container
tests, and the generator's docs say so. No silent fallback.

## 8. The type mapper

Convergent shared files require dialect-independent Go types:
`TIMESTAMPTZ` / `DATETIME(6)` / `DATETIME` → `time.Time`; `BIGINT` / `int8` →
`int64`; `TEXT` / `VARCHAR` → `string`; nullability → pointer or `sql.Null*`
per an option. The mapper is one table in the plugin, and it is the *only*
place per-dialect knowledge exists in shared-file emission — the byte-identical
overwrites are the standing proof it stays converged.

**SQLite is the weakest engine, but it is not the only one.** sqlc's SQLite
analyzer resolves types more loosely — columns come back untyped more often —
so the mapper accepts per-column hints. That mechanism shipped as the general
`type_overrides` option rather than a SQLite-specific one, because Postgres
needs it too: it types a `COALESCE`'d `LIMIT` as `any`. The two jobs turned out
to be one job. An override names a column as `table.column`, or `*.column` to
match by name wherever it appears — the form a parameter needs, since a
parameter built from an expression belongs to no table — and it is the *final*
type, nullability included: write `*int64` to get a pointer.

**An unresolved type is an error, never a guess.** A column the analyzer could
not type and the consumer did not override stops generation with a message
naming the column and the override that would fix it.

## 9. Table prefixes without SQL surgery

The blocker that kept sqlc out of platform-go: sqlc fixes identifiers at
generate time, while shared-database deployments prefix table names from
consumer config at construction.

Unison's resolution: the plugin **knows every table name from the analyzed
catalog**, so at emission it rewrites whole-word table identifiers in the query
text to `{{prefix}}<name>`, and the generated constructor does one
`strings.ReplaceAll` per statement at construction. This is not runtime SQL
surgery — the marker is placed by the generator using the analyzer's knowledge
of which tokens are tables, not by a regex guessing at execution time.

Sharp edge, documented and linted: a string *literal* inside a query that
spells a table name would be rewritten too. The plugin warns when it detects
one; the fix is renaming the literal. Queries are a controlled corpus; this is
acceptable.

## 10. Distribution, pinning, CI

- **Own repository** (`github.com/primandproper/sqlc-gen-unison`). It versions
  against sqlc's plugin protocol and the pinned sqlc release, not against any
  consumer's major; its dependency set (plugin-sdk-go, protobuf) never touches
  a consumer's module graph.
- **One native binary, installed like any other tool.** Consumers pin via a
  `.unison-version` file and their existing ensure-tool-installed machinery,
  exactly like `.sqlc-version` and `.protoc-version` today. The binary is both
  modes, and that is what makes the pin sufficient: `unison generate` renders a
  sqlc config naming `os.Executable()` as the plugin command, so the plugin
  that runs is the same build as the orchestrator that invoked it, by
  construction. There is no second artifact to version.
- **Generated-files gate in consumers:** regenerate in CI, fail on dirty tree.
  Determinism (§2) is what makes this gate meaningful; the tool's own test
  suite asserts double-generation is byte-identical.

### Settled: no WASM distribution

The first draft held WASM packaging (URL + sha256 in config, hermetic,
sandboxed) as a later milestone. It is dropped, because the orchestrator
dissolved the problem WASM solves.

sqlc's WASM support exists so a consumer writing `sqlc.yaml` by hand can name a
plugin without installing a binary and getting the reference right. But unison's
consumers do not write that config — the orchestrator writes it, and points it
at itself. The consumer already installed the binary, because installing it is
how they run `unison generate` at all.

Adopting WASM would cost more than it returns:

- A `wasip1` build is **plugin-only**: no `os/exec`, so the orchestrator half
  cannot run there. It is a second artifact, not a replacement for the first.
- It would trade "same version by construction" for a sha256 the orchestrator
  must know about its own release asset — a strictly weaker guarantee than the
  one `os.Executable()` gives for free.
- The module is ~24 MB, fetched and cached per-hash by sqlc.

The only capability it adds is running unison's plugin from a hand-written
`sqlc.yaml` without the orchestrator — and for unison that is the configuration
we least want written by hand, since a roster disagreeing with the schemas is
exactly what the orchestrator exists to make impossible. If an outside consumer
ever needs hermetic pinning, this changes packaging and not design, and can be
reconsidered then.

## 11. Testing

- **Golden corpus:** platform-go's identity store — three committed schemas,
  three query files, the richest shape variety (creates with read-back, keyset
  pagination, both-counts lists, scoped predicates). Golden-file tests over
  emitted Go. Source: platform-go's `identity/migrations` (schemas, rendered
  per dialect) and the committed `<dialect>_generated.sql` under
  `identity/internal/queries` — note these land with platform-go PR #317
  (branch `feat/identity-querygen-port`), so copy from that branch until it
  merges. The corpus is vendored into this repo as `testdata/`, frozen at
  copy time; it is a test fixture, not a live dependency on platform.
- **Compile tests:** the emitted package is built as part of the tool's suite;
  a deliberately divergent fixture asserts the failure is a compile error
  naming the query's symbol.
- **One execution smoke test, in this repo, against real engines.** Golden
  files and compile tests cannot catch a generator that marshals a dialect's
  arguments in the wrong order — that compiles cleanly and fails on first
  execution, which is exactly the deploy-to-discover class this tool exists to
  kill. So the suite runs the emitted corpus package's queries against real
  Postgres and MySQL (via `testcontainers-go` **directly** — never platform's
  `testutils/containers`: platform-go must not appear in go.mod, per milestone
  0, and that house rule is a platform-repo rule, not an org one) and against
  SQLite with no container. Drivers (`pgx`, `go-sql-driver/mysql`, a sqlite
  driver matching platform's choice) are test-only dependencies. Gate on
  `RUN_CONTAINER_TESTS=true`, skipping otherwise, mirroring the convention
  unison's consumers already use.
- **The boundary:** unison's execution smoke proves the *generator* — args
  marshaled and rows scanned correctly per dialect. Consumers still prove
  their own semantics against real engines, as platform's three-dialect
  suites already do.

## 12. Milestones

Milestones 0 and 1 are **done**: the plugin, the orchestrator, `unison check`,
the type mapper, the prefix markers, the golden corpus, and an execution suite
that runs the emitted package against real Postgres, MySQL, and SQLite. Every
milestone-1 exit criterion has a test standing behind it — a swapped field, a
dropped query, and a retyped column are each a compile error naming the symbol,
and regeneration is byte-identical.

What follows is everything that is left.

### 2. Release — the blocking milestone

Nothing can adopt unison until it can be installed at a version. The tool
generates a header carrying its own version into every emitted file, and that
version currently reads `dev` for every build, because no tag exists.

**Done:** `scripts/release_build.sh` cross-compiles the four targets consumers
and their CI run — linux and darwin, amd64 and arm64 — with CGO off, and
checksums them; `.github/workflows/release.yaml` runs the whole suite against the
tagged commit and publishes the artifacts. The script refuses to guess a version,
because the version it stamps ends up inside every consumer's generated files.

**Left: cut `v0.1.0`.** Until a tag exists, the only build anyone can install
reports `dev`.

A note on what the tag does *not* move: the golden files. They are produced by a
binary the test suite builds with a plain `go build`, so they report `dev`
whatever tags exist, and they stay stable across releases. Only a consumer
running a released binary gets a real version in their headers — which is the
point of the string.

### 3. Usage — how a consumer actually adopts it

- **`.unison-version` and the ensure-installed path.** Document the pin file
  and the fetch-if-missing script shape consumers already use for
  `.sqlc-version` and `.protoc-version`. This is the piece that makes §10's
  pinning story real rather than aspirational.
- **A worked adoption guide.** The `unison.yaml`, the `make generate` target,
  the clean-tree CI gate, and the fact that sqlc must be on PATH at the pinned
  version. The README covers the config; what is missing is the install-to-CI
  path end to end.

### 4. Known gaps to close alongside

**Done: `UNISON_LOG_LEVEL` now reaches plugin mode.** sqlc does not hand a
process plugin the environment it was run with — it builds one holding
`SQLC_VERSION` plus only the keys the `plugins:` block's `env:` list names — and
the rendered config named none, so the variable never arrived however faithfully
plugin mode read it. Closing it turned up a second silence behind the first: a
level this process could not parse returned without ever being printed, because
plugin mode does not go through cobra and only the generation error was being
reported. Both are fixed, and `TestLogLevelReachesPluginMode` drives the whole
chain rather than asserting on rendered YAML.

**Left: `:execresult` has no corpus coverage.** It is in scope, emitted, and unit
tested, but the identity corpus does not use it, so it is the one in-scope
annotation the execution suite never runs. Given that §11's whole argument is
that only execution catches a marshaling bug, that hole should be filled by a
corpus query rather than argued away.

### 5. Platform pilot and rollout

platform-go ports the identity store onto the generated package; `identity/scan.go`
and the runtime binder delete; the three-dialect container suite stays green.
Then per-package adoption across platform's stores, each preceded by the "can
its queries converge" check; non-converging statements stay hand-written under
the container gate.

None of this gates platform's v13 release train: adoption changes no store's
public API.

## 13. Risks

- **Plugin protocol churn** — sqlc's CodeGenRequest evolves; the pin file
  (`internal/sqlcdriver/sqlc-version`, embedded so CI installs exactly what the
  code requires) plus the orchestrator's refusal to run against a mismatched
  sqlc keep consumer and tool moving in lockstep.
- **Scope creep toward conventions** — the temptation to teach unison
  platform's row conventions. Resisted by charter: conventions stay in
  `database/querygen` (which may *emit* the canonical `.sql` unison consumes);
  unison generates from whatever SQL it is given.
- **Prefix literal collision** (§9) — warned, documented, corpus-controlled.
- **Semantically divergent, structurally identical shapes** (§6) — the one
  class the compiler cannot reach. Accepted; it is what the consumer's
  three-dialect suites are for.

*Retired:* **SQLite analysis quality** was spike risk #1. It landed as the
general `type_overrides` option (§8) and did not turn out to be
disproportionate — the corpus needs a handful of hints, and Postgres needs them
too.

## 14. Settled questions

All three of the original open questions are closed, each pinned by code:

- **The name is `unison`.** `polyglot`, `manifold`, and `chorus` were the
  alternates; nothing was renamed.
- **`New` takes an emitted `Dialect` enum**, not a string — a typo'd dialect
  should not be a runtime error in the one tool whose thesis is moving errors
  earlier. The package also emits `Dialects()` for callers that need to
  enumerate the roster.
- **The orchestrator subsumes the compile-only gate.** `unison check` runs
  sqlc's static analysis over every dialect and writes nothing, so a project
  runs one tool for both tiers: the statements that go through generation, and
  the ones that are still hand-written but should still be checked against the
  schema they run against.
