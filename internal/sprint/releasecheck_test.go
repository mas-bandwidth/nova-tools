package sprint

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRelease is the release check's snapshot as a test builds it: a clock and a log.
type fakeRelease struct {
	now      time.Time
	lines    []Line
	dealtMax time.Duration
}

func (f fakeRelease) Now() time.Time          { return f.now }
func (f fakeRelease) Log() []Line             { return f.lines }
func (f fakeRelease) DealtMax() time.Duration { return f.dealtMax }
func fleetMove(at time.Time, card, from, to string, set map[string]string) Line {
	return Line{Kind: LineMove, At: at, Table: Fleet, Card: card, Stream: "s1", From: from, To: to, Set: set}
}

// at is the clock t0 plus h hours.
func at(h float64) time.Time { return t0.Add(time.Duration(h * float64(time.Hour))) }

func relFacts(now time.Time, lines ...Line) fakeRelease {
	return fakeRelease{now: now, lines: lines, dealtMax: DealtMaxDefault}
}

func TestReleaseCheckFailsWhileAFriendWasStuckInTheLastFourHours(t *testing.T) {
	t.Parallel()
	amy := FriendRow("amy")

	t.Run("a friend's working card past its deadline fails, naming her and the time", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0, deadline 2h, now hour 3: late since hour 2, inside the last four hours
		f := relFacts(at(3), fleetMove(at(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(at(0))}))
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Equal(t, CheckNoStuckFriend, r.Name)
		assert.Contains(t, r.Evidence, "friend amy")
		assert.Contains(t, r.Evidence, "s1-1.w1")
		assert.Contains(t, r.Evidence, stamp(at(2)), "the moment she became stuck")
		assert.Contains(t, r.Evidence, "nova-sprint card s1-1", "what to look at")
		assert.Equal(t, "RELEASE CHECK no-stuck-friend fail "+r.Evidence, r.Line())
	})

	t.Run("a stuck spell that ended before the window is not a failure", func(t *testing.T) {
		t.Parallel()
		// late from hour 2 to hour 3 (finished), now hour 9: the window opens at hour 5
		f := relFacts(at(9),
			fleetMove(at(0), "s1-1.w1", "", amy+":working", nil),
			fleetMove(at(3), "s1-1.w1", amy+":working", amy+":done_ok", nil))
		r := NoStuckFriend(f)
		assert.True(t, r.OK, r.Evidence)
		assert.Equal(t, "RELEASE CHECK no-stuck-friend ok "+r.Evidence, r.Line())
	})

	t.Run("a spell that ended inside the window fails: stuck at any moment", func(t *testing.T) {
		t.Parallel()
		f := relFacts(at(6),
			fleetMove(at(0), "s1-1.w1", "", amy+":working", nil),
			fleetMove(at(3), "s1-1.w1", amy+":working", amy+":done_ok", nil))
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, stamp(at(2)), "the spell began at hour 2, inside the window opened at hour 2")
	})

	t.Run("a card finished inside its deadline is never stuck", func(t *testing.T) {
		t.Parallel()
		f := relFacts(at(3),
			fleetMove(at(0), "s1-1.w1", "", amy+":working", nil),
			fleetMove(at(1), "s1-1.w1", amy+":working", amy+":done_ok", nil))
		assert.True(t, NoStuckFriend(f).OK)
	})

	t.Run("her own longer deadline holds", func(t *testing.T) {
		t.Parallel()
		set := map[string]string{"friend_deadline": strconv.Itoa(5 * 3600)}
		f := relFacts(at(4), fleetMove(at(0), "s1-1.w1", "", amy+":working", set))
		assert.True(t, NoStuckFriend(f).OK, "late only from hour 5")
		f.now = at(6)
		assert.False(t, NoStuckFriend(f).OK)
	})

	t.Run("a card dealt and never taken past the dealt bound fails", func(t *testing.T) {
		t.Parallel()
		f := relFacts(at(7), fleetMove(at(0), "s1-2.w1", "", amy+":ready", nil))
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, "dealt, never taken")
		assert.Contains(t, r.Evidence, stamp(at(6)), "dealt at 0 and the dealt bound is 6h")
	})

	t.Run("a card taken back from her stops the clock", func(t *testing.T) {
		t.Parallel()
		f := relFacts(at(9),
			fleetMove(at(0), "s1-2.w1", "", amy+":ready", nil),
			fleetMove(at(1), "s1-2.w1", amy+":ready", "m1:ready", nil))
		assert.True(t, NoStuckFriend(f).OK, "it left her row")
	})

	t.Run("a machine's late card is not a friend's", func(t *testing.T) {
		t.Parallel()
		f := relFacts(at(9), fleetMove(at(0), "s1-1.w1", "", "m1:working", nil))
		assert.True(t, NoStuckFriend(f).OK)
	})

	t.Run("no log is no stuck friend", func(t *testing.T) {
		t.Parallel()
		assert.True(t, NoStuckFriend(relFacts(at(9))).OK)
	})
}

func TestTheReleaseReportSaysOKOrNotReadyByTheChecksRun(t *testing.T) {
	t.Parallel()
	good := relFacts(at(1))
	rep, err := RunReleaseChecks(good, nil)
	require.NoError(t, err)
	assert.Equal(t, "RELEASE OK checks=1", rep.Summary)
	assert.Equal(t, 0, rep.ExitCode())
	assert.Equal(t, 1, len(rep.Results))

	bad := relFacts(at(3), fleetMove(at(0), "s1-1.w1", "", FriendRow("amy")+":working", nil))
	rep, err = RunReleaseChecks(bad, []string{CheckNoStuckFriend})
	require.NoError(t, err)
	assert.Equal(t, "RELEASE NOT READY failed=1", rep.Summary)
	assert.Equal(t, 1, rep.ExitCode())
	assert.False(t, rep.Ready)

	_, err = RunReleaseChecks(good, []string{"no-such-check"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-check")
	assert.Contains(t, err.Error(), CheckNoStuckFriend, "the refusal names the checks there are")
}

func TestEveryReleaseCheckStatesItsBar(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, c := range ReleaseChecks {
		assert.NotEmpty(t, c.Name)
		assert.NotEmpty(t, c.Bar, c.Name)
		assert.False(t, seen[c.Name], "%s twice", c.Name)
		seen[c.Name] = true
	}
}

func TestReleaseStreamsFilterKeepsTheStreamsTheGlobNames(t *testing.T) {
	t.Parallel()
	a := fleetMove(at(0), "a-1.w1", "", FriendRow("amy")+":working", nil)
	a.Stream = "alpha"
	b := fleetMove(at(0), "b-1.w1", "", FriendRow("bob")+":working", nil)
	b.Stream = "beta"
	got, err := ReleaseStreamLines([]Line{a, b}, "al*")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a-1.w1", got[0].Card)
	all, err := ReleaseStreamLines([]Line{a, b}, "")
	require.NoError(t, err)
	assert.Len(t, all, 2)
	_, err = ReleaseStreamLines(nil, "[")
	require.Error(t, err)
}
