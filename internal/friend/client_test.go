package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer is the sprint server's lane records for one friend, in memory: what each lane
// holds and her ready cards, answering take --as friend.<name> --lane <n> --json as
// nova-sprint does (a lane holding a card is answered that card; a free lane takes her next
// ready card; none, holds=-). No sockets, no time, no store.
type fakeServer struct {
	row   string
	epoch uint64
	held  map[int]string // lane -> card
	ready []string
	asks  int
	takes int
	fail  map[int]bool // lanes whose ask the server refuses
	lose  map[int]bool // lanes whose ask commits on the server and whose answer is lost

	asked     []int // the lanes asked, in order
	cardAsks  int
	cardsDown bool // friend cards refused
}

// finish is the model's finish of lane n's card through the server: the lane holds nothing.
func (f *fakeServer) finish(n int) { delete(f.held, n) }

// renewed is the lanes asked since the last call, sorted, and forgets them.
func (f *fakeServer) renewed() []int {
	got := slices.Clone(f.asked)
	slices.Sort(got)
	f.asked = nil
	return got
}

// config is her canonical nova-config row as nova-config friend show --json prints it.
func config(width int) string {
	return fmt.Sprintf(`{"result":{"verb":"friend show","status":"ok","exit":0},"facts":{},"items":[{"kind":"friend","fields":{"name":"amy","tiers":"flash,pro","width":"%d"}}]}`, width)
}

// row is a fake nova-config: the width it answers now, or its failure.
type row struct {
	width   int
	down    bool
	fetches int
}

func (r *row) fetch(_ context.Context, name string) (string, error) {
	r.fetches++
	if r.down {
		return "", errors.New("nova-config: the store did not answer")
	}
	return config(r.width), nil
}

// t0 is the tests' clock start (never 0, so a zero time shows as a timeout).
var t0 = time.Unix(1000, 0)

// client is amy's client on srv at the canonical width cfg answers, applied once.
func client(t *testing.T, srv *fakeServer, cfg *row) *Client {
	t.Helper()
	c := &Client{Name: "amy", Ask: srv.ask, Fetch: cfg.fetch, Dir: t.TempDir()}
	_, changed := c.Apply(context.Background(), t0)
	require.True(t, changed)
	return c
}

func (f *fakeServer) ask(_ context.Context, argv []string) (string, error) {
	if len(argv) == 4 && argv[0] == "friend" && argv[1] == "cards" && argv[3] == "--json" {
		f.cardAsks++
		if f.cardsDown {
			return "", errors.New("friend cards refused: the server is busy")
		}
		a := struct {
			Friend string           `json:"friend"`
			Cards  []map[string]any `json:"cards"`
		}{Friend: argv[2], Cards: []map[string]any{}}
		for l, c := range f.held {
			a.Cards = append(a.Cards, map[string]any{"card": c, "job": c, "col": "working", "lane": l, "tier": "flash", "epoch": f.epoch, "brief": ""})
		}
		b, err := json.Marshal(a)
		return string(b), err
	}
	f.asks++
	if len(argv) >= 5 {
		n, _ := strconv.Atoi(argv[4])
		f.asked = append(f.asked, n)
	}
	if len(argv) < 6 || argv[0] != "take" || argv[1] != "--as" || argv[2] != f.row || argv[3] != "--lane" || argv[5] != "--json" {
		return "", fmt.Errorf("the fake server reads only a lane's take: %q", argv)
	}
	n, _ := strconv.Atoi(argv[4])
	if f.fail[n] {
		return "", errors.New("take refused: the server is busy")
	}
	card, ok := f.held[n]
	if !ok && len(f.ready) > 0 {
		card, f.ready = f.ready[0], f.ready[1:]
		f.held[n] = card
		f.takes++
		ok = true
	}
	if f.lose[n] {
		return "", errors.New("take: the answer timed out")
	}
	a := answer{}
	if !ok {
		a.Says = []string{fmt.Sprintf("LANE %s lane=%d epoch=%d holds=-", f.row, n, f.epoch)}
	} else {
		a.Says = []string{fmt.Sprintf("LANE %s lane=%d epoch=%d holds=%s@1 rank=1 kind=work job=%s~%d", f.row, n, f.epoch, card, card, f.epoch)}
		a.Packets = append(a.Packets, struct {
			Card  string `json:"card"`
			Brief string `json:"brief"`
		}{Card: card, Brief: "the brief of " + card})
	}
	b, err := json.Marshal(a)
	return string(b), err
}

