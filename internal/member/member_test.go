package member

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptSprint is a Sprint that records every argv and answers from a table,
// keyed by the verb: beat, queue, take, begin (read --begin), finish, report
// (read --ok/--broken). An unlisted verb answers 0 with an empty body.
type scriptSprint struct {
	mu      sync.Mutex
	calls   [][]string
	answers map[string]answer
}

type answer struct {
	code int
	out  string
}

func newScript() *scriptSprint { return &scriptSprint{answers: map[string]answer{}} }

func verbOf(args []string) string {
	switch {
	case args[0] == "fleet":
		return "beat"
	case args[0] == "read" && slices.Contains(args, "--begin"):
		return "begin"
	case args[0] == "read":
		return "report"
	}
	return args[0]
}

func (s *scriptSprint) Run(args ...string) (int, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, slices.Clone(args))
	a := s.answers[verbOf(args)]
	return a.code, []byte(a.out)
}

func (s *scriptSprint) set(verb string, code int, out string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers[verb] = answer{code, out}
}

// take returns the calls of one verb, each as a space-joined line.
func (s *scriptSprint) lines(verb string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		if verbOf(c) == verb {
			out = append(out, strings.Join(c, " "))
		}
	}
	return out
}

// reset forgets the recorded calls, not the answers.
func (s *scriptSprint) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
}

// fakeChild is a child the test ends by hand.
type fakeChild struct {
	mu   sync.Mutex
	done bool
	res  Result
}

func (c *fakeChild) Done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done
}

func (c *fakeChild) Result() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.res
}

func (c *fakeChild) end(r Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.done, c.res = true, r
}

// fakeRunner records every packet it is asked to start and hands back one
// fakeChild each, in order.
type fakeRunner struct {
	mu       sync.Mutex
	packets  []Packet
	children map[string]*fakeChild
	failFor  map[string]bool
}

func newRunner() *fakeRunner {
	return &fakeRunner{children: map[string]*fakeChild{}, failFor: map[string]bool{}}
}

func (r *fakeRunner) Start(p Packet) (Child, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failFor[p.Card] {
		return nil, fmt.Errorf("no slot for %s", p.Card)
	}
	c := &fakeChild{}
	r.packets = append(r.packets, p)
	r.children[p.Card] = c
	return c, nil
}

func (r *fakeRunner) started() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, p := range r.packets {
		out = append(out, p.Card)
	}
	return out
}

func (r *fakeRunner) child(card string) *fakeChild {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.children[card]
}

// queueJSON is `nova-sprint queue --json` for the cards given.
func queueJSON(t *testing.T, epoch uint64, cards ...queueCard) string {
	t.Helper()
	b, err := json.Marshal(queueOut{As: "m", Epoch: epoch, Cards: cards})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func takeJSON(t *testing.T, ps ...Packet) string {
	t.Helper()
	b, err := json.Marshal(takeOut{Packets: ps})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// rig is one member over a script and a runner, with the loop's output kept.
type rig struct {
	m   *Member
	s   *scriptSprint
	r   *fakeRunner
	out *bytes.Buffer
}

func newRig(cfg Config) *rig {
	s, r, out := newScript(), newRunner(), &bytes.Buffer{}
	return &rig{m: New(cfg, s, r, out), s: s, r: r, out: out}
}

func (g *rig) tick(t *testing.T) (int, error) {
	t.Helper()
	return g.m.Tick(time.Unix(0, 0))
}

func ready(id string) queueCard { return queueCard{ID: id, Col: "ready"} }

func working(id string, gen int, p *Packet) queueCard {
	return queueCard{ID: id, Col: "working", Gen: gen, Packet: p}
}

func pk(card string) Packet {
	return Packet{Card: card, Kind: "work", As: "m", Primary: "p-" + card, Stream: "a", Attempt: 1, Gen: 1, Epoch: 7, Branch: "work/" + card}
}

// TestTickWithTwoReadyAndWidthTwoTakesTwoInOneVerb pins the take: one verb
// for the whole room, `take --as m --limit 2 --json` (the epoch follows), and
// one child started for each packet it returns.
func TestTickWithTwoReadyAndWidthTwoTakesTwoInOneVerb(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1"), ready("c2")))
	g.s.set("take", 0, takeJSON(t, pk("c1"), pk("c2")))
	acted, err := g.tick(t)
	if err != nil {
		t.Fatal(err)
	}
	if acted != 2 || g.m.Running() != 2 {
		t.Fatalf("acted=%d running=%d, want 2 and 2", acted, g.m.Running())
	}
	if got := g.s.lines("take"); !slices.Equal(got, []string{"take --as m --limit 2 --json --epoch 7"}) {
		t.Fatalf("take lines: %q, want exactly one, for limit 2", got)
	}
	if got := g.r.started(); !slices.Equal(got, []string{"c1", "c2"}) {
		t.Fatalf("started %v, want c1 c2", got)
	}
	if !strings.Contains(g.out.String(), "start c1 attempt=1 gen=1 running=1/2") {
		t.Fatalf("the start line is missing: %q", g.out.String())
	}
}

// TestTakeLimitIsTheRoom pins that the member asks for its whole room, not
// for what its queue snapshot showed: a deal that lands between the queue
// read and the take is taken in the same tick. Three places, one ready card
// seen: `--limit 3`.
func TestTakeLimitIsTheRoom(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 3})
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	g.s.set("take", 0, takeJSON(t, pk("c1")))
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	if got := g.s.lines("take"); !slices.Equal(got, []string{"take --as m --limit 3 --json --epoch 7"}) {
		t.Fatalf("take lines: %q", got)
	}
}

