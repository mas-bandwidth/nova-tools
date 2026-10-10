package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// racingAsk is the in-memory store with other writers racing the tick's ask,
// on the harness's clock. Before each write of the ask (its Acquire), cost is
// the time the write's read-to-apply window took, and lose says whether
// another writer committed inside it: when it does, a real commit of another
// writer (a happened note) moves the fence first, and the ask's write finds it
// moved. Every other step writes as the store does.
type racingAsk struct {
	*Mem
	h      *harness
	lose   func(primaries []string) bool
	cost   func(primaries []string) time.Duration
	writes *int // the ask's writes
}

func (r racingAsk) AtEpoch(epoch uint64, old bool) Backend {
	return racingAsk{Mem: r.Mem.AtEpoch(epoch, old).(*Mem), h: r.h, lose: r.lose, cost: r.cost, writes: r.writes}
}

func (r racingAsk) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if op.Verb != "tick ask" {
		return r.Mem.Acquire(ctx, gen, op)
	}
	ps := opPrimaries(op)
	*r.writes++
	if r.cost != nil {
		r.h.mu.Lock()
		r.h.now = r.h.now.Add(r.cost(ps))
		r.h.mu.Unlock()
	}
	if r.lose != nil && r.lose(ps) {
		other := &Store{B: r.Mem, Names: r.h.st.Names, Actor: "coordinator", Now: r.h.st.Now, NewID: r.h.st.NewID, Sleep: r.h.st.Sleep}
		if _, err := other.Run(ctx, Step{Verb: "poke", Plan: func(sn *sprint.Snapshot) sprint.Plan {
			return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "poked", Who: "coordinator", At: sn.Now, What: "another writer"}}}
		}}); err != nil {
			return false, err
		}
	}
	return r.Mem.Acquire(ctx, gen, op)
}

// opPrimaries is the primaries an operation's lines name, each once.
func opPrimaries(op OpRecord) []string {
	var out []string
	for _, l := range op.Log {
		if l.Primary != "" && !strings.Contains(strings.Join(out, " ")+" ", l.Primary+" ") {
			out = append(out, l.Primary)
		}
	}
	return out
}

// inReview is a harness whose n primaries are in review, none asked yet: the
// next tick's ask asks them. Worker takes and reports require RUNNING.
func inReview(t *testing.T, n int) *harness {
	t.Helper()
	h := newHarness(t)
	h.setup(n)
	h.startMachine()
	h.machine()
	h.st.CheckTwin = nil // the racing writers commit from inside the ask's write
	// No second tick runs between finish and the ask under test.
	h.work("m1")
	h.work("m2")
	require.Len(t, h.snap().Work.Column(sprint.Review), n, "the primaries in review before the tick")
	return h
}

// askOf is the ask part's moves and refusals in a tick.
func askOf(res TickResult) (asked []string, refused []sprint.Refusal) {
	for _, p := range res.Parts {
		if p.Name != "ask" {
			continue
		}
		for _, m := range p.Result.Moved {
			if strings.Contains(m, " asked of ") {
				asked = append(asked, m)
			}
		}
		refused = append(refused, p.Result.Refused...)
	}
	return asked, refused
}

// An uncontended review backlog fits in one fenced ask step: the tick still
// asks every primary, but does not replan the review table after each five.
func TestTheAskBatchesTwentyPrimariesInOneStep(t *testing.T) {
	t.Parallel()
	h := inReview(t, 20)
	writes := 0
	h.st.B = racingAsk{Mem: h.m, h: h, writes: &writes}
	res, err := h.st.Tick(h.ctx)
	require.NoError(t, err)
	asked, refused := askOf(res)
	require.Empty(t, refused)
	require.Len(t, asked, 20)
	require.Equal(t, 1, writes, "one fenced ask for twenty primaries")
	h.st.B = h.m
	h.clean("after the batched ask")
}

