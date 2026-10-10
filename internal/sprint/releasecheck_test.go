package sprint

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRelease is the release check's snapshot as a test builds it: a clock, a log
// and the acceptance sentinel's facts.
type fakeRelease struct {
	now         time.Time
	lines       []Line
	dealtMax    time.Duration
	accept      Acceptance
	mergeWindow time.Duration
	mergeP90    time.Duration
	product     Product
}

func (f fakeRelease) Now() time.Time          { return f.now }
func (f fakeRelease) Log() []Line             { return f.lines }
func (f fakeRelease) DealtMax() time.Duration { return f.dealtMax }
func (f fakeRelease) Acceptance() Acceptance  { return f.accept }

// MergeWindow and MergeP90 fall back to the defaults so a twin built for
// another check still answers the merge queue's check.
func (f fakeRelease) MergeWindow() time.Duration {
	if f.mergeWindow == 0 {
		return MergeQueueWindowDefault
	}
	return f.mergeWindow
}

func (f fakeRelease) MergeP90() time.Duration {
	if f.mergeP90 == 0 {
		return MergeQueueP90Default
	}
	return f.mergeP90
}

func (f fakeRelease) Product() Product { return f.product }

// relFacts is the release check's facts for testing.
func relFacts(now time.Time, lines ...Line) fakeRelease {
	return fakeRelease{now: now, lines: lines, dealtMax: DealtMaxDefault, product: Product{Name: "nova-sprint", Streams: "sprint-v1-*"}}
}

// relFactsProduct returns facts for a specific product.
func relFactsProduct(now time.Time, p Product, lines ...Line) fakeRelease {
	return fakeRelease{now: now, lines: lines, dealtMax: DealtMaxDefault, product: p}
}

func fleetMove(at time.Time, card, from, to string, set map[string]string) Line {
	return Line{Kind: LineMove, At: at, Table: Fleet, Card: card, Stream: "s1", From: from, To: to, Set: set}
}

// hr is the clock t0 plus h hours.
func hr(h float64) time.Time { return t0.Add(time.Duration(h * float64(time.Hour))) }

