package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The remind verb on the twin store, the clock stepped by hand: no sleeps
// (docs/SPEC-SPRINT.md, "Timers").

// step moves the test's clock by d and beats the fleet, as a tick's wait does.
func (ta *testApp) step(d time.Duration) {
	ta.mu.Lock()
	ta.now = ta.now.Add(d)
	ta.mu.Unlock()
	ta.beat()
}

// timerNotes is every judgment of kind "timer" the store holds.
func (ta *testApp) timerNotes() []sprint.Note {
	all, _, err := ta.m.NotesSince(context.Background(), "", 100000)
	require.NoError(ta.t, err)
	var out []sprint.Note
	for _, n := range all {
		if n.Type == sprint.NTimer && n.Kind == sprint.Judgment {
			out = append(out, n)
		}
	}
	return out
}

func TestRemindSetsListsCancelsAndADryRunWritesNothing(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("start")

	first := ta.ok(`remind --in 30m --note window-closes`)
	out := first
	require.Contains(t, out, "REMIND OK id=", "the timer's id: %s", out)
	require.Contains(t, out, "for=coordinator", "--for defaults to the caller: %s", out)
	require.Contains(t, out, "due="+t0.Add(30*time.Minute).Format(time.RFC3339), "its due time: %s", out)
	require.Contains(t, out, "by=coordinator", "who set it: %s", out)

	out = ta.ok("remind --list")
	require.Contains(t, out, "REMINDER id=", "--list: %s", out)
	require.Contains(t, out, "for=coordinator", "--list: %s", out)
	require.Contains(t, out, `note=window-closes`, "--list: %s", out)

	// --dry-run says what it would write and writes nothing.
	out = ta.ok(`remind --in 5m --note second-timer --dry-run`)
	require.Contains(t, out, "REMIND DRY-RUN", "--dry-run: %s", out)
	require.Contains(t, out, "due="+t0.Add(5*time.Minute).Format(time.RFC3339), "--dry-run: %s", out)
	require.Contains(t, out, "nothing was written", "--dry-run: %s", out)
	require.NotContains(t, ta.ok("remind --list"), "second-timer", "--dry-run wrote a timer")

	// --cancel takes the open timer off; the tick then raises nothing.
	id := strings.Fields(strings.Split(first, "id=")[1])[0]
	require.Contains(t, ta.ok("remind --cancel "+id), "REMIND OK id="+id, "cancel")
	require.Contains(t, ta.ok("remind --list"), "REMIND none", "the record after the cancel")
	ta.step(30 * time.Minute)
	ta.ok("tick")
	assert.Empty(t, ta.timerNotes(), "a cancelled timer fired")

	// --json is the same value.
	ta.ok(`remind --at 2030-01-02T04:04:05Z --note third`)
	var v remindView
	ta.json(`remind --at 2030-01-02T05:04:05Z --note fourth`, &v)
	assert.Equal(t, "coordinator", v.For, "--json: %+v", v)
	assert.Equal(t, "fourth", v.Note, "--json: %+v", v)
	assert.Equal(t, time.Date(2030, 1, 2, 5, 4, 5, 0, time.UTC), v.Due, "--json: %+v", v)
	assert.NotEmpty(t, v.ID, "--json: %+v", v)
}

func TestRemindForAFriendIsRaisedToHer(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("start")
	ta.ok(`remind --in 30m --note card-dealt --for friend-a`)
	require.Contains(t, ta.ok("remind --list"), "for=friend-a", "--list")

	// Before its due time the tick raises nothing.
	ta.step(29 * time.Minute)
	ta.ok("tick")
	require.Empty(t, ta.timerNotes(), "raised before its due time")

	// At its due time it is raised once, addressed to the friend: the seat
	// push writes a group addressed to someone to that actor's inbox
	// (cmd/nova-sprint/seat.go, pushTarget.dirOf), the route her judgments
	// already take.
	ta.step(time.Minute)
	ta.ok("tick")
	notes := ta.timerNotes()
	require.Len(t, notes, 1, "one judgment at its due time")
	assert.Equal(t, "friend-a", notes[0].To, "addressed to its actor")
	assert.Equal(t, sprint.NTimer, notes[0].Type, "of kind timer")
	assert.Contains(t, notes[0].What, "card-dealt", "its note")

	// The inbox shows it as a judgment addressed to her, and a later tick
	// writes no second one.
	require.Contains(t, ta.ok("inbox"), "friend-a", "the inbox")
	ta.step(time.Hour)
	ta.ok("tick")
	assert.Len(t, ta.timerNotes(), 1, "a second judgment")
}