// The ask writes in small fenced steps: other writers commit every 30 ms and a
// write's window is 5 ms a primary it names, so a write of six or more always
// holds another writer's commit. A large batch loses its tries (the live
// failure of 2026-10-06); the ask retries its primaries alone, so every one
// is asked in the one tick and none is refused for the fence. (At 10 ms a
// primary the lost batch's three tries alone took 600 ms, past AskBy: the ask
// then rightly leaves the rest to the next tick, which
// TestTheAskBeginsNoStepPastHalfItsTick holds.)
func TestTheAskStepAsksInSmallFencedSteps(t *testing.T) {
	t.Parallel()
	h := inReview(t, 20)
	writes := 0
	h.st.B = racingAsk{Mem: h.m, h: h, writes: &writes,
		cost: func(ps []string) time.Duration { return time.Duration(len(ps)) * 5 * time.Millisecond },
		lose: func(ps []string) bool { return len(ps)*5 >= 30 }}
	res, err := h.st.Tick(h.ctx)
	require.NoError(t, err, "the tick")
	t.Logf("the ask's writes: %d; %s", writes, res.TimesLine())
	asked, refused := askOf(res)
	for _, r := range refused {
		require.NotContains(t, r.Why, "kept changing", "%s refused for the fence: %s", r.Key, r.Why)
	}
	require.GreaterOrEqual(t, len(asked), 15, "asked in one tick: %d (%d writes), refused %v", len(asked), writes, refused)
	require.Len(t, asked, 20, "every primary asked in the one tick: refused %v", refused)
	require.Regexp(t, regexp.MustCompile(` readers/ask=\d+ms/\d+t/20asked/0refused`), res.TimesLine())
	h.st.B = h.m
	h.clean("after the ask")
}

// The ask stops at its budget: every write of the ask finds the fence moved and
// takes 250 ms, so no try of it ever commits. The ask plans no try past
// AskBudget and the tick goes on, its primaries due; the next tick, with the
// writers gone, asks them all.
func TestTheAskStepStopsAtItsBudget(t *testing.T) {
	t.Parallel()
	h := inReview(t, 20)
	writes := 0
	const cost = 250 * time.Millisecond
	h.st.B = racingAsk{Mem: h.m, h: h, writes: &writes,
		cost: func([]string) time.Duration { return cost },
		lose: func([]string) bool { return true }}
	res, err := h.st.Tick(h.ctx)
	require.NoError(t, err, "the tick")
	t.Logf("the ask's writes: %d; %s", writes, res.TimesLine())
	took := time.Duration(writes) * cost
	require.LessOrEqual(t, took, AskBudget, "the ask's writes took %s on the clock (%d writes), past its budget", took, writes)
	asked, refused := askOf(res)
	require.Empty(t, asked)
	require.Less(t, len(refused), 20, "the ask gave up every primary at once: %v", refused)
	for _, r := range refused {
		require.NotContains(t, r.Why, "kept changing", "%s refused for the whole plan: %s", r.Key, r.Why)
	}
	require.Positive(t, res.Due, "the primaries the ask did not reach are due")
	h.st.B = h.m
	h.tick(time.Second)
	res = h.machine()
	asked, refused = askOf(res)
	require.Len(t, asked, 20, "the next tick asks them all: refused %v", refused)
	h.clean("after the next tick")
}

// A primary whose every write meets another writer is one refusal, its own: its
// batch is tried again a primary at a time, the other nineteen are asked in the
// tick, and the next tick asks it.
func TestAnAskConflictIsOneCardsRefusalNotTheTicks(t *testing.T) {
	t.Parallel()
	h := inReview(t, 20)
	const contested = "s1-3"
	writes := 0
	h.st.B = racingAsk{Mem: h.m, h: h, writes: &writes,
		lose: func(ps []string) bool { return strings.Contains(" "+strings.Join(ps, " ")+" ", " "+contested+" ") }}
	res, err := h.st.Tick(h.ctx)
	require.NoError(t, err, "the tick")
	t.Logf("the ask's writes: %d; %s", writes, res.TimesLine())
	asked, refused := askOf(res)
	require.Len(t, refused, 1, "one refusal: %v", refused)
	require.Equal(t, contested, refused[0].Key)
	require.Contains(t, refused[0].Why, "the next tick asks it again")
	require.Len(t, asked, 19, "the other primaries asked in the tick")
	require.Regexp(t, regexp.MustCompile(` readers/ask=\d+ms/\d+t/19asked/1refused`), res.TimesLine())
	h.st.B = h.m
	h.tick(time.Second)
	res = h.machine()
	asked, _ = askOf(res)
	require.Len(t, asked, 1, "the next tick asks the contested primary")
	require.Contains(t, asked[0], contested)
	h.clean("after the next tick")
}