// the cards held across lanes, each once: no card is held by two lanes
func (f *fakeServer) heldOnce(t *testing.T) {
	t.Helper()
	seen := map[string]int{}
	for l, c := range f.held {
		was, ok := seen[c]
		require.False(t, ok, "%s held by lanes %d and %d", c, was, l)
		seen[c] = l
	}
}

func newFake() *fakeServer {
	return &fakeServer{row: "friend.amy", epoch: 3, held: map[int]string{}, ready: []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"}}
}

// The card's TEST: a client killed mid-card and started again in an empty working directory
// resumes from the server's records alone: the same cards at the same generations on the
// same lanes, nothing taken twice, nothing lost, every brief cached again, and the model
// told again what she holds (tla/FriendLaneClient.tla RestartResumes, FinishedOnce).
func TestALaneRestartedFromNothingResumesFromTheServerAlone(t *testing.T) {
	t.Parallel()
	srv := newFake()
	first := client(t, srv, &row{width: 2})
	holds, fresh, errs := first.Round(context.Background(), true)
	require.Empty(t, errs)
	require.Len(t, holds, 2)
	require.Equal(t, holds, fresh, "a first round tells every hold")
	first.Told(fresh)
	require.Equal(t, 2, srv.takes)

	// killed: a new process, its working directory empty
	dir := t.TempDir()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
	again := &Client{Name: "amy", Ask: srv.ask, Fetch: (&row{width: 2}).fetch, Dir: dir}
	again.Apply(context.Background(), t0)
	resumed, retold, errs := again.Round(context.Background(), true)
	require.Empty(t, errs)
	assert.Equal(t, 2, srv.takes, "the restart took nothing new: it resumed what the server holds")
	assert.Equal(t, []string{"s1-3.w1"}, srv.ready, "nothing lost back to ready, nothing taken twice")
	srv.heldOnce(t)
	require.Len(t, resumed, 2)
	for i, h := range resumed {
		assert.Equal(t, holds[i].Lane, h.Lane)
		assert.Equal(t, holds[i].Card, h.Card)
		assert.Equal(t, holds[i].Gen, h.Gen)
		assert.Equal(t, holds[i].Epoch, h.Epoch)
		b, err := os.ReadFile(h.Brief)
		require.NoError(t, err, "the brief is cached again from the server")
		assert.Equal(t, "the brief of "+h.Card, string(b))
		assert.True(t, strings.HasPrefix(h.Brief, dir))
	}
	assert.Equal(t, resumed, retold, "a restarted client tells the model again what she holds")
	again.Told(retold)
	_, none, _ := again.Round(context.Background(), true)
	assert.Empty(t, none, "told once a process life: the next round tells nothing")
}

// A client whose model does not prove asks nothing, so it renews no lease: the server's
// lease returns her cards (tla/FriendLaneClient.tla DeadModelReleases; witness
// MCFriendLaneClientBrokenRenewUnproven.cfg).
func TestAnUnprovenRoundAsksNothing(t *testing.T) {
	t.Parallel()
	srv := newFake()
	c := client(t, srv, &row{width: 8})
	holds, fresh, errs := c.Round(context.Background(), false)
	assert.Empty(t, holds)
	assert.Empty(t, fresh)
	assert.Empty(t, errs)
	assert.Zero(t, srv.asks)
	assert.Zero(t, srv.cardAsks)
}

// Free width and ready cards: each free lane takes one, in order, until one answers empty;
// a lane the server refuses is skipped with its error and stops takes for the round; the
// next round takes on.
func TestARoundTakesToWidthAndStopsAtARefusedLane(t *testing.T) {
	t.Parallel()
	srv := newFake()
	srv.fail = map[int]bool{2: true}
	c := client(t, srv, &row{width: 5})
	holds, _, errs := c.Round(context.Background(), true)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "lane 2")
	assert.Equal(t, []int{1}, lanesOf(holds), "lane 1 took; the refused lane 2 stops takes this round")
	assert.Equal(t, []int{1, 2}, srv.renewed())

	srv.fail = nil
	holds, _, errs = c.Round(context.Background(), true)
	require.Empty(t, errs)
	assert.Equal(t, []int{1, 2, 3}, lanesOf(holds), "three ready cards in all; lane 4 answered empty and the round stopped there")
	assert.Equal(t, []int{1, 2, 3, 4}, srv.renewed(), "lane 1 renewed, 2 and 3 taken, 4 empty; never lane 5")
	srv.heldOnce(t)
}