// TestEndedOkCardIsFinishedWithItsHeadBranchAndEpoch pins the report verb:
// `finish --as m <id>@<gen> --report <line> --head <sha> --branch <b> --epoch <e>`
// for a child that ended ok, the gen from the queue (not the packet taken).
func TestEndedOkCardIsFinishedWithItsHeadBranchAndEpoch(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	p := pk("c1")
	p.Gen = 3 // the take hands the card at its generation; the queue says the same
	g.s.set("take", 0, takeJSON(t, p))
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.r.child("c1").end(Result{Ran: true, OK: true, Head: "abc123", Report: "# Result\n\nlanded the thing\nsecond line"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 3, &p)))
	g.s.reset()
	acted, err := g.tick(t)
	if err != nil {
		t.Fatal(err)
	}
	want := "finish --as m c1@3 --report landed the thing --head abc123 --branch work/c1 --epoch 7"
	if got := g.s.lines("finish"); !slices.Equal(got, []string{want}) {
		t.Fatalf("finish lines: %q, want %q", got, want)
	}
	if acted != 1 || g.m.Running() != 0 {
		t.Fatalf("acted=%d running=%d, want 1 and 0", acted, g.m.Running())
	}
	if len(g.s.lines("take")) != 0 {
		t.Fatalf("nothing was ready and nothing was taken: %q", g.s.lines("take"))
	}
}

// TestEndedNotOkCardIsFinishedFailed pins --failed, placed before the epoch,
// and that an unknown head adds no --head.
func TestEndedNotOkCardIsFinishedFailed(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	if _, err := g.tick(t); err != nil { // restart: the child is ours now
		t.Fatal(err)
	}
	g.r.child("c1").end(Result{Ran: true, OK: false, Report: "the harness fell over"})
	g.s.reset()
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	want := "finish --as m c1@2 --report the harness fell over --branch work/c1 --failed --epoch 7"
	if got := g.s.lines("finish"); !slices.Equal(got, []string{want}) {
		t.Fatalf("finish lines: %q, want %q", got, want)
	}
}

// TestFinishWithoutABranchInThePacketCarriesNone pins that --branch is the
// packet's and is left off when the queue's card has no packet.
func TestFinishWithoutABranchInThePacketCarriesNone(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1})
	p := pk("c1")
	p.Branch = "" // the branch is the launch's packet's; none here
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.r.child("c1").end(Result{Ran: true, OK: true, Head: "h1", Report: "done"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, nil)))
	g.s.reset()
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	want := "finish --as m c1@1 --report done --head h1 --epoch 7"
	if got := g.s.lines("finish"); !slices.Equal(got, []string{want}) {
		t.Fatalf("finish lines: %q, want %q", got, want)
	}
}

