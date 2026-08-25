package execution_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/primandproper/sqlc-gen-unison/testdata/golden/identitydb"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver.
)

// prefixCounter isolates concurrent suites sharing one server.
//
//nolint:gochecknoglobals // one counter per test binary is the point.
var prefixCounter atomic.Uint64

// env is one dialect's live database.
type env struct {
	db      *sql.DB
	name    string
	dialect identitydb.Dialect
}

// verifiedAt is whole seconds because the corpus stores microseconds on MySQL
// and seconds on SQLite, and the claim under test is that a time round-trips at
// all, not that every engine keeps the same precision.
//
//nolint:gochecknoglobals // a fixture value, not state.
var verifiedAt = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

// TestQuerier_SQLite runs the suite with no container at all.
//
// A temp file rather than :memory:, and one connection rather than a pool:
// SQLite gives each connection to an in-memory database its own separate
// database, so a pool would migrate one and query another.
//
// each case builds on the rows the last one left.
//
//nolint:tparallel // the suite is sequential against one schema, deliberately:
func TestQuerier_SQLite(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "identity.db"))
	must.NoError(t, err)

	t.Cleanup(func() { must.NoError(t, db.Close()) })

	db.SetMaxOpenConns(1)

	runQuerierSuite(t, t.Context(), env{db: db, name: "sqlite", dialect: identitydb.DialectSQLite})
}

