package converge

// docs/SPEC-CHECK.md rule 11: "A tick at or before the remembered instant is
// that tick read again, not a second one, and never advances the streak." The
// remembered instant is written into --state, so it carries the clock's
// fraction only while the write keeps it: a state entry rounded to whole
// seconds makes a replayed subsecond tick look later than what was
// remembered, and one widening read twice counts as two and exits 1.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSubsecondTickReplayNeverAdvancesStreak(t *testing.T) {
	t.Parallel()

	// A first widening at a nonzero nanosecond: the streak is one, no exit 1.
	first := time.Date(2026, 9, 18, 12, 0, 0, 250_000_000, time.UTC)
	st := State{Streams: map[string]StreamState{}}
	_, st, streak := widening(5, 3).Apply(st, first)
	require.False(t, streak, "the first widening tick went red: %+v", st.Streams["EDGES"])
	require.Equal(t, 1, st.Streams["EDGES"].Widening, "the first widening was not counted: %+v", st.Streams["EDGES"])

	// Save/LoadState round-trips the remembered instant whole: what comes
	// back compares against the same instant the tick took.
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, st.Save(path))
	back, err := LoadState(path)
	require.NoError(t, err)
	require.Equal(t, st.Streams["EDGES"], back.Streams["EDGES"], "round trip gave %+v, want %+v", back.Streams["EDGES"], st.Streams["EDGES"])
	require.True(t, first.Equal(parseState(back.Streams["EDGES"].At)),
		"the remembered instant lost the clock's fraction: %q", back.Streams["EDGES"].At)
	st = back

	// Replaying that exact tick is the same reading: never the streak.
	for i := range 3 {
		var again State
		_, again, streak = widening(5, 3).Apply(st, first)
		require.False(t, streak, "re-reading the subsecond tick %d times went red", i+1)
		require.Equal(t, 1, again.Streams["EDGES"].Widening, "re-reading the subsecond tick counted it again: %+v", again.Streams["EDGES"])
		st = again
	}

	// An earlier fractional tick is a reading from before the remembered one:
	// also the same tick, and the remembered instant does not move backwards.
	var earlier State
	_, earlier, streak = widening(5, 3).Apply(st, first.Add(-150*time.Millisecond))
	require.False(t, streak, "a fractional tick before the remembered instant went red")
	require.Equal(t, 1, earlier.Streams["EDGES"].Widening, "a tick before the remembered instant advanced the streak: %+v", earlier.Streams["EDGES"])
	require.Equal(t, st.Streams["EDGES"].At, earlier.Streams["EDGES"].At, "a tick before the remembered instant moved the remembered instant")
	st = earlier

	// A strictly later fractional tick is a new one: it advances the streak
	// and gives the two-consecutive-widenings exit-1 condition.
	_, _, streak = widening(6, 3).Apply(st, first.Add(250*time.Millisecond))
	require.True(t, streak, "a later subsecond tick that widens again is the exit-1 condition")

	// State written at whole-second precision stays readable: the exact same
	// tick is still the same reading, and a later fraction is still a new one.
	whole := State{Streams: map[string]StreamState{"EDGES": {Now: 5, At: windowNow, Widening: 1}}}
	atWhole := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	_, _, streak = widening(5, 3).Apply(whole, atWhole)
	require.False(t, streak, "replaying the exact whole-second tick went red")
	_, nextWhole, streak := widening(6, 3).Apply(whole, atWhole.Add(250*time.Millisecond))
	require.True(t, streak, "a fraction after a whole-second remembered tick must still be a new tick")
	require.Equal(t, 2, nextWhole.Streams["EDGES"].Widening, "a new tick after whole-second state did not count: %+v", nextWhole.Streams["EDGES"])
}
