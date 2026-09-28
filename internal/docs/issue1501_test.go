package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue1501 pins nova-tools#1501 on the registry file -- decide route: the
// registry's availability is hand-written, so the ladder routes to minds that
// are asleep. The hurt: hygiene-delete-on-shape routed to emma ask=bus on
// 2026-09-18 while Emma had been asleep on the bus since about 21:45Z, because
// every friend's row said "available" since somebody typed it there.
//
// The text this proves is the ask, word for word: the route reads presence at
// decision time rather than trust the file, and the registry row as the floor
// under it (a row marked asleep or reserved stays off the ladder whatever
// presence says, so the file can still take a mind out by hand). The file is
// the floor and never the live truth; carrying that contract is its half of
// the fix. The #1501 design ruling names the source: `nova-wake awake` (not
// the superseded `nova-bus presence`), one immutable snapshot per decision,
// and no fallback to the hand-typed "available" field on stale, unreadable,
// absent, malformed or unknown evidence. The presence READ itself is a new data
// path in internal/decide (LoadRegistry / RouteRules over that snapshot) and is
// the follow-up;
// this pins the file's side so the next hand edit cannot forget the row is a
// floor (see tune1_test.go's TestAnAsleepMindIsNotOnTheHeightLadder for the
// half that says the ladder honours the field once it is true).
func TestIssue1501(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../decide/registry.json")
	if err != nil {
		t.Fatalf("internal/decide/registry.json: %v", err)
	}
	content := string(body)

	for _, want := range []string{
		"read presence at decision time",
		"the registry row as the floor under it",
		"asleep or reserved stays off the ladder",
		"nova-tools#1501",
		"nova-wake awake",
		"one immutable nova-wake awake snapshot per decision",
		"never falls back to the hand-typed available field",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("internal/decide/registry.json missing %q; the row is the floor under presence, not the live truth (nova-tools#1501)", want)
		}
	}
	// The ruling superseded the proposed source name; the contract must not
	// point the follow-up at it.
	if strings.Contains(content, "nova-bus presence") {
		t.Errorf("internal/decide/registry.json still names %q; the #1501 ruling source is nova-wake awake", "nova-bus presence")
	}
}
