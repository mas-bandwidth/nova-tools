package friend

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The table of evidence to status (docs/SPEC-FRIEND.md, "A friend's status,
// from evidence"): each rule in its order, and the evidence a person reads.
func TestStatusIsUpOnlyWithASessionAnswerInsideTheBound(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 10, 5, 17, 0, 0, 0, time.UTC) // Mon 1:00 PM in New York
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	fresh := now.Add(-40 * time.Second)
	old := now.Add(-12 * time.Minute)
	live := Evidence{Harness: HarnessRunning, LastAnswer: fresh, LastResult: now.Add(-3 * time.Minute), LastExit: 0}

	with := func(f func(*Evidence)) Evidence { e := live; f(&e); return e }
	cases := []struct {
		name   string
		e      Evidence
		status string
		reason string
	}{
		{"every piece of evidence good is up", live, "up", "session answer 40s"},
		{"an unknown harness does not stop an answering session", with(func(e *Evidence) { e.Harness = HarnessUnknown }), "up", "session answer 40s"},
		{"a harness not seen does not stop an answering session (dsh headless)", with(func(e *Evidence) { e.Harness = HarnessNotSeen }), "up", "session answer 40s"},
		{"a harness not seen decides nothing beside a limit", with(func(e *Evidence) { e.Harness = HarnessNotSeen; e.LimitUntil = reset }), "down", "limit until Mon 1:00 PM"},
		{"a harness seen running is no answer", with(func(e *Evidence) { e.LastAnswer = time.Time{} }), "down", "no session answer ever"},
		{"at a limit is down until the reset", with(func(e *Evidence) { e.LimitUntil = reset }), "down", "limit until Mon 1:00 PM"},
		{"a limit says its reason", with(func(e *Evidence) { e.LimitUntil = reset; e.Limit = "weekly" }), "down", "weekly limit until Mon 1:00 PM"},
		{"a limit past its reset is no limit", with(func(e *Evidence) { e.LimitUntil = now.Add(-time.Minute) }), "up", "session answer 40s"},
		{"a limit outranks a stale answer", with(func(e *Evidence) { e.LimitUntil = reset; e.LastAnswer = old }), "down", "limit until Mon 1:00 PM"},
		{"no session answer within the bound is down", with(func(e *Evidence) { e.LastAnswer = old }), "down", "no session answer 12m"},
		{"a session that never answered is down", with(func(e *Evidence) { e.LastAnswer = time.Time{} }), "down", "no session answer ever"},
		{"the bound itself is down", with(func(e *Evidence) { e.LastAnswer = now.Add(-AnswerBound) }), "down", "no session answer 6m"},
		{"the beat alone never makes up", Evidence{Harness: HarnessRunning, DaemonUp: true}, "down", "no session answer ever"},
		{"a stale answer outranks a blocked bus", with(func(e *Evidence) { e.LastAnswer = old; e.BusBlocked = "the store: refused" }), "down", "no session answer 12m"},
		{"a bus that cannot deliver is down", with(func(e *Evidence) { e.BusBlocked = "session broken"; e.Undelivered = 4 }), "down", "bus cannot deliver: session broken (4 undelivered)"},
		{"undelivered messages alone are work waiting, not down", with(func(e *Evidence) { e.Undelivered = 3 }), "up", "session answer 40s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			v := FriendStatus(c.e, now, AnswerBound, ny)
			assert.Equal(t, c.status, v.Status)
			assert.Equal(t, c.reason, v.Reason)
		})
	}

	// The evidence is shown beside the status, every piece of it.
	v := FriendStatus(with(func(e *Evidence) { e.Undelivered = 2; e.LimitUntil = reset; e.LastExit = 1 }), now, AnswerBound, ny)
	assert.Equal(t, []string{"harness running", "session answer 40s", "limit until Mon 1:00 PM", "2 undelivered", "last result 3m exit=1"}, v.Evidence)
	v = FriendStatus(Evidence{}, now, AnswerBound, ny)
	assert.Equal(t, []string{"harness unknown", "no session answer ever", "no limit", "0 undelivered", "no result yet"}, v.Evidence)
}

func TestLastResultIsTheNewestTurnLineOfTheLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	at, exit, err := LastResult(dir)
	require.NoError(t, err)
	assert.True(t, at.IsZero())
	assert.Equal(t, 0, exit)
	log := "2026-10-04T14:00:00Z subject=a messages=1 took=2s exit=0 acked=true\n" +
		"2026-10-04T14:05:00Z subject=b messages=2 took=9s exit=3\n" +
		"2026-10-04T14:06:00Z mode: batch, from one-shot (the friend row)\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, LogFile), []byte(log), 0o644))
	at, exit, err = LastResult(dir)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 4, 14, 5, 0, 0, time.UTC), at)
	assert.Equal(t, 3, exit)
}

func TestLimitFileIsReadWhenThere(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l, found, err := ReadLimitFile(dir)
	require.NoError(t, err)
	assert.False(t, found)
	want := Limit{Reason: "weekly", Until: time.Date(2026, 10, 5, 17, 0, 0, 0, time.UTC)}
	require.NoError(t, write(filepath.Join(dir, LimitFile), want))
	l, found, err = ReadLimitFile(dir)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, want.Reason, l.Reason)
	assert.True(t, want.Until.Equal(l.Until))
}
