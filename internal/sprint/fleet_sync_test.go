package sprint

import (
	"strings"
	"testing"
)

// TestValidSyncRefusesWhatTheFleetRefuses: a name or width fleet up would
// refuse, and a name twice, are refused whole.
func TestValidSyncRefusesWhatTheFleetRefuses(t *testing.T) {
	t.Parallel()
	if why := ValidSync([]SyncMember{{"m1", 4}, {"m2", MaxWidth}}); why != "" {
		t.Fatalf("a valid sync: %s", why)
	}
	if why := ValidSync(nil); why != "" {
		t.Fatalf("an empty sync holds the fleet and is valid: %s", why)
	}
	for _, c := range []struct {
		want []SyncMember
		say  string
	}{
		{[]SyncMember{{"m 1", 4}}, "a member name wants"},
		{[]SyncMember{{"m1", 0}}, "a width wants a whole number from 1"},
		{[]SyncMember{{"m1", MaxWidth + 1}}, "a width wants a whole number from 1"},
		{[]SyncMember{{"m1", 1}, {"m1", 2}}, "m1 is named twice"},
	} {
		if why := ValidSync(c.want); !strings.Contains(why, c.say) {
			t.Errorf("%v: %q, want %q", c.want, why, c.say)
		}
	}
}

// TestDriftSaysWhatASyncWouldWrite: one line for each kind of drift.
func TestDriftSaysWhatASyncWouldWrite(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d   Drift
		say string
	}{
		{Drift{Member: "m1", Kind: DriftAdd, To: 4}, "m1 is a member of the inventory and not of the fleet: add it at width 4"},
		{Drift{Member: "m1", Kind: DriftWidth, From: 4, To: 6}, "m1 has width 4 and the inventory says 6"},
		{Drift{Member: "m1", Kind: DriftHold}, "m1 is a member of the fleet and not of the inventory: hold it down"},
	} {
		if got := c.d.Line(); got != c.say {
			t.Errorf("%q, want %q", got, c.say)
		}
	}
}
