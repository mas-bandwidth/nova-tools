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

// readLaneStep (read_lanes.go:444) takes the reads through the same lane loop as her cards
// (lane_take.go): each free read lane asks the server `take --as reader-<friend> --lane <n>`,
// which answers the read the lane holds, else begins her next asked read on it; the lane runs
// exactly that read and reports its verdict by the ids the server gave and its lane.

// readLaneServer is the sprint server as a reader's read lanes see it: a reader variant of
// laneServer (lane_take_test.go) that routes on the --as member, answering the card member
// friend-<friend> holds=- so the card lanes run nothing and each read lane n the holds= line
// its test scripted. It records every take it is sent.
type readLaneServer struct {
	mu     sync.Mutex
	holds  map[int]string // lane -> the holds= token of the LANE line ("a.w1@2", or "" for none)
	jobs   map[int]string
	kinds  map[int]string
	moved  map[int]bool // lane -> the answer carries a MOVED line for its card ("took")
	once   map[int]bool // lane -> the hold is answered once, then the lane is idle again
	refuse map[int]error
	stop   bool
	argvs  [][]string
}

func newReadLaneServer() *readLaneServer {
	return &readLaneServer{holds: map[int]string{}, jobs: map[int]string{}, kinds: map[int]string{}, moved: map[int]bool{}, once: map[int]bool{}, refuse: map[int]error{}}
}

// hold scripts lane n to answer the card at its generation, its kind and its job.
func (s *readLaneServer) hold(n int, card, job, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holds[n], s.jobs[n], s.kinds[n] = card, job, kind
}

func (s *readLaneServer) take(_ context.Context, argv []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.argvs = append(s.argvs, slices.Clone(argv))
	member := argv[2]
	n, _ := strconv.Atoi(argv[4])
	if member != ReaderOf("bob") {
		return fmt.Sprintf("TAKE OK moved=0\nLANE %s lane=%d epoch=15 holds=-\n", member, n), nil
	}
	if s.stop {
		return "", &Refused{Why: "take refused: REFUSED take: the machine is STOPPED: no new work or read may start"}
	}
	if err := s.refuse[n]; err != nil {
		return "", err
	}
	hold := s.holds[n]
	if hold == "" {
		return fmt.Sprintf("TAKE OK moved=0\nLANE %s lane=%d epoch=15 holds=-\n", member, n), nil
	}
	out := fmt.Sprintf("TAKE OK moved=0\nLANE %s lane=%d epoch=15 holds=%s kind=%s job=%s\n", member, n, hold, s.kinds[n], s.jobs[n])
	if s.moved[n] {
		card, _, _ := strings.Cut(hold, "@")
		out = fmt.Sprintf("MOVED %s fleet ready -> working member=%s gen=1 lane=%d\n", card, member, n) + out
	}
	if s.once[n] {
		s.holds[n] = ""
	}
	return out, nil
}

// readerTakes is every take whose worker is the reader row, the read lanes' own asks.
func (s *readLaneServer) readerTakes() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]string
	for _, a := range s.argvs {
		if len(a) >= 3 && a[2] == ReaderOf("bob") {
			out = append(out, a)
		}
	}
	return out
}

func (s *readLaneServer) readerLanes() []int {
	var lanes []int
	for _, a := range s.readerTakes() {
		n, _ := strconv.Atoi(flagValue(a, "--lane"))
		lanes = append(lanes, n)
	}
	return lanes
}

// readLaneRig is readRig (read_lanes_test.go) with the reader's take wired to a read lane
// server, so serverLanes() is true and readStep takes the read lane path.
func readLaneRig(t *testing.T, h *readHarness, sp *readSprint, srv *readLaneServer, slots int) *rig {
	r := readRig(t, h, sp, slots)
	r.d.Take = srv.take
	return r
}

// queueWithZ asks z.w1 with a packet, for a read lane that was answered z.w1 while the queue
// gave it no brief.
const queueWithZ = `{"epoch":"15","cards":[
 {"id":"z.w1","col":"asked","packet":{"tier":"pro","head":"zzz","work_branch":"sprint/z","attempt":1,"brief":"REPO: o/r\nBASE: main\n","report":"Verdict: LAND"}}]}`

// read_lanes.go:439-443: a read lane answered a card with a MOVED line says the server took
// it, runs exactly that read on the harness, and its verdict names the lane and the epoch the
// server gave.
func TestAReadLaneRunsTheReadTheServerSaysItTook(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.hold(1, "a.w1@2", "a.w1~15", "read")
		srv.moved[1], srv.once[1] = true, true
		r := readLaneRig(t, h, sp, srv, 1)
		r.run(t, 8)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "read lane 1: took read a.w1@2 (epoch 15): the server says lane 1 holds it", records)
		require.Len(t, h.prompts, 1, "exactly that read runs: %s", records)
		assert.Contains(t, h.prompts[0], dir+"/reads/a.w1")
		oks := sp.verbs("--ok")
		require.Len(t, oks, 1)
		assert.Equal(t, "a.w1", oks[0][4])
		assert.Equal(t, "15", flagValue(oks[0], "--epoch"))
		assert.Equal(t, "1", flagValue(oks[0], "--lane"))
	})
}

