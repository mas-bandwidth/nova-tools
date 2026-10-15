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
// Whether she is there is the nova-hook record's alone.
func TestStatusIsUpOnlyWhileTheHookRecordProvesHerThere(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 10, 5, 17, 0, 0, 0, time.UTC) // Mon 1:00 PM in New York
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	proven := HookRecord{State: HookProven, Since: now.Add(-time.Hour), LastAnswer: now.Add(-40 * time.Second)}
	live := Evidence{Hook: proven, LastResult: now.Add(-3 * time.Minute), LastExit: 0}
	upWords := "hook proven since 1h ago, answer 40s ago"

	with := func(f func(*Evidence)) Evidence { e := live; f(&e); return e }
	cases := []struct {
		name   string
		e      Evidence
		status string
		reason string
	}{
		{"every piece of evidence good is up", live, "up", upWords},
		{"at a limit is down until the reset", with(func(e *Evidence) { e.LimitUntil = reset }), "down", "limit until Mon 1:00 PM"},
		{"a limit says its reason", with(func(e *Evidence) { e.LimitUntil = reset; e.Limit = "weekly" }), "down", "weekly limit until Mon 1:00 PM"},
		{"a limit past its reset is no limit", with(func(e *Evidence) { e.LimitUntil = now.Add(-time.Minute) }), "up", upWords},
		{"a limit outranks an unhooked record", with(func(e *Evidence) { e.LimitUntil = reset; e.Hook.State = "unhooked" }), "down", "limit until Mon 1:00 PM"},
		{"unhooked is down with the record's reason", with(func(e *Evidence) {
			e.Hook = HookRecord{State: "unhooked", Since: now.Add(-12 * time.Minute), LastAnswer: now.Add(-14 * time.Minute), Misses: 2, Down: "missed 2 challenges"}
		}), "down", "hook unhooked since 12m ago, answer 14m ago, 2 missed: missed 2 challenges"},
		{"proven but read down is down", with(func(e *Evidence) { e.Hook.Down = "no heartbeat server" }), "down", upWords + ": no heartbeat server"},
		{"no record is down", with(func(e *Evidence) { e.Hook = HookRecord{State: "none"} }), "down", "hook none, never answered"},
		{"a record that cannot be read is down", with(func(e *Evidence) { e.Hook = HookRecord{ReadError: "connection refused"} }), "down", "hook record not read: connection refused"},
		{"the daemon alone never makes up", Evidence{DaemonUp: true}, "down", "hook none, never answered"},
		{"a bus that cannot deliver is down", with(func(e *Evidence) { e.BusBlocked = "the store: refused"; e.Undelivered = 4 }), "down", "bus cannot deliver: the store: refused (4 undelivered)"},
		{"undelivered messages alone are work waiting, not down", with(func(e *Evidence) { e.Undelivered = 3 }), "up", upWords},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			v := FriendStatus(c.e, now, ny)
			assert.Equal(t, c.status, v.Status)
			assert.Equal(t, c.reason, v.Reason)
		})
	}

	// The evidence is shown beside the status, every piece of it.
	v := FriendStatus(with(func(e *Evidence) { e.Undelivered = 2; e.LimitUntil = reset; e.LastExit = 1 }), now, ny)
	assert.Equal(t, []string{upWords, "limit until Mon 1:00 PM", "2 undelivered", "last result 3m exit=1"}, v.Evidence)
	v = FriendStatus(Evidence{}, now, ny)
	assert.Equal(t, []string{"hook none, never answered", "no limit", "0 undelivered", "no result yet"}, v.Evidence)
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
