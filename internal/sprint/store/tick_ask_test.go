package store

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

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
// next tick's ask asks them.
func inReview(t *testing.T, n int) *harness {
	t.Helper()
	h := newHarness(t)
	h.setup(n)
	h.startMachine()
	h.machine()
	h.st.CheckTwin = nil // the racing writers commit from inside the ask's write
	// the workers finish while the machine is stopped, so no tick asks before
	// the one under test
	h.stopMachine()
	h.work("m1")
	h.work("m2")
	h.startMachine()
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

// The ask writes in small fenced steps: other writers commit every 60 ms and a
// write's window is 10 ms a primary it names, so a write of six or more always
// holds another writer's commit. Twenty primaries in one write lost every try
// (the live failure of 2026-10-06); in steps of five, every one is asked in the
// one tick and none is refused for the fence.
func TestTheAskStepAsksInSmallFencedSteps(t *testing.T) {
	t.Parallel()
	h := inReview(t, 20)
	writes := 0
	h.st.B = racingAsk{Mem: h.m, h: h, writes: &writes,
		cost: func(ps []string) time.Duration { return time.Duration(len(ps)) * 10 * time.Millisecond },
		lose: func(ps []string) bool { return len(ps)*10 >= 60 }}
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

// friendRead closes a friend's read of the primary at its attempt with her
// report, as friend sync does (cmd/nova-sprint friendcards.go).
func (h *harness) friendRead(name, primary, report string) {
	h.t.Helper()
	at := h.st.PinnedEpoch()
	card := sprint.ReadCardID(primary, h.snap().Work.Card(primary).Int("attempt"), name)
	h.must(Step{Verb: "read", Named: true, Mirrors: true, Load: []string{sprint.Fleet, sprint.Work},
		Extras: sprint.NamedExtras(sprint.Fleet, []string{card}), Actor: sprint.FriendRow(name), Epoch: &at,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FriendReadClose(s, name, primary, report) }})
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
// it. Here the machine readers are away and three friends read: after their
// first reads close (one broken), every primary read ok is asked its second
// read of another friend in the next tick, the broken one waits on its
// judgment, and nothing is refused.
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
	require.Empty(t, refused, "the first reads")
	first := map[string]string{}
	s := h.snap()
	for _, id := range ids {
		got := friendReadsOf(s, id)
		require.Len(t, got, 1, "%s: its first read, of a friend", id)
		first[id] = got[0]
	}
	broken := ids[0]
	for _, id := range ids {
		report := "Verdict: LAND\n"
		if id == broken {
			report = "Verdict: HOLD\nmain.go is wrong\n"
		}
		h.friendRead(first[id], id, report)
	}
	h.tick(time.Second)
	for _, f := range []string{"stella", "johnny", "zhi"} {
		h.up(f)
	}
	res = h.machine()
	asked, refused := askOf(res)
	require.Empty(t, refused, "the second reads: asked %v", asked)
	s = h.snap()
	for _, id := range ids[1:] {
		got := friendReadsOf(s, id)
		require.Len(t, got, 1, "%s: its second read asked, of a friend (asked %v)", id, asked)
		require.NotEqual(t, first[id], got[0], "%s: its second read is another friend's", id)
	}
	require.Empty(t, friendReadsOf(s, broken), "the broken read's primary waits on its judgment")
}
