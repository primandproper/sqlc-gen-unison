# identity — the golden corpus

platform-go's identity store, vendored frozen. **This is a test fixture, not a
dependency.** Nothing here is imported, and unison never reads it at runtime.

    source: github.com/primandproper/platform-go
    branch: feat/identity-querygen-port
    commit: 655ff2e0322d6a2b480ce0bf35645424fd41b085

It is the corpus because it has the richest shape variety in reach — creates,
keyset pagination, both-counts list subqueries, scoped predicates, nullable
arguments, and all five in-scope annotations (`:one`, `:many`, `:exec`,
`:execrows`) across three dialects that were authored to agree.

## What is here, and how it was produced

`queries/<dialect>.sql` are the committed `<dialect>_generated.sql` files from
`identity/internal/queries`, copied verbatim.

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