// TestAWorkingCardWithNoChildOfOursIsRestartedFromItsPacket pins the
// restart: a card the queue shows as working that this process never started
// is run again from the packet the queue carries, and nothing is finished.
func TestAWorkingCardWithNoChildOfOursIsRestartedFromItsPacket(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	p := pk("c9")
	p.Attempt, p.Gen = 2, 4
	g.s.set("queue", 0, queueJSON(t, 7, working("c9", 4, &p), working("c8", 4, nil)))
	acted, err := g.tick(t)
	if err != nil {
		t.Fatal(err)
	}
	if acted != 1 || !slices.Equal(g.r.started(), []string{"c9"}) {
		t.Fatalf("acted=%d started=%v, want c9 only (c8 has no packet to run)", acted, g.r.started())
	}
	if g.r.packets[0].Attempt != 2 || g.r.packets[0].Gen != 4 {
		t.Fatalf("restarted with %+v, want the queue's own packet", g.r.packets[0])
	}
	if len(g.s.lines("finish")) != 0 || len(g.s.lines("take")) != 0 {
		t.Fatalf("a restart finishes and takes nothing: %q %q", g.s.lines("finish"), g.s.lines("take"))
	}
	// The next pass finds the child ours and starts nothing more.
	g.s.reset()
	if acted, _ = g.tick(t); acted != 0 || len(g.r.started()) != 1 {
		t.Fatalf("second pass acted=%d started=%v, want 0 and one start in all", acted, g.r.started())
	}
}

// TestNoTakeWhenWidthIsFullOrNothingIsReady pins both refusals to ask.
func TestNoTakeWhenWidthIsFullOrNothingIsReady(t *testing.T) {
	t.Parallel()
	t.Run("width full", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "m", Width: 1})
		p := pk("c1")
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p), ready("c2")))
		if _, err := g.tick(t); err != nil {
			t.Fatal(err)
		}
		if got := g.s.lines("take"); len(got) != 0 {
			t.Fatalf("width 1 with one child running took: %q", got)
		}
	})
	t.Run("nothing ready", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "m", Width: 4})
		g.s.set("queue", 0, queueJSON(t, 7))
		if acted, err := g.tick(t); err != nil || acted != 0 {
			t.Fatalf("acted=%d err=%v on an empty queue", acted, err)
		}
		if got := g.s.lines("take"); len(got) != 0 {
			t.Fatalf("an empty queue took: %q", got)
		}
	})
}

// TestReaderLoopBeginsAskedCardsAndReportsEndedReads pins the readers
// table's loop: `read --as r --begin --limit n --json`, then
// `read --as r --ok|--broken <id> --finding <line> --epoch <e>`, and no beat.
func TestReaderLoopBeginsAskedCardsAndReportsEndedReads(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 2, Reader: true})
	a1 := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7, Head: "h1"}
	a2 := Packet{Card: "r2", Kind: "read", As: "r", Attempt: 1, Epoch: 7, Head: "h2"}
	asked := func(id string, p *Packet) queueCard { return queueCard{ID: id, Col: "asked", Packet: p} }
	g.s.set("queue", 0, queueJSON(t, 7, asked("r1", &a1), asked("r2", &a2)))
	acted, err := g.tick(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.s.lines("begin"); !slices.Equal(got, []string{"read --as r --begin r1 r2 --epoch 7"}) {
		t.Fatalf("begin lines: %q", got)
	}
	if acted != 2 || !slices.Equal(g.r.started(), []string{"r1", "r2"}) {
		t.Fatalf("acted=%d started=%v", acted, g.r.started())
	}
	if got := g.s.lines("beat"); len(got) != 0 {
		t.Fatalf("a reader beats no fleet row: %q", got)
	}
	g.r.child("r1").end(Result{Ran: true, OK: true, Verdict: "ok", Report: "clean\nsecond"})
	g.r.child("r2").end(Result{Ran: true, OK: false, Verdict: "broken", Report: "## Finding\nthe merge is wrong"})
	reading := func(id string, p *Packet) queueCard { return queueCard{ID: id, Col: "reading", Packet: p} }
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &a1), reading("r2", &a2)))
	g.s.reset()
	if acted, err = g.tick(t); err != nil || acted != 2 {
		t.Fatalf("acted=%d err=%v", acted, err)
	}
	want := []string{
		"read --as r --ok r1 --finding clean --epoch 7",
		"read --as r --broken r2 --finding the merge is wrong --epoch 7",
	}
	if got := g.s.lines("report"); !slices.Equal(got, want) {
		t.Fatalf("report lines: %q, want %q", got, want)
	}
	if g.m.Running() != 0 {
		t.Fatalf("running=%d after both reads were reported", g.m.Running())
	}
}