// read_lanes.go:439-443: a lane the server says already holds the read (no MOVED line) runs
// it as held, and no read lane flow sends `read --begin`.
func TestAReadLaneRunsAHeldReadAndSendsNoBegin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.hold(1, "a.w1@2", "a.w1~15", "read")
		srv.once[1] = true
		r := readLaneRig(t, h, sp, srv, 1)
		r.run(t, 8)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "read lane 1: holds read a.w1@2 (epoch 15)")
		assert.NotContains(t, records, "took read a.w1")
		require.Len(t, h.prompts, 1, records)
		assert.Empty(t, sp.verbs("--begin"), "no read lane flow sends --begin")
		oks := sp.verbs("--ok")
		require.Len(t, oks, 1)
		assert.Equal(t, "1", flagValue(oks[0], "--lane"))
	})
}

// read_lanes.go:443: a refusal runs nothing and the lane asks again; the refusal is said once
// however many steps it stands, and the next take is at least TakeRetry later.
func TestARefusedReadTakeRunsNothingAndAsksAgainAfterTakeRetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.refuse[1] = &Refused{Why: "take refused: the server is away"}
		r := readLaneRig(t, h, sp, srv, 1)
		var atFour int
		r.at[4] = func() { atFour = len(srv.readerTakes()) }
		r.run(t, 12)

		records := strings.Join(r.records, "\n")
		assert.Empty(t, h.prompts, "a refusal runs nothing")
		assert.Equal(t, 1, strings.Count(records, "asked the server what it holds: refused"), records)
		lanes := srv.readerLanes()
		assert.Equal(t, 1, atFour, "no second take within TakeRetry: %v", lanes)
		assert.GreaterOrEqual(t, len(lanes), 2, "asked again after TakeRetry: %v", lanes)
		assert.LessOrEqual(t, len(lanes), 12/int(TakeRetry.Seconds())+1, "paced at TakeRetry: %v", lanes)
	})
}

// read_lanes.go:477-480: an idle lane (holds=-) runs nothing and records nothing, and it
// asks again only after LaneIdleAsk.
func TestAnIdleReadLaneRunsNothingAndRecordsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		r := readLaneRig(t, h, sp, srv, 1)
		r.run(t, 6)

		assert.Empty(t, h.prompts, "holds=- runs nothing")
		assert.NotContains(t, strings.Join(r.records, "\n"), "read lane", "and records nothing")
		lanes := srv.readerLanes()
		assert.Equal(t, []int{1}, lanes, "asked once, again only after LaneIdleAsk")
	})
}

// read_lanes.go:487-491: a held read with no packet on the reader queue is not begun, and
// runs once the queue gives its packet.
func TestAHeldReadWithNoPacketRunsOnceTheQueueGivesIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"z.w1": okResult}}
		sp := &readSprint{queue: askedQueue} // z.w1 is on it, at an empty brief
		srv := newReadLaneServer()
		srv.hold(1, "z.w1@1", "z.w1~15", "read")
		r := readLaneRig(t, h, sp, srv, 1)
		r.at[3] = func() {
			sp.mu.Lock()
			sp.queue = queueWithZ
			sp.mu.Unlock()
		}
		r.run(t, 20)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "not begun yet: the reader queue has not given its packet", records)
		require.NotEmpty(t, h.prompts, "once the queue gives the packet the read runs: %s", records)
		assert.Contains(t, h.prompts[0], dir+"/reads/z.w1")
	})
}

// read_lanes.go:482-485: a read the server says another read lane of this daemon already runs
// is not run twice.
func TestAReadAnotherReadLaneRunsIsNotRunTwice(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}, rblock: make(chan struct{})}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.hold(1, "a.w1@2", "a.w1~15", "read")
		srv.moved[1] = true
		srv.hold(2, "a.w1@2", "a.w1~15", "read")
		srv.moved[2] = true
		r := readLaneRig(t, h, sp, srv, 2)
		r.run(t, 10)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "read lane 2: the server says it holds read a.w1, which another read lane of this daemon runs", records)
		assert.Len(t, h.prompts, 1, "the read is not run twice: %s", records)
	})
}

// read_lanes.go:451-453: with two read slots the takes go to lane 1 and lane 2 only, and a
// lane running a read is not asked.
func TestReadTakesGoToTheSlotLanesAndABusyLaneIsNotAsked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}, rblock: make(chan struct{})}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.hold(1, "a.w1@2", "a.w1~15", "read") // lane 1 runs this read for the whole run
		r := readLaneRig(t, h, sp, srv, 2)
		r.run(t, 8)

		lanes := srv.readerLanes()
		require.NotEmpty(t, lanes)
		for _, n := range lanes {
			assert.Contains(t, []int{1, 2}, n, "takes go to the slot lanes only: %v", lanes)
		}
		assert.Equal(t, 1, strings.Count(fmt.Sprint(lanes), "1"), "a lane running a read is not asked: %v", lanes)
		assert.Contains(t, lanes, 2, "the free lane is asked: %v", lanes)
	})
}

// read_lanes.go:452: with the machine STOPPED no reader take is sent.
func TestNoReaderTakeIsSentWhenTheMachineIsStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		srv := newReadLaneServer()
		srv.hold(1, "a.w1@2", "a.w1~15", "read")
		r := readLaneRig(t, h, sp, srv, 2)
		r.d.MachineStopped = func() bool { return true }
		r.run(t, 6)

		assert.Empty(t, srv.readerTakes(), "no reader take while STOPPED")
		assert.Empty(t, h.prompts, "nothing runs while STOPPED")
	})
}
