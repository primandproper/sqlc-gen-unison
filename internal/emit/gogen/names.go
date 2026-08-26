package gogen

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// initialisms are the words Go convention spells in full caps. The list is
// short and conventional rather than exhaustive: a name unison guesses wrong is
// a name a consumer reads every day, so it errs toward leaving words alone.
var initialisms = map[string]string{
	"acl": "ACL", "api": "API", "ascii": "ASCII", "cpu": "CPU", "css": "CSS",
	"dns": "DNS", "eof": "EOF", "guid": "GUID", "html": "HTML", "http": "HTTP",
	// The plural earns its place because list parameters make it common: a
	// field bound against a set of keys is named for the plural, and UserIds
	// beside UserID reads as a typo rather than a convention.
	"https": "HTTPS", "id": "ID", "ids": "IDs", "ip": "IP", "json": "JSON", "lhs": "LHS",
	"qps": "QPS", "ram": "RAM", "rhs": "RHS", "rpc": "RPC", "sla": "SLA",
	"smtp": "SMTP", "sql": "SQL", "ssh": "SSH", "tcp": "TCP", "tls": "TLS",
	"ttl": "TTL", "udp": "UDP", "ui": "UI", "uid": "UID", "uuid": "UUID",
	"uri": "URI", "url": "URL", "utf8": "UTF8", "vm": "VM", "xml": "XML",
	"xmpp": "XMPP", "xsrf": "XSRF", "xss": "XSS",
}

// exportedName turns a SQL name into an exported Go identifier:
// email_address becomes EmailAddress, id becomes ID.
func exportedName(name string) string {
	return joinWords(splitWords(name), true)
}

// unexportedName turns a SQL name into an unexported Go identifier, for the
// statement constants and struct fields the generated package keeps to itself.
func unexportedName(name string) string {
	return joinWords(splitWords(name), false)
}

// splitWords breaks a SQL name on underscores and on case boundaries, so that
// both snake_case and an already-camelCased query name come apart correctly.
func splitWords(name string) []string {
	var (
		words []string
		word  []rune
	)

	flush := func() {
		if len(word) > 0 {
			words = append(words, string(word))
			word = word[:0]
		}
	}

	runes := []rune(name)

	for i, r := range runes {
		switch {
		case r == '_' || r == ' ' || r == '-' || r == '.':
			flush()
		case unicode.IsUpper(r) && i > 0 && !unicode.IsUpper(runes[i-1]):
			flush()

			word = append(word, r)
		default:
			word = append(word, r)
		}
	}

	flush()

	return words
}

// joinWords assembles words into an identifier, capitalizing each and honoring
// the initialism list. When the result would not start with a letter — a column
// named `2fa`, say — an underscore is prepended so the identifier is legal.
func joinWords(words []string, exported bool) string {
	var b strings.Builder

	for i := range words {
		word := words[i]

		lower := strings.ToLower(word)

		if i == 0 && !exported {
			// The first word of an unexported name stays lowercase, including
			// an initialism: idParam, not IDParam or iDParam.
			b.WriteString(lower)

			continue
		}

		if replacement, ok := initialisms[lower]; ok {
			b.WriteString(replacement)

			continue
		}

		b.WriteString(strings.ToUpper(lower[:1]) + lower[1:])
	}

	out := b.String()
	if out == "" {
		return "_"
	}

	if r, _ := utf8.DecodeRuneInString(out); !unicode.IsLetter(r) && r != '_' {
		return "_" + out
	}

	return out
}

// dialectNames spells the engine names sqlc uses the way Go would.
var dialectNames = map[string]string{
	"postgresql": "PostgreSQL",
	"mysql":      "MySQL",
	"sqlite":     "SQLite",
}

// dialectName reports the exported spelling of a dialect.
func dialectName(dialect string) string {
	if name, ok := dialectNames[dialect]; ok {
		return name
	}

	return exportedName(dialect)
}

// dialectReceiver reports the unexported type name for a dialect's querier.
func dialectReceiver(dialect string) string {
	return unexportedName(dialect) + "Queries"
}

// dialectConstructor reports the unexported constructor name for a dialect.
func dialectConstructor(dialect string) string {
	return "new" + dialectName(dialect)
}
