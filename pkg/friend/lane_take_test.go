package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// laneServer is the sprint server as her lanes see it (nova-sprint take --lane, lane_hold.go):
// her row's cards by job, each ready, working or done and the lane that holds it, at epoch 15,
// and the machine's word. A lane's take answers the card the lane holds, else gives it a
// working card no lane holds, else her next ready card, and says it on its LANE line; a
// progress or finish naming another lane's card, or an epoch not the server's, is refused.
type laneServer struct {
	mu       sync.Mutex
	running  bool
	moveless bool              // the server before the fix: a take answers OK, moves nothing and says no LANE line
	col      map[string]string // job -> ready | working | done
	lane     map[string]int    // job -> the lane that holds it
	takes    []string
	reports  []string
}

func newLaneServer(running bool, cols map[string]string) *laneServer {
	return &laneServer{running: running, col: cols, lane: map[string]int{}}
}

func (s *laneServer) set(running bool) { s.mu.Lock(); s.running = running; s.mu.Unlock() }

func (s *laneServer) jobs() []string {
	return slices.Sorted(func(yield func(string) bool) {
		for j := range s.col {
			if !yield(j) {
				return
			}
		}
	})
}

func (s *laneServer) take(_ context.Context, argv []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.takes = append(s.takes, strings.Join(argv, " "))
	if !slices.Contains(argv, "--lane") {
		return "", &Refused{Why: "take refused: this server serves a lane's take only"}
	}
	if !s.running {
		return "", &Refused{Why: "take refused: REFUSED take: the machine is STOPPED: no new work or read may start"}
	}
	if s.moveless {
		return "TAKE OK moved=0 refused=0 notes=0\n", nil
	}
	member, n := argv[2], 0
	n, _ = strconv.Atoi(argv[4])
	line := func(job string) string {
		if job == "" {
			return fmt.Sprintf("NOTE LANE %s lane=%d epoch=15 holds=-\n", member, n)
		}
		return fmt.Sprintf("NOTE LANE %s lane=%d epoch=15 holds=%s@1 kind=work job=%s\n", member, n, strings.TrimSuffix(job, "~15"), job)
	}
	for _, job := range s.jobs() {
		if s.col[job] == "working" && s.lane[job] == n {
			return "TAKE OK moved=0\n" + line(job), nil
		}
	}
	for _, job := range s.jobs() {
		if s.col[job] == "working" && s.lane[job] == 0 {
			s.lane[job] = n
			return fmt.Sprintf("MOVED %s friend.bob:working -> lane %d (working, no lane held it)\nTAKE OK moved=1\n", strings.TrimSuffix(job, "~15"), n) + line(job), nil
		}
	}
	for _, job := range s.jobs() {
		if s.col[job] == "ready" {
			s.col[job], s.lane[job] = "working", n
			return fmt.Sprintf("MOVED %s fleet ready -> working member=friend.bob gen=1 lane=%d\nTAKE OK moved=1\n", strings.TrimSuffix(job, "~15"), n) + line(job), nil
		}
	}
	return "TAKE OK moved=0\n" + line(""), nil
}

// report is a progress or a finish: refused unless the card is working on the lane it names
// at the server's epoch.
func (s *laneServer) report(_ context.Context, argv []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, strings.Join(argv, " "))
	id, _, _ := strings.Cut(argv[3], "@")
	job := id + "~15"
	epoch, lane := "", 0
	for i, w := range argv {
		switch w {
		case "--epoch":
			epoch = argv[i+1]
		case "--lane":
			lane, _ = strconv.Atoi(argv[i+1])
		}
	}
	switch {
	case epoch != "15":
		return &Refused{Why: argv[0] + " refused: REFUSED epoch " + epoch + ": this step holds epoch " + epoch + ", which is unknown to this sprint"}
	case s.col[job] != "working":
		return &Refused{Why: argv[0] + " refused: REFUSED " + id + ": not working (it is friend.bob:" + s.col[job] + ")"}
	case lane != 0 && s.lane[job] != lane:
		return &Refused{Why: fmt.Sprintf("%s refused: REFUSED %s: held by lane %d of friend.bob, not lane %d", argv[0], id, s.lane[job], lane)}
	}
	if argv[0] == "finish" {
		s.col[job], s.lane[job] = "done", 0
	}
	return nil
}

