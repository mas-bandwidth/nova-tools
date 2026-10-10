package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// takeServer is the sprint server as her daemon sees it: her row's cards by job and column,
// and the machine's word. A take by id moves a ready card to working while the machine runs
// and is refused while it is STOPPED (store/engine.go: StartsWork steps); a take by count
// takes her ready cards. A finish of a card not working is refused, as the server does.
type takeServer struct {
	mu       sync.Mutex
	running  bool
	refuse   map[string]string // job -> why its take by id is refused (a route resting)
	col      map[string]string // job -> ready | working | done
	takes    []string
	finishes []string
}

func (s *takeServer) set(running bool) { s.mu.Lock(); s.running = running; s.mu.Unlock() }

func (s *takeServer) take(_ context.Context, argv []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.takes = append(s.takes, strings.Join(argv, " "))
	if !s.running {
		return errors.New("take refused: REFUSED take: the machine is STOPPED: no new work or read may start")
	}
	if len(argv) > 3 && strings.Contains(argv[3], "@") {
		job := strings.Split(argv[3], "@")[0] + "~15"
		if why := s.refuse[job]; why != "" {
			return errors.New("take refused: REFUSED " + job + ": " + why)
		}
		if s.col[job] != "ready" {
			return errors.New("take refused: REFUSED " + job + ": not in friend.bob ready (it is " + s.col[job] + ")")
		}
		s.col[job] = "working"
		return nil
	}
	for job, c := range s.col {
		if c == "ready" {
			s.col[job] = "working"
		}
	}
	return nil
}

func (s *takeServer) finish(_ context.Context, argv []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishes = append(s.finishes, strings.Join(argv, " "))
	job := strings.Split(argv[3], "@")[0] + "~15"
	if s.col[job] != "working" {
		return errors.New("finish refused: REFUSED " + job + ": not working (it is friend.bob:" + s.col[job] + ")")
	}
	s.col[job] = "done"
	return nil
}

func (s *takeServer) held(context.Context) (Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cards []HeldCard
	for _, job := range slices.Sorted(func(yield func(string) bool) {
		for j := range s.col {
			if !yield(j) {
				return
			}
		}
	}) {
		if c := s.col[job]; c != "done" {
			cards = append(cards, HeldCard{Card: strings.TrimSuffix(job, "~15"), Job: job, Col: c, Kind: "work", Epoch: 15, Brief: "STATUS: nova-sprint card\n"})
		}
	}
	return Row{Cards: cards, Reads: true, From: FromCards}, nil
}

func (s *takeServer) got() (takes []string, cols map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cols = map[string]string{}
	for k, v := range s.col {
		cols[k] = v
	}
	return append([]string(nil), s.takes...), cols
}

// The fault of 2026-10-10 20:14Z: her daemon read the machine RUNNING while the server still
// refused every take (STOPPED), and its lanes ran the cards dealt ready on her row anyway. Now
// NoLaneWithoutTake: no lane turn begins while the take is refused; the refused take changes
// nothing, is asked again after TakeRetry (never once a second), and once the server runs the
// cards are taken and run, each taken exactly once.
func TestALaneNeverStartsACardItsTakeWasRefusedAndTakesItAgainOnALaterPass(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 2)
		srv := &takeServer{col: map[string]string{"c1~15": "ready", "c2~15": "ready"}}
		r.d.Held, r.d.Take, r.d.Finish = srv.held, srv.take, srv.finish
		var turnsWhileRefused []string
		var takesWhileRefused int
		r.at[20] = func() {
			turnsWhileRefused, _, _ = h.got()
			takes, _ := srv.got()
			takesWhileRefused = len(takes)
			srv.set(true)
		}
		r.run(t, 40)

		records := strings.Join(r.records, "\n")
		assert.Empty(t, turnsWhileRefused, "no lane turn while every take is refused (NoLaneWithoutTake): %s", records)
		assert.GreaterOrEqual(t, takesWhileRefused, 2, "a refused take is asked again on a later pass: %s", records)
		assert.LessOrEqual(t, takesWhileRefused, 20/int(TakeRetry.Seconds())+1, "asked again after TakeRetry, never once a second")
		assert.Equal(t, 1, strings.Count(records, "its take was refused"), "the refusal said once while it stands: %s", records)
		assert.NotContains(t, records, "set_aside", "a refused take sets nothing aside")
		turns, _, _ := h.got()
		var ran []string
		for _, tn := range turns {
			_, card, _ := strings.Cut(tn, ": ")
			ran = append(ran, card)
		}
		assert.ElementsMatch(t, []string{"c1", "c2"}, ran, "each card runs once its take is answered: %s", records)
		takes, cols := srv.got()
		assert.Contains(t, takes, "take --as friend.bob c1@1 --epoch 15")
		assert.Contains(t, takes, "take --as friend.bob c2@1 --epoch 15")
		assert.Equal(t, map[string]string{"c1~15": "done", "c2~15": "done"}, cols, "every card run was working on her row, and its finish was taken")
		assert.NotContains(t, records, "finish refused", "no finish of a card the server does not have working")
		assert.Regexp(t, `lane \d: took card c1@1 \(ready -> working on her row\)`, records)
	})
}