// A batch that loses its tries and that the budget cuts before it is tried again a
// primary at a time is not lost from sight: its primaries are due for the next tick, the
// ask is unfinished, and the TIMES line says how many (<n>lost). Here every write of more
// than one primary loses, and the first batch's three tries spend the budget on the
// harness's clock.
func TestALostBatchIsCountedAsDue(t *testing.T) {
	t.Parallel()
	h := inReview(t, 10)
	writes := 0
	h.st.B = racingAsk{Mem: h.m, h: h, writes: &writes,
		cost: func([]string) time.Duration { return 700 * time.Millisecond },
		lose: func(ps []string) bool { return len(ps) > 1 }}
	res, err := h.st.Tick(h.ctx)
	require.NoError(t, err, "the tick")
	t.Logf("the ask's writes: %d; due %d; %s", writes, res.Due, res.TimesLine())
	asked, refused := askOf(res)
	require.Empty(t, asked)
	require.Empty(t, refused, "a lost batch is no primary's refusal")
	lost := min(AskBatch, 10)
	require.Regexp(t, regexp.MustCompile(fmt.Sprintf(` readers/ask=\d+ms/\d+t/0asked/0refused/%dlost`, lost)), res.TimesLine())
	require.GreaterOrEqual(t, res.Due, 10, "the lost batch and the cards the ask did not reach are due")
	h.st.B = h.m
	h.tick(time.Second)
	res = h.machine()
	asked, refused = askOf(res)
	require.Len(t, asked, 10, "the next tick asks them all: refused %v", refused)
	h.clean("after the next tick")

	// the budget's share of the tick's deadline is read on the store's clock
	dl := time.Now().Add(time.Hour)
	ctx, cancel := context.WithDeadline(context.Background(), dl)
	defer cancel()
	require.Equal(t, 1500*time.Millisecond, askBudget(ctx, dl.Add(-3*time.Second)))
	require.Equal(t, AskBudget, askBudget(context.Background(), t0))
}

// The ask begins no step past AskBy into its tick, the owner's sub-second tick
// (2026-09-30), whatever its own budget leaves: its budget of two seconds inside a
// tick of one made every tick with a backlog of reads a two-second tick (the
// certification drive of 2026-10-10, readers/ask 2.006 to 2.063 s). Here every
// write of the ask takes 600 ms on the clock and none is contested: the first
// step asks twenty and ends past AskBy, so no second step begins; the other
// twenty are due, and the next tick asks them.
func TestTheAskBeginsNoStepPastHalfItsTick(t *testing.T) {
	t.Parallel()
	h := inReview(t, 2*AskBatch)
	writes := 0
	h.st.B = racingAsk{Mem: h.m, h: h, writes: &writes,
		cost: func([]string) time.Duration { return 600 * time.Millisecond }}
	res, err := h.st.Tick(h.ctx)
	require.NoError(t, err, "the tick")
	t.Logf("the ask's writes: %d; due %d; %s", writes, res.Due, res.TimesLine())
	asked, refused := askOf(res)
	require.Empty(t, refused)
	require.Equal(t, 1, writes, "one step began: the second would begin past AskBy")
	require.Len(t, asked, AskBatch, "the first step always begins and asks its batch")
	require.GreaterOrEqual(t, res.Due, AskBatch, "the primaries the ask did not reach are due")
	h.st.B = h.m
	h.tick(time.Second)
	res = h.machine()
	asked, refused = askOf(res)
	require.Len(t, asked, AskBatch, "the next tick asks the rest: refused %v", refused)
	h.clean("after the next tick")

	// when the ask begins no further step: AskBy past the tick's beginning when that
	// is sooner than its budget, the budget alone for a tick with no beginning
	t0 := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	bg := context.Background()
	require.Equal(t, t0.Add(AskBy), askUntil(bg, t0, t0))
	require.Equal(t, t0.Add(AskBy), askUntil(bg, t0, t0.Add(300*time.Millisecond)), "the ask's own start does not move the tick's half")
	require.Equal(t, t0.Add(AskBudget), askUntil(bg, time.Time{}, t0))
	dl := t0.Add(400 * time.Millisecond)
	ctx, cancel := context.WithDeadline(bg, dl)
	defer cancel()
	require.Equal(t, t0.Add(200*time.Millisecond), askUntil(ctx, t0, t0), "half of the tick's deadline when that is sooner")
}