func lanesOf(hs []Hold) []int {
	var ls []int
	for _, h := range hs {
		ls = append(ls, h.Lane)
	}
	return ls
}

// Grow under active jobs: two lanes working at width 2, the canonical row raised to 4; the
// next round takes into lanes 3 and 4, the working cards keep their lanes and generations,
// nothing is taken twice (tla/FriendLaneClient.tla SetWidth, GrowTakes).
func TestAWidthRaisedUnderActiveJobsTakesOnTheNextRound(t *testing.T) {
	t.Parallel()
	srv := newFake()
	srv.ready = append(srv.ready, "s1-4.w1", "s1-5.w1")
	cfg := &row{width: 2}
	c := client(t, srv, cfg)
	holds, fresh, _ := c.Round(context.Background(), true)
	require.Equal(t, []int{1, 2}, lanesOf(holds))
	c.Told(fresh)
	before := slices.Clone(holds)

	cfg.width = 4
	ch, changed := c.Apply(context.Background(), t0.Add(ConfigEvery))
	require.True(t, changed, "the raised width is a change, said once")
	assert.Equal(t, 4, ch.Config.Width)
	holds, fresh, errs := c.Round(context.Background(), true)
	require.Empty(t, errs)
	assert.Equal(t, []int{1, 2, 3, 4}, lanesOf(holds))
	for i, h := range before {
		assert.Equal(t, h.key(), holds[i].key(), "the working cards keep their lanes, cards, generations and epoch")
		assert.Equal(t, h.Job, holds[i].Job)
	}
	assert.Equal(t, []int{3, 4}, lanesOf(fresh), "only the new lanes' cards are told")
	assert.Equal(t, 4, srv.takes)
	srv.heldOnce(t)
}

// Shrink under active jobs: three lanes working at width 3, the row lowered to 1. Every
// working lane is still asked (renewed) each round, none takes, no card goes back to ready or
// to another lane; as they finish nothing is taken until the held cards are under 1, and then
// one lane takes (tla/FriendLaneClient.tla NoLostCard, NoDuplicateHold, DrainNeverTakes).
func TestAWidthLoweredUnderActiveJobsDrainsWithNoLossAndNoDuplicate(t *testing.T) {
	t.Parallel()
	srv := newFake()
	srv.ready = append(srv.ready, "s1-4.w1")
	cfg := &row{width: 3}
	c := client(t, srv, cfg)
	holds, fresh, _ := c.Round(context.Background(), true)
	require.Equal(t, []int{1, 2, 3}, lanesOf(holds))
	c.Told(fresh)
	srv.renewed()

	cfg.width = 1
	_, changed := c.Apply(context.Background(), t0.Add(ConfigEvery))
	require.True(t, changed)
	holds, fresh, errs := c.Round(context.Background(), true)
	require.Empty(t, errs)
	assert.Equal(t, []int{1, 2, 3}, lanesOf(holds), "every working card still held on its lane")
	assert.Empty(t, fresh, "nothing new told: nothing re-dealt")
	assert.Equal(t, []int{1, 2, 3}, srv.renewed(), "each working lane renewed; no free lane asked")
	assert.Equal(t, 3, srv.takes)
	assert.Equal(t, []string{"s1-4.w1"}, srv.ready, "nothing returned to ready")

	srv.finish(1)
	holds, _, _ = c.Round(context.Background(), true)
	assert.Equal(t, []int{2, 3}, lanesOf(holds))
	assert.Equal(t, []int{2, 3}, srv.renewed(), "two held at width 1: no lane takes")
	srv.finish(3)
	holds, _, _ = c.Round(context.Background(), true)
	assert.Equal(t, []int{2}, lanesOf(holds), "one held at width 1: still no take")
	assert.Equal(t, 3, srv.takes)
	srv.finish(2)
	holds, fresh, _ = c.Round(context.Background(), true)
	assert.Equal(t, []int{1}, lanesOf(holds), "drained under the width: lane 1 takes")
	assert.Equal(t, "s1-4.w1", fresh[0].Card)
	srv.heldOnce(t)
}

