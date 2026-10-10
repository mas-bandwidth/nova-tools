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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Hold and unhold on the twin store (store.Mem, the in-memory store the command's
// `--redis mem:<file>` runs on): one verb pair for a fleet member, a reader, a friend and a
// stream (docs/SPEC-SPRINT.md section 11; the owner, 2026-10-04 1:50 PM: "there should be a
// hold verb in nova-sprint"; 1:51 PM: the same for friends, hold and unhold).

// holdRig is a running sprint on the twin: readers reader-a..c, members m1 and m2 (width
// 2), friends amy and bob (width 2), stream s1 of machine cards and f1 of friends' cards.
type holdRig struct {
	t   *testing.T
	st  *store.Store
	ctx context.Context
	mu  sync.Mutex
	now time.Time
}

var holdT0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

// friendsBrief is a friend's card's brief: its WHO line names who.
func friendsBrief(who string) string {
	return "c: a friend's card tier: pro\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
}

func newHoldRig(t *testing.T, s1, f1 int) *holdRig {
	t.Helper()
	m := store.NewMem()
	r := &holdRig{t: t, ctx: context.Background(), now: holdT0}
	n := 0
	r.st = &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, m.RowsAdd(r.ctx, "t-readers", []string{"reader-a", "reader-b", "reader-c"}))
	require.NoError(t, m.SetCoordinator(r.ctx, "coordinator"))
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{{Name: "amy", Width: 2, Class: "pro"}, {Name: "bob", Width: 2, Class: "pro"}})
	require.NoError(t, err)
	r.beat()
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m2", Width: 2}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: s1}))
	var cards []sprint.CardAdd
	for i := range f1 {
		cards = append(cards, sprint.CardAdd{ID: fmt.Sprintf("f1-%d", i+1), Brief: friendsBrief("friend")})
	}
	r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: cards}))
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	return r
}

// beat is one beat of every member, reader and friend, and a wake ping each friend's
// session answered (a friend's beat is no evidence): each of them is there.
func (r *holdRig) beat() {
	r.t.Helper()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	for _, m := range []string{"m1", "m2"} {
		_, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{})
		require.NoError(r.t, err)
	}
	for _, f := range []string{"amy", "bob"} {
		_, err := r.st.FriendBeat(r.ctx, f)
		require.NoError(r.t, err)
		_, _, _, err = r.st.FriendHealth(r.ctx, f, "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
		require.NoError(r.t, err)
	}
}

// tick moves the clock a second, beats everyone and runs one tick of the machine.
func (r *holdRig) tick() {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	r.beat()
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

func (r *holdRig) must(step store.Step) store.Result {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, step)
	require.NoError(r.t, err, step.Verb)
	require.Empty(r.t, res.Refused, "%s refused", step.Verb)
	return res
}

// hold is the coordinator's hold (or unhold) of names; it must apply.
func (r *holdRig) hold(req sprint.HoldReq) store.Result {
	r.t.Helper()
	req.Who = "coordinator"
	res, err := r.st.Hold(r.ctx, req)
	require.NoError(r.t, err)
	require.Empty(r.t, res.Refused, "hold %v refused", req.Names)
	return res
}

func (r *holdRig) snap() *sprint.Snapshot {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	return s
}

// onRow is the work cards of the stream placed on a fleet row, in the columns named.
func onRow(s *sprint.Snapshot, row, stream string, cols ...string) []string {
	var out []string
	for _, col := range cols {
		for _, c := range s.Fleet.Cell(row, col) {
			if c.F("stream") == stream {
				out = append(out, c.ID)
			}
		}
	}
	return out
}

// takeOne has member m take one ready card, as its loop does; the card taken.
func (r *holdRig) takeOne(m string) string {
	r.t.Helper()
	res := r.must(store.TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: 1}, Who: m}))
	require.NotEmpty(r.t, res.Moved, "%s took nothing", m)
	ws := r.snap().Fleet.Cell(m, sprint.Working)
	require.NotEmpty(r.t, ws)
	return ws[len(ws)-1].ID
}

