package friend

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readLaneStep is the reads through the same lane loop as her cards (read_lanes.go:439-443,
// lane_take.go): each read lane free of a read asks the server `take --as reader-<friend>
// --lane <n>` (LaneTakeArgv), runs exactly the read the server's LANE line names, and reports
// its verdict by the ids the server gave and its lane. These tests drive the daemon with Take
// set (serverLanes() true) and read the argv it sends, the harness runs it starts, and the
// lines it records.

// readLaneServer is the sprint server as a read lane sees it: it routes each take on the --as
// member, answering holds=- for the card member (friend.<name>) and, for reader-<name>, the
// read the lane holds at its generation; the answer carries a MOVED line unless moveless. It
// refuses a lane's ask when refuse names one, and records every ask.
type readLaneServer struct {
	mu       sync.Mutex
	held     map[int]string
	gen      map[int]int
	moveless map[int]bool
	refuse   map[int]error
	sp       *readSprint // when set, a read the queue has marked done is no longer held
	takes    []string
}

func newReadLaneServer() *readLaneServer {
	return &readLaneServer{held: map[int]string{}, gen: map[int]int{}, moveless: map[int]bool{}, refuse: map[int]error{}}
}

func (s *readLaneServer) take(_ context.Context, argv []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.takes = append(s.takes, strings.Join(argv, " "))
	member, n := argv[2], 0
	if len(argv) > 4 {
		n, _ = strconv.Atoi(argv[4])
	}
	hold := func(card string) string {
		if card == "" {
			return fmt.Sprintf("NOTE LANE %s lane=%d epoch=15 holds=-\n", member, n)
		}
		gen := s.gen[n]
		if gen == 0 {
			gen = 1
		}
		return fmt.Sprintf("NOTE LANE %s lane=%d epoch=15 holds=%s@%d kind=read job=%s~15\n", member, n, card, gen, card)
	}
	if member != ReaderOf("bob") { // the card member's lanes are answered nothing here
		return hold(""), nil
	}
	if err := s.refuse[n]; err != nil {
		return "", err
	}
	card := s.held[n]
	if card != "" && s.sp != nil && s.sp.done(card) {
		card = "" // the verdict landed: a real server no longer holds the read on any lane
	}
	if card == "" || s.moveless[n] {
		if card == "" {
			return hold(""), nil
		}
		return "TAKE OK moved=0\n" + hold(card), nil
	}
	return fmt.Sprintf("MOVED %s fleet ready -> working gen=%d lane=%d\nTAKE OK moved=1\n", card, s.gen[n], n) + hold(card), nil
}

func (s *readLaneServer) set(lane int, card string, gen int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held[lane], s.gen[lane] = card, gen
}

func (s *readLaneServer) asks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.takes)
}

// readLaneAsks counts the asks sent to reader-<friend> at a given lane.
func readLaneAsks(asks []string, lane int) int {
	n := 0
	for _, a := range asks {
		if strings.Contains(a, ReaderOf("bob")) && strings.Contains(a, " --lane "+strconv.Itoa(lane)) {
			n++
		}
	}
	return n
}

// readLaneRig is readRig with Take wired, so serverLanes() is true and readStep steps each
// read lane through the server (read_lanes.go:311-313).
func readLaneRig(t *testing.T, h *readHarness, sp *readSprint, srv *readLaneServer, slots int) *rig {
	r := readRig(t, h, sp, slots)
	r.d.Take = srv.take
	srv.sp = sp
	return r
}

// done says the reader queue has the read recorded (a verdict or return landed): a real
// server no longer holds it on any lane.
func (s *readSprint) done(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state[id] == "done"
}

// setQueue swaps the reader queue's answer (readSprint, read_lanes_test.go), for a step that
// later gives a held read its packet.
func (s *readSprint) setQueue(q string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = q
}

func (s *readSprint) queueAsks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, a := range s.argvs {
		if a[0] == "queue" {
			n++
		}
	}
	return n
}

// A take whose answer moved the read to the lane ("MOVED a.w1 ", LaneHold) makes the daemon
// record "took read a.w1@2 (epoch 15)", run exactly that read on the harness, and report its
// verdict at the server's epoch and its lane (read_lanes.go:494-501; the doc's "by the ids the
// server gave").
func TestAReadLaneRunsTheReadItsTakeMovedToIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}},
			verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.set(1, "a.w1", 2)
		r := readLaneRig(t, h, sp, srv, 1)
		r.run(t, 30)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "read lane 1: took read a.w1@2 (epoch 15)")
		assert.Equal(t, []string{"m-pro"}, h.models, "the read ran as a one-shot of the harness")
		oks := sp.verbs("--ok")
		require.Len(t, oks, 1)
		assert.Equal(t, "a.w1", oks[0][4])
		assert.Equal(t, "15", flagValue(oks[0], "--epoch"))
		assert.Equal(t, "1", flagValue(oks[0], "--lane"), "the verdict names the lane the server said")
		assert.Empty(t, sp.verbs("--begin"), "no read lane flow sends read --begin")
	})
}

// A take whose answer carries no MOVED line (the server says it holds the read) makes the
// daemon record "holds" instead of "took" and still run the held read, never sending read
// --begin (read_lanes.go:495-498): a restarted daemon runs what the server says it holds.
func TestAReadLaneRunsAHeldReadAndSaysHolds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}},
			verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.set(1, "a.w1", 2)
		srv.moveless[1] = true
		r := readLaneRig(t, h, sp, srv, 1)
		r.run(t, 30)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "read lane 1: holds read a.w1@2 (epoch 15)")
		assert.NotContains(t, records, "took", "the server's line did not move it")
		require.Len(t, sp.verbs("--ok"), 1, "the held read ran")
		assert.Empty(t, sp.verbs("--begin"), "no read lane flow sends read --begin")
	})
}