// A config fetch that fails closes intake, said once; held lanes are still renewed; when the
// row reads again intake opens on the next round (never a made-up width in between).
func TestAConfigFetchFailureStopsIntakeAndRecovers(t *testing.T) {
	t.Parallel()
	srv := newFake()
	cfg := &row{width: 1}
	c := client(t, srv, cfg)
	holds, fresh, _ := c.Round(context.Background(), true)
	require.Equal(t, []int{1}, lanesOf(holds))
	c.Told(fresh)
	srv.renewed()

	cfg.down = true
	cfg.width = 3
	ch, changed := c.Apply(context.Background(), t0.Add(ConfigEvery))
	require.True(t, changed)
	assert.False(t, ch.OK)
	assert.Contains(t, ch.Why, "did not answer")
	_, again := c.Apply(context.Background(), t0.Add(2*ConfigEvery))
	assert.False(t, again, "the same failure is said once")
	assert.Zero(t, c.Intake())
	holds, _, _ = c.Round(context.Background(), true)
	assert.Equal(t, []int{1}, lanesOf(holds))
	assert.Equal(t, []int{1}, srv.renewed(), "the held lane renewed; no free lane asked")
	assert.Equal(t, 1, srv.takes)

	cfg.down = false
	ch, changed = c.Apply(context.Background(), t0.Add(3*ConfigEvery))
	require.True(t, changed)
	assert.True(t, ch.OK)
	holds, _, _ = c.Round(context.Background(), true)
	assert.Equal(t, []int{1, 2, 3}, lanesOf(holds), "the row reads again: intake to its width")
}

// Apply fetches at most once a ConfigEvery, on the caller's clock, and says a change once.
func TestApplyFetchesOnceASecondAndSaysAChangeOnce(t *testing.T) {
	t.Parallel()
	cfg := &row{width: 2}
	c := &Client{Name: "amy", Fetch: cfg.fetch}
	assert.Zero(t, c.Intake(), "no row applied: nothing takes")
	_, changed := c.Apply(context.Background(), t0)
	assert.True(t, changed)
	assert.Equal(t, 2, c.Intake())
	_, changed = c.Apply(context.Background(), t0.Add(ConfigEvery/2))
	assert.False(t, changed)
	assert.Equal(t, 1, cfg.fetches, "not due: not fetched")
	_, changed = c.Apply(context.Background(), t0.Add(ConfigEvery))
	assert.False(t, changed, "the same row is no change")
	assert.Equal(t, 2, cfg.fetches)
	none := &Client{Name: "amy"}
	ch, changed := none.Apply(context.Background(), t0)
	assert.True(t, changed)
	assert.False(t, ch.OK, "no nova-config read wired: intake closed, never a default")
}

// The row as nova-config prints it; a refusal, another friend's row or no width is no row.
func TestParseConfigNeverMakesUpAWidth(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig("amy", config(3))
	require.NoError(t, err)
	assert.Equal(t, Config{Width: 3}, cfg)
	for _, bad := range []string{
		"FRIEND name=amy width=3",
		`{"result":{"status":"refused","why":["no store named"]}}`,
		`{"result":{"status":"ok"},"items":[{"kind":"friend","fields":{"name":"bob","width":"3"}}]}`,
		`{"result":{"status":"ok"},"items":[{"kind":"friend","fields":{"name":"amy","width":""}}]}`,
		`{"result":{"status":"ok"},"items":[{"kind":"friend","fields":{"name":"amy","width":"0"}}]}`,
	} {
		_, err := ParseConfig("amy", bad)
		assert.Error(t, err, bad)
	}
}