func TestReleaseCheckFailsWhileAFriendWasStuckInTheLastFourHours(t *testing.T) {
	t.Parallel()
	amy := FriendRow("amy")

	t.Run("a friend's working card past its deadline fails, naming her and the time", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0, deadline 2h, now hour 3: late since hour 2, inside the last four hours
		f := relFacts(hr(3), fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}))
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Equal(t, CheckNoStuckFriend, r.Name)
		assert.Contains(t, r.Evidence, "friend amy")
		assert.Contains(t, r.Evidence, "s1-1.w1")
		assert.Contains(t, r.Evidence, stamp(hr(2)), "the moment she became stuck")
		assert.Contains(t, r.Evidence, "nova-sprint card s1-1", "what to look at")
		assert.Equal(t, "RELEASE CHECK no-stuck-friend fail "+r.Evidence, r.Line())
	})

	t.Run("a stuck spell that ended before the window is not a failure", func(t *testing.T) {
		t.Parallel()
		// late from hour 2 to hour 3 (finished), now hour 9: the window opens at hour 5
		f := relFacts(hr(9),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", nil),
			fleetMove(hr(3), "s1-1.w1", amy+":working", amy+":done_ok", nil))
		r := NoStuckFriend(f)
		assert.True(t, r.OK, r.Evidence)
		assert.Equal(t, "RELEASE CHECK no-stuck-friend ok "+r.Evidence, r.Line())
	})

	t.Run("a spell that ended inside the window fails: stuck at any moment", func(t *testing.T) {
		t.Parallel()
		f := relFacts(hr(6),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", nil),
			fleetMove(hr(3), "s1-1.w1", amy+":working", amy+":done_ok", nil))
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, stamp(hr(2)), "the spell began at hour 2, inside the window opened at hour 2")
	})

	t.Run("a card finished inside its deadline is never stuck", func(t *testing.T) {
		t.Parallel()
		f := relFacts(hr(3),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", nil),
			fleetMove(hr(1), "s1-1.w1", amy+":working", amy+":done_ok", nil))
		assert.True(t, NoStuckFriend(f).OK)
	})

	t.Run("her own longer deadline holds", func(t *testing.T) {
		t.Parallel()
		set := map[string]string{"friend_deadline": strconv.Itoa(5 * 3600)}
		f := relFacts(hr(4), fleetMove(hr(0), "s1-1.w1", "", amy+":working", set))
		assert.True(t, NoStuckFriend(f).OK, "late only from hour 5")
		f.now = hr(6)
		assert.False(t, NoStuckFriend(f).OK)
	})

	t.Run("a card dealt and never taken past the dealt bound fails", func(t *testing.T) {
		t.Parallel()
		f := relFacts(hr(7), fleetMove(hr(0), "s1-2.w1", "", amy+":ready", nil))
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, "dealt, never taken")
		assert.Contains(t, r.Evidence, stamp(hr(6)), "dealt at 0 and the dealt bound is 6h")
	})

	t.Run("a card taken back from her stops the clock", func(t *testing.T) {
		t.Parallel()
		f := relFacts(hr(9),
			fleetMove(hr(0), "s1-2.w1", "", amy+":ready", nil),
			fleetMove(hr(1), "s1-2.w1", amy+":ready", "m1:ready", nil))
		assert.True(t, NoStuckFriend(f).OK, "it left her row")
	})

	t.Run("a card taken back inside its deadline resets to untaken bound and does not fail", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0, withdrawn at hour 1 (untaken bound is 6h from hour 1 = hour 7), now hour 3
		f := relFacts(hr(3),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}),
			fleetMove(hr(1), "s1-1.w1", amy+":working", amy+":withdrawn", map[string]string{FieldTakenBack: "taken back", "untaken_since": stamp(hr(1))}),
		)
		r := NoStuckFriend(f)
		assert.True(t, r.OK, r.Evidence)
		assert.Equal(t, "RELEASE CHECK no-stuck-friend ok "+r.Evidence, r.Line())

		// but at hour 8, it is late past the untaken bound (hour 7)
		f.now = hr(8)
		r8 := NoStuckFriend(f)
		assert.False(t, r8.OK)
		assert.Contains(t, r8.Evidence, "dealt, never taken")
		assert.Contains(t, r8.Evidence, stamp(hr(7)))
	})

	t.Run("a card taken back after its deadline was stuck and fails inside the window", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0, deadline 2h, withdrawn at hour 3 (stuck from hour 2 to hour 3), now hour 4
		f := relFacts(hr(4),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}),
			fleetMove(hr(3), "s1-1.w1", amy+":working", amy+":withdrawn", map[string]string{FieldTakenBack: "taken back", "untaken_since": stamp(hr(3))}),
		)
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, "friend amy")
		assert.Contains(t, r.Evidence, stamp(hr(2)))
	})

	t.Run("a card whose friend deadline is increased historically does not fail", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0 with default 2h deadline; at hour 1 friend_deadline updated to 5h; checked at hour 4
		f := relFacts(hr(4),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}),
			fleetMove(hr(1), "s1-1.w1", amy+":working", amy+":working", map[string]string{FieldFriendDeadline: strconv.Itoa(5 * 3600)}),
		)
		r := NoStuckFriend(f)
		assert.True(t, r.OK, r.Evidence)

		// checked at hour 6: late from hour 5
		f.now = hr(6)
		r6 := NoStuckFriend(f)
		assert.False(t, r6.OK)
		assert.Contains(t, r6.Evidence, stamp(hr(5)))
	})

	t.Run("a card stuck before a clear inside the window fails", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0, deadline 2h; clear runs at hour 2.5; checked at hour 3
		clearLine := Line{Kind: LineMove, At: hr(2.5), Verb: "clear"}
		f := relFacts(hr(3),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}),
			clearLine,
		)
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, "friend amy")
		assert.Contains(t, r.Evidence, stamp(hr(2)))
	})

	t.Run("a card not yet stuck before a clear does not fail", func(t *testing.T) {
		t.Parallel()
		// taken at hour 2.2, deadline 2h; clear runs at hour 2.5; checked at hour 3
		clearLine := Line{Kind: LineMove, At: hr(2.5), Verb: "clear"}
		f := relFacts(hr(3),
			fleetMove(hr(2.2), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(2.2))}),
			clearLine,
		)
		r := NoStuckFriend(f)
		assert.True(t, r.OK, r.Evidence)
	})

	t.Run("a machine's late card is not a friend's", func(t *testing.T) {
		t.Parallel()
		f := relFacts(hr(9), fleetMove(hr(0), "s1-1.w1", "", "m1:working", nil))
		assert.True(t, NoStuckFriend(f).OK)
	})

	t.Run("no log is no stuck friend", func(t *testing.T) {
		t.Parallel()
		assert.True(t, NoStuckFriend(relFacts(hr(9))).OK)
	})

	t.Run("consecutive late periods for a card merge across intervening moves of other cards", func(t *testing.T) {
		t.Parallel()
		bob := FriendRow("bob")
		f := relFacts(hr(4),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}),
			fleetMove(hr(1), "s1-2.w1", "", bob+":working", map[string]string{"first_taken": stamp(hr(1))}),
			fleetMove(hr(2.5), "s1-1.w1", amy+":working", amy+":working", map[string]string{"first_taken": stamp(hr(0)), "note": "update"}),
			fleetMove(hr(3.5), "s1-2.w1", bob+":working", bob+":working", map[string]string{"first_taken": stamp(hr(1)), "note": "update"}),
		)
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		spans, _ := friendLateSpans(f.Log(), f.Now(), f.DealtMax())
		assert.Len(t, spans, 2, "one merged span per card, not fragmented")
	})
}