// A refused take runs nothing; its refusal is recorded once however many steps it stands, and
// the lane asks again no sooner than TakeRetry later (read_lanes.go:472-476; TakeRetry).
func TestARefusedReaderTakeRunsNothingAndAsksAgainAfterTakeRetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}},
			verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.set(1, "a.w1", 2)
		srv.refuse[1] = &Refused{Why: "take refused: the machine is STOPPED"}
		r := readLaneRig(t, h, sp, srv, 1)
		r.run(t, 40)

		records := strings.Join(r.records, "\n")
		assert.Equal(t, 1, strings.Count(records, "asked the server what it holds: refused"), "said once while it stands: %s", records)
		turns, _, _ := h.got()
		assert.Empty(t, turns, "a refusal runs nothing")
		assert.Empty(t, sp.verbs("--ok"))
		asks := readLaneAsks(srv.asks(), 1)
		assert.GreaterOrEqual(t, asks, 2, "asked again")
		assert.LessOrEqual(t, asks, 40/int(TakeRetry.Seconds())+1, "asked again after TakeRetry, never once a step")
	})
}

// A take answered holds=- runs nothing and records nothing; the lane asks again only after
// LaneIdleAsk (read_lanes.go:477-481; LaneIdleAsk).
func TestAnIdleReaderLaneRunsNothingAndAsksAgainAfterLaneIdleAsk(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}},
			verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer() // every lane holds nothing
		r := readLaneRig(t, h, sp, srv, 1)
		r.run(t, 40)

		records := strings.Join(r.records, "\n")
		assert.NotContains(t, records, "read lane 1", "an idle lane records nothing")
		turns, _, _ := h.got()
		assert.Empty(t, turns)
		assert.Empty(t, sp.verbs("--ok"))
		asks := readLaneAsks(srv.asks(), 1)
		assert.GreaterOrEqual(t, asks, 1)
		assert.LessOrEqual(t, asks, 40/int(LaneIdleAsk.Seconds())+1, "asked again after LaneIdleAsk, never once a step")
	})
}

// A read the server says it holds whose packet the reader queue has not given (missing, or an
// empty brief) records "not begun yet: the reader queue has not given its packet" and runs
// nothing; the queue is read again on the next step, and once it gives the packet the read
// runs (read_lanes.go:487-492).
func TestAHeldReadWithNoPacketWaitsForTheReaderQueue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}},
			verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: `{"epoch":"15","cards":[]}`}
		srv := newReadLaneServer()
		srv.set(1, "a.w1", 2)
		r := readLaneRig(t, h, sp, srv, 1)
		var atBlank int
		r.at[4] = func() { atBlank = sp.queueAsks() } // the queue had not given the packet yet
		r.at[6] = func() { sp.setQueue(askedQueue) }  // now it does
		r.run(t, 30)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "not begun yet: the reader queue has not given its packet")
		assert.GreaterOrEqual(t, atBlank, 2, "the queue is read again on the next step")
		require.Len(t, sp.verbs("--ok"), 1, "once the queue gives the packet, the read runs")
		assert.Equal(t, "a.w1", sp.verbs("--ok")[0][4])
	})
}

// Two read lanes, and the server says lane 2 holds the read lane 1 already runs: it is not run
// twice, and lane 2 records "which another read lane of this daemon runs" (read_lanes.go:482-485).
func TestAReadAnotherReadLaneOfTheDaemonRunsIsNotRunTwice(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}},
			verdicts: map[string]string{"a.w1": okResult}, rblock: make(chan struct{})}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.set(1, "a.w1", 2)
		srv.set(2, "a.w1", 2)
		r := readLaneRig(t, h, sp, srv, 2)
		r.at[10] = func() { close(h.rblock) } // let the one run end
		r.run(t, 30)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "which another read lane of this daemon runs")
		require.Len(t, h.prompts, 1, "the read ran exactly once: %s", records)
	})
}

// With ReadSlots 2, takes go to --lane 1 and --lane 2 only, and a lane running a read is not
// asked (read_lanes.go:447-453; ReadSlots is the row's read slots).
func TestReaderTakesGoToTheReadSlotsAndARunningLaneIsNotAsked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}},
			verdicts: map[string]string{"a.w1": okResult}, rblock: make(chan struct{})}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.set(1, "a.w1", 2) // lane 1 runs a.w1 and stays busy
		r := readLaneRig(t, h, sp, srv, 2)
		r.run(t, 40)

		for _, a := range srv.asks() {
			if !strings.Contains(a, ReaderOf("bob")) {
				continue
			}
			lane := flagValue(strings.Fields(a), "--lane")
			assert.Contains(t, []string{"1", "2"}, lane, "a take names one of the read slots: %s", a)
		}
		assert.Equal(t, 1, readLaneAsks(srv.asks(), 1), "a lane running a read is not asked again")
	})
}

// MachineStopped true: no reader take is sent (read_lanes.go:452; NoLaunchAfterStop).
func TestNoReaderTakeIsSentWhileTheMachineIsStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}},
			verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.set(1, "a.w1", 2)
		r := readLaneRig(t, h, sp, srv, 1)
		r.d.MachineStopped = func() bool { return true }
		r.run(t, 20)

		assert.Empty(t, srv.asks(), "STOPPED: no reader take is sent")
		turns, _, _ := h.got()
		assert.Empty(t, turns, "STOPPED: nothing runs")
	})
}