// A held lane is renewed even when friend cards does not answer: the lanes the last round
// found held are asked again.
func TestAHeldLaneIsRenewedWhenFriendCardsDoesNotAnswer(t *testing.T) {
	t.Parallel()
	srv := newFake()
	c := client(t, srv, &row{width: 2})
	c.Round(context.Background(), true)
	srv.renewed()
	srv.cardsDown = true
	srv.ready = nil
	holds, _, errs := c.Round(context.Background(), true)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "friend cards")
	assert.Equal(t, []int{1, 2}, lanesOf(holds))
	assert.Equal(t, []int{1, 2}, srv.renewed())
}

// A client started fresh (it has found no held lane yet) whose friend cards read fails asks
// nothing: it knows no lane to renew, and a blind ask of a free lane would be a take. Its
// held lane is renewed on the first round friend cards answers again (the bound,
// tla/FriendLaneClient.tla RenewBlind; the lease returns the card if the failure outlasts it).
func TestAFreshClientWhoseCardsReadFailsAsksNothingUntilItReads(t *testing.T) {
	t.Parallel()
	srv := newFake()
	srv.held[2] = "s1-9.w1"
	srv.cardsDown = true
	c := client(t, srv, &row{width: 2})
	holds, _, errs := c.Round(context.Background(), true)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "friend cards")
	assert.Empty(t, srv.renewed(), "a blind fresh client asks no lane")
	assert.Empty(t, holds)
	assert.Zero(t, srv.takes)

	srv.cardsDown = false
	holds, _, errs = c.Round(context.Background(), true)
	require.Empty(t, errs)
	assert.Contains(t, srv.renewed(), 2, "friend cards answers: lane 2 renewed")
	assert.Contains(t, lanesOf(holds), 2)
}

// A blind round never takes past her width: with lanes 1-3 held and her width lowered to 1,
// lane 1 finishing and friend cards failing, no lane is asked (an ask of lane 1 would take
// into it, 3 held at width 1), and the last view is kept for the next round
// (tla/FriendLaneClient.tla DrainNeverTakes; witness MCFriendLaneClientBrokenBlindTakes.cfg).
func TestABlindRoundWhileDrainingTakesNothing(t *testing.T) {
	t.Parallel()
	srv := newFake()
	srv.ready = append(srv.ready, "s1-4.w1")
	cfg := &row{width: 3}
	c := client(t, srv, cfg)
	holds, _, _ := c.Round(context.Background(), true)
	require.Equal(t, []int{1, 2, 3}, lanesOf(holds))
	srv.renewed()
	cfg.width = 1
	_, changed := c.Apply(context.Background(), t0.Add(ConfigEvery))
	require.True(t, changed)

	srv.finish(1)
	srv.cardsDown = true
	_, _, errs := c.Round(context.Background(), true)
	require.Len(t, errs, 1)
	assert.Empty(t, srv.renewed(), "draining and blind: no lane asked")
	assert.Equal(t, 3, srv.takes, "nothing taken into lane 1")
	assert.Equal(t, []string{"s1-4.w1"}, srv.ready)

	srv.cardsDown = false
	holds, _, _ = c.Round(context.Background(), true)
	assert.Equal(t, []int{2, 3}, lanesOf(holds), "cards reads again: the held lanes renewed, still no take")
	assert.Equal(t, 3, srv.takes)
}

