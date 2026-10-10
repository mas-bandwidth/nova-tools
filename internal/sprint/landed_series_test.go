package sprint_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
	"github.com/stretchr/testify/require"
)

// TestTheLandedSeriesCountsEachCardOnceByItsWorker: a friend landing, a machine
// landing, a sentinel's release and a card landed twice. The buckets count each
// real landing once, under the worker of the landed attempt. The store's actor
// is friend.lander, but the RUNNING tick applies its queued Work move as machine;
// a fold that takes the landing actor for the worker loses the actual worker.
// The lifecycle refuses a second move to landed, so the duplicate is a second
// line on the log the store wrote. An earlier
// friend.amy:ok for the machine card is appended after the real m1:ok, so the
// last line is not the last time. A set :ok line names every card of one
// finish step, so each card keeps the set's worker. A set move names every card
// and counts each one once.
func TestTheLandedSeriesCountsEachCardOnceByItsWorker(t *testing.T) {
	t.Parallel()
	r := newLandedRig(t)
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
	// The friend is absent for the machine card's deal: a friend up takes a
	// flash card that names no one (friend deal runs before the fleet's).
	r.beatMachines()
	r.must(store.AddStep(sprint.AddReq{Stream: "mach", Cards: []sprint.CardAdd{{
		ID: "mach-1", Brief: "tier: flash\n\nA machine card.",
	}}}))
	r.must(store.AddStep(sprint.AddReq{Stream: "gate", IDs: []string{"gate-1"}, Sentinel: true}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.beatMachines()
	_, err = r.st.Tick(r.ctx)
	require.NoError(t, err)
	mach := r.snap().Fleet.Card("mach-1.w1")
	require.True(t, mach.Placed(), "mach-1.w1 dealt")
	require.Equal(t, "m1", mach.Row, "the machine card is m1's")

	_, _, _, err = r.st.SyncFriends(r.ctx, []store.FriendSpec{{Name: "amy", Width: 1, Class: "flash"}})
	require.NoError(t, err)
	r.must(store.AddStep(sprint.AddReq{Stream: "fr", Cards: []sprint.CardAdd{{
		ID: "fr-1", Brief: "tier: flash\nWHO: only friend amy\n\nA friend's card.",
	}}}))
	r.beat()
	_, err = r.st.Tick(r.ctx)
	require.NoError(t, err)
	fr := r.snap().Fleet.Card("fr-1.w1")
	require.True(t, fr.Placed(), "fr-1.w1 dealt")
	require.Equal(t, "friend.amy", fr.Row, "the friend card is amy's")

	// Release the sentinel while stopped, as this fixture originally did, after
	// both work cards were dealt and before any owner began them.
	_, _, _, err = r.st.SetMachine(r.ctx, false)
	require.NoError(t, err)
	r.must(store.ReleaseStep(sprint.ReleaseReq{
		IDs: []string{"gate-1"}, Reason: "nothing before it", Coordinator: "coordinator", Who: "coordinator",
	}))
	require.Equal(t, sprint.Landed, r.snap().StateOf("gate-1"), "the sentinel released")
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.land("mach-1", "mach")
	r.land("fr-1", "fr")

	lines, err := r.st.Log(r.ctx)
	require.NoError(t, err)
	require.NotEmpty(t, landedMoves(lines, "mach-1", ":merging", ":landed"), "machine landing\n%s", dumpMoves(lines))
	require.NotEmpty(t, landedMoves(lines, "fr-1", ":merging", ":landed"), "friend landing\n%s", dumpMoves(lines))
	require.NotEmpty(t, landedMoves(lines, "gate-1", ":waiting", ":landed"), "sentinel release\n%s", dumpMoves(lines))
	machOK := okMoves(lines, "mach-1", "m1")
	require.NotEmpty(t, machOK, "machine :ok\n%s", dumpMoves(lines))
	require.NotEmpty(t, okMoves(lines, "fr-1", "friend.amy"), "friend :ok\n%s", dumpMoves(lines))
	for _, l := range landedMoves(lines, "mach-1", ":merging", ":landed") {
		require.Equal(t, "machine", l.Actor, "the RUNNING tick applies the lander's queued Work move; the line does not name the worker")
	}

	// The second landing is the same card again. A later line with an earlier
	// time must not win the worker, and a second landing must not count.
	again := landedMoves(lines, "mach-1", ":merging", ":landed")[0]
	again.At = again.At.Add(time.Second)
	lines = append(lines, again)
	lines = append(lines, sprint.Line{
		Kind: sprint.LineMove, At: machOK[0].At.Add(-time.Second), Table: sprint.Fleet,
		Card: "mach-1.w1", From: "friend.amy:working", To: "friend.amy:ok", Actor: "friend.amy",
	})
	// A set move names every card. Card repeats the first, and counts once.
	lines = append(lines,
		sprint.Line{
			Kind: sprint.LineMove, At: r.now, Table: sprint.Work,
			Card: "batch-a", Cards: []string{"batch-a", "batch-b"},
			From: "s:merging", To: "s:landed", Actor: "friend.lander",
		},
		sprint.Line{Kind: sprint.LineMove, At: r.now, Table: sprint.Fleet, Card: "batch-a.w1", To: "m2:ok"},
		sprint.Line{Kind: sprint.LineMove, At: r.now, Table: sprint.Fleet, Card: "batch-b.w1", To: "friend.bea:ok"},
	)
	// A set :ok line names two work cards of one finish step. Both keep the
	// set's worker, so neither lands unknown.
	lines = append(lines,
		sprint.Line{
			Kind: sprint.LineMove, At: r.now, Table: sprint.Fleet,
			Card: "set-a.w1", Cards: []string{"set-a.w1", "set-b.w1"}, To: "m3:ok",
		},
		sprint.Line{Kind: sprint.LineMove, At: r.now, Table: sprint.Work, Card: "set-a", From: "s:merging", To: "s:landed"},
		sprint.Line{Kind: sprint.LineMove, At: r.now, Table: sprint.Work, Card: "set-b", From: "s:merging", To: "s:landed"},
	)
	// A landing with no :ok line is unknown: it counts in totals.unknown
	// and not in workers, so workers never carries a "-" key.
	lines = append(lines,
		sprint.Line{Kind: sprint.LineMove, At: r.now, Table: sprint.Work, Card: "unknown-card", From: "s:merging", To: "s:landed"},
	)

	got := sprint.LandedSeriesOf(lines, r.now)
	require.Equal(t, 600, got.BucketSeconds)
	require.Equal(t, 144, got.Buckets)
	require.Len(t, got.Friends, 144)
	require.Len(t, got.Fleet, 144)
	end := r.now.Unix() / 600 * 600
	require.Equal(t, end-(144-1)*600, got.Start)
	for i := 0; i < 143; i++ {
		require.Zero(t, got.Friends[i], "friends bucket %d", i)
		require.Zero(t, got.Fleet[i], "fleet bucket %d", i)
	}
	require.Equal(t, 2, got.Friends[143], "amy and bea, once each")
	require.Equal(t, 4, got.Fleet[143], "m1, m2 and m3's two, once each; the second mach-1 landing does not count")
	require.Equal(t, sprint.SeriesTotals{Friends: 2, Fleet: 4, Unknown: 1}, got.Totals)
	require.Equal(t, sprint.SeriesLastHour{Friends: 2, Fleet: 4}, got.LastHour)
	require.NotContains(t, got.Workers, "-", "an unknown landing is not a worker")
	require.Equal(t, map[string]int{"friend.amy": 1, "friend.bea": 1, "m1": 1, "m2": 1, "m3": 2}, got.Workers)
}

type landedRig struct {
	t   *testing.T
	st  *store.Store
	ctx context.Context
	mu  sync.Mutex
	now time.Time
	n   int
}

func newLandedRig(t *testing.T) *landedRig {
	t.Helper()
	m := store.NewMem()
	r := &landedRig{t: t, ctx: context.Background(), now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	r.st = &store.Store{
		B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "friend.lander",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); r.n++; return fmt.Sprint(r.n) },
		Sleep: func(time.Duration) {},
	}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, m.RowsAdd(r.ctx, "t-readers", []string{"reader-a", "reader-b"}))
	require.NoError(t, m.SetCoordinator(r.ctx, "coordinator"))
	return r
}

