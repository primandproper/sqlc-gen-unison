package converge

// textTimeLayout is the shape a text-timestamp engine stores and compares.
//
// It is whole seconds because that is what CURRENT_TIMESTAMP writes, and a
// bound value that carries more precision than a stored one is a value that
// compares correctly by accident rather than by construction — the accident
// this layout exists to remove. Every engine below stores seconds and every
// argument binds seconds, so the two are the same shape.
const textTimeLayout = "2006-01-02 15:04:05"

// timeLayout reports how a dialect spells a timestamp, or "" when it has a
// timestamp type and needs no spelling.
//
// SQLite is the whole list. It has no date type at all: a DATETIME column holds
// text, and every comparison against one is a string comparison. A driver
// binding a Go time.Time hands the engine that value's own rendering, whose
// leading characters are the stored shape only while the value is UTC — so a
// caller in any other zone compares a wall clock against a UTC clock, silently,
// and every window is off by their offset.
//
// Deciding it here rather than in the emitter is the same division of labor the
// rest of this package keeps: the emitter renders shapes, and which engines
// have which types is analysis. The emitter is handed a layout and formats
// with it, exactly as it is handed a placeholder rather than deriving one.
func timeLayout(dialect string) string {
	if dialect == "sqlite" {
		return textTimeLayout
	}

	return ""
}
