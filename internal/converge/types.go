package converge

import (
	"fmt"
	"strings"

	"github.com/primandproper/sqlc-gen-unison/internal/ir"
)

// engineTypes maps what an analyzer reports onto what unison converges it to.
//
// This is §8's "one table", and it is the only place per-dialect knowledge lives
// in shared-file emission. There is no per-engine section on purpose: a name is
// looked up the same way whoever reported it, so `text`, `varchar`, and `TEXT`
// converge because they are all here, not because a branch decided which engine
// was asking. The byte-identical overwrites are the standing proof it stays
// converged — the moment two engines disagree about a column, the shared files
// stop matching and the compiler says so.
//
// Keys are lowercased before lookup, which is why SQLite's shouty `DATETIME` and
// Postgres's `timestamptz` sit in one table, and Postgres's `pg_catalog.`
// qualifier is stripped first, so `pg_catalog.int8` and MySQL's `bigint` land on
// the same entry rather than needing a qualified copy of every row.
var engineTypes = map[string]ir.Kind{
	// Text.
	"text":              ir.KindString,
	"varchar":           ir.KindString,
	"char":              ir.KindString,
	"bpchar":            ir.KindString,
	"character":         ir.KindString,
	"character varying": ir.KindString,
	"nvarchar":          ir.KindString,
	"tinytext":          ir.KindString,
	"mediumtext":        ir.KindString,
	"longtext":          ir.KindString,
	"clob":              ir.KindString,
	"uuid":              ir.KindString,

	// Times. Postgres's timestamptz, MySQL's DATETIME(6), and SQLite's DATETIME
	// are the same instant to Go, which is the whole reason a shared row struct
	// is possible.
	"timestamptz":                 ir.KindTime,
	"timestamp":                   ir.KindTime,
	"timestamp with time zone":    ir.KindTime,
	"timestamp without time zone": ir.KindTime,
	"datetime":                    ir.KindTime,
	"date":                        ir.KindTime,
	"time":                        ir.KindTime,

	// Booleans. MySQL has no boolean: BOOLEAN is TINYINT(1), and the analyzer
	// reports tinyint. Mapping it here is what lets a BOOLEAN column converge
	// across the three. A consumer who genuinely stores a small number in a
	// TINYINT gets a divergence against Postgres's smallint, which is a compile
	// error naming the column — the right outcome, and overridable.
	"bool":    ir.KindBool,
	"boolean": ir.KindBool,
	"tinyint": ir.KindBool,

	// Integers, all of them 64 bits wide.
	//
	// SQLite has exactly one integer type and it is 64-bit, so anything
	// narrower on another engine would diverge against it. Widening everything
	// costs nothing a consumer will notice and converges by construction; the
	// alternative is a shared struct whose int32 field is a lie on one engine.
	"bigint":       ir.KindInt64,
	"int8":         ir.KindInt64,
	"integer":      ir.KindInt64,
	"int":          ir.KindInt64,
	"int4":         ir.KindInt64,
	"mediumint":    ir.KindInt64,
	"smallint":     ir.KindInt64,
	"int2":         ir.KindInt64,
	"serial":       ir.KindInt64,
	"bigserial":    ir.KindInt64,
	"int unsigned": ir.KindInt64,

	// Floats.
	"double":           ir.KindFloat64,
	"double precision": ir.KindFloat64,
	"float":            ir.KindFloat64,
	"float4":           ir.KindFloat64,
	"float8":           ir.KindFloat64,
	"real":             ir.KindFloat64,

	// Bytes.
	"blob":       ir.KindBytes,
	"bytea":      ir.KindBytes,
	"binary":     ir.KindBytes,
	"varbinary":  ir.KindBytes,
	"tinyblob":   ir.KindBytes,
	"mediumblob": ir.KindBytes,
	"longblob":   ir.KindBytes,
}

// mapType converges one analyzed column onto a neutral type.
//
// An override wins outright: it is not checked against the engine type, and it
// replaces the analyzer's nullability as well as its type. Both halves of that
// are deliberate.
//
// The reason to write an override is usually that the analyzer got the type
// wrong or got nothing at all, so validating it against what it replaces would
// reject exactly the cases it exists for.
//
// Nullability has to go the same way because it diverges on its own. A LIMIT
// argument is nullable on Postgres and SQLite, where COALESCE can absorb a NULL,
// and NOT NULL on MySQL, where a bare placeholder cannot — the same column
// reaching the shared struct as *int64 from two dialects and int64 from the
// third. Since both spellings compile wherever the field is merely passed
// through, this is one of the few divergences the compiler would not catch, so
// the override settles it: whatever the consumer wrote is the type, and every
// dialect emits it. Write *int64 to get a pointer.
func mapType(table, column, engineType string, notNull bool, override func(string, string) (string, bool)) (ir.Type, error) {
	if goType, ok := override(table, column); ok {
		return ir.Type{Override: goType}, nil
	}

	kind, ok := engineTypes[normalizeTypeName(engineType)]
	if !ok {
		return ir.Type{}, unmappedTypeError(table, column, engineType)
	}

	return ir.Type{Kind: kind, Nullable: !notNull}, nil
}

// normalizeTypeName reduces an engine's spelling to the table's key: lowercased,
// with any length or precision dropped. `DATETIME(6)` and `varchar(255)` are the
// same type as far as convergence is concerned.
func normalizeTypeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))

	// Postgres reports its built-ins qualified by the catalog they live in.
	// That is the same type by another spelling, so it is normalized away
	// rather than duplicated through the table above.
	name = strings.TrimPrefix(name, "pg_catalog.")

	if open := strings.IndexByte(name, '('); open >= 0 {
		name = strings.TrimSpace(name[:open])
	}

	return strings.TrimSuffix(name, " unsigned")
}

// unmappedTypeError explains a type unison will not guess at, and says what to
// do about it.
//
// `any` is the common case and deserves its own sentence, because it is not a
// missing entry in the table above — it is the analyzer reporting that it could
// not resolve a type at all. SQLite is the engine §8 warns about, but Postgres
// does it too: a COALESCE'd LIMIT comes back as `any` there.
func unmappedTypeError(table, column, engineType string) error {
	where := column
	if table != "" {
		where = table + "." + column
	}

	if normalizeTypeName(engineType) == "any" || engineType == "" {
		return fmt.Errorf(
			"unison: the analyzer could not resolve a type for %s, so there is nothing to converge. "+
				"Give it one with a type_override: {column: %q, go_type: ...}",
			where, "*."+column)
	}

	return fmt.Errorf(
		"unison: no converged type for %s of database type %q. "+
			"Either it is a type unison does not map, or it is genuinely dialect-specific; "+
			"say what it should be with a type_override: {column: %q, go_type: ...}",
		where, engineType, "*."+column)
}