// A free lane's take that commits on the server but whose answer is lost stays in the view:
// with the width then lowered and friend cards failing, the blind round counts that lane and
// takes nothing (tla/FriendLaneClient.tla TakeAnswerLost; the view a superset of the server's).
func TestATakeWhoseAnswerIsLostStaysInTheView(t *testing.T) {
	t.Parallel()
	srv := newFake()
	srv.ready = append(srv.ready, "s1-4.w1")
	srv.lose = map[int]bool{2: true}
	cfg := &row{width: 2}
	c := client(t, srv, cfg)
	_, _, errs := c.Round(context.Background(), true)
	require.Len(t, errs, 1, "lane 2's answer lost")
	require.Equal(t, map[int]string{1: "s1-1.w1", 2: "s1-2.w1"}, srv.held, "both takes committed on the server")
	srv.renewed()
	srv.lose = nil

	cfg.width = 1
	_, changed := c.Apply(context.Background(), t0.Add(ConfigEvery))
	require.True(t, changed)
	srv.finish(1)
	srv.cardsDown = true
	c.Round(context.Background(), true)
	assert.Empty(t, srv.renewed(), "two lanes in the view at width 1: the blind round asks nothing")
	assert.Equal(t, 2, srv.takes, "nothing taken into lane 1")
}

// A blind round takes nothing while her row is unread (intake 0), even on a lane it found
// held that finished since.
func TestABlindRoundWithTheRowUnreadTakesNothing(t *testing.T) {
	t.Parallel()
	srv := newFake()
	cfg := &row{width: 1}
	c := client(t, srv, cfg)
	holds, _, _ := c.Round(context.Background(), true)
	require.Equal(t, []int{1}, lanesOf(holds))
	srv.renewed()
	cfg.down = true
	_, changed := c.Apply(context.Background(), t0.Add(ConfigEvery))
	require.True(t, changed)
	require.Zero(t, c.Intake())

	srv.finish(1)
	srv.cardsDown = true
	c.Round(context.Background(), true)
	assert.Empty(t, srv.renewed(), "row unread and blind: no lane asked")
	assert.Equal(t, 1, srv.takes, "nothing taken")
}

// The one notice: how many were taken, the briefs, and the server's finish verb for each
// card she holds, by the ids, epoch and lane the server gave.
func TestTheNoticeNamesTheBriefsAndTheServersVerbs(t *testing.T) {
	t.Parallel()
	srv := newFake()
	c := client(t, srv, &row{width: 2})
	holds, fresh, errs := c.Round(context.Background(), true)
	require.Empty(t, errs)
	subject, body := c.Notice(fresh, holds)
	assert.Equal(t, "SPRINT CARDS n=2 as=friend.amy", subject)
	assert.True(t, strings.HasPrefix(body, "2 cards taken; briefs at "+fresh[0].Brief+", "+fresh[1].Brief+"; work each in a child agent to width.\n"), body)
	assert.Contains(t, body, "- lane 1 s1-1.w1 (job s1-1.w1~3): nova-sprint finish --as friend.amy s1-1.w1@1 --epoch 3 --lane 1 (--head <commit> | --failed)")
	assert.Contains(t, body, "nova-sprint progress --as friend.amy s1-2.w1@1 --epoch 3 --lane 2")
	assert.NotContains(t, body, "tier", "the notice names no tier, route or model")
}

// An answer that is not the take verb's JSON, or names another lane, runs nothing.
func TestAnAnswerTheClientCannotReadHoldsNothing(t *testing.T) {
	t.Parallel()
	cards := `{"friend":"amy","cards":[]}`
	c := &Client{Name: "amy", Fetch: (&row{width: 1}).fetch, Dir: t.TempDir(), Ask: func(_ context.Context, argv []string) (string, error) {
		if argv[0] == "friend" {
			return cards, nil
		}
		return "TAKE OK", nil
	}}
	c.Apply(context.Background(), t0)
	holds, _, errs := c.Round(context.Background(), true)
	assert.Empty(t, holds)
	require.Len(t, errs, 1)
	c.Ask = func(_ context.Context, argv []string) (string, error) {
		if argv[0] == "friend" {
			return cards, nil
		}
		return `{"says":["LANE friend.amy lane=2 epoch=1 holds=s1-1.w1@1 kind=work job=s1-1.w1"]}`, nil
	}
	holds, _, errs = c.Round(context.Background(), true)
	assert.Empty(t, holds)
	require.Len(t, errs, 1)
}
