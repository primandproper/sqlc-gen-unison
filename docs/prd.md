# sqlc-gen-unison

> **Status:** draft PRD. Working name — `unison` names the design invariant (every
> dialect emits the same Go types, in unison); rename is a search-and-replace.
> This document lives in platform-go only until the tool's repository exists,
> then moves there as its founding design doc.

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
dialects: [postgresql, mysql, sqlite]   # the roster — see §6
package: identitydb
out: internal/identitydb
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
  type_overrides:
    - column: "*.scope"
      go_type: github.com/primandproper/platform-go/v13/tenancy.Scope
```

Queries carry the same **name and annotation** across dialects (`-- name:
CreateUser :one` in all three files); the query name is the join key.

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
    CreateUser(ctx context.Context, db DBTX, arg CreateUserParams) (User, error)
    // ...
}

func New(dialect string, prefix string) (Querier, error) {
    switch dialect { // the roster, known to every invocation via options
    case "postgresql": return newPostgres(prefix), nil
    case "mysql":      return newMySQL(prefix), nil
    case "sqlite":     return newSQLite(prefix), nil
    }
    return nil, fmt.Errorf("unison: unknown dialect %q", dialect)
}

// queries_mysql.go (per-dialect)
const createUserMySQL = "INSERT INTO {{prefix}}identity_users (...) VALUES (?, ?, ?, ?)"

func (q *mysqlQueries) CreateUser(ctx context.Context, db DBTX, arg CreateUserParams) (User, error) {
    // args marshaled by the generator in MySQL's positional order,
    // read-back SELECT for the RETURNING-shaped result — same signature.
}
```

## 7. Shape-divergence policy

**Refused, not reconciled.** The known catalog of three-dialect divergence
(measured while porting platform's identity store) is finite and each entry has
an authoring-side fix that converges the shape:

| divergence | convergent authoring |
| --- | --- |
| MySQL has no `RETURNING` | the MySQL query does exec + read-back; the generated method presents the same `:one` result shape |
| MySQL `LIMIT` takes only a bare placeholder | generator emits the marker; still reported under the same named argument |
| Reserved words differ (`cursor` in MySQL) | canonical argument names avoid the union of reserved words; the checker catches new ones |
| `CURRENT_TIMESTAMP` granularity | schema convention (`DATETIME(6)`) — a consumer schema concern, checked by sqlc against that schema |
| rows-changed vs rows-matched | `:execrows` docs state the semantics per dialect; queries that gate on the count use a discriminating predicate |

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

**SQLite is the known weak engine.** sqlc's SQLite analyzer resolves types more
loosely (columns come back untyped more often), so the mapper accepts
per-column hints via options. Spike risk #1; testable on day one against the
identity corpus.

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
- **Process plugin first.** Consumers pin via a `.unison-version` file and
  their existing ensure-tool-installed machinery, exactly like `.sqlc-version`
  and `.protoc-version` today. WASM distribution (URL + sha256 in config,
  hermetic, sandboxed) is a later milestone once releases exist — it changes
  packaging, not design.
- **Generated-files gate in consumers:** regenerate in CI, fail on dirty tree.
  Determinism (§2) is what makes this gate meaningful; the tool's own test
  suite asserts double-generation is byte-identical.

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
- **Container tests stay in consumers.** Unison proves emission; consumers
  prove semantics against real engines, as platform's three-dialect suites
  already do.

## 12. Milestones

0. **De-template the repo.** The repository was created from
   `primandproper/template-go`, which is an *application* template built on
   platform-go; unison keeps its toolchain and sheds its application layer, as
   one commit so the diff documents what this tool deliberately does not carry:
   - Drop the `platform-go` dependency entirely. Unison's charter (§10) is that
     it versions against sqlc's plugin protocol, never a consumer's major —
     and platform will pin `.unison-version`, so depending back on platform is
     a cross-repo version loop. Logging is stdlib `slog` to **stderr** (§5).
   - Delete `internal/config`, `config/localdev.json`, `config/production.json`,
     the `make configs` target, and the `SQLC_GEN_UNISON_` env-prefix
     machinery. Unison's configuration is the consumer's `unison.yaml` plus
     flags; it has no service-style environment config.
   - Delete the `vendor`/`clean_vendor`/`revendor` Makefile targets — the
     template predates its upstream dropping vendoring, and the same reasoning
     applies here.
   - Bump `go 1.26` → `1.27`; add `github.com/sqlc-dev/plugin-sdk-go`.
   - Keep: Makefile + `scripts/`, `.golangci.yml`, `.github` CI, shellcheck,
     the format targets, `shoenig/test`, the moq conventions, and the cobra
     CLI shape — `generate` and `check` as subcommands, plugin mode as the
     no-args root behavior.
1. **Spike:** plugin + orchestrator against the identity corpus; shared types,
   three dialect files, prefix marker, SQLite hints. Exit criteria: a
   swapped-field paste is a compile error; a dropped MySQL query is a compile
   error naming it; regeneration is byte-identical.
2. **First tag + platform pilot:** platform-go ports the identity store onto
   the generated package; `identity/scan.go` and the runtime binder delete;
   three-dialect container suite stays green.
3. **Rollout:** per-package adoption across platform's stores, each preceded by
   the "can its queries converge" check; non-converging statements stay
   hand-written under the container gate.
4. **WASM distribution**, if and when a consumer outside the org wants
   hermetic pinning.

None of this gates platform's v13 release train: adoption changes no store's
public API.

## 13. Risks

- **SQLite analysis quality** (§8) — mitigated by hints; worst case, SQLite
  columns need more annotation than feels proportionate.
- **Plugin protocol churn** — sqlc's CodeGenRequest evolves; the pin file plus
  the tool's own sqlc-version pin keep consumer and tool moving in lockstep.
- **Scope creep toward conventions** — the temptation to teach unison
  platform's row conventions. Resisted by charter: conventions stay in
  `database/querygen` (which may *emit* the canonical `.sql` unison consumes);
  unison generates from whatever SQL it is given.
- **Prefix literal collision** (§9) — warned, documented, corpus-controlled.

## 14. Open questions

- Final name (working: `unison`; alternates considered: `polyglot`,
  `manifold`, `chorus`).
- Whether `querier.go`'s `New` takes a dialect string or a small enum the tool
  also emits (leaning enum — a typo'd dialect should not be a runtime error in
  the one tool whose thesis is moving errors earlier).
- Whether the orchestrator subsumes the `sqlc compile`-only gate for statements
  that *don't* go through generation (probably yes: `unison check` = compile
  all dialects, generate nothing), letting consumers run one tool for both
  tiers.