// holdLines is the log's notes of holds and releases, as written.
func (r *holdRig) holdLines() []string {
	r.t.Helper()
	lines, err := r.st.Log(r.ctx)
	require.NoError(r.t, err)
	var out []string
	for _, l := range lines {
		if l.Note != nil && (l.Note.Type == sprint.NHeld || l.Note.Type == sprint.NUnheld) {
			out = append(out, l.Note.What)
		}
	}
	return out
}

// heldView is the hold in force on name, from Holds (where --json's holds).
func (r *holdRig) heldView(name string) (sprint.HoldView, bool) {
	r.t.Helper()
	hs, err := r.st.Holds(r.ctx)
	require.NoError(r.t, err)
	for _, h := range hs {
		if h.Name == name {
			return h, true
		}
	}
	return sprint.HoldView{}, false
}

func (r *holdRig) friendStatus(name string) string {
	r.t.Helper()
	rows, err := r.st.FriendRows(r.ctx, r.st.Now())
	require.NoError(r.t, err)
	for _, f := range rows {
		if f.Name == name {
			return f.Status
		}
	}
	return ""
}

// TestHoldTakesNoNewCardsAndUnholdResumesForMembersFriendsAndStreams: a held member,
// friend or stream takes no new card while its dealt work finishes; --return hands the
// work back now; unhold resumes it, each with its reason in its status and the log.
func TestHoldTakesNoNewCardsAndUnholdResumesForMembersFriendsAndStreams(t *testing.T) {
	t.Parallel()
	t.Run("member", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 12, 0)
		r.tick()
		working := r.takeOne("m1")
		r.hold(sprint.HoldReq{Names: []string{"m1"}, Reason: "the build cache cleaner deletes live entries"})
		s := r.snap()
		assert.Equal(t, sprint.Held, sprint.MemberStatus(s.MemberCtl("m1"), sprint.Beat{At: r.st.Now()}, r.st.Now()), "status reads held")
		assert.Equal(t, []string{working}, onRow(s, "m1", "s1", sprint.Ready, sprint.Working), "its ready cards went round the fleet; the card it works stays to finish")
		hv, ok := r.heldView("m1")
		require.True(t, ok)
		assert.Equal(t, sprint.HoldView{Kind: sprint.HoldMember, Name: "m1", Reason: "the build cache cleaner deletes live entries", At: hv.At}, hv)
		res, err := r.st.Run(r.ctx, store.TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
		require.NoError(t, err)
		assert.Empty(t, res.Moved, "a held member takes no new card")
		for range 3 {
			r.tick()
		}
		s = r.snap()
		assert.Equal(t, []string{working}, onRow(s, "m1", "s1", sprint.Ready, sprint.Working), "no tick deals it a card, nor sweeps its working card away")
		r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{working}}, Gens: map[string]int{working: s.Fleet.Card(working).Int("gen")}, Who: "m1"}))

		r.hold(sprint.HoldReq{Names: []string{"m1"}, Release: true, Reason: "the cleaner is fixed"})
		r.tick()
		s = r.snap()
		assert.Equal(t, sprint.Up, s.MemberCtl("m1").F("status"), "unhold: a member that beats is up at once")
		assert.NotEmpty(t, onRow(s, "m1", "s1", sprint.Ready), "unhold resumes its deals")
		_, ok = r.heldView("m1")
		assert.False(t, ok)
		assert.Equal(t, []string{
			"member m1 held: the build cache cleaner deletes live entries; its work begun finishes",
			"member m1 released from its hold: the cleaner is fixed",
		}, r.holdLines(), "the log holds the hold and its release, the reason in each")

		// --return: the work begun is handed back now
		taken := r.takeOne("m2")
		r.hold(sprint.HoldReq{Names: []string{"m2"}, Reason: "drain it", Return: true})
		s = r.snap()
		assert.Empty(t, onRow(s, "m2", "s1", sprint.Ready, sprint.Working), "--return deals its working cards round the fleet too")
		wc := s.Fleet.Card(taken)
		assert.NotEqual(t, "m2:"+sprint.Working, wc.Row+":"+wc.Col, "%s handed back: dealt to a member up, or withdrawn", taken)
	})
	t.Run("stream", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 8, 0)
		r.tick()
		working := r.takeOne("m1")
		r.hold(sprint.HoldReq{Names: []string{"s1"}, Reason: "the layer below is not secured"})
		s := r.snap()
		assert.True(t, sprint.StreamHeld(s, "s1"))
		assert.Equal(t, sprint.Held, sprint.StreamStateText(s.StreamCtl("s1").Fields), "its state reads held")
		assert.Equal(t, []string{working}, append(onRow(s, "m1", "s1", sprint.Ready, sprint.Working), onRow(s, "m2", "s1", sprint.Ready, sprint.Working)...),
			"its ready work cards are withdrawn; the one begun finishes")
		r.tick()
		r.tick()
		s = r.snap()
		assert.Empty(t, onRow(s, "m2", "s1", sprint.Ready), "no tick deals a held stream's card")
		assert.Empty(t, s.Fleet.Cell("m1", sprint.Ready), "no tick deals a held stream's card")
		hv, ok := r.heldView("s1")
		require.True(t, ok)
		assert.Equal(t, "the layer below is not secured", hv.Reason)

		r.hold(sprint.HoldReq{Names: []string{"s1"}, Release: true, Reason: "secured"})
		r.tick()
		s = r.snap()
		assert.False(t, sprint.StreamHeld(s, "s1"))
		assert.NotEmpty(t, append(onRow(s, "m1", "s1", sprint.Ready), onRow(s, "m2", "s1", sprint.Ready)...), "unhold deals it again")

		r.hold(sprint.HoldReq{Names: []string{"s1"}, Reason: "stop it all", Return: true})
		s = r.snap()
		assert.Empty(t, append(onRow(s, "m1", "s1", sprint.Ready, sprint.Working), onRow(s, "m2", "s1", sprint.Ready, sprint.Working)...),
			"--return withdraws the stream's working cards too")
		r.tick() // the pump applies the work table's queue; a held stream's card is dealt nowhere
		s = r.snap()
		assert.Equal(t, sprint.Ready, s.StateOf(strings.TrimSuffix(working, ".w1")), "its primary is ready again, held by its stream")
	})
	t.Run("friend", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 2)
		r.tick()
		r.startFriends()
		s := r.snap()
		amy := onRow(s, sprint.FriendRow("amy"), "f1", sprint.Working)
		require.Len(t, amy, 1, "a card for any friend goes to the one with the most free width, the first by name among equals")
		r.hold(sprint.HoldReq{Names: []string{"amy"}, Reason: "out of credits"})
		assert.Equal(t, sprint.Held, r.friendStatus("amy"))
		hv, ok := r.heldView("amy")
		require.True(t, ok)
		assert.Equal(t, "out of credits", hv.Reason)
		r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{{ID: "f1-9", Brief: friendsBrief("only friend amy")}}}))
		r.tick()
		s = r.snap()
		assert.Empty(t, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Ready, sprint.Working), "a held friend keeps no begun card and is dealt no new one")
		assert.Equal(t, sprint.Ready, s.StateOf("f1-9"), "her card waits ready while she is held")

		r.hold(sprint.HoldReq{Names: []string{"amy"}, Release: true, Reason: "credits back"})
		assert.Equal(t, sprint.Up, r.friendStatus("amy"))
		r.tick()
		assert.Equal(t, sprint.Working, r.snap().StateOf("f1-9"), "unhold resumes: her card is dealt to her")

		r.hold(sprint.HoldReq{Names: []string{"amy"}, Reason: "away for the night", Return: true})
		s = r.snap()
		assert.Empty(t, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Working), "--return withdraws the cards she holds")
		r.tick()
		assert.Equal(t, sprint.Ready, r.snap().StateOf("f1-9"), "her card waits for her again")
	})
}