// TestAStoreThatDoesNotAnswerStopsTheTickActingOnNothing pins exit 2 as an
// error at each step and that nothing further is asked of the sprint.
func TestAStoreThatDoesNotAnswerStopsTheTickActingOnNothing(t *testing.T) {
	t.Parallel()
	t.Run("queue", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "m", Width: 2})
		// a body that would parse and act, with the exit that says the store never answered
		g.s.set("queue", 2, queueJSON(t, 7, ready("c1")))
		g.s.set("take", 0, takeJSON(t, pk("c1")))
		acted, err := g.tick(t)
		if err == nil || !strings.Contains(err.Error(), "queue: exit 2") {
			t.Fatalf("err = %v, want a queue exit 2 error", err)
		}
		if acted != 0 || len(g.r.started()) != 0 {
			t.Fatalf("acted=%d started=%v on a store that did not answer", acted, g.r.started())
		}
		for _, verb := range []string{"take", "finish", "begin", "report"} {
			if got := g.s.lines(verb); len(got) != 0 {
				t.Fatalf("%s was issued after queue failed: %q", verb, got)
			}
		}
	})
	t.Run("beat", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "m", Width: 2})
		g.s.set("beat", 2, "no route to the store")
		acted, err := g.tick(t)
		if err == nil || !strings.Contains(err.Error(), "beat") || acted != 0 {
			t.Fatalf("acted=%d err=%v, want a beat error", acted, err)
		}
		if got := g.s.lines("queue"); len(got) != 0 {
			t.Fatalf("queue was read after a beat that failed: %q", got)
		}
	})
	t.Run("finish keeps the child for the next pass", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "m", Width: 2})
		p := pk("c1")
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
		if _, err := g.tick(t); err != nil {
			t.Fatal(err)
		}
		g.r.child("c1").end(Result{Ran: true, OK: true, Head: "h", Report: "r"})
		g.s.set("finish", 2, "no route to the store")
		if _, err := g.tick(t); err == nil {
			t.Fatal("a finish the store never answered is an error")
		}
		if g.m.Running() != 1 {
			t.Fatalf("running=%d: the unreported child is still ours", g.m.Running())
		}
		g.s.set("finish", 0, "")
		if acted, err := g.tick(t); err != nil || acted != 1 || g.m.Running() != 0 {
			t.Fatalf("retry: acted=%d err=%v running=%d", acted, err, g.m.Running())
		}
	})
}

// TestARefusedTakeIsPrintedAndTheTickContinues pins exit 1: the refusal is
// printed, the reports made earlier in the same tick stand, no error is
// returned, and the next pass takes.
func TestARefusedTakeIsPrintedAndTheTickContinues(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1})
	p := pk("c1")
	p.Gen = 1
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.r.child("c1").end(Result{Ran: true, OK: true, Head: "h", Report: "r"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p), ready("c2")))
	g.s.set("take", 1, "the sprint is stopped")
	acted, err := g.tick(t)
	if err != nil {
		t.Fatalf("a refusal is not an error: %v", err)
	}
	if acted != 1 {
		t.Fatalf("acted=%d, want the one finish", acted)
	}
	if !strings.Contains(g.out.String(), "take refused: the sprint is stopped") {
		t.Fatalf("the refusal was not printed: %q", g.out.String())
	}
	g.s.set("take", 0, takeJSON(t, pk("c2")))
	g.s.set("queue", 0, queueJSON(t, 7, ready("c2")))
	if acted, err = g.tick(t); err != nil || acted != 1 || !slices.Equal(g.r.started(), []string{"c1", "c2"}) {
		t.Fatalf("next pass acted=%d err=%v started=%v", acted, err, g.r.started())
	}
}