func TestTheReleaseReportSaysOKOrNotReadyByTheChecksRun(t *testing.T) {
	t.Parallel()
	good := relFacts(hr(1))
	rep, err := RunReleaseChecks(good, nil)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("RELEASE OK checks=%d", len(ReleaseChecks)), rep.Summary)
	assert.Equal(t, 0, rep.ExitCode())
	assert.Equal(t, len(ReleaseChecks), len(rep.Results))

	bad := relFacts(hr(3), fleetMove(hr(0), "s1-1.w1", "", FriendRow("amy")+":working", nil))
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
	a := fleetMove(hr(0), "a-1.w1", "", FriendRow("amy")+":working", nil)
	a.Stream = "alpha"
	b := fleetMove(hr(0), "b-1.w1", "", FriendRow("bob")+":working", nil)
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

// TestReleaseCheckRunsTheAcceptanceSentinelsSixChecks is the card's test
// (docs/SPEC-RELEASE.md, "the acceptance sentinel's six checks", source: the
// coordinator's answer, Rowan 2026-10-06 12:50 ET): each of the six is green
// on a twin of the facts that keeps it and red on a twin that breaks exactly
// it, naming what to look at; no socket, no clock of its own.
func TestReleaseCheckRunsTheAcceptanceSentinelsSixChecks(t *testing.T) {
	t.Parallel()

	green := func() Acceptance {
		return Acceptance{
			Streams: []string{"s1"},
			Cards: []AcceptCard{
				{ID: "s1-1.w1", Stream: "s1", Col: Landed, Tier: "pro", Head: "aaa1"},
				{ID: "s1-2.w1", Stream: "s1", Col: Landed, Tier: "flash", Head: "bbb2"},
			},
			Reads: []AcceptRead{
				{Primary: "s1-1.w1", Stream: "s1", Head: "aaa1", Reader: "r1", Verdict: "ok"},
				{Primary: "s1-1.w1", Stream: "s1", Head: "aaa1", Reader: "r2", Verdict: "ok"},
				{Primary: "s1-1.w1", Stream: "s1", Head: "aaa1", Reader: "r3", Verdict: "broken"},
				{Primary: "s1-2.w1", Stream: "s1", Head: "bbb2", Reader: "r1", Verdict: "ok"},
			},
			Gates: []AcceptGate{
				{Base: "base1", Class: "unit", OK: true},
				{Base: "base1", Class: "functional", OK: true},
				{Base: "base1", Class: "docs", OK: true},
				{Base: "base1", Class: "ci", OK: true},
			},
			Prose: []AcceptProse{
				{Path: "docs/SPEC-RELEASE.md", Check: "links", OK: true},
				{Path: "cmd/nova-sprint", Check: "nocode", OK: true},
			},
			Promote: AcceptPromotion{Sha: "abc1234", Queued: true, AfterLastLanding: true},
		}
	}

	six := []string{
		CheckCardsSettled, CheckBaseGateGreen, CheckTwoOKReads,
		CheckProseTrue, CheckLandingsPromoted, CheckNoOpenJudgment,
	}

	// every check is green on the facts that keep it.
	t.Run("all six are green on a stream that keeps every bar", func(t *testing.T) {
		t.Parallel()
		f := fakeRelease{now: hr(1), dealtMax: DealtMaxDefault, accept: green()}
		rep, err := RunReleaseChecks(f, six)
		require.NoError(t, err)
		require.Len(t, rep.Results, len(six))
		assert.True(t, rep.Ready)
		assert.Equal(t, fmt.Sprintf("RELEASE OK checks=%d", len(six)), rep.Summary)
		for _, r := range rep.Results {
			assert.True(t, r.OK, r.Line())
			assert.NotEmpty(t, r.Evidence, r.Name)
			assert.Equal(t, "RELEASE CHECK "+r.Name+" ok ", r.Line()[:len("RELEASE CHECK "+r.Name+" ok ")])
		}
	})

	// each check's red twin breaks exactly that check, and its evidence names
	// what to look at.
	twins := []struct {
		name    string
		want    string
		breakIt func(*Acceptance)
	}{
		{CheckCardsSettled, "s1-1.w1", func(a *Acceptance) { a.Cards[0].Col = Working }},
		{CheckCardsSettled, "no reason", func(a *Acceptance) {
			a.Cards = []AcceptCard{{ID: "s1-3.w1", Stream: "s1", Col: Landed, Tier: "flash", Head: "ccc3"}}
			a.Dropped = []AcceptDrop{{ID: "s1-4.w1", Stream: "s1"}}
		}},
		{CheckBaseGateGreen, "functional", func(a *Acceptance) { a.Gates[1].OK = false }},
		{CheckBaseGateGreen, "no ci gate result", func(a *Acceptance) { a.Gates = a.Gates[:3] }},
		{CheckTwoOKReads, "s1-1.w1", func(a *Acceptance) { a.Reads = a.Reads[:1] }},
		{CheckTwoOKReads, "s1-2.w1", func(a *Acceptance) { a.Reads = a.Reads[:2] }},
		{CheckProseTrue, "nocode", func(a *Acceptance) { a.Prose[1].OK = false }},
		{CheckProseTrue, "no nova-check links result", func(a *Acceptance) { a.Prose = a.Prose[1:] }},
		{CheckLandingsPromoted, "not in dev", func(a *Acceptance) { a.Promote = AcceptPromotion{} }},
		{CheckLandingsPromoted, "older than", func(a *Acceptance) { a.Promote.AfterLastLanding = false }},
		{CheckNoOpenJudgment, "j1", func(a *Acceptance) {
			a.Judgments = []AcceptJudgment{{ID: "j1", Stream: "s1", Kind: "stale"}}
		}},
	}
	for _, tc := range twins {
		t.Run(tc.name+" is red on a broken twin naming "+tc.want, func(t *testing.T) {
			t.Parallel()
			a := green()
			tc.breakIt(&a)
			f := fakeRelease{now: hr(1), dealtMax: DealtMaxDefault, accept: a}
			rep, err := RunReleaseChecks(f, []string{tc.name})
			require.NoError(t, err)
			require.Len(t, rep.Results, 1)
			r := rep.Results[0]
			assert.False(t, r.OK, r.Line())
			assert.Contains(t, r.Evidence, tc.want, r.Line())
			assert.Equal(t, "RELEASE CHECK "+tc.name+" fail "+r.Evidence, r.Line())
		})
	}

	// no stream named is no acceptance to check: every check passes and says so.
	t.Run("no stream named leaves every acceptance check vacuous", func(t *testing.T) {
		t.Parallel()
		f := fakeRelease{now: hr(1), dealtMax: DealtMaxDefault}
		rep, err := RunReleaseChecks(f, six)
		require.NoError(t, err)
		assert.True(t, rep.Ready)
		for _, r := range rep.Results {
			assert.True(t, r.OK, r.Line())
			assert.Contains(t, r.Evidence, "no stream is being accepted", r.Line())
		}
	})
}

