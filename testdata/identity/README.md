# identity — the golden corpus

platform-go's identity store, vendored frozen. **This is a test fixture, not a
dependency.** Nothing here is imported, and unison never reads it at runtime.

    source: github.com/primandproper/platform-go
    branch: feat/identity-querygen-port
    commit: 655ff2e0322d6a2b480ce0bf35645424fd41b085

It is the corpus because it has the richest shape variety in reach — creates,
keyset pagination, both-counts list subqueries, scoped predicates, nullable
arguments, variable-length `IN` lists, and all five in-scope annotations
(`:one`, `:many`, `:exec`, `:execrows`) across three dialects that were authored
to agree.

## What is here, and how it was produced

`queries/<dialect>.sql` are the committed `<dialect>_generated.sql` files from
`identity/internal/queries`, copied verbatim — with one appended exception,
below.

`AssignUserRole` and `ListRolesForUsers` were **authored here**, not copied.
They are the corpus's variable-length `IN` list, and the source module has no
canonical spelling to copy because that is precisely the class it still
hand-builds: `buildSelectUsersByIDs` and `buildSelectRoles` are the escape
hatches unison closes. They are written the way each engine's static form
requires — `= ANY(sqlc.arg(user_ids)::TEXT[])` on Postgres,
`IN (sqlc.slice(user_ids))` on the other two — under one query name, which is
the whole claim under test. When the source module's port lands, these should be
replaced by whatever it commits.

`schema/<dialect>.sql` were produced by that package's generator rather than
copied, because there is no hand-written schema file to copy — the DDL is
rendered from `identity/migrations` per dialect:

    go run ./internal/queriesgen -schema postgres   # and mysql, sqlite

at the empty table prefix, which is the prefix the canonical queries name and the
only one sqlc could resolve, since an identifier is not a bind parameter in any
of the three engines.

The files are named for sqlc's engine names (`postgresql`, `mysql`, `sqlite`)
rather than platform's dialect names, because those names are the roster.

## Why it is frozen

The corpus is a fixture at a known commit, so a test that fails is unison
changing rather than platform changing. Refreshing it is a deliberate act: copy
again, re-record the commit above, and expect the golden files to move.