func (r *landedRig) beatMachines() {
	r.t.Helper()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	_, err := r.st.Beat(r.ctx, "m1", &zero, hostload.Source{})
	require.NoError(r.t, err)
}

func (r *landedRig) beat() {
	r.t.Helper()
	r.beatMachines()
	_, err := r.st.FriendBeat(r.ctx, "amy")
	require.NoError(r.t, err)
	// her beat is no evidence: a wake ping her session answered makes her up
	_, _, _, err = r.st.FriendHealth(r.ctx, "amy", "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
	require.NoError(r.t, err)
}

func (r *landedRig) must(step store.Step) {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, step)
	require.NoError(r.t, err, step.Verb)
	require.Empty(r.t, res.Refused, "%s refused: %v", step.Verb, res.Refused)
}

func (r *landedRig) snap() *sprint.Snapshot {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	return s
}

// land finishes, reads, accepts and merges one dealt primary while RUNNING.
func (r *landedRig) land(id, stream string) {
	r.t.Helper()
	s := r.snap()
	pr := s.Work.Card(id)
	require.True(r.t, pr.Placed(), "%s not placed (%s)", id, s.StateOf(id))
	wc := s.Fleet.Card(pr.F("work"))
	require.True(r.t, wc.Placed(), "%s work card %q", id, pr.F("work"))
	if wc.Col == sprint.Ready {
		r.must(store.TakeStep(sprint.TakeReq{
			As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: wc.Row,
		}))
		wc = r.snap().Fleet.Card(wc.ID)
	}
	require.Equal(r.t, sprint.Working, wc.Col, "%s is %s on %s", wc.ID, wc.Col, wc.Row)
	r.must(store.FinishStep(sprint.FinishReq{
		As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")},
		Head: "abc", Who: wc.Row,
	}))
	r.beat()
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
	require.Equal(r.t, sprint.Review, r.snap().StateOf(id), "%s after finish", id)
	s = r.snap()
	require.Zero(r.t, sprint.ReadsWanted(s, s.Work.Card(id)), "the RUNNING tick handled %s's initial read demand", id)
	for {
		s = r.snap()
		for _, rc := range s.Fleet.Of(id) {
			if rc.F("kind") == "read" && (rc.Col == sprint.Ready || rc.Col == sprint.Working) {
				r.must(store.ReadStep(sprint.ReadReq{
					As: rc.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: rc.Row,
				}))
			}
		}
		for _, rc := range s.Readers.Of(id) {
			if rc.Col == sprint.Asked || rc.Col == sprint.Reading {
				r.must(store.ReadStep(sprint.ReadReq{
					As: rc.F("reader"), Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: rc.F("reader"),
				}))
			}
		}
		s = r.snap()
		pr = s.Work.Card(id)
		if pr.Col != sprint.Review || sprint.ReadsWanted(s, pr) == 0 {
			break
		}
		res, err := r.st.Run(r.ctx, store.AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}}))
		require.NoError(r.t, err)
		if len(res.Refused) > 0 || len(res.Moved) == 0 {
			break
		}
	}
	r.beat()
	_, err = r.st.Tick(r.ctx)
	require.NoError(r.t, err)
	require.Equal(r.t, sprint.Merging, r.snap().StateOf(id), "%s accepted after its reads", id)
	merge, err := r.st.Run(r.ctx, store.MergeStep(sprint.MergeReq{Stream: stream, Cards: []string{id}}))
	require.NoError(r.t, err)
	require.Empty(r.t, merge.Refused, "%s merge refused: %v", id, merge.Refused)
	require.NotEmpty(r.t, merge.Moved, "%s merge made no move; merge queue=%v", id, r.snap().Merge.Cell(stream, sprint.Queued))
	// A RUNNING non-pump step queues its Work move; the tick applies it.
	r.beat()
	_, err = r.st.Tick(r.ctx)
	require.NoError(r.t, err)
	require.Equal(r.t, sprint.Landed, r.snap().StateOf(id), "%s landed", id)
}