// friendRead closes a friend's read of the primary at its attempt with her
// report, as friend sync does (cmd/nova-sprint friendcards.go).
func (h *harness) friendRead(name, primary, report string) {
	h.t.Helper()
	at := h.st.PinnedEpoch()
	card := sprint.ReadCardID(primary, h.snap().Work.Card(primary).Int("attempt"), name)
	h.must(Step{Verb: "read", Named: true, Mirrors: true, Load: []string{sprint.Fleet, sprint.Work},
		Extras: sprint.NamedExtras(sprint.Fleet, []string{card}), Actor: sprint.FriendRow(name), Epoch: &at,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.FriendReadCloseChecked(s, name, primary, "", report, nil)
		}})
}

// friendReadsOf is the friends' read cards placed for the primary, by friend.
func friendReadsOf(s *sprint.Snapshot, primary string) []string {
	var out []string
	for _, c := range s.Fleet.Cards() {
		if c.Placed() && c.F("kind") == "read" && c.F("primary") == primary {
			out = append(out, c.F("reader"))
		}
	}
	return out
}

// The friend ask asks every review card a friend may read (2026-10-06, 5:20 PM:
// 283 of 300 review cards with no read outstanding, three friends with room,
// and the tick's ask asked none). The tick plans on a sparse read whose extras
// held the readers table's retired read cards and not a friend's on her fleet
// row: a read a friend had closed was invisible, so the ask saw the primary
// unread and planned her read card again, a create of a record that exists,
// which the store refused; every try of the ask's one write was refused with
// it. Here the machine readers are away and three friends read: every primary
// is asked both its reads together, of two friends (reads are asked together,
// sprint.ReadsWanted); after the first of each closes (one broken), the next
// tick sees the closed read, plans no read again, and nothing is refused; the
// second reads close and the tick after refuses nothing either.
func TestTheFriendAskAsksEveryReviewCardAFriendMayRead(t *testing.T) {
	t.Parallel()
	h := inReview(t, 6)
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		require.NoError(t, h.st.SetReaderAway(h.ctx, rd, true, "coordinator"))
	}
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{
		{Name: "stella", Width: 32, Class: "flash,frontier,heavy,pro"},
		{Name: "johnny", Width: 16, Class: "flash,pro"},
		{Name: "zhi", Width: 16, Class: "pro"},
	})
	require.NoError(t, err)
	for _, f := range []string{"stella", "johnny", "zhi"} {
		h.up(f)
	}
	ids := []string{"s1-1", "s1-2", "s1-3", "s1-4", "s1-5", "s1-6"}
	res := h.machine()
	_, refused := askOf(res)
	require.Empty(t, refused, "the reads")
	both := map[string][]string{}
	s := h.snap()
	for _, id := range ids {
		got := friendReadsOf(s, id)
		require.Len(t, got, 2, "%s: both its reads asked together, of friends", id)
		require.NotEqual(t, got[0], got[1], "%s: of two different friends", id)
		both[id] = got
	}
	broken := ids[0]
	for _, id := range ids {
		report := "Verdict: LAND\n"
		if id == broken {
			report = "Verdict: HOLD\nmain.go is wrong\n"
		}
		h.friendRead(both[id][0], id, report)
	}
	h.tick(time.Second)
	for _, f := range []string{"stella", "johnny", "zhi"} {
		h.up(f)
	}
	res = h.machine()
	asked, refused := askOf(res)
	require.Empty(t, refused, "the closed reads are seen: asked %v", asked)
	s = h.snap()
	for _, id := range ids {
		assert.Equal(t, both[id][1:], friendReadsOf(s, id), "%s: its other read stands and none is planned again (asked %v)", id, asked)
	}
	for _, id := range ids[1:] {
		h.friendRead(both[id][1], id, "Verdict: LAND\n")
	}
	h.tick(time.Second)
	for _, f := range []string{"stella", "johnny", "zhi"} {
		h.up(f)
	}
	res = h.machine()
	asked, refused = askOf(res)
	require.Empty(t, refused, "the second reads closed: asked %v", asked)
	for _, id := range ids[1:] {
		assert.Empty(t, friendReadsOf(h.snap(), id), "%s: read twice ok, no read planned again", id)
	}
}