// TestBeatCarriesLoadAsRunningOverWidth pins --load as running*100/width,
// integer, taken before the tick's own reports and starts.
func TestBeatCarriesLoadAsRunningOverWidth(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ running, width, load int }{
		{0, 4, 0}, {1, 3, 33}, {2, 3, 66}, {2, 2, 100}, {3, 8, 37},
	} {
		t.Run(fmt.Sprintf("%dof%d", tc.running, tc.width), func(t *testing.T) {
			t.Parallel()
			g := newRig(Config{As: "m", Width: tc.width})
			var cards []queueCard
			for i := 0; i < tc.running; i++ {
				p := pk("c" + strconv.Itoa(i))
				cards = append(cards, working(p.Card, 1, &p))
			}
			g.s.set("queue", 0, queueJSON(t, 7, cards...))
			if _, err := g.tick(t); err != nil { // restarts: running is now tc.running
				t.Fatal(err)
			}
			if g.m.Running() != tc.running {
				t.Fatalf("running=%d, want %d", g.m.Running(), tc.running)
			}
			g.s.reset()
			if _, err := g.tick(t); err != nil {
				t.Fatal(err)
			}
			want := "fleet beat m --load " + strconv.Itoa(tc.load)
			if got := g.s.lines("beat"); !slices.Equal(got, []string{want}) {
				t.Fatalf("beat lines: %q, want %q", got, want)
			}
		})
	}
}

// TestACardTheQueueNoLongerListsIsReapedWhenItsChildEnds pins that a child
// whose card left the member's queue (the sprint was cleared, the card was
// dealt elsewhere) does not hold a place of the width for ever: once it has
// ended it is forgotten, and a still-running one is left alone.
func TestACardTheQueueNoLongerListsIsReapedWhenItsChildEnds(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1})
	p := pk("c1")
	p.Gen = 1
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.s.set("queue", 0, queueJSON(t, 8))
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	if g.m.Running() != 1 {
		t.Fatalf("running=%d: a child still running is left alone", g.m.Running())
	}
	g.r.child("c1").end(Result{Ran: true, OK: true, Head: "h", Report: "r"})
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	if g.m.Running() != 0 {
		t.Fatalf("running=%d: an ended child of a card the queue dropped still holds a place of the width", g.m.Running())
	}
	if got := g.s.lines("finish"); len(got) != 0 {
		t.Fatalf("a card the queue dropped is not finished: %q", got)
	}
}

// TestAStartThatFailsIsPrintedAndLeavesNoChild pins the runner's error as a
// printed line and no place taken.
func TestAStartThatFailsIsPrintedAndLeavesNoChild(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	g.r.failFor["c1"] = true
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1"), ready("c2")))
	g.s.set("take", 0, takeJSON(t, pk("c1"), pk("c2")))
	acted, err := g.tick(t)
	if err != nil || acted != 1 || g.m.Running() != 1 {
		t.Fatalf("acted=%d err=%v running=%d, want 1 nil 1", acted, err, g.m.Running())
	}
	if !strings.Contains(g.out.String(), "start c1: no slot for c1") {
		t.Fatalf("the failed start was not printed: %q", g.out.String())
	}
}

// TestCardTextForAWorkPacket pins what a child reads: the brief verbatim and
// first, then the sprint's mechanics: the attempt, the branch and base, the
// fix, every note, and the exact finish line with card@gen, epoch and branch.
func TestCardTextForAWorkPacket(t *testing.T) {
	t.Parallel()
	p := Packet{
		Card: "c1", Kind: "work", As: "m1", Primary: "p1", Stream: "a", Attempt: 2, Gen: 5, Epoch: 9,
		Brief: "Build the thing.\nsecond line\n\n", Fix: " Mind the edge. ", Notes: []string{"first note", "  ", "second note"},
		Branch: "work/c1", Base: "sprint/base",
	}
	got := CardText(p, "nova-sprint")
	if !strings.HasPrefix(got, "Build the thing.\nsecond line\n\n## From the sprint\n\n") {
		t.Fatalf("the brief is not verbatim and first:\n%s", got)
	}
	for _, want := range []string{
		"This is c1: attempt 2 of p1 (stream a).",
		"The work is branch work/c1 from sprint/base;",
		"Fix, this attempt:\n\nMind the edge.\n",
		"Note:\n\nfirst note\n",
		"Note:\n\nsecond note\n",
		"    nova-sprint finish --as m1 c1@5 --epoch 9 --branch work/c1 --head <sha> --report '<one line>' [--failed]\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the work card lacks %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "Note:\n"); n != 2 {
		t.Fatalf("%d notes, want 2 (a blank note is dropped):\n%s", n, got)
	}
	if strings.Contains(got, " read --as ") {
		t.Fatalf("a work card names the read verb:\n%s", got)
	}
}