// TestReleaseCheckForNovaToolsScopesEveryCheckToItsStreamsAndHead verifies that
// --product nova-tools scopes checks to tools-v1-2-0-* streams.
func TestReleaseCheckForNovaToolsScopesEveryCheckToItsStreamsAndHead(t *testing.T) {
	t.Parallel()

	novaToolsProduct := Product{Name: "nova-tools", Streams: "tools-v1-2-0-*"}

	t.Run("nova-tools check passes when tools stream is green", func(t *testing.T) {
		t.Parallel()
		now := hr(2)
		toolsLine := fleetMove(hr(0), "card-1", "", FriendRow("amy")+":done_ok", nil)
		toolsLine.Stream = "tools-v1-2-0-1"
		f := relFactsProduct(now, novaToolsProduct, toolsLine)
		rep, err := RunReleaseChecks(f, []string{CheckNoStuckFriend})
		require.NoError(t, err)
		require.Len(t, rep.Results, 1)
		assert.True(t, rep.Results[0].OK, rep.Results[0].Line())
	})

	t.Run("nova-tools check fails when tools stream breaks", func(t *testing.T) {
		t.Parallel()
		now := hr(7)
		toolsLine := fleetMove(hr(0), "card-1", "", FriendRow("amy")+":working", map[string]string{"first_taken": stamp(hr(0))})
		toolsLine.Stream = "tools-v1-2-0-1"
		f := relFactsProduct(now, novaToolsProduct, toolsLine)
		rep, err := RunReleaseChecks(f, []string{CheckNoStuckFriend})
		require.NoError(t, err)
		require.Len(t, rep.Results, 1)
		r := rep.Results[0]
		assert.False(t, r.OK, r.Line())
		assert.Equal(t, "RELEASE CHECK no-stuck-friend fail "+r.Evidence, r.Line())
	})

	t.Run("sprint-v1 streams ignored when checking nova-tools product", func(t *testing.T) {
		t.Parallel()
		now := hr(7)
		// Sprint stream broken, but should be ignored for nova-tools
		toolsLine := fleetMove(hr(1), "card-2", "", FriendRow("amy")+":done_ok", nil)
		toolsLine.Stream = "tools-v1-2-0-1"
		f := relFactsProduct(now, novaToolsProduct, toolsLine)
		rep, err := RunReleaseChecks(f, []string{CheckNoStuckFriend})
		require.NoError(t, err)
		require.Len(t, rep.Results, 1)
		assert.True(t, rep.Results[0].OK, rep.Results[0].Line())
	})
}