func (s *laneServer) held(context.Context) (Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cards []HeldCard
	for _, job := range s.jobs() {
		if c := s.col[job]; c != "done" {
			cards = append(cards, HeldCard{Card: strings.TrimSuffix(job, "~15"), Job: job, Col: c, Kind: "work", Epoch: 15, Lane: s.lane[job], Brief: "STATUS: nova-sprint card\n"})
		}
	}
	return Row{Cards: cards, Reads: true, From: FromCards}, nil
}

func (s *laneServer) got() (takes, reports []string, cols map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cols = map[string]string{}
	for k, v := range s.col {
		cols[k] = v
	}
	return slices.Clone(s.takes), slices.Clone(s.reports), cols
}

func (s *laneServer) wire(r *rig) {
	r.d.Held, r.d.Take, r.d.Finish = s.held, s.take, s.report
	r.d.Progress = func(ctx context.Context, cards []Card) error {
		for _, argv := range ProgressArgv("bob", cards) {
			if err := s.report(ctx, argv); err != nil {
				return err
			}
		}
		return nil
	}
}

func ranCards(turns []string) []string {
	var ran []string
	for _, tn := range turns {
		_, card, _ := strings.Cut(tn, ": ")
		ran = append(ran, card)
	}
	return ran
}

// Each lane asks the server what it holds, naming her row and its lane and nothing else, runs
// exactly the card the answer names, and reports by the ids the server gave and its lane.
func TestALaneRunsExactlyWhatTheServerSaysItHolds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 2)
		srv := newLaneServer(true, map[string]string{"c1~15": "ready", "c2~15": "ready"})
		srv.wire(r)
		r.run(t, 30)

		records := strings.Join(r.records, "\n")
		turns, _, _ := h.got()
		assert.ElementsMatch(t, []string{"c1", "c2"}, ranCards(turns), records)
		takes, reports, cols := srv.got()
		require.NotEmpty(t, takes)
		for _, tk := range takes {
			assert.Regexp(t, `^take --as friend\.bob --lane \d+$`, tk, "a lane's ask names her row and its lane and nothing else")
		}
		assert.Equal(t, map[string]string{"c1~15": "done", "c2~15": "done"}, cols)
		for _, rep := range reports {
			assert.Contains(t, rep, "--epoch 15", "reported at the server's epoch")
			assert.Regexp(t, `--lane \d`, rep, "reported by its lane")
		}
		assert.Regexp(t, `lane \d: took card c\d@1 \(kind work, epoch 15\): the server says lane \d holds it`, records)
		assert.NotRegexp(t, `(take|progress|finish) refused`, records, "no report or ask the server refused")
	})
}

// The fault of 2026-10-10 21:42Z on a one-shot friend host, red before the fix: the take answered OK and
// moved nothing, her daemon logged "took card ... (ready -> working on the friend's row)" and ran the
// card while the server still showed it ready, and every progress after it was refused. A
// lane runs a card only when the server's LANE line names it.
func TestATakeAnsweredOKWithNoLaneLineRunsNothingAndSaysNoTook(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		srv := newLaneServer(true, map[string]string{"c1~15": "ready"})
		srv.moveless = true
		srv.wire(r)
		r.run(t, 30)

		records := strings.Join(r.records, "\n")
		turns, _, _ := h.got()
		assert.Empty(t, turns, "nothing runs on an answer that names no card: %s", records)
		assert.NotContains(t, records, "took", records)
		assert.Equal(t, 1, strings.Count(records, "the server answered with no LANE line"), "the refusal said once while it stands: %s", records)
		takes, _, cols := srv.got()
		assert.GreaterOrEqual(t, len(takes), 2, "asked again")
		assert.LessOrEqual(t, len(takes), 30/int(TakeRetry.Seconds())+1, "asked again after TakeRetry, never once a step")
		assert.Equal(t, "ready", cols["c1~15"])
	})
}