// TestCardTextForAWorkPacketWithNoBaseWorksInPlace pins the other branch of
// the mechanics: with no base the child works where it starts, and the
// finish line still names the branch.
func TestCardTextForAWorkPacketWithNoBaseWorksInPlace(t *testing.T) {
	t.Parallel()
	got := CardText(Packet{Card: "c2", Kind: "work", As: "m1", Primary: "p2", Stream: "b", Attempt: 1, Gen: 1, Epoch: 3, Branch: "work/c2"}, "nova-sprint")
	if !strings.HasPrefix(got, "## From the sprint\n\n") {
		t.Fatalf("a packet with no brief starts at the sprint's part:\n%s", got)
	}
	for _, want := range []string{"Work in the directory you start in and nowhere else.", "finish --as m1 c2@1 --epoch 3 --branch work/c2 "} {
		if !strings.Contains(got, want) {
			t.Fatalf("the card lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "The work is branch") {
		t.Fatalf("a card with no base names a branch to start from:\n%s", got)
	}
}

// TestCardTextForAReadPacket pins a read card: the brief, the head, the work
// branch and base, the worker's report, and the read line.
func TestCardTextForAReadPacket(t *testing.T) {
	t.Parallel()
	p := Packet{
		Card: "r1", Kind: "read", As: "rd", Primary: "p1", Attempt: 1, Epoch: 4, Worker: "m2", Brief: "Read it well.",
		Head: "deadbeef", WorkBranch: "work/c1", WorkBase: "sprint/base", Report: " landed it ",
	}
	got := CardText(p, "/bin/nova-sprint")
	if !strings.HasPrefix(got, "Read it well.\n\n## From the sprint\n\n") {
		t.Fatalf("the brief is not verbatim and first:\n%s", got)
	}
	for _, want := range []string{
		"This is read r1: attempt 1 of p1, worked by m2, at head deadbeef on branch work/c1 (base sprint/base).",
		"The worker's report:\n\nlanded it\n",
		"    /bin/nova-sprint read --as rd (--ok | --broken) r1 --epoch 4 --finding '<one line>'\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the read card lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, " finish --as ") {
		t.Fatalf("a read card names the finish verb:\n%s", got)
	}
}

// TestOneLine pins the report as one line: the first non-empty line that is
// not a heading, cut at 500 bytes, "" when none.
func TestOneLine(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 600)
	for _, tc := range []struct{ name, in, want string }{
		{"first line", "one\ntwo", "one"},
		{"blank and heading lines skipped", "\n\n# Result\n## One line\n  \n  the line  \nmore", "the line"},
		{"only headings", "# a\n## b\n", ""},
		{"empty", "", ""},
		{"cut at 500 bytes", long, strings.Repeat("x", 500)},
		{"exactly 500 kept", strings.Repeat("y", 500), strings.Repeat("y", 500)},
	} {
		if got := oneLine(tc.in); got != tc.want {
			t.Errorf("%s: oneLine = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestAReadBeginThatIsRefusedOrUnansweredStartsNothing pins the reader's two
// failures of `read --begin`: a refusal (exit 1) is printed and starts no
// child; a store that does not answer (exit 2) is an error.
func TestAReadBeginThatIsRefusedOrUnansweredStartsNothing(t *testing.T) {
	t.Parallel()
	a1 := Packet{Card: "r1", Kind: "read", As: "r", Epoch: 7}
	body := queueJSON(t, 7, queueCard{ID: "r1", Col: "asked", Packet: &a1})
	t.Run("refused", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "r", Width: 1, Reader: true})
		g.s.set("queue", 0, body)
		g.s.set("begin", 1, "stale epoch")
		acted, err := g.tick(t)
		if err != nil || acted != 0 || len(g.r.started()) != 0 {
			t.Fatalf("acted=%d err=%v started=%v", acted, err, g.r.started())
		}
		if !strings.Contains(g.out.String(), "read --begin refused: stale epoch") {
			t.Fatalf("the refusal was not printed: %q", g.out.String())
		}
	})
	t.Run("unanswered", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "r", Width: 1, Reader: true})
		g.s.set("queue", 0, body)
		g.s.set("begin", 2, "no route")
		if _, err := g.tick(t); err == nil || len(g.r.started()) != 0 {
			t.Fatalf("err=%v started=%v, want an error and no start", err, g.r.started())
		}
	})
}
