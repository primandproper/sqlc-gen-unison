package converge

import (
	"testing"

	"github.com/shoenig/test"
)

// TestTimeLayout pins which engines need a timestamp spelled for them.
//
// SQLite is the whole list, and the layout is whole seconds because that is
// what CURRENT_TIMESTAMP writes. An engine added to this without the emitter
// having anything to format with, or a layout that stopped matching what the
// engine stores, is the silent case: every comparison still runs and every
// window is off.
func TestTimeLayout(t *testing.T) {
	t.Parallel()

	test.Eq(t, "2006-01-02 15:04:05", timeLayout("sqlite"))

	// Both of these have a timestamp type, so their drivers already send one.
	test.Eq(t, "", timeLayout("postgresql"))
	test.Eq(t, "", timeLayout("mysql"))
}