func landedMoves(lines []sprint.Line, card, fromSuffix, toSuffix string) []sprint.Line {
	var out []sprint.Line
	for _, l := range lines {
		if l.Kind != sprint.LineMove || l.Table != sprint.Work {
			continue
		}
		if l.Card != card && !containsCard(l.Cards, card) {
			continue
		}
		if strings.HasSuffix(l.From, fromSuffix) && strings.HasSuffix(l.To, toSuffix) {
			out = append(out, l)
		}
	}
	return out
}

func okMoves(lines []sprint.Line, primary, worker string) []sprint.Line {
	var out []sprint.Line
	for _, l := range lines {
		if l.Kind != sprint.LineMove || !strings.HasPrefix(l.Card, primary+".w") || !strings.HasSuffix(l.To, ":ok") {
			continue
		}
		if strings.TrimSuffix(l.To, ":ok") == worker {
			out = append(out, l)
		}
	}
	return out
}

func containsCard(ids []string, id string) bool {
	for _, c := range ids {
		if c == id {
			return true
		}
	}
	return false
}

func dumpMoves(lines []sprint.Line) string {
	var b strings.Builder
	for _, l := range lines {
		if l.Kind != sprint.LineMove {
			continue
		}
		fmt.Fprintf(&b, "%s %s %s -> %s card=%s cards=%v actor=%s\n", l.Table, l.Verb, l.From, l.To, l.Card, l.Cards, l.Actor)
	}
	return b.String()
}
