// Package execution runs the generated package against real database servers.
//
// Everything else in this repository proves that the emitted code compiles,
// converges, and regenerates byte-identically. None of that executes a
// statement, and there is one class of bug that survives all three: unison
// generates the argument order, and an argument order that is wrong is wrong in
// every consumer at once, silently.
//
// The MySQL case is the reason this package exists. sqlc reports a parameter's
// Number, and on MySQL that number is not the placeholder's position — the bare
// `?` that LIMIT requires comes back numbered 1 while appearing last in the
// text. MySQL also does not deduplicate repeated named arguments, so the
// corpus's list queries bind sixteen placeholders from eight fields. Get either
// wrong and the package still compiles, still matches its golden file, and
// still regenerates identically. Only a server disagrees.
//
// The division of labor the PRD draws still holds: consumers prove their
// queries mean the right thing. This package proves only that what unison
// emitted is what runs — that a value bound to a field named Scope arrives in
// the scope column, and that a row scanned into a field named Username holds
// the username.
//
// The suite is one function run once per dialect, so a case added for one
// dialect is a case added for all three.
package execution
