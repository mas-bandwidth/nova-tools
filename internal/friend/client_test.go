package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

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
}

func (f *fakeServer) ask(_ context.Context, argv []string) (string, error) {
	f.asks++
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
	first := &Client{Name: "amy", Width: 2, Ask: srv.ask, Dir: t.TempDir()}
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
	again := &Client{Name: "amy", Width: 2, Ask: srv.ask, Dir: dir}
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
	c := &Client{Name: "amy", Width: 8, Ask: srv.ask, Dir: t.TempDir()}
	holds, fresh, errs := c.Round(context.Background(), false)
	assert.Empty(t, holds)
	assert.Empty(t, fresh)
	assert.Empty(t, errs)
	assert.Zero(t, srv.asks)
}

// Free width and ready cards: each free lane takes one; past her ready cards a lane holds
// nothing; a lane the server refuses is skipped this round with its error, the rest go on.
func TestARoundTakesToWidthAndSkipsARefusedLane(t *testing.T) {
	t.Parallel()
	srv := newFake()
	srv.fail = map[int]bool{2: true}
	c := &Client{Name: "amy", Width: 5, Ask: srv.ask, Dir: t.TempDir()}
	holds, _, errs := c.Round(context.Background(), true)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "lane 2")
	var lanes []int
	for _, h := range holds {
		lanes = append(lanes, h.Lane)
	}
	assert.Equal(t, []int{1, 3, 4}, lanes, "three ready cards on the free lanes 1, 3 and 4; lane 5 holds nothing")
	srv.heldOnce(t)
}

// The one notice: how many were taken, the briefs, and the server's finish verb for each
// card she holds, by the ids, epoch and lane the server gave.
func TestTheNoticeNamesTheBriefsAndTheServersVerbs(t *testing.T) {
	t.Parallel()
	srv := newFake()
	c := &Client{Name: "amy", Width: 2, Ask: srv.ask, Dir: t.TempDir()}
	holds, fresh, errs := c.Round(context.Background(), true)
	require.Empty(t, errs)
	subject, body := c.Notice(fresh, holds)
	assert.Equal(t, "SPRINT CARDS n=2 as=friend.amy", subject)
	assert.True(t, strings.HasPrefix(body, "2 cards taken; briefs at "+fresh[0].Brief+", "+fresh[1].Brief+"; work each in a child agent to width.\n"), body)
	assert.Contains(t, body, "nova-sprint finish --as friend.amy s1-1.w1@1 --epoch 3 --lane 1 (--head <commit> | --failed)")
	assert.Contains(t, body, "nova-sprint progress --as friend.amy s1-2.w1@1 --epoch 3 --lane 2")
}

// An answer that is not the take verb's JSON, or names another lane, runs nothing.
func TestAnAnswerTheClientCannotReadHoldsNothing(t *testing.T) {
	t.Parallel()
	c := &Client{Name: "amy", Width: 1, Dir: t.TempDir(), Ask: func(context.Context, []string) (string, error) { return "TAKE OK", nil }}
	holds, _, errs := c.Round(context.Background(), true)
	assert.Empty(t, holds)
	require.Len(t, errs, 1)
	c.Ask = func(context.Context, []string) (string, error) {
		return `{"says":["LANE friend.amy lane=2 epoch=1 holds=s1-1.w1@1 kind=work job=s1-1.w1"]}`, nil
	}
	holds, _, errs = c.Round(context.Background(), true)
	assert.Empty(t, holds)
	require.Len(t, errs, 1)
}