// The second fault of 2026-10-10, red before the fix: a job directory that names no epoch
// (<id>, not <id>~<epoch>) made every progress and finish say --epoch 0, and the server
// refused them all. A lane's card reports at the epoch and generation the server's answer
// named, and its lane.
func TestALanesCardReportsAtTheServersEpochNeverItsDirectorysGuess(t *testing.T) {
	t.Parallel()
	c := Card{ID: "c1.w1", Brief: "/w/inbox/c1.w1/BRIEF.md", Outbox: "/w/outbox/c1.w1", Lane: 3, ServerEpoch: "16", ServerGen: 2}
	assert.Equal(t, "16", c.Epoch())
	assert.Equal(t, 2, c.Gen())
	assert.Equal(t, [][]string{{"progress", "--as", "friend.ann", "c1.w1@2", "--epoch", "16", "--lane", "3"}}, ProgressArgv("ann", []Card{c}))
	finish := FinishArgv("ann", c, "Verdict: FAIL\n\nno report\n", "", "")
	assert.Equal(t, []string{"finish", "--as", "friend.ann", "c1.w1@2", "--epoch", "16", "--failed", "--lane", "3"}, finish[:9])
	// a card no lane holds (batch mode, friend sync's) keeps its directory's word
	legacy := Card{ID: "c2", Outbox: "/w/outbox/c2~15.g3"}
	assert.Equal(t, "15", legacy.Epoch())
	assert.Equal(t, 3, legacy.Gen())
}

// A refused ask runs nothing and changes nothing: asked again after TakeRetry, said once while
// it stands; once the server runs, the lane runs what it is given.
func TestARefusedAskRunsNothingAndTheLaneAsksAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		srv := newLaneServer(false, map[string]string{"c1~15": "ready"})
		srv.wire(r)
		var turnsWhileRefused []string
		r.at[20] = func() { turnsWhileRefused, _, _ = h.got(); srv.set(true) }
		r.run(t, 40)
		records := strings.Join(r.records, "\n")
		assert.Empty(t, turnsWhileRefused, records)
		assert.Equal(t, 1, strings.Count(records, "the machine is STOPPED"), "said once: %s", records)
		turns, _, _ := h.got()
		assert.Equal(t, []string{"c1"}, ranCards(turns), records)
	})
}

// After a crash, a restart or a clear, nothing local decides what a lane holds: the lane asks
// the server and continues what it says, and what lanes.json says a lane began or set aside is
// never finished, run or kept on its word.
func TestARestartedLaneContinuesWhatTheServerSaysItHolds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		// the daemon before the restart: lane 1 began c2 and set c1 aside; the server says lane 1
		// holds c1, and c2 is another lane's
		state.Started = map[string]Started{"c2~15": {Lane: 1, Card: Card{ID: "c2", Brief: filepath.Join(dir, "inbox", "c2~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c2~15")}}}
		state.GivenUp = []string{"c1~15"}
		srv := newLaneServer(true, map[string]string{"c1~15": "working", "c2~15": "working"})
		srv.lane["c1~15"], srv.lane["c2~15"] = 1, 7
		srv.wire(r)
		r.run(t, 20)

		records := strings.Join(r.records, "\n")
		turns, _, _ := h.got()
		assert.Equal(t, []string{"c1"}, ranCards(turns), "lane 1 continues c1, the server's word: %s", records)
		assert.Regexp(t, `lane 1: holds card c1@1 \(kind work, epoch 15\)`, records)
		assert.NotContains(t, records, "its run is gone", "nothing kept locally is finished on its own word")
		_, reports, cols := srv.got()
		for _, rep := range reports {
			assert.NotContains(t, rep, "c2", "c2 is lane 7's: this daemon reports nothing on it")
		}
		assert.Equal(t, "working", cols["c2~15"])
		assert.Empty(t, state.Started, "lanes.json keeps no card a lane began")
		assert.Empty(t, state.GivenUp, "lanes.json keeps no card a lane set aside")
	})
}