// TestProductDefaultAndFor tests the product lookup functions.
func TestProductDefaultAndFor(t *testing.T) {
	t.Parallel()

	t.Run("DefaultProducts returns both products", func(t *testing.T) {
		t.Parallel()
		prods := DefaultProducts()
		assert.Len(t, prods, 2)
		assert.Equal(t, "nova-sprint", prods[0].Name)
		assert.Equal(t, "nova-tools", prods[1].Name)
	})

	t.Run("ProductFor finds nova-sprint", func(t *testing.T) {
		t.Parallel()
		p, err := ProductFor("nova-sprint")
		require.NoError(t, err)
		assert.Equal(t, "nova-sprint", p.Name)
		assert.Equal(t, "sprint-v1-*", p.Streams)
	})

	t.Run("ProductFor finds nova-tools", func(t *testing.T) {
		t.Parallel()
		p, err := ProductFor("nova-tools")
		require.NoError(t, err)
		assert.Equal(t, "nova-tools", p.Name)
		assert.Equal(t, "tools-v1-2-0-*", p.Streams)
	})

	t.Run("ProductFor returns error for unknown product", func(t *testing.T) {
		t.Parallel()
		p, err := ProductFor("unknown")
		assert.Error(t, err)
		assert.Equal(t, Product{}, p)
	})
}