func TestRemindRefusals(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	for _, tc := range []struct {
		line string
		want string
	}{
		{"remind --note x", "one of --in <duration>"},
		{"remind --in 5m --at 2030-01-02T04:04:05Z --note x", "not both"},
		{"remind --in 5m", "--note"},
		{"remind --in 5m --note x --list", "exactly one form"},
		{"remind --list --cancel 1", "exactly one form"},
		{"remind --at nonsense --note x", "is none of RFC3339"},
		{"remind --in 5m --note x --for ../etc", "lower-case letters"},
		{"remind --cancel none", "no open timer is none"},
		{"remind x", "takes no positional argument"},
	} {
		t.Run(tc.line, func(t *testing.T) {
			exit, _, errOut := ta.do(tc.line)
			assert.Equal(t, 2, exit, "%s: %s", tc.line, errOut)
			assert.Contains(t, errOut, "remind REFUSED", tc.line)
			assert.Contains(t, errOut, tc.want, tc.line)
		})
	}
	assert.NotContains(t, ta.ok("remind --list"), "REMINDER", "a refusal wrote a timer")
}

// TestRemindDryRunSaysTheWriteAndAtIsToday pins what --dry-run says: the row
// the write would record (by is the caller, whoever the timer wakes), and for
// a cancel the open timer it would take off, refusing an id the write refuses;
// and that --at a time of day is that time today.
func TestRemindDryRunSaysTheWriteAndAtIsToday(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("start")

	// --for friend-a --dry-run: woken is the friend, by is the caller.
	out := ta.ok(`remind --in 30m --note for-her --for friend-a --dry-run`)
	require.Contains(t, out, "REMIND DRY-RUN", "%s", out)
	assert.Contains(t, out, "for=friend-a", "%s", out)
	assert.Contains(t, out, "by=coordinator", "the write records the caller as by: %s", out)

	// --at a time of day is today: t0 is 03:04:05 on 2030-01-02 (UTC).
	var v remindView
	ta.json(`remind --at 04:00 --note today`, &v)
	assert.Equal(t, time.Date(2030, 1, 2, 4, 0, 0, 0, time.UTC), v.Due, "--at 04:00 is today: %+v", v)
	v = remindView{}
	ta.json(`remind --at 03:30:15 --note today-seconds --dry-run`, &v)
	assert.Empty(t, v.ID, "a dry run names no id")
	assert.Equal(t, time.Date(2030, 1, 2, 3, 30, 15, 0, time.UTC), v.Due, "--at 03:30:15 is today: %+v", v)
	for _, line := range []string{`remind --at 03:00 --note gone`, `remind --at 03:00 --note gone --dry-run`} {
		exit, _, errOut := ta.do(line)
		assert.Equal(t, 2, exit, "%s: %s", line, errOut)
		assert.Contains(t, errOut, "not after now", "a time of day already past today: %s", line)
	}

	// --cancel --dry-run prints the open timer's row and writes nothing.
	v = remindView{}
	ta.json(`remind --at 04:00 --note to-cancel`, &v)
	out = ta.ok("remind --cancel " + v.ID + " --dry-run")
	require.Contains(t, out, "REMIND DRY-RUN id="+v.ID, "%s", out)
	assert.Contains(t, out, "for=coordinator", "the timer's row: %s", out)
	assert.Contains(t, out, "due=2030-01-02T04:00:00Z", "its due time, not now: %s", out)
	assert.Contains(t, out, "note=to-cancel", "its note: %s", out)
	assert.Contains(t, ta.ok("remind --list"), "to-cancel", "--cancel --dry-run took it off")
	exit, _, errOut := ta.do("remind --cancel nosuch --dry-run")
	assert.Equal(t, 2, exit, "%s", errOut)
	assert.Contains(t, errOut, "no open timer is nosuch", "an unknown id is refused as the write refuses it")

	// The listing is bounded by --max with the MORE line.
	out = ta.ok("remind --list --max 1")
	assert.Equal(t, 1, strings.Count(out, "REMINDER "), "%s", out)
	assert.Contains(t, out, "MORE kind=reminder shown=1 total=2", "%s", out)

	// A missing --note is refused in a timer's own words.
	_, _, errOut = ta.do(`remind --in 5m`)
	assert.Contains(t, errOut, "the timer's note is empty", "%s", errOut)
}
