package gogen

import (
	"testing"

	"github.com/shoenig/test"
)

func TestExportedName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"id":                               "ID",
		"email_address":                    "EmailAddress",
		"two_factor_secret_verified_at":    "TwoFactorSecretVerifiedAt",
		"owner_user_id":                    "OwnerUserID",
		"email_address_verification_token": "EmailAddressVerificationToken",
		"scope":                            "Scope",
		"address_line1":                    "AddressLine1",
		"total_count":                      "TotalCount",
		"GetUser":                          "GetUser",
		"url":                              "URL",
	}

	for input, expected := range cases {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			test.Eq(t, expected, exportedName(input))
		})
	}
}

func TestUnexportedName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"GetUser":     "getUser",
		"ListUsers":   "listUsers",
		"ArchiveUser": "archiveUser",
		// An initialism that lands first stays lowercase, so the identifier is
		// unexported rather than merely odd.
		"id": "id",
	}

	for input, expected := range cases {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			test.Eq(t, expected, unexportedName(input))
		})
	}
}

func TestDialectName(t *testing.T) {
	t.Parallel()

	test.Eq(t, "PostgreSQL", dialectName("postgresql"))
	test.Eq(t, "MySQL", dialectName("mysql"))
	test.Eq(t, "SQLite", dialectName("sqlite"))
	test.Eq(t, "mysqlQueries", dialectReceiver("mysql"))
	test.Eq(t, "newSQLite", dialectConstructor("sqlite"))
}