// TestHoldAndUnholdServeMembersReadersFriendsAndStreams: the one verb pair names each of
// the four, in one call, all or none; a reader's hold asks it nothing and with --return
// takes back its reads; the reason is in each status and in the log.
func TestHoldAndUnholdServeMembersReadersFriendsAndStreams(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 4, 2)
	r.tick()

	// a name that names nothing refuses the whole call, nothing written
	res, err := r.st.Hold(r.ctx, sprint.HoldReq{Names: []string{"m1", "nobody"}, Reason: "x", Who: "coordinator"})
	require.NoError(t, err)
	require.Len(t, res.Refused, 1)
	assert.Equal(t, "nobody", res.Refused[0].Key)
	assert.Contains(t, res.Refused[0].Why, "names no fleet member, reader, friend or stream")
	assert.Empty(t, r.holdLines())
	assert.Equal(t, sprint.Up, r.snap().MemberCtl("m1").F("status"), "nothing was held")

	// one hold of all four
	r.hold(sprint.HoldReq{Names: []string{"m1", "reader-a", "bob", "s1"}, Reason: "the gocache trim"})
	s := r.snap()
	assert.Equal(t, sprint.Held, sprint.MemberStatus(s.MemberCtl("m1"), sprint.Beat{At: r.st.Now()}, r.st.Now()))
	assert.Equal(t, sprint.Held, sprint.StreamStateText(s.StreamCtl("s1").Fields))
	assert.Equal(t, sprint.Held, r.friendStatus("bob"))
	states, err := r.st.ReaderStates(r.ctx, []string{"reader-a"}, r.st.Now())
	require.NoError(t, err)
	assert.Equal(t, sprint.ReaderHeld, states["reader-a"], "a held reader's state reads held, whatever it beats")
	hs, err := r.st.Holds(r.ctx)
	require.NoError(t, err)
	var kinds []string
	for _, h := range hs {
		kinds = append(kinds, h.Kind+" "+h.Name+": "+h.Reason)
	}
	assert.Equal(t, []string{"member m1: the gocache trim", "reader reader-a: the gocache trim", "friend bob: the gocache trim", "stream s1: the gocache trim"}, kinds)
	assert.Len(t, r.holdLines(), 4, "one line of the log per name held")

	r.hold(sprint.HoldReq{Names: []string{"m1", "reader-a", "bob", "s1"}, Release: true, Reason: "trimmed"})
	hs, err = r.st.Holds(r.ctx)
	require.NoError(t, err)
	assert.Empty(t, hs, "unhold releases all four")
	states, err = r.st.ReaderStates(r.ctx, []string{"reader-a"}, r.st.Now())
	require.NoError(t, err)
	assert.Equal(t, sprint.ReaderUp, states["reader-a"], "released, the reader's state is its beat's")

	// a reader: its reads asked go to another; --return takes back the reads begun
	r.tick()
	pr := strings.TrimSuffix(r.takeOne("m2"), ".w1")
	s = r.snap()
	wc := s.Fleet.Card(s.Work.Card(pr).F("work"))
	r.must(store.FinishStep(sprint.FinishReq{As: "m2", Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: "m2"}))
	// Amy and bob are pro, so each is asked a flash read while she has room
	// (docs/SPEC-SPRINT.md). Hold them so this read stays on a reader.
	r.hold(sprint.HoldReq{Names: []string{"amy", "bob"}, Reason: "the flash read stays with a reader"})
	r.tick()
	reads := r.snap().Readers.Of(pr)
	require.Len(t, reads, 1, "a flash card is read once")
	reader := reads[0].Row
	r.must(store.ReadStep(sprint.ReadReq{As: reader, Begin: true, Sel: sprint.Sel{IDs: []string{reads[0].ID}}, Who: reader}))
	r.hold(sprint.HoldReq{Names: []string{reader}, Reason: "its model rests"})
	r.tick()
	assert.Equal(t, sprint.Reading, r.snap().Readers.Card(reads[0].ID).Col, "a held reader's read begun finishes")
	r.hold(sprint.HoldReq{Names: []string{reader}, Reason: "its model rests all day", Return: true})
	assert.False(t, r.snap().Readers.Card(reads[0].ID).Placed(), "--return takes the read begun back")
	r.tick()
	again := r.snap().Readers.Of(pr)
	require.NotEmpty(t, again)
	for _, rc := range again {
		if rc.Placed() {
			assert.NotEqual(t, reader, rc.Row, "the ask asks another reader")
		}
	}
	lines := r.holdLines()
	assert.Contains(t, lines[len(lines)-1], "reader "+reader+" held: its model rests all day; --return")
}
