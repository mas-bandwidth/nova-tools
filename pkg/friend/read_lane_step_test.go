package friend

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The read lane's step (read_lanes.go:439-443, lane_take.go's header, tla/FriendLane.tla): each
// read lane free of a read asks the server `take --as reader-<friend> --lane <n>`, which answers
// the read the lane holds, else gives it one; the lane runs exactly that read and reports its
// verdict by the lane and the server's epoch. These tests drive the daemon, never readLaneStep.

// readTake is the sprint server as a read lane sees it: `take --as reader-<friend> --lane <n>`
// answers the read lane n holds (holds= "a.w1@2 kind=read job=a.w1~15", "-" for none), with a
// MOVED line when moved says the take moved it; `take --as friend-<friend>` (the card lanes'
// ask) answers holds=-. refuse answers the read lanes' ask with a refusal. When once is set, a
// lane's read is handed once, as a server whose read left the lane after it was taken. Every
// ask is recorded.
type readTake struct {
	mu     sync.Mutex
	holds  map[int]string
	moved  map[int]bool
	once   bool
	handed map[int]bool
	refuse bool
	asks   [][]string
}

func (s *readTake) take(_ context.Context, argv []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asks = append(s.asks, append([]string(nil), argv...))
	if argv[2] != ReaderOf("bob") {
		return "TAKE OK moved=0\nNOTE LANE " + argv[2] + " lane=" + argv[4] + " epoch=15 holds=-\n", nil
	}
	if s.refuse {
		return "", &Refused{Why: "take refused: the machine is STOPPED: no new work or read may start"}
	}
	n, _ := strconv.Atoi(argv[4])
	body := s.holds[n]
	if s.once && s.handed[n] {
		body = ""
	}
	if body == "" {
		return "TAKE OK moved=0\nNOTE LANE " + argv[2] + " lane=" + argv[4] + " epoch=15 holds=-\n", nil
	}
	if s.once {
		if s.handed == nil {
			s.handed = map[int]bool{}
		}
		s.handed[n] = true
	}
	line := "NOTE LANE " + argv[2] + " lane=" + argv[4] + " epoch=15 holds=" + body + "\n"
	if s.moved[n] {
		card, _, _ := strings.Cut(body, "@")
		return "MOVED " + card + " " + argv[2] + ":working -> lane " + argv[4] + " (read, no lane held it)\nTAKE OK moved=1\n" + line, nil
	}
	return "TAKE OK moved=0\n" + line, nil
}

// readerTakes is every ask a read lane sent, in order.
func (s *readTake) readerTakes() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]string
	for _, a := range s.asks {
		if len(a) > 2 && a[2] == ReaderOf("bob") {
			out = append(out, a)
		}
	}
	return out
}

// setQueue replaces the reader queue the fake answers with, under its lock.
func (s *readSprint) setQueue(q string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = q
}

// readLaneRig is readRig with the server's lane word wired: the daemon asks the server what each
// read lane holds (serverLanes, lane_take.go:283), as it does for her card lanes.
func readLaneRig(t *testing.T, h *readHarness, sp *readSprint, tk *readTake, slots int) *rig {
	t.Helper()
	r := readRig(t, h, sp, slots)
	r.d.Take = tk.take
	return r
}

// queueX is a reader queue naming x.w1 asked, for a lane the server says holds a read the queue
// at first does not carry.
const queueX = `{"epoch":"15","cards":[
 {"id":"x.w1","col":"asked","packet":{"tier":"pro","head":"xxx","work_branch":"sprint/x","attempt":1,"brief":"REPO: o/r\nBASE: main\n","report":"Verdict: LAND"}}]}`

// A take that answers the read a lane holds with a MOVED line: the daemon records it took the
// read, runs exactly that read on the harness, and reports the verdict at the server's epoch
// and the lane (read_lanes.go:495-501).
func TestAReadLaneRunsTheReadTheServerSaysItTook(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		tk := &readTake{holds: map[int]string{1: "a.w1@2 kind=read job=a.w1~15"}, moved: map[int]bool{1: true}, once: true}
		r := readLaneRig(t, h, sp, tk, 1)
		r.run(t, 8)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "read lane 1: took read a.w1@2 (epoch 15)", records)
		require.Len(t, h.models, 1, "the read the server named, once: %s", records)
		oks := sp.verbs("--ok")
		require.Len(t, oks, 1, records)
		assert.Equal(t, "15", flagValue(oks[0], "--epoch"), "the epoch the server gave")
		assert.Equal(t, "1", flagValue(oks[0], "--lane"), "the lane the server named")
	})
}

// The same hold with no MOVED line records "holds", never "took": a restarted daemon continues
// the read the server says it holds, and no read lane flow sends read --begin
// (read_lanes.go:493-500).
func TestAReadLaneRunsAHeldReadWithoutSayingItTookIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		tk := &readTake{holds: map[int]string{1: "a.w1@2 kind=read job=a.w1~15"}, once: true}
		r := readLaneRig(t, h, sp, tk, 1)
		r.run(t, 8)

		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "read lane 1: holds read a.w1@2 (epoch 15)", records)
		assert.NotContains(t, records, "read lane 1: took", records)
		require.Len(t, h.models, 1, records)
		assert.Empty(t, sp.verbs("--begin"), "no read lane flow sends --begin")
	})
}