// A progress the server refuses (the card bounced back, or another lane holds it) drops the
// job: the lane's run is stopped, nothing is finished by it, and the lane asks again.
func TestARefusedProgressDropsTheLanesJob(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}, block: make(chan struct{})}
		r, _ := laneRig(t, h, 1)
		srv := newLaneServer(true, map[string]string{"c1~15": "ready"})
		srv.wire(r)
		r.d.Held = nil // her row is not read: the refusal alone tells the lane
		r.at[30] = func() { srv.mu.Lock(); srv.lane["c1~15"] = 9; srv.mu.Unlock() }
		r.run(t, int(ProgressEvery.Seconds())+60)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "lane 1: card c1 dropped: the server refused its progress", records)
		assert.Contains(t, records, "held by lane 9 of friend.bob, not lane 1")
		_, reports, cols := srv.got()
		for _, rep := range reports {
			assert.False(t, strings.HasPrefix(rep, "finish"), "nothing is finished by a lane that dropped its job: %v", reports)
		}
		assert.Equal(t, "working", cols["c1~15"])
	})
}

// The LANE line is read for the lane that asked: no line, another member's or lane's, or one
// naming no card at a generation is a refusal.
func TestParseLaneHold(t *testing.T) {
	t.Parallel()
	h, err := ParseLaneHold("friend.ann", 2, "MOVED c1 fleet ready -> working member=friend.ann gen=1 lane=2\nTAKE OK moved=1\nNOTE LANE friend.ann lane=2 epoch=16 holds=c1@1 kind=work job=c1~16\n")
	require.NoError(t, err)
	assert.Equal(t, LaneHold{Lane: 2, Epoch: 16, Card: "c1", Gen: 1, Kind: "work", Job: "c1~16"}, h)
	h, err = ParseLaneHold("friend.ann", 2, "TAKE OK moved=0\nLANE friend.ann lane=2 epoch=16 holds=-\n")
	require.NoError(t, err)
	assert.Equal(t, LaneHold{Lane: 2, Epoch: 16}, h)
	for _, out := range []string{
		"TAKE OK moved=0 refused=0 notes=0\n",
		"LANE friend.bob lane=2 epoch=16 holds=-\n",
		"LANE friend.ann lane=3 epoch=16 holds=-\n",
		"LANE friend.ann lane=2 epoch=16 holds=c1 kind=work job=c1~16\n",
		"LANE friend.ann lane=2 epoch=16 holds=c1@1\n",
	} {
		_, err := ParseLaneHold("friend.ann", 2, out)
		var refused *Refused
		assert.ErrorAs(t, err, &refused, out)
	}
	assert.Equal(t, []string{"take", "--as", "friend.ann", "--lane", "4"}, LaneTakeArgv("friend.ann", 4))
}

var _ = os.Remove // the fixtures write the files the lanes read

// A read of her row asked before a lane was given its card says nothing of that card: it still
// shows it ready, the take came after. The lane is never dropped on such a read, only on one
// asked after its take (OneRequestApart: lane and server differ by at most one request).
func TestARowReadOlderThanTheTakeNeverDropsTheLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}, block: make(chan struct{})}
		r, _ := laneRig(t, h, 1)
		srv := newLaneServer(true, map[string]string{"c1~15": "ready"})
		srv.wire(r)
		first, _ := srv.held(context.Background()) // her row as the server says it before any take: c1 ready
		asked := false
		r.d.Held = func(context.Context) (Row, error) {
			if asked {
				return Row{}, ErrNotDue // no later read comes back
			}
			asked = true
			return first, nil
		}
		r.run(t, 30)
		records := strings.Join(r.records, "\n")
		turns, _, _ := h.got()
		assert.Equal(t, []string{"c1"}, ranCards(turns), records)
		assert.NotContains(t, records, "dropped", "a read older than the take is not the server disagreeing: %s", records)
	})
}

