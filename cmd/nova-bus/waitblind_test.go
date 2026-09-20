package main

import "testing"

// Issue #1518: a line whose CURSOR is further behind than the walk's bound never sees new
// mail, and says nothing about it.
//
// Johnny's harness loop -- `nova-bus wait --as Johnny --timeout 60s --beat 60s --advance`,
// running for days -- printed this every minute for hours, with 760 unread notes behind a
// cursor the bus had long since left behind:
//
//	INBOX WALK bounded commits=500 remedy="raise --max-commits or close --before <instant>"
//	WAIT as=Johnny timeout=1m0s interval=1m0s cursor=8cd06f5a...
//	WAIT TIMEOUT after=1m2.926s polls=2 cursor=8cd06f5a...
//
// Exit 0, every time. The loop is running, the loop is green, and the loop is deaf --
// which is the failure the whole wake design exists to prevent (Glenn, 2026-09-19 01:20Z:
// "I'm sick of manually sitting here and waking friends up").
//
// Two things are wrong and each is enough on its own. `inbox` takes --max-commits and
// `wait` does not, so the remedy the bounded line prescribes cannot be typed at the verb
// that needs it. And a wait that cannot see the bus at all reports the same "nothing yet"
// as a wait over an empty one, so nothing in the transcript distinguishes them.
func TestWaitTakesMaxCommitsAndSaysSoWhenItIsBlind(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout := longBus(t, 1000)

	// A bound the caller raised past the distance: the wait runs, and times out in the
	// ordinary way because this fixture's notes are all older than the cursor.
	invoke(t, "", waitFlags(checkout, "Ada", "300ms", "--max-commits", "2000")...).
		mustCode(t, 0).
		mustContain(t, "stdout", "WAIT TIMEOUT after=")

	// The default bound, against a cursor a thousand commits behind: this wait can see
	// NOTHING, and every poll it makes will see nothing. It says so, once, and refuses,
	// rather than running to its deadline and reporting "nothing yet" like a healthy one.
	invoke(t, "", waitFlags(checkout, "Ada", "30s")...).
		mustCode(t, 2).
		mustContain(t, "stderr", `WAIT BLIND commits=500 remedy="raise --max-commits or close --before <instant>"`)
}