// A take refused for one card's own reason (its route resting) holds back that card alone: the
// next card is taken and run after TakeRetry, and the refused one is asked again after
// CardTakeRetry.
func TestARefusedTakeHoldsBackOnlyItsOwnCard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		srv := &takeServer{running: true, col: map[string]string{"c1~15": "ready", "c2~15": "ready"}, refuse: map[string]string{"c1~15": "its route rests"}}
		r.d.Held, r.d.Take, r.d.Finish = srv.held, srv.take, srv.finish
		r.at[15] = func() { srv.mu.Lock(); delete(srv.refuse, "c1~15"); srv.mu.Unlock() }
		r.run(t, 60)
		turns, _, _ := h.got()
		takes, cols := srv.got()
		require.NotEmpty(t, turns, strings.Join(r.records, "\n"))
		assert.Equal(t, "ses_1: c2", turns[0], "c2 runs while c1's take is refused")
		assert.Contains(t, turns, "ses_1: c1", "c1 is taken again once it may be")
		assert.Equal(t, map[string]string{"c1~15": "done", "c2~15": "done"}, cols)
		assert.LessOrEqual(t, strings.Count(strings.Join(takes, "\n"), "c1@1"), 60/int(CardTakeRetry.Seconds())+1, "c1 asked again after CardTakeRetry: %v", takes)
	})
}

// A card the server already has working on her row (her finish took it for her, or a lane
// held it before a restart) is resumed with no take: the server's word, never a local guess.
func TestACardWorkingOnHerRowRunsWithNoTake(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		srv := &takeServer{col: map[string]string{"c1~15": "working"}}
		r.d.Held, r.d.Take = srv.held, srv.take
		r.run(t, 10)
		turns, _, _ := h.got()
		takes, _ := srv.got()
		assert.Equal(t, []string{"ses_1: c1"}, turns)
		assert.Empty(t, takes, "no take for a card already working on her row")
	})
}

// Fix 4: a card a lane set aside whose take or finish the server refused (the incident's 23
// cards: set aside, their lane-written REPORT.md left, still ready on her row) is not
// stranded: its REPORT.md is moved off, it is held aside no longer, and a free lane takes it
// and runs it. A REPORT.md the friend wrote herself is never moved.
func TestACardSetAsideButStillReadyOnHerRowIsTakenAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		for id, report := range map[string]string{
			"c1": "Verdict: FAIL\n\nnova-friend lane 1 of bob finished card c1: the turn exited 125 with no RESULT.md; no pushed head found.\n",
			"c2": "Verdict: HOLD\n\nI stopped here on purpose.\n", // hers
		} {
			out := filepath.Join(dir, "outbox", id+"~15")
			require.NoError(t, os.MkdirAll(out, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(out, "REPORT.md"), []byte(report), 0o644))
			// the ended mark endCard left: while it stands laneHolder refuses every lane the card
			require.NoError(t, endLaneMark(dir, id+"~15", "bob lane 1 (daemon 999.1)"))
		}
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
		r, state := laneRig(t, h, 2)
		state.GivenUp = []string{"c1~15", "c2~15"}
		srv := &takeServer{running: true, col: map[string]string{"c1~15": "ready", "c2~15": "ready"}}
		r.d.Held, r.d.Take = srv.held, srv.take
		r.run(t, 20)

		records := strings.Join(r.records, "\n")
		turns, _, _ := h.got()
		require.Len(t, turns, 1, "the set-aside card is taken and run; hers is left: %s", records)
		assert.True(t, strings.HasSuffix(turns[0], ": c1"), "%v", turns)
		moved, _ := filepath.Glob(filepath.Join(dir, "outbox", "c1~15", "REPORT.md.untaken-*"))
		assert.Len(t, moved, 1, "the lane's own report is moved off before the card is taken again")
		assert.FileExists(t, filepath.Join(dir, "outbox", "c2~15", "REPORT.md"), "a report the friend wrote is never moved")
		assert.Equal(t, []string{"c2~15"}, state.GivenUp, "c1 is held aside no longer")
		assert.Contains(t, records, "lanes: card c1 was set aside but is ready on her row")
		assert.NotContains(t, records, "card c1 refused", "the ended lane mark goes with the set-aside: %s", records)
		_, marked := ReadLaneMark(dir, "c2~15")
		assert.True(t, marked, "a card left aside keeps its mark")
		_, cols := srv.got()
		assert.Equal(t, "working", cols["c1~15"])
	})
}

// Batch mode (a dsh friend opens no session per lane, so her row's one-shot runs in batch): her
// ready cards are taken by her daemon, the server choosing which and cutting to her width,
// never while the machine is STOPPED, a refusal asked again at the next BatchTakeEvery.
func TestABatchDaemonTakesHerReadyCardsAndAsksAgainAfterARefusal(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.passive = true
		srv := &takeServer{col: map[string]string{"c1~15": "ready", "c2~15": "ready"}}
		r.d.Held, r.d.Take = srv.held, srv.take
		r.at[20] = func() { srv.set(true) }
		r.run(t, 40)
		takes, cols := srv.got()
		require.NotEmpty(t, takes)
		assert.Equal(t, "take --as friend.bob --max 2", takes[0])
		assert.LessOrEqual(t, len(takes), 40/int(BatchTakeEvery.Seconds())+1, "paced at BatchTakeEvery: %v", takes)
		assert.Equal(t, map[string]string{"c1~15": "working", "c2~15": "working"}, cols, "taken once the machine runs")
		assert.Equal(t, 1, strings.Count(strings.Join(r.records, "\n"), "ready card(s) not taken"), "the refusal said once")
	})
}

func TestTakeArgvs(t *testing.T) {
	t.Parallel()
	c := Card{ID: "c1", Brief: "/w/inbox/c1~16.g2/BRIEF.md", Outbox: "/w/outbox/c1~16.g2"}
	assert.Equal(t, []string{"take", "--as", "friend.ann", "c1@2", "--epoch", "16"}, LaneTakeArgv("ann", c))
	assert.Equal(t, []string{"take", "--as", "friend.ann", "--max", "3"}, BatchTakeArgv("ann", 3))
}
