package swarm

import (
	"strings"
	"testing"
	"time"
)

// SLICE 10: THE USAGE ROW IS READ FROM THE HARNESS'S OWN STORE.
//
// The native run's numbers come from the `message` table of the harness's sqlite store: the
// rows whose `data` JSON says role=assistant, whose `time_created` (MILLISECONDS since the
// epoch) falls inside the run's own window, grouped by providerID and modelID and summed into
// the six numeric columns. A column no message reported stays a dash. The fixture is a small
// schema built in the test with the real sqlite3 into a temp directory, and read back by
// ReadCardUsage with a window around the fixture's own millisecond stamps.

// cardUsageBaseMs is the shared epoch of the testdata fixture's rows.
const cardUsageBaseMs = int64(1760000000000)

// cardUsageWindow is a started/ended pair that brackets the fixture's rows (the reader widens
// it five seconds each side, so an exact match is comfortably inside).
func cardUsageWindow() (time.Time, time.Time) {
	t := time.UnixMilli(cardUsageBaseMs)
	return t, t
}

// A data home with no store at either standard location yields the dash row, an empty path,
// a note that names the store the reader looked for, and the no-store absence reason --
// never a silent dash.
func TestUsageRowNamesMissingStore(t *testing.T) {
	t.Parallel()

	dataHome := t.TempDir()
	started, ended := cardUsageWindow()
	usage, note, path, reason := ReadCardUsage(dataHome, started, ended)
	if note == "" {
		t.Fatal("a missing store is a note naming it, not silence")
	}
	if !strings.Contains(note, "opencode") {
		t.Errorf("the note names the harness store it looked for: %q", note)
	}
	if reason != "no-store" {
		t.Errorf("a missing store names its absence reason, got %q", reason)
	}
	if path != "" {
		t.Errorf("a missing store answers with no path, got %q", path)
	}
	for _, c := range TokenColumns {
		if got := usage.Values[c]; got != Dash {
			t.Errorf("%s is %q, want %q when the store is missing", c, got, Dash)
		}
	}
	if got := usage.Values["usd"]; got != Dash {
		t.Errorf("usd is %q, want %q when the store is missing", got, Dash)
	}
}