// A lane the server says holds a card whose outbox has the report of a run before, which no
// step finishes (here a RESULT.md and no REPORT.md: a run cut between the two writes), is not
// held there forever: past ReportWait the lane finishes it failed itself, by its lane, and the
// card leaves it (the reader's finding: before, the lane waited and was bounced and took it
// again, round and round).
func TestALanesStaleReportIsFinishedByTheLaneAfterReportWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		out := filepath.Join(dir, "outbox", "c1~15")
		require.NoError(t, os.MkdirAll(out, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: c1\n"), 0o644))
		h := &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		srv := newLaneServer(true, map[string]string{"c1~15": "working"})
		srv.lane["c1~15"] = 1
		srv.wire(r)
		r.run(t, int(ReportWait.Seconds())+40)

		records := strings.Join(r.records, "\n")
		turns, _, _ := h.got()
		assert.Empty(t, turns, "the card is not run again over its own report: %s", records)
		_, reports, cols := srv.got()
		assert.Equal(t, "done", cols["c1~15"], "finished: %v\n%s", reports, records)
		require.NotEmpty(t, reports)
		assert.Contains(t, reports[len(reports)-1], "finish --as friend.bob c1@1 --epoch 15 --failed")
		assert.Contains(t, reports[len(reports)-1], "--lane 1")
		assert.Contains(t, records, "finished failed by lane 1")
		assert.FileExists(t, filepath.Join(out, "REPORT.md"), "the lane wrote the report it finished by")
	})
}

// The outbox step finishes a lane's card naming the lane and the server's epoch and generation.
func TestTheOutboxFinishNamesTheLane(t *testing.T) {
	t.Parallel()
	c := Card{ID: "c1", Outbox: "/w/outbox/c1", Lane: 4, ServerEpoch: "16", ServerGen: 2}
	argv := OutboxFinishArgv("ann", c, "FAIL", "", "", "Verdict: FAIL\n\nno\n")
	assert.Equal(t, []string{"finish", "--as", "friend.ann", "c1@2", "--epoch", "16", "--failed", "--lane", "4"}, argv[:9])
}

// A report with a Verdict is the outbox step's alone, however long it waits: the lane never
// finishes it failed (both readers' finding: a good LAND must never be thrown away as a
// failure), whatever its wait; only the outbox step's own LAND, at its head, is sent.
func TestALaneNeverFinishesAReportWithAVerdict(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		out := filepath.Join(dir, "outbox", "c1~15")
		require.NoError(t, os.MkdirAll(out, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(out, "REPORT.md"), []byte("Verdict: LAND\nHead: "+strings.Repeat("a", 40)+"\n\nDone.\n"), 0o644))
		h := &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		srv := newLaneServer(true, map[string]string{"c1~15": "working"})
		srv.lane["c1~15"] = 1
		srv.wire(r)
		r.d.Tip = func(context.Context, string, string) (string, error) {
			return "", errors.New("ls-remote: the network is down")
		}
		r.run(t, int(ReportWait.Seconds())*3)
		records := strings.Join(r.records, "\n")
		turns, _, _ := h.got()
		assert.Empty(t, turns, records)
		_, reports, _ := srv.got()
		for _, rep := range reports {
			if strings.HasPrefix(rep, "finish") {
				assert.NotContains(t, rep, "--failed", "a LAND is never finished failed: %s", rep)
				assert.Contains(t, rep, "--head "+strings.Repeat("a", 40), "only the outbox step's own LAND, at its head")
			}
		}
		assert.NotContains(t, records, "finished failed by lane", records)
	})
}
