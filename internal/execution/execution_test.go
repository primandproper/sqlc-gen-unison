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

	t.Run("a list parameter binds a set of any size", func(t *testing.T) {
		// The shape whose *generated code* differs by dialect rather than only
		// its text: one bound array on Postgres, one placeholder per element on
		// the other two. Everything else in the repository passes on a package
		// that expands the wrong number of placeholders or binds the elements
		// beside the scope instead of after it — the statement is assembled at
		// query time, so only a real engine can say it came out right.
		//
		// Distinct roles per user, so a transposition cannot compare equal, and
		// two rows for one user so that a list element bound where the join key
		// belongs shows up as a missing pair rather than a missing row.
		assignments := []identitydb.AssignUserRoleParams{
			{UserID: "u1", Role: "admin"},
			{UserID: "u1", Role: "member"},
			{UserID: "u2", Role: "member"},
			{UserID: "u3", Role: "archived-role"},
			{UserID: "z9", Role: "other-tenant-role"},
		}

		for i := range assignments {
			must.NoError(t, q.AssignUserRole(ctx, e.db, assignments[i]))
		}

		// Four elements expand to four placeholders, and the scope bound ahead
		// of them still excludes the other tenant. u3 is archived and z9 is
		// another tenant's, so asking for both proves the list narrows the set
		// rather than replacing the predicate.
		rows, listErr := q.ListRolesForUsers(ctx, e.db, identitydb.ListRolesForUsersParams{
			Scope: "acme", UserIDs: []string{"u1", "u2", "u3", "z9"},
		})
		must.NoError(t, listErr)
		must.SliceLen(t, 3, rows)

		test.Eq(t, "u1", rows[0].UserID)
		test.Eq(t, "admin", rows[0].Role)
		test.Eq(t, "u1", rows[1].UserID)
		test.Eq(t, "member", rows[1].Role)
		test.Eq(t, "u2", rows[2].UserID)
		test.Eq(t, "member", rows[2].Role)

		// One element is the case that expands to a single placeholder, which
		// is also the shape a `?` would have had if nothing expanded at all —
		// so it has to pick the right row rather than merely run.
		one, oneErr := q.ListRolesForUsers(ctx, e.db, identitydb.ListRolesForUsersParams{
			Scope: "acme", UserIDs: []string{"u2"},
		})
		must.NoError(t, oneErr)
		must.SliceLen(t, 1, one)
		test.Eq(t, "u2", one[0].UserID)

		// The empty list is the defined answer: it matches nothing, on every
		// dialect. `IN ()` is a syntax error on two of the three, so the
		// expansion renders NULL; Postgres binds an empty array. Both mean the
		// same thing, and neither is an error the caller has to guard against.
		empty, emptyErr := q.ListRolesForUsers(ctx, e.db, identitydb.ListRolesForUsersParams{
			Scope: "acme", UserIDs: []string{},
		})
		must.NoError(t, emptyErr)
		test.SliceEmpty(t, empty)

		// A nil slice is the same empty set, and it is what a caller who built
		// the list from a filtered loop actually passes.
		nilled, nilErr := q.ListRolesForUsers(ctx, e.db, identitydb.ListRolesForUsersParams{
			Scope: "acme", UserIDs: nil,
		})
		must.NoError(t, nilErr)
		test.SliceEmpty(t, nilled)
	})

	t.Run("a zoned time argument binds as the instant it names", func(t *testing.T) {
		// The class that survives everything else in this repository. SQLite
		// has no date type: a DATETIME column holds text, and a comparison
		// against one compares two strings. A bound time.Time reaches the
		// engine as whatever the driver renders it, and that rendering's
		// leading characters are the stored shape only while the value is UTC
		// — so a caller whose clock is not UTC compares their wall clock
		// against a UTC one, every window is off by their offset, and nothing
		// reports an error. The other two engines have a timestamp type and are
		// unharmed, which is exactly what makes it invisible unless the case
		// runs on all three.
		//
		// Both zones below are extremes and neither is the host's, so nothing
		// here depends on where the suite runs.
		east := time.FixedZone("east-of-utc", 14*60*60)
		west := time.FixedZone("west-of-utc", -12*60*60)

		// The window is built around a timestamp the server itself wrote rather
		// than around this process's clock, so the assertion says nothing about
		// either machine's idea of what time it is.
		anchor, anchorErr := q.GetUser(ctx, e.db, identitydb.GetUserParams{ID: "u1", Scope: "acme"})
		must.NoError(t, anchorErr)

		since := anchor.CreatedAt.Add(-time.Minute).In(east)
		until := anchor.CreatedAt.Add(time.Minute).In(west)

		// A lower bound a minute before the rows were written matches all of
		// them, whatever zone it carries. Bound as it stands, the eastern
		// rendering reads fourteen hours into the future and this finds none.
		matched, matchedErr := q.ListUsers(ctx, e.db, identitydb.ListUsersParams{
			Scope: "acme", IncludeArchived: true, ResultLimit: 10,
			CreatedAfter: &since,
		})
		must.NoError(t, matchedErr)
		must.SliceLen(t, 3, matched)

		// A lower bound a minute after them matches none. That is the same
		// claim from the other side, and it is what a formatter that dropped
		// the argument rather than spelling it would fail.
		none, noneErr := q.ListUsers(ctx, e.db, identitydb.ListUsersParams{
			Scope: "acme", IncludeArchived: true, ResultLimit: 10,
			CreatedAfter: &until,
		})
		must.NoError(t, noneErr)
		test.SliceEmpty(t, none)

		// And the same for a time the caller writes rather than one the engine
		// did: a non-null argument in a zone of its own has to come back the
		// instant it went in.
		expires := time.Date(2031, 7, 4, 9, 30, 0, 0, east)

		must.NoError(t, q.CreateInvitation(ctx, e.db, identitydb.CreateInvitationParams{
			ID: "i1", Scope: "acme", BelongsToAccount: "a1", FromUser: "u1",
			ToEmail: "invitee@example.com", ToName: "invitee",
			Token: "token-i1", Status: "pending", Note: "note-i1",
			ExpiresAt: expires,
		}))

		invitation, invitationErr := q.GetInvitation(ctx, e.db,
			identitydb.GetInvitationParams{ID: "i1", Scope: "acme"})
		must.NoError(t, invitationErr)

		test.True(t, expires.Equal(invitation.ExpiresAt),
			test.Sprintf("want %s, got %s", expires.UTC(), invitation.ExpiresAt.UTC()))
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