// A refused take runs nothing; the refusal is recorded once while it stands, said once per
// line, and the lane asks again after TakeRetry (read_lanes.go:472-475).
func TestARefusedReadTakeRunsNothingAndIsSaidOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		tk := &readTake{refuse: true}
		r := readLaneRig(t, h, sp, tk, 1)
		r.run(t, 30)

		records := strings.Join(r.records, "\n")
		assert.Empty(t, h.models, "a refused take runs nothing: %s", records)
		assert.Equal(t, 1, strings.Count(records, "read lane 1: asked the server what it holds: refused"), "said once while it stands: %s", records)
		takes := tk.readerTakes()
		assert.GreaterOrEqual(t, len(takes), 2, "asked again after TakeRetry: %v", takes)
		assert.LessOrEqual(t, len(takes), 30/int(TakeRetry.Seconds())+1, "never once a step: %v", takes)
	})
}

// An answer of holds=- runs nothing and records nothing, and the lane asks again only after
// LaneIdleAsk (read_lanes.go:477-480).
func TestAnIdleReadLaneRunsNothingAndAsksAgainAfterLaneIdleAsk(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		tk := &readTake{}
		r := readLaneRig(t, h, sp, tk, 1)
		r.run(t, 30)

		records := strings.Join(r.records, "\n")
		assert.Empty(t, h.models, records)
		assert.NotContains(t, records, "read lane", records)
		takes := tk.readerTakes()
		assert.GreaterOrEqual(t, len(takes), 2, "asked again after LaneIdleAsk: %v", takes)
		assert.LessOrEqual(t, len(takes), 30/int(LaneIdleAsk.Seconds())+1, "never once a step: %v", takes)
	})
}

// A held read the reader queue has not given the packet for records "not begun yet" and runs
// nothing; the queue is read again on the next step, and the read runs once it gives the packet
// (read_lanes.go:487-491).
func TestAHeldReadWithNoPacketYetRunsWhenTheQueueGivesIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"x.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		tk := &readTake{holds: map[int]string{1: "x.w1@2 kind=read job=x.w1~15"}}
		r := readLaneRig(t, h, sp, tk, 1)
		before := 0
		r.at[4] = func() { before = len(h.models); sp.setQueue(queueX) }
		r.run(t, 20)

		records := strings.Join(r.records, "\n")
		assert.Equal(t, 0, before, "nothing runs while the queue has not given the packet: %s", records)
		assert.Contains(t, records, "not begun yet: the reader queue has not given its packet", records)
		assert.NotEmpty(t, h.models, "the read runs once the queue gives its packet: %s", records)
	})
}

// Two read lanes, and the server says lane 2 holds the read lane 1 already runs: it is not run
// twice, and lane 2 records that another read lane runs it (read_lanes.go:482-485).
func TestAReadAnotherLaneRunsIsNotRunTwice(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, rblock: make(chan struct{})}
		sp := &readSprint{queue: askedQueue}
		tk := &readTake{holds: map[int]string{1: "a.w1@2 kind=read job=a.w1~15", 2: "a.w1@2 kind=read job=a.w1~15"}, moved: map[int]bool{1: true}}
		r := readLaneRig(t, h, sp, tk, 2)
		r.run(t, 8)

		records := strings.Join(r.records, "\n")
		require.Len(t, h.models, 1, "the read runs once, on one lane: %s", records)
		assert.Contains(t, records, "read lane 2: the server says it holds read a.w1, which another read lane of this daemon runs", records)
	})
}

// With ReadSlots 2, takes name --lane 1 and --lane 2 only, and a lane running a read is not
// asked again (read_lanes.go:451-452, 465-466).
func TestReadTakesNameOnlyTheReadSlotsAndNeverARunningLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, rblock: make(chan struct{})}
		sp := &readSprint{queue: askedQueue}
		tk := &readTake{holds: map[int]string{1: "a.w1@2 kind=read job=a.w1~15", 2: "b.w1@2 kind=read job=b.w1~15"}, moved: map[int]bool{1: true, 2: true}}
		r := readLaneRig(t, h, sp, tk, 2)
		r.run(t, 8)

		takes := tk.readerTakes()
		require.NotEmpty(t, takes)
		byLane := map[string]int{}
		for _, a := range takes {
			lane := flagValue(a, "--lane")
			assert.Contains(t, []string{"1", "2"}, lane, "no take beyond the read slots: %v", a)
			byLane[lane]++
		}
		assert.Equal(t, map[string]int{"1": 1, "2": 1}, byLane, "a lane running a read is not asked: %v", takes)
		require.Len(t, h.models, 2, "both reads run")
	})
}

// The machine's word STOPPED: no reader take is sent (read_lanes.go:452; tla/StopCancels.tla
// NoLaneWhileStopped).
func TestNoReadTakeIsSentWhileTheMachineIsStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, verdicts: map[string]string{"a.w1": okResult}}
		sp := &readSprint{queue: askedQueue}
		tk := &readTake{holds: map[int]string{1: "a.w1@2 kind=read job=a.w1~15"}, moved: map[int]bool{1: true}}
		r := readLaneRig(t, h, sp, tk, 1)
		r.d.MachineStopped = func() bool { return true }
		r.run(t, 10)

		assert.Empty(t, tk.readerTakes(), "no reader take while STOPPED")
		assert.Empty(t, h.models)
	})
}