// runQuerierSuite is the whole contract, run once per dialect.
//
// It is one function rather than a file of top-level tests so that the three
// dialects cannot drift: a case added for one is a case added for all three.
func runQuerierSuite(t *testing.T, ctx context.Context, e env) {
	t.Helper()

	prefix := fmt.Sprintf("u%d_", prefixCounter.Add(1))

	applySchema(t, ctx, e.db, e.name, prefix)

	q, err := identitydb.New(e.dialect, prefix)
	must.NoError(t, err)

	// The prefix reached the statements. If §9's marker were left unreplaced,
	// or placed on something that is not a table, every statement below would
	// fail against a schema that does not have those names.
	must.NoError(t, q.CreateUser(ctx, e.db, newUser("u1", "acme", "ada")))

	t.Run("every column lands in the field that names it", func(t *testing.T) {
		got, getErr := q.GetUser(ctx, e.db, identitydb.GetUserParams{ID: "u1", Scope: "acme"})
		must.NoError(t, getErr)

		// The assertion §1 opens with. Each value is distinct from every other,
		// so two same-typed columns swapped anywhere between the projection and
		// the scan shows up here rather than as a puzzling row in production.
		test.Eq(t, "u1", got.ID)
		test.Eq(t, "acme", got.Scope)
		test.Eq(t, "ada", got.Username)
		test.Eq(t, "ada@example.com", got.EmailAddress)
		test.Eq(t, "first-ada", got.FirstName)
		test.Eq(t, "last-ada", got.LastName)
		test.Eq(t, "hashed-ada", got.HashedPassword)
		test.Eq(t, "secret-ada", got.TwoFactorSecret)
		test.Eq(t, "token-ada", got.EmailAddressVerificationToken)
		test.Eq(t, "active", got.AccountStatus)
		test.Eq(t, "explained-ada", got.AccountStatusExplanation)
		test.True(t, got.RequiresPasswordChange)

		// A nullable time is the convergence claim of §8 made concrete:
		// timestamptz, DATETIME(6), and DATETIME all have to arrive as one
		// *time.Time.
		must.NotNil(t, got.EmailAddressVerifiedAt)
		test.True(t, verifiedAt.Equal(got.EmailAddressVerifiedAt.UTC()),
			test.Sprintf("want %s, got %s", verifiedAt, got.EmailAddressVerifiedAt.UTC()))

		// A column the insert left NULL must arrive nil rather than zero.
		test.Nil(t, got.PasswordLastChangedAt)
	})

	t.Run("execrows reports what it changed", func(t *testing.T) {
		affected, updateErr := q.UpdateUser(ctx, e.db, identitydb.UpdateUserParams{
			Username: "ada-renamed", EmailAddress: "ada2@example.com",
			FirstName: "first-ada2", LastName: "last-ada2",
			ID: "u1", Scope: "acme",
		})
		must.NoError(t, updateErr)
		test.Eq(t, int64(1), affected)

		// A row that does not match must report zero rather than an error, and
		// the scope has to be what excludes it.
		affected, updateErr = q.UpdateUser(ctx, e.db, identitydb.UpdateUserParams{
			Username: "nope", EmailAddress: "nope@example.com",
			FirstName: "no", LastName: "no",
			ID: "u1", Scope: "other-tenant",
		})
		must.NoError(t, updateErr)
		test.Eq(t, int64(0), affected)

		got, getErr := q.GetUser(ctx, e.db, identitydb.GetUserParams{ID: "u1", Scope: "acme"})
		must.NoError(t, getErr)
		test.Eq(t, "ada-renamed", got.Username)
	})

	t.Run("the list query binds every argument in the right position", func(t *testing.T) {
		// This is the case the harness exists for. On MySQL these eight fields
		// bind sixteen placeholders, and the LIMIT argument is the one sqlc
		// numbers 1 while it appears last. A transposition puts a bool where a
		// scope string belongs and a string where LIMIT belongs, and the engine
		// says so.
		for _, id := range []string{"u2", "u3"} {
			must.NoError(t, q.CreateUser(ctx, e.db, newUser(id, "acme", id)))
		}

		// A different tenant, which must never appear below.
		must.NoError(t, q.CreateUser(ctx, e.db, newUser("z9", "other-tenant", "zed")))

		rows, listErr := q.ListUsers(ctx, e.db, identitydb.ListUsersParams{
			Scope: "acme", IncludeArchived: false, ResultLimit: 2,
		})
		must.NoError(t, listErr)

		// ResultLimit bound where LIMIT expects it.
		must.SliceLen(t, 2, rows)

		// Scope bound where the predicate expects it, in all three of the
		// places the query repeats it.
		test.Eq(t, "u1", rows[0].ID)
		test.Eq(t, "u2", rows[1].ID)

		// The correlated subqueries count the same tenant, not the whole table.
		test.Eq(t, int64(3), rows[0].FilteredCount)
		test.Eq(t, int64(3), rows[0].TotalCount)

		// The projection's own columns still line up beside the counts.
		test.Eq(t, "ada-renamed", rows[0].Username)

		// The keyset cursor advances rather than restarting.
		page, pageErr := q.ListUsers(ctx, e.db, identitydb.ListUsersParams{
			Scope: "acme", ResultLimit: 10, PageCursor: new("u1"),
		})
		must.NoError(t, pageErr)
		must.SliceLen(t, 2, page)
		test.Eq(t, "u2", page[0].ID)
		test.Eq(t, "u3", page[1].ID)

		// A tenant with one row sees one row, which is the scope predicate
		// doing its job rather than the limit.
		other, otherErr := q.ListUsers(ctx, e.db, identitydb.ListUsersParams{
			Scope: "other-tenant", ResultLimit: 10,
		})
		must.NoError(t, otherErr)
		must.SliceLen(t, 1, other)
		test.Eq(t, "z9", other[0].ID)
	})

	t.Run("include_archived binds as a boolean", func(t *testing.T) {
		affected, archiveErr := q.ArchiveUser(ctx, e.db, identitydb.ArchiveUserParams{ID: "u3", Scope: "acme"})
		must.NoError(t, archiveErr)
		test.Eq(t, int64(1), affected)

		// An archived row is gone from a :one that filters it out.
		_, getErr := q.GetUser(ctx, e.db, identitydb.GetUserParams{ID: "u3", Scope: "acme"})
		test.True(t, errors.Is(getErr, sql.ErrNoRows),
			test.Sprintf("want sql.ErrNoRows for an archived user, got %v", getErr))

		// false excludes it; true includes it. The flag is bound in three
		// separate places in this query, so a wrong position shows up as a
		// count that disagrees with the rows.
		live, liveErr := q.ListUsers(ctx, e.db, identitydb.ListUsersParams{
			Scope: "acme", IncludeArchived: false, ResultLimit: 10,
		})
		must.NoError(t, liveErr)
		must.SliceLen(t, 2, live)
		test.Eq(t, int64(2), live[0].TotalCount)

		all, allErr := q.ListUsers(ctx, e.db, identitydb.ListUsersParams{
			Scope: "acme", IncludeArchived: true, ResultLimit: 10,
		})
		must.NoError(t, allErr)
		must.SliceLen(t, 3, all)
		test.Eq(t, int64(3), all[0].TotalCount)
	})

	t.Run("a second table generates and runs alongside the first", func(t *testing.T) {
		must.NoError(t, q.CreateAccount(ctx, e.db, identitydb.CreateAccountParams{
			ID: "a1", Scope: "acme", Name: "Acme", OwnerUserID: "u1",
			BillingStatus: "trialing", PaymentProcessorCustomerID: "cus_1",
			AddressLine1: "line1", AddressLine2: "line2", AddressCity: "city",
			AddressState: "state", AddressPostalCode: "postal",
			AddressCountry: "country", AddressPhone: "phone", TimeZone: "UTC",
		}))

		got, getErr := q.GetAccount(ctx, e.db, identitydb.GetAccountParams{ID: "a1", Scope: "acme"})
		must.NoError(t, getErr)

		// Sixteen same-typed string columns is the widest opportunity in the
		// corpus for a projection and a scan to disagree.
		test.Eq(t, "Acme", got.Name)
		test.Eq(t, "u1", got.OwnerUserID)
		test.Eq(t, "trialing", got.BillingStatus)
		test.Eq(t, "line1", got.AddressLine1)
		test.Eq(t, "line2", got.AddressLine2)
		test.Eq(t, "city", got.AddressCity)
		test.Eq(t, "state", got.AddressState)
		test.Eq(t, "postal", got.AddressPostalCode)
		test.Eq(t, "country", got.AddressCountry)
		test.Eq(t, "phone", got.AddressPhone)
		test.Eq(t, "UTC", got.TimeZone)
		test.Nil(t, got.SubscriptionPlanID)
	})
}

// newUser builds a user whose every string field is distinct, so that a
// transposition cannot coincidentally compare equal.
func newUser(id, scope, username string) identitydb.CreateUserParams {
	return identitydb.CreateUserParams{
		ID:                            id,
		Scope:                         scope,
		Username:                      username,
		EmailAddress:                  username + "@example.com",
		FirstName:                     "first-" + username,
		LastName:                      "last-" + username,
		HashedPassword:                "hashed-" + username,
		RequiresPasswordChange:        true,
		TwoFactorSecret:               "secret-" + username,
		EmailAddressVerifiedAt:        &verifiedAt,
		EmailAddressVerificationToken: "token-" + username,
		AccountStatus:                 "active",
		AccountStatusExplanation:      "explained-" + username,
	}
}

// ptr is the shortest way to hand a literal to a nullable field.
//
//go:fix inline
func ptr[T any](v T) *T { return new(v) }
