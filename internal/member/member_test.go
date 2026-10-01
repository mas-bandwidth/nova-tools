package member

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
)

// scriptSprint is a Sprint that records every argv and answers from a table,
// keyed by the verb: beat, queue, take, begin (read --begin), finish, report
// (read --ok/--broken). An unlisted verb answers 0 with an empty body.
type scriptSprint struct {
	mu      sync.Mutex
	calls   [][]string
	answers map[string]answer
	first   map[string]answer // the answer of a verb's next call only (failOnce)
}

type answer struct {
	code int
	out  string
}

// failOnce makes the verb's next call answer code and its calls after it
// answer as set: a store that timed out once.
func (s *scriptSprint) failOnce(verb string, code int, out string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.first == nil {
		s.first = map[string]answer{}
	}
	s.first[verb] = answer{code, out}
}

func newScript() *scriptSprint { return &scriptSprint{answers: map[string]answer{}} }

func verbOf(args []string) string {
	switch {
	case args[0] == "fleet":
		return "beat"
	case args[0] == "read" && slices.Contains(args, "--begin"):
		return "begin"
	case args[0] == "read" && slices.Contains(args, "--return"):
		return "return"
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
	if f, ok := s.first[verbOf(args)]; ok {
		delete(s.first, verbOf(args))
		a = f
	}
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
	ended    []string
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

// Ended records each launch the member is done with, `<card>:<failed>` (Ender).
func (r *fakeRunner) Ended(p Packet, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ended = append(r.ended, p.Card+":"+strconv.FormatBool(failed))
}

func (r *fakeRunner) endedLaunches() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ended)
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
	return &rig{m: New(cfg, s, r, &fakePusher{}, out), s: s, r: r, out: out}
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

// TestEndedOkCardIsFinishedWithItsHeadBranchAndEpoch pins the report verb:
// `finish --as m <id>@<gen> --report <line> --head <sha> --branch <b> --epoch <e>`
// for a child that ended ok, the gen from the queue (not the packet taken).
func TestEndedOkCardIsFinishedWithItsHeadBranchAndEpoch(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	g.m.pusher = &fakePusher{def: Push{Sha: fullSha}}
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	p := pk("c1")
	p.Gen = 3 // the take hands the card at its generation; the queue says the same
	g.s.set("take", 0, takeJSON(t, p))
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc123", Report: "# Result\n\nlanded the thing\nsecond line"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 3, &p)))
	g.s.reset()
	acted, err := g.tick(t)
	if err != nil {
		t.Fatal(err)
	}
	want := "finish --as m c1@3 --report pushed=" + fullSha + " to work/c1: landed the thing --head " + fullSha + " --branch work/c1 --epoch 7"
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
// and that a finish with nothing pushed names no --head and no --branch.
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
	want := "finish --as m c1@2 --report no RESULT.md shape; the harness fell over --failed --epoch 7"
	if got := g.s.lines("finish"); !slices.Equal(got, []string{want}) {
		t.Fatalf("finish lines: %q, want %q", got, want)
	}
}

// A run the provider failed that left no result is finished failed with the kind
// `provider failure` first and the provider's own reason, whatever the missing shape
// says; the sprint deals that card again (tla/CardContract.tla, ProviderFailure).
func TestAProviderFailedRunIsFinishedFailedWithTheProvidersKindAndReason(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t) // restart: the child is ours now
	require.NoError(t, err)
	g.r.child("c1").end(Result{Report: "the child ended without a result", End: EndProvider, Provider: "provider: message=\"stream error\" server_error"})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{"finish --as m c1@2 --report provider failure: provider: message=\"stream error\" server_error; the child ended without a result --failed --epoch 7"}, g.s.lines("finish"))
}

// Judge puts the provider's kind and reason on a run with no result that the provider failed,
// and only on that: a launch the provider never accepted (no reason), a refused push and a
// result with the shape are the card's own failure whatever the run's end, and a finished
// result pushed is finished work.
func TestJudgeNamesTheProviderOnlyForTheRunItFailedWithNoResult(t *testing.T) {
	t.Parallel()
	fin, why := Judge(Result{End: EndProvider, Provider: "provider: ended without a final message"}, Push{None: "nothing"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, "provider failure: provider: ended without a final message", why)

	for name, c := range map[string]struct {
		r    Result
		pu   Push
		want string
	}{
		"a launch the provider never accepted names no reason": {Result{End: EndProvider}, Push{None: "nothing"}, "no RESULT.md shape"},
		"a refused push is git's, said first":                  {Result{End: EndProvider, Provider: "provider: x"}, Push{Refused: "rejected"}, "push refused: rejected"},
		"a result with nothing to do is the card's":            {Result{End: EndProvider, Provider: "provider: x", Shaped: true, Verdict: "nothing", Report: "nothing: done already"}, Push{None: "no commit"}, "nothing to do: done already"},
		"a result not done is the card's":                      {Result{End: EndProvider, Provider: "provider: x", Shaped: true, Verdict: "not-done"}, Push{None: "no commit"}, "verdict not-done"},
		"no provider end: the shape's reason":                  {Result{Provider: "provider: x"}, Push{None: "nothing"}, "no RESULT.md shape"},
		"a budget still names itself first":                    {Result{End: EndBudget}, Push{None: "nothing"}, "budget: no RESULT.md shape"},
	} {
		fin, why := Judge(c.r, c.pu)
		require.Equal(t, FinishFailed, fin, name)
		require.Equal(t, c.want, why, name)
		require.NotContains(t, why, EndProvider+": "+c.r.Provider+"; ", name)
	}

	fin, why = Judge(Result{End: EndProvider, Provider: "provider: x", Shaped: true, Verdict: "ok"}, Push{Sha: fullSha})
	require.Equal(t, FinishOK, fin, "a shaped ok result pushed is finished work")
	require.Empty(t, why)
}

// The finish a member reports for a provider-ended run that is the card's own failure (a
// refused push, a shaped result) carries no `provider failure` kind: the sprint opens the
// failed-work judgment for it, and does not deal it again.
func TestAProviderEndedRunThatIsTheCardsOwnFailureIsReportedAsFailedWork(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		r    Result
		pu   Push
		want string
	}{
		"refused push":  {Result{Report: "r", Head: fullSha, End: EndProvider, Provider: "provider: x"}, Push{Refused: "rejected"}, "push refused: rejected; r"},
		"shaped result": {Result{Report: "r", End: EndProvider, Provider: "provider: x", Shaped: true, Verdict: "not-done"}, Push{None: "no commit"}, "verdict not-done; r"},
	} {
		g := newRig(Config{As: "m", Width: 1})
		g.m.pusher = &fakePusher{def: c.pu}
		p := pk("c1")
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
		_, err := g.tick(t)
		require.NoError(t, err)
		g.r.child("c1").end(c.r)
		g.s.reset()
		_, err = g.tick(t)
		require.NoError(t, err)
		require.Equal(t, []string{"finish --as m c1@1 --report " + c.want + " --failed --epoch 7"}, g.s.lines("finish"), name)
	}
}

// TestFinishWithoutABranchInThePacketCarriesNone pins that --branch is the
// packet's: with none there is nothing to push to, and the finish is failed,
// no commit, naming no head and no branch.
func TestFinishWithoutABranchInThePacketCarriesNone(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1})
	p := pk("c1")
	p.Branch = "" // the branch is the launch's packet's; none here
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "h1", Report: "done"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, nil)))
	g.s.reset()
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	want := "finish --as m c1@1 --report no commit: the packet names no branch to push to; done --failed --epoch 7"
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

// TestRecoveryWithExcessInFlightPacketsDoesNotExceedWidth pins that recovery
// clamps in-flight working packets to member width so a restart never oversubscribes:
// excess cards remain queued and are recovered on subsequent passes as capacity opens.
func TestRecoveryWithExcessInFlightPacketsDoesNotExceedWidth(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	p1, p2, p3 := pk("c1"), pk("c2"), pk("c3")
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p1), working("c2", 1, &p2), working("c3", 1, &p3)))
	acted, err := g.tick(t)
	require.NoError(t, err)
	require.Equal(t, 2, acted, "clamped to width 2")
	require.Equal(t, 2, g.m.Running())
	require.Equal(t, []string{"c1", "c2"}, g.r.started(), "c3 is left for a subsequent pass")
	require.Empty(t, g.s.lines("take"))
	require.Equal(t, 1, strings.Count(g.out.String(), "recover "), "one line for the one card left:\n%s", g.out)
	require.Contains(t, g.out.String(), "recover c3 deferred: width 2 full\n")

	// When c1 finishes and is reported, the freed slot allows c3 to be recovered.
	g.r.child("c1").end(Result{Ran: true, OK: true, Head: "head-1", Report: "done c1"})
	g.s.reset()
	// Next pass: queue still reports working cards until sprint processes finish.
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p1), working("c2", 1, &p2), working("c3", 1, &p3)))
	acted, err = g.tick(t)
	require.NoError(t, err)
	require.Equal(t, 2, acted, "one finish and one start")
	require.Equal(t, 2, g.m.Running())
	finishes := g.s.lines("finish")
	require.Len(t, finishes, 1)
	require.Contains(t, finishes[0], "c1@1")
	require.Equal(t, []string{"c1", "c2", "c3"}, g.r.started())
}

// TestReaderRecoveryWithExcessInFlightPacketsDoesNotExceedWidth pins the same
// width clamping for a reader recovering reading packets.
func TestReaderRecoveryWithExcessInFlightPacketsDoesNotExceedWidth(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 1, Reader: true})
	r1 := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
	r2 := Packet{Card: "r2", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &r1), reading("r2", &r2)))
	acted, err := g.tick(t)
	require.NoError(t, err)
	require.Equal(t, 1, acted, "clamped to width 1")
	require.Equal(t, 1, g.m.Running())
	require.Equal(t, []string{"r1"}, g.r.started())

	// When r1 finishes with a verdict, reporting it frees the slot for r2.
	g.r.child("r1").end(Result{Ran: true, OK: true, Verdict: "ok", Report: "clean"})
	g.s.reset()
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &r1), reading("r2", &r2)))
	acted, err = g.tick(t)
	require.NoError(t, err)
	require.Equal(t, 2, acted, "one report and one start")
	require.Equal(t, 1, g.m.Running())
	reports := g.s.lines("report")
	require.Len(t, reports, 1)
	require.Contains(t, reports[0], "r1")
	require.Equal(t, []string{"r1", "r2"}, g.r.started())
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
		err := g.m.Beat()
		require.ErrorContains(t, err, "beat: exit 2: no route to the store")
		assert.Empty(t, g.s.lines("queue"), "a beat reads no queue")
		g.s.set("queue", 0, queueJSON(t, 7))
		_, err = g.tick(t)
		require.NoError(t, err, "the work pass sends no beat, so a beat that fails stops nothing")
		assert.Len(t, g.s.lines("beat"), 2, "the beat was asked again once, and the pass sent none")
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

// TestBeatCarriesNoLoadOfItsOwn pins the beat as `fleet beat <member>` alone, however
// many cards are running: the load is the machine's CPU use, which nova-sprint fleet beat
// measures and keeps the ten-second peak of (docs/SPEC-SPRINT.md, the fleet), never the
// member's running over its width.
func TestBeatCarriesNoLoadOfItsOwn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ running, width int }{{0, 4}, {1, 3}, {2, 2}} {
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
			require.Equal(t, tc.running, g.m.Running())
			g.s.reset()
			require.NoError(t, g.m.Beat())
			require.Equal(t, []string{"fleet beat m"}, g.s.lines("beat"))
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
		"The checkout is on branch work/c1;",
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

// TestCardTextForAWorkPacketWithNoBaseNamesTheJob pins the other branch of
// the mechanics: with no base the card still points at JOB.md and names the
// branch, and the finish line names it too.
func TestCardTextForAWorkPacketWithNoBaseNamesTheJob(t *testing.T) {
	t.Parallel()
	got := CardText(Packet{Card: "c2", Kind: "work", As: "m1", Primary: "p2", Stream: "b", Attempt: 1, Gen: 1, Epoch: 3, Branch: "work/c2"}, "nova-sprint")
	if !strings.HasPrefix(got, "## From the sprint\n\n") {
		t.Fatalf("a packet with no brief starts at the sprint's part:\n%s", got)
	}
	for _, want := range []string{"The checkout is on branch work/c2; JOB.md", "finish --as m1 c2@1 --epoch 3 --branch work/c2 "} {
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

// reading is a reader's queue card, in flight.
func reading(id string, p *Packet) queueCard { return queueCard{ID: id, Col: "reading", Packet: p} }

// asked is a reader's queue card, not yet begun.
func asked(id string, p *Packet) queueCard { return queueCard{ID: id, Col: "asked", Packet: p} }

// TestAMovedClaimIsReapedNotReported pins that a child settles only the claim
// it was launched for. The child ends ok, but the queue now shows the card at
// another generation (a redeal), in another epoch (a clear), or, for a read,
// at another attempt: its result is nobody's, so no finish or read is issued,
// the child is dropped, and the new claim is run from the packet the queue
// carries.
func TestAMovedClaimIsReapedNotReported(t *testing.T) {
	t.Parallel()
	t.Run("a redeal: the generation moved", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "m", Width: 2})
		old := pk("c1") // gen 1, epoch 7
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &old)))
		if _, err := g.tick(t); err != nil {
			t.Fatal(err)
		}
		g.r.child("c1").end(Result{Ran: true, OK: true, Head: "h", Report: "done"})
		moved := pk("c1")
		moved.Gen = 2
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &moved)))
		g.s.reset()
		acted, err := g.tick(t)
		if err != nil {
			t.Fatal(err)
		}
		// Removing the `l.gen != c.Packet.Gen` term of the moved-claim test in
		// Member.Tick makes this fail: the old child is finished as c1@1.
		if got := g.s.lines("finish"); len(got) != 0 {
			t.Fatalf("a moved claim was reported: %q", got)
		}
		if acted != 1 || g.m.Running() != 1 {
			t.Fatalf("acted=%d running=%d, want the one start of the new claim", acted, g.m.Running())
		}
		if ps := g.r.packets; len(ps) != 2 || ps[1].Gen != 2 || ps[1].Epoch != 7 {
			t.Fatalf("started %+v, want the old launch then the gen-2 packet", ps)
		}
		if !strings.Contains(g.out.String(), "reaped c1: the claim moved") {
			t.Fatalf("the reap was not printed: %q", g.out.String())
		}
	})
	t.Run("a clear: the epoch moved, the generation did not", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "m", Width: 2})
		old := pk("c1") // gen 1, epoch 7
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &old)))
		if _, err := g.tick(t); err != nil {
			t.Fatal(err)
		}
		g.r.child("c1").end(Result{Ran: true, OK: true, Head: "h", Report: "done"})
		moved := pk("c1")
		moved.Epoch = 8
		g.s.set("queue", 0, queueJSON(t, 8, working("c1", 1, &moved)))
		g.s.reset()
		acted, err := g.tick(t)
		if err != nil {
			t.Fatal(err)
		}
		// Removing the `l.epoch != c.Packet.Epoch` term of the moved-claim test
		// in Member.Tick makes this fail: the old child is finished under
		// epoch 7 against a card of epoch 8.
		if got := g.s.lines("finish"); len(got) != 0 {
			t.Fatalf("a claim of another epoch was reported: %q", got)
		}
		if acted != 1 || g.m.Running() != 1 {
			t.Fatalf("acted=%d running=%d, want the one start of the new claim", acted, g.m.Running())
		}
		if ps := g.r.packets; len(ps) != 2 || ps[1].Epoch != 8 || ps[1].Gen != 1 {
			t.Fatalf("started %+v, want the old launch then the epoch-8 packet", ps)
		}
	})
	t.Run("a read re-asked: the attempt moved", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "r", Width: 2, Reader: true})
		old := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
		g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &old)))
		if _, err := g.tick(t); err != nil {
			t.Fatal(err)
		}
		g.r.child("r1").end(Result{Ran: true, Verdict: "ok", Report: "clean"})
		moved := old
		moved.Attempt = 2
		g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &moved)))
		g.s.reset()
		if _, err := g.tick(t); err != nil {
			t.Fatal(err)
		}
		// Removing the `l.attempt != c.Packet.Attempt` term of the moved-claim
		// test in Member.Tick makes this fail: attempt 1's verdict is filed
		// against attempt 2.
		if got := g.s.lines("report"); len(got) != 0 {
			t.Fatalf("a read of another attempt was reported: %q", got)
		}
		if ps := g.r.packets; len(ps) != 2 || ps[1].Attempt != 2 || g.m.Running() != 1 {
			t.Fatalf("started %+v running=%d, want the old launch then attempt 2", ps, g.m.Running())
		}
	})
	t.Run("a child still running is left alone and not started over", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "m", Width: 2})
		old := pk("c1")
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &old)))
		if _, err := g.tick(t); err != nil {
			t.Fatal(err)
		}
		moved := pk("c1")
		moved.Gen = 2
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &moved)))
		if acted, err := g.tick(t); err != nil || acted != 0 {
			t.Fatalf("acted=%d err=%v", acted, err)
		}
		// Removing the `continue` after `!l.child.Done()` in the moved-claim
		// branch makes this fail: the card is reaped while its child runs, and
		// a second child starts beside the first.
		if len(g.r.packets) != 1 || g.m.Running() != 1 {
			t.Fatalf("started %d, running %d: a moved claim's live child is reaped only when it ends", len(g.r.packets), g.m.Running())
		}
	})
}

// TestAReadWithNoVerdictIsReturnedForTheSprintToAskAgain pins that a reader
// files a finding only when it has one: a child that did not run (a launch
// refused at staging), or ran and gave no verdict (or one that is not ok or
// broken), files no ok or broken; the read is returned with the reason, in
// the same pass, so the sprint's next tick asks it of another reader, and the
// child holds no place.
func TestAReadWithNoVerdictIsReturnedForTheSprintToAskAgain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		res  Result
		want string
	}{
		{"ran, no verdict", Result{Ran: true, OK: true, Verdict: "", Report: "it ran"},
			`read --as r --return r1 --reason no verdict (ran=true verdict=""): it ran --epoch 7`},
		{"ran, a word that is no verdict", Result{Ran: true, OK: true, Verdict: "maybe", Report: "it ran"},
			`read --as r --return r1 --reason no verdict (ran=true verdict="maybe"): it ran --epoch 7`},
		{"did not run, no verdict", Result{Ran: false, Verdict: "", Report: "STAGE FAIL no bench mirror"},
			`read --as r --return r1 --reason no verdict (ran=false verdict=""): STAGE FAIL no bench mirror --epoch 7`},
		{"did not run, a stale verdict line", Result{Ran: false, Verdict: "ok", Report: "the child ended without a result"},
			`read --as r --return r1 --reason no verdict (ran=false verdict="ok"): the child ended without a result --epoch 7`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newRig(Config{As: "r", Width: 1, Reader: true})
			p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
			g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
			if _, err := g.tick(t); err != nil {
				t.Fatal(err)
			}
			g.r.child("r1").end(tc.res)
			g.s.reset()
			acted, err := g.tick(t)
			require.NoError(t, err)
			// Removing the `!r.Ran ||` term (the "did not run" cases) or the
			// verdict-is-ok-or-broken term (the "no verdict" cases) of the
			// reader's no-verdict test in Member.Tick makes this fail.
			assert.Empty(t, g.s.lines("report"), "a read with no finding files no ok or broken")
			assert.Equal(t, []string{tc.want}, g.s.lines("return"))
			assert.Equal(t, 1, acted)
			assert.Equal(t, 0, g.m.Running(), "a returned read holds no place")
			assert.Empty(t, g.s.lines("begin"))
		})
	}
}

// TestAWidthOneReaderReturningThreeReadsHoldsAtMostOne pins the live break of
// 2026-10-01: a width-1 reader whose launches are refused returns each read
// before it begins the next, so it never holds more than one, and after the
// third return it holds none.
func TestAWidthOneReaderReturningThreeReadsHoldsAtMostOne(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 1, Reader: true})
	ps := []Packet{}
	for _, id := range []string{"r1", "r2", "r3"} {
		ps = append(ps, Packet{Card: id, Kind: "read", As: "r", Attempt: 1, Epoch: 7})
	}
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &ps[0])))
	_, err := g.tick(t)
	require.NoError(t, err)
	for i := range ps {
		g.r.child(ps[i].Card).end(Result{Ran: false, Report: "STAGE FAIL no bench mirror"})
		cards := []queueCard{reading(ps[i].Card, &ps[i])}
		if i+1 < len(ps) {
			cards = append(cards, asked(ps[i+1].Card, &ps[i+1]))
		}
		g.s.set("queue", 0, queueJSON(t, 7, cards...))
		_, err := g.tick(t)
		require.NoError(t, err)
		assert.LessOrEqual(t, g.m.Running(), 1, "after return %d", i+1)
	}
	assert.Len(t, g.s.lines("return"), 3)
	assert.Equal(t, []string{"read --as r --begin r2 --epoch 7", "read --as r --begin r3 --epoch 7"}, g.s.lines("begin"))
	assert.Equal(t, 0, g.m.Running(), "three returned, none held")
	assert.Empty(t, g.s.lines("report"))
}

// TestAReadItReturnedIsNotBegunAgainBeforeTheRetry pins the reader's side of
// a return that is not a read: the sprint may ask the returned read of the
// same reader again (tla/DirtyTick.tla, JudgedOnlyAfterTheBound), and a
// reader that cannot launch does not begin it again before ReadStageRetry, so
// it does not take and return the same read every pass.
func TestAReadItReturnedIsNotBegunAgainBeforeTheRetry(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 1, Reader: true})
	p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	g.r.child("r1").end(Result{Ran: false, Report: "NATIVE REFUSED: no identity"})
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Len(t, g.s.lines("return"), 1)
	// the sprint asked it of this reader again, in place
	g.s.set("queue", 0, queueJSON(t, 7, asked("r1", &p)))
	g.s.reset()
	_, err = g.m.Tick(time.Unix(0, 0).Add(ReadStageRetry - time.Second))
	require.NoError(t, err)
	assert.Empty(t, g.s.lines("begin"), "begun again inside the retry")
	assert.Equal(t, 0, g.m.Running())
	_, err = g.m.Tick(time.Unix(0, 0).Add(ReadStageRetry))
	require.NoError(t, err)
	assert.Equal(t, []string{"read --as r --begin r1 --epoch 7"}, g.s.lines("begin"), "begun once the retry has passed")
}

// TestAReadReportsOnlyItsVerdict pins that the verdict flag is the reader's
// own word, never the harness's ok: `broken` files --broken even when the
// child ran ok, and `ok` files --ok even when OK is false.
func TestAReadReportsOnlyItsVerdict(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		res  Result
		want string
	}{
		{"broken though the child ran ok", Result{Ran: true, OK: true, Verdict: "broken", Report: "the merge is wrong"}, "read --as r --broken r1 --finding the merge is wrong --epoch 7"},
		{"ok though OK is false", Result{Ran: true, OK: false, Verdict: "ok", Report: "clean"}, "read --as r --ok r1 --finding clean --epoch 7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newRig(Config{As: "r", Width: 1, Reader: true})
			p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
			g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
			if _, err := g.tick(t); err != nil {
				t.Fatal(err)
			}
			g.r.child("r1").end(tc.res)
			g.s.reset()
			if _, err := g.tick(t); err != nil {
				t.Fatal(err)
			}
			// Replacing `r.Verdict == "broken"` with `!r.OK` in the read's
			// report makes this fail in both cases.
			if got := g.s.lines("report"); !slices.Equal(got, []string{tc.want}) {
				t.Fatalf("report lines: %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReadBeginNamesTheCards pins that the reads begun are named: two asked,
// room for one, and the verb names the first asked in queue order (not the
// sorted order), with the epoch; only that packet is started.
func TestReadBeginNamesTheCards(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 1, Reader: true})
	first := Packet{Card: "zr", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
	second := Packet{Card: "ar", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
	g.s.set("queue", 0, queueJSON(t, 7, asked("zr", &first), asked("ar", &second)))
	acted, err := g.tick(t)
	if err != nil {
		t.Fatal(err)
	}
	// Dropping the `len(ids) < room` bound of the named reads in Member.Tick
	// makes this fail: both are named and both started, past the width.
	if got := g.s.lines("begin"); !slices.Equal(got, []string{"read --as r --begin zr --epoch 7"}) {
		t.Fatalf("begin lines: %q, want the first asked only", got)
	}
	if acted != 1 || !slices.Equal(g.r.started(), []string{"zr"}) || g.m.Running() != 1 {
		t.Fatalf("acted=%d started=%v running=%d, want zr only", acted, g.r.started(), g.m.Running())
	}
}

// TestTakeAsksForTheRoom pins the limit as the member's whole room, whatever
// the queue showed ready: more ready than room asks for the room, fewer asks
// for the room too, and a child already running takes its place out of it.
func TestTakeAsksForTheRoom(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                         string
		width, running, ready, limit int
	}{
		{"room 3, ready 5", 3, 0, 5, 3},
		{"room 3, ready 1", 3, 0, 1, 3},
		{"width 5 with 2 running, ready 5", 5, 2, 5, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newRig(Config{As: "m", Width: tc.width})
			var cards []queueCard
			for i := 0; i < tc.running; i++ {
				p := pk("w" + strconv.Itoa(i))
				cards = append(cards, working(p.Card, 1, &p))
			}
			for i := 0; i < tc.ready; i++ {
				cards = append(cards, ready("c"+strconv.Itoa(i)))
			}
			g.s.set("queue", 0, queueJSON(t, 7, cards...))
			g.s.set("take", 0, takeJSON(t))
			if _, err := g.tick(t); err != nil {
				t.Fatal(err)
			}
			// Replacing `strconv.Itoa(room)` with the ready count in the take
			// verb's --limit makes the "ready 5" cases fail.
			want := "take --as m --limit " + strconv.Itoa(tc.limit) + " --json --epoch 7"
			if got := g.s.lines("take"); !slices.Equal(got, []string{want}) {
				t.Fatalf("take lines: %q, want %q", got, want)
			}
		})
	}
}

// TestASpentReadStaysSpentAcrossTicks pins that a read whose child gave no
// verdict, and whose return the store refused, is not run again while its
// claim stands: three more ticks start nothing and issue no ok or broken.
// Removing the spent record (member.go, the `l.spent = true` line) fails it:
// the card would be restarted every tick.
func TestASpentReadStaysSpentAcrossTicks(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 2, Reader: true})
	p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7, Head: "h1"}
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
	g.s.set("return", 1, "refused")
	if _, err := g.tick(t); err != nil {
		t.Fatal(err)
	}
	g.r.child("r1").end(Result{Ran: true, Verdict: ""})
	starts := len(g.r.started())
	for i := 0; i < 3; i++ {
		g.s.reset()
		if _, err := g.tick(t); err != nil {
			t.Fatal(err)
		}
		if got := g.s.lines("report"); len(got) != 0 {
			t.Fatalf("tick %d issued %q, want no ok or broken for a spent read", i+2, got)
		}
	}
	if len(g.r.started()) != starts {
		t.Fatalf("a spent read was started again (%d starts, was %d)", len(g.r.started()), starts)
	}
	if g.m.Running() != 0 {
		t.Fatalf("a spent read holds a place: running=%d", g.m.Running())
	}
}

// TestAnEndedLaunchWhoseCardCameBackReadyIsReapedAndTheWidthFreed pins the
// finish of a launch whose claim moved while it ran and whose card the queue now
// lists back in this member's own ready (asked) column at a later generation
// (attempt): the store's redeal and a withdrawn card dealt again both put it
// there, never in working. The ended child is reaped, never reported, within
// one tick, and the place it held is taken again in the same tick
// (tla/CardContract.tla, Reap and EveryLaunchEnds). Moving the moved-claim test
// below the in-flight filter in Member.Tick fails it: the ended launch holds
// the width with nothing reported, the half-hour sit of a finished card.
func TestAnEndedLaunchWhoseCardCameBackReadyIsReapedAndTheWidthFreed(t *testing.T) {
	t.Parallel()
	t.Run("work: dealt again to this member's ready", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "m", Width: 1})
		old := pk("c1") // gen 1, epoch 7
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &old)))
		_, err := g.tick(t)
		require.NoError(t, err)
		g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "h", Report: "done"})
		again := pk("c1")
		again.Gen = 3
		g.s.set("queue", 0, queueJSON(t, 7, queueCard{ID: "c1", Col: "ready", Gen: 3, Packet: &again}))
		g.s.set("take", 0, takeJSON(t, again))
		g.s.reset()
		acted, err := g.tick(t)
		require.NoError(t, err)
		require.Empty(t, g.s.lines("finish"), "a moved claim is never reported")
		require.Contains(t, g.out.String(), "reaped c1: the claim moved")
		require.Len(t, g.s.lines("take"), 1, "the freed place is taken in the same tick")
		require.Equal(t, 1, acted)
		require.Equal(t, 1, g.m.Running())
		require.Len(t, g.r.packets, 2)
		require.Equal(t, 3, g.r.packets[1].Gen)
	})
	t.Run("read: asked again at the next attempt", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "r", Width: 1, Reader: true})
		old := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
		g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &old)))
		_, err := g.tick(t)
		require.NoError(t, err)
		g.r.child("r1").end(Result{Ran: true, Verdict: "ok", Report: "clean"})
		again := old
		again.Attempt = 2
		g.s.set("queue", 0, queueJSON(t, 7, asked("r1", &again)))
		g.s.reset()
		_, err = g.tick(t)
		require.NoError(t, err)
		require.Empty(t, g.s.lines("report"), "attempt 1's verdict is never filed against attempt 2")
		require.Contains(t, g.out.String(), "reaped r1: the claim moved")
		require.Len(t, g.r.packets, 2)
		require.Equal(t, 2, g.r.packets[1].Attempt)
	})
}

// A member told to drain (its binary was replaced) reports what ended and takes
// nothing new, though room is left and cards are ready; it starts no child for an
// in-flight card of no child of ours either.
func TestADrainingMemberTakesNothingNewButReportsWhatEnded(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	g.s.set("take", 0, takeJSON(t, pk("c1")))
	_, err := g.tick(t)
	require.NoError(t, err)
	require.Equal(t, 1, g.m.Running())
	g.m.Drain()
	p := pk("c1")
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Report: "done"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p), ready("c2"), working("c3", 1, &Packet{Card: "c3", Kind: "work", Gen: 1, Epoch: 7})))
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Len(t, g.s.lines("finish"), 1, "what ended is reported")
	require.Empty(t, g.s.lines("take"), "nothing new is taken")
	require.Equal(t, []string{"c1"}, g.r.started(), "no child is started, taken or recovered")
}

// tickAt is one tick at the given second.
func (g *rig) tickAt(t *testing.T, sec int64) (int, error) {
	t.Helper()
	return g.m.Tick(time.Unix(sec, 0))
}

// TestAReadWhoseStageFailedOnceIsRunAgainThenReads pins the rule of docs/SPEC-SPRINT.md's
// readers: a read's stage failure is never a verdict. No verb is sent and the reader keeps its
// place until ReadStageRetry has passed, then runs the same card again, with no other verb;
// if that run stages, its verdict is reported as any verdict is. Treating the stage failure
// like any no-verdict end fails it: the read is returned at the first failure.
func TestAReadWhoseStageFailedOnceIsRunAgainThenReads(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 1, Reader: true})
	p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7, Head: "h1"}
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
	_, err := g.tickAt(t, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"r1"}, g.r.started())
	g.r.child("r1").end(Result{End: EndStaging, Staging: "git checkout failed", Report: "no child ran"})
	g.s.reset()

	acted, err := g.tickAt(t, 1)
	require.NoError(t, err)
	assert.Zero(t, acted)
	assert.Empty(t, g.s.lines("report"), "a first stage failure sends no verb")
	assert.Equal(t, 1, g.m.Running(), "the reader keeps its place while the read waits to run again")
	assert.Contains(t, g.out.String(), "stage failed")

	acted, err = g.tickAt(t, int64(ReadStageRetry/time.Second)-1)
	require.NoError(t, err)
	assert.Zero(t, acted, "before ReadStageRetry the read is not run again")
	assert.Equal(t, []string{"r1"}, g.r.started())

	acted, err = g.tickAt(t, int64(ReadStageRetry/time.Second)+1)
	require.NoError(t, err)
	assert.Equal(t, 1, acted)
	assert.Equal(t, []string{"r1", "r1"}, g.r.started(), "the same read, run again by the same reader")
	assert.Empty(t, g.s.lines("report"))
	assert.Empty(t, g.s.lines("begin"), "no other read is begun ahead of the retry")

	g.r.child("r1").end(Result{Ran: true, OK: true, Verdict: "ok", Report: "clean"})
	_, err = g.tickAt(t, int64(ReadStageRetry/time.Second)+2)
	require.NoError(t, err)
	assert.Equal(t, []string{"read --as r --ok r1 --finding clean --epoch 7"}, g.s.lines("report"))
}

// TestAReadWhoseStageFailedTwiceIsReturned pins that the retry is once: a second stage failure
// hands the read back with its reason (`read --return`), so the sprint asks another reader, and
// the reader holds no place for it. Retrying in place for ever fails it.
func TestAReadWhoseStageFailedTwiceIsReturned(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 1, Reader: true})
	p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7, Head: "h1"}
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
	fail := Result{End: EndStaging, Staging: "staging refused: head h1 is in neither the mirror nor origin", Report: "no child ran"}
	retry := int64(ReadStageRetry/time.Second) + 1
	_, err := g.tickAt(t, 0)
	require.NoError(t, err)
	g.r.child("r1").end(fail)
	_, err = g.tickAt(t, 1)
	require.NoError(t, err)
	_, err = g.tickAt(t, retry+1)
	require.NoError(t, err)
	require.Equal(t, []string{"r1", "r1"}, g.r.started())
	g.s.reset()
	g.r.child("r1").end(fail)

	acted, err := g.tickAt(t, retry+2)
	require.NoError(t, err)
	assert.Equal(t, 1, acted)
	assert.Empty(t, g.s.lines("report"), "no verdict is reported")
	got := g.s.lines("return")
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "read --as r --return r1 --reason ")
	assert.Contains(t, got[0], "staging refused: head h1", "the reason is the stage's")
	assert.Contains(t, got[0], "--epoch 7")
	assert.Zero(t, g.m.Running())
	assert.Equal(t, []string{"r1", "r1"}, g.r.started(), "not run a third time")
}

// TestTheStageRetryIsRememberedAcrossAStartThatFails pins that the once is once: when the start
// of the retry fails and the recovery path later launches the read, that launch is the retry,
// and its stage failure is returned. Forgetting the retry at the failed start fails it: the
// recovered launch gets a retry of its own.
func TestTheStageRetryIsRememberedAcrossAStartThatFails(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "r", Width: 1, Reader: true})
	p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7, Head: "h1"}
	g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
	fail := Result{End: EndStaging, Staging: "git checkout failed", Report: "no child ran"}
	retry := int64(ReadStageRetry/time.Second) + 1
	_, err := g.tickAt(t, 0)
	require.NoError(t, err)
	g.r.child("r1").end(fail)
	_, err = g.tickAt(t, 1)
	require.NoError(t, err)
	g.r.failFor["r1"] = true
	_, err = g.tickAt(t, retry+1) // the retry's start fails
	require.NoError(t, err)
	g.r.failFor["r1"] = false
	_, err = g.tickAt(t, retry+2) // the recovery path launches the read
	require.NoError(t, err)
	require.Equal(t, []string{"r1", "r1"}, g.r.started())
	g.s.reset()
	g.r.child("r1").end(fail)
	_, err = g.tickAt(t, retry+3)
	require.NoError(t, err)
	got := g.s.lines("return")
	require.Len(t, got, 1, "the recovered launch is the retry: its stage failure is returned")
	assert.Contains(t, got[0], "staging refused: git checkout failed")
}

// TestAWorkCardWhoseStageFailedIsFinishedFailedAsBefore pins that rule 2 is a read's: a work
// card whose launch ended at staging is judged as every other ended work card (a failed
// finish), never run again by the member.
func TestAWorkCardWhoseStageFailedIsFinishedFailedAsBefore(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1})
	p := pk("c1")
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err := g.tickAt(t, 0)
	require.NoError(t, err)
	g.r.child("c1").end(Result{End: EndStaging, Staging: "git checkout failed", Report: "no child ran"})
	g.s.reset()
	_, err = g.tickAt(t, 1) // pushes (none: no head), then finishes
	require.NoError(t, err)
	_, err = g.tickAt(t, 2)
	require.NoError(t, err)
	fin := g.s.lines("finish")
	require.Len(t, fin, 1)
	assert.Contains(t, fin[0], "--failed")
}

// secondsOfLoad is a Sampler whose seconds are the percents it is fed: each Step takes
// the next.
func secondsOfLoad(pcts ...float64) (s *hostload.Sampler, feed func(...float64)) {
	var q []float64
	s = hostload.NewSampler(hostload.Source{CPUSecond: func() (float64, error) {
		p := q[0]
		q = q[1:]
		return p, nil
	}})
	feed = func(more ...float64) {
		for _, p := range more {
			q = append(q, p)
			s.Step()
		}
	}
	feed(pcts...)
	return s, feed
}

// TestBeatCarriesTheHighestSecondSinceTheLastBeat: the beat names the highest of the
// one-second samples taken since the last beat that was written (so nova-sprint's own
// ten-second highest over the beats is the highest of the last ten seconds, not of
// twenty); with no sample it names none and the beat measures; a beat the store did not
// answer leaves its samples for the next.
func TestBeatCarriesTheHighestSecondSinceTheLastBeat(t *testing.T) {
	t.Parallel()
	meter, feed := secondsOfLoad()
	g := newRig(Config{As: "m", Width: 1, Meter: meter})
	g.s.set("queue", 0, queueJSON(t, 7))
	beat := func() string {
		t.Helper()
		g.s.reset()
		require.NoError(t, g.m.Beat())
		return strings.Join(g.s.lines("beat"), "|")
	}
	require.Equal(t, "fleet beat m", beat(), "no sample yet: the beat measures")
	feed(10, 70, 20)
	require.Equal(t, "fleet beat m --load 70.0", beat(), "10, 70, 20 beat 70")
	require.Equal(t, "fleet beat m", beat(), "no sample since: the beat measures")
	feed(5, 5, 5)
	g.s.set("beat", 2, "no store")
	g.s.reset()
	require.Error(t, g.m.Beat(), "a store that does not answer is a beat that failed")
	g.s.set("beat", 0, "")
	feed(5)
	require.Equal(t, "fleet beat m --load 5.0", beat(), "the 70 went with the beat that wrote it; the lost beat's samples ride the next")
	feed(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12)
	require.Equal(t, "fleet beat m --load 12.0", beat(), "at most the ten seconds the ring holds")
}

// TestAReaderBeatsNothing: a reader sends no fleet beat and reads no sample; its beat is its
// queue (docs/SPEC-SPRINT.md, the readers).
func TestAReaderBeatsNothing(t *testing.T) {
	t.Parallel()
	meter, _ := secondsOfLoad(40)
	g := newRig(Config{As: "r", Width: 1, Reader: true, Meter: meter})
	g.s.set("queue", 0, queueJSON(t, 7))
	_, err := g.tick(t)
	require.NoError(t, err)
	require.Empty(t, g.s.lines("beat"))
	require.NoError(t, g.m.Beat())
	require.Empty(t, g.s.lines("beat"))
	require.Equal(t, []string{"queue --as r --json", "queue --as r --json"}, g.s.lines("queue"), "the pass's queue, then the beat's")
}

// A store that times out once on the beat or the queue is asked again once
// before the member reports a miss (docs/SPEC-SPRINT.md section 5; the model's
// Lapse needs MissedBeatsDown misses): the tick succeeds, the verb was asked
// twice, and nothing was reported. Asked twice and failing twice is the error
// the member already reported.
func TestAStoreThatTimesOutOnceIsAskedAgain(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"beat", "queue"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			g := newRig(Config{As: "m", Width: 2})
			g.s.set("queue", 0, queueJSON(t, 7))
			g.s.failOnce(verb, 2, "i/o timeout")
			if verb == "beat" {
				require.NoError(t, g.m.Beat())
			} else {
				_, err := g.tick(t)
				require.NoError(t, err)
			}
			require.Len(t, g.s.lines(verb), 2, "the verb is asked again once")
		})
	}
}

// TestTheRunnerIsToldWhenTheMemberIsDoneWithALaunch pins Ender: each launch the member is
// done with is told to the runner once, failed when its finish failed or a read was returned
// with no verdict, so the runner removes or keeps what the launch staged; a launch still
// running, or ended and not yet reported, is told nothing.
func TestTheRunnerIsToldWhenTheMemberIsDoneWithALaunch(t *testing.T) {
	t.Parallel()
	ok := Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc123", Report: "done"}
	for _, tc := range []struct {
		name   string
		reader bool
		res    Result
		drop   bool // the queue stops listing the card: reaped
		want   []string
	}{
		{"a work card finished ok", false, ok, false, []string{"c1:false"}},
		{"a work card finished failed", false, Result{Ran: true, Report: "the harness fell over"}, false, []string{"c1:true"}},
		{"a work card reaped", false, ok, true, []string{"c1:false"}},
		{"a read with its verdict", true, Result{Ran: true, OK: true, Verdict: "broken", Report: "a bug"}, false, []string{"c1:false"}},
		{"a read returned with no verdict", true, Result{Ran: true, Report: "no verdict"}, false, []string{"c1:true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newRig(Config{As: "m", Width: 1, Reader: tc.reader})
			g.m.pusher = &fakePusher{def: Push{Sha: fullSha}}
			p := pk("c1")
			card := working("c1", 1, &p)
			if tc.reader {
				p = Packet{Card: "c1", Kind: "read", As: "m", Attempt: 1, Epoch: 7}
				card = reading("c1", &p)
			}
			g.s.set("queue", 0, queueJSON(t, 7, card))
			_, err := g.tick(t)
			require.NoError(t, err)
			_, err = g.tick(t)
			require.NoError(t, err)
			assert.Empty(t, g.r.endedLaunches(), "a launch still running is told nothing")
			g.r.child("c1").end(tc.res)
			if tc.drop {
				g.s.set("queue", 0, queueJSON(t, 7))
			}
			_, err = g.tick(t)
			require.NoError(t, err)
			assert.Equal(t, tc.want, g.r.endedLaunches())
			assert.Equal(t, 0, g.m.Running())
		})
	}
}

// TestNoRoomRefusesAtStagingWithTheReason pins Config.Room: while it says no, no child is
// started, and every card the tick would start (a taken one, a recovered one, a read begun)
// is ended on the staging refusal path with the reason, so the sprint deals it elsewhere and
// says why; the refusal and the resume are each said once in the member's log.
func TestNoRoomRefusesAtStagingWithTheReason(t *testing.T) {
	t.Parallel()
	room, why := false, "free disk 3.0 GiB under the floor of 10 GiB"
	g := newRig(Config{As: "m", Width: 2, Room: func() (bool, string) { return room, why }})
	p := pk("c2")
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1"), working("c2", 1, &p)))
	g.s.set("take", 0, takeJSON(t, pk("c1")))
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Empty(t, g.r.started(), "neither a taken card nor a recovered one is started")
	assert.Equal(t, []string{
		"finish --as m c2@1 --failed --report staging refused: " + why + " --epoch 7",
		"finish --as m c1@1 --failed --report staging refused: " + why + " --epoch 7",
	}, g.s.lines("finish"))
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(g.out.String(), "take REFUSED: "+why+"\n"), "said once, with the reason")

	r := newRig(Config{As: "r", Width: 1, Reader: true, Room: func() (bool, string) { return false, why }})
	a := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
	r.s.set("queue", 0, queueJSON(t, 7, asked("r1", &a)))
	_, err = r.tick(t)
	require.NoError(t, err)
	assert.Empty(t, r.r.started())
	assert.Equal(t, []string{"read --as r --return r1 --reason staging refused: " + why + " --epoch 7"}, r.s.lines("return"))
	// a read handed back under the floor is a return like any other: asked again of
	// this reader, it is not begun again before ReadStageRetry (the sprint's re-ask
	// bound then judges it, internal/sprint MaxReadReasks)
	r.s.reset()
	_, err = r.tick(t)
	require.NoError(t, err)
	assert.Empty(t, r.s.lines("begin"), "begun again inside the retry")
	assert.Empty(t, r.s.lines("return"))

	room, why = true, "free disk 120.0 GiB above the floor of 10 GiB"
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"c2", "c1"}, g.r.started())
	assert.Empty(t, g.s.lines("finish"))
	assert.Contains(t, g.out.String(), "NOTE take resumed: "+why+"\n")
}

// blockingSprint is a script whose one verb holds until released: a pass held by a slow
// store, the way sixteen finishes from a machine far from the store hold one.
type blockingSprint struct {
	*scriptSprint
	verb     string
	entered  chan struct{}
	released chan struct{}
}

func (b *blockingSprint) Run(args ...string) (int, []byte) {
	if verbOf(args) == b.verb {
		b.entered <- struct{}{}
		<-b.released
	}
	return b.scriptSprint.Run(args...)
}

// lockedBuffer is an output two goroutines write.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// heldPass is a member whose work pass is held in a finish: a card it recovered has ended,
// its push is made (the pass advanced at the clock's start), and the finish does not answer.
// It returns the member, the script, the clock's setter, the release and the pass's end.
func heldPass(t *testing.T) (*Member, *blockingSprint, func(time.Duration), func(), <-chan struct{}) {
	t.Helper()
	start := time.Unix(1_000_000, 0)
	var at atomic.Int64
	at.Store(start.UnixNano())
	bs := &blockingSprint{scriptSprint: newScript(), verb: "finish", entered: make(chan struct{}), released: make(chan struct{})}
	r := newRunner()
	m := New(Config{As: "m", Width: 2, Now: func() time.Time { return time.Unix(0, at.Load()) }}, bs, r, &fakePusher{def: Push{Sha: fullSha}}, &bytes.Buffer{})
	p := pk("c1")
	bs.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err := m.Tick(start)
	require.NoError(t, err)
	r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc", Report: "done"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.Tick(start) // ignored: the pass's own result is not what these tests pin
	}()
	<-bs.entered
	after := func(d time.Duration) { at.Store(start.Add(d).UnixNano()) }
	var once sync.Once
	release := func() { once.Do(func() { close(bs.released) }) }
	t.Cleanup(func() { release(); <-done })
	return m, bs, after, release, done
}

// beats runs the member's beat loop on ticks the test sends; stop ends it and returns once
// the loop has finished every tick it took, so what it sent can be counted.
func beats(m *Member) (ticks chan time.Time, out *lockedBuffer, stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks, out = make(chan time.Time), &lockedBuffer{}
	ended := make(chan struct{})
	go func() { defer close(ended); m.BeatLoop(ctx, ticks, out) }()
	return ticks, out, func() { cancel(); <-ended }
}

// A MEMBER BUSY IN ITS PASS KEEPS BEATING (the fleet pass of 2026-10-01: a machine pushing
// and finishing was marked down for no beat in 45 s). The pass is held in a finish for
// longer than MissedBeatsDown beat windows; the beat, on its own clock, goes on.
func TestAMemberBusyInItsPassKeepsBeating(t *testing.T) {
	t.Parallel()
	m, bs, after, release, done := heldPass(t)
	ticks, out, stop := beats(m)
	for _, d := range []time.Duration{15 * time.Second, 30 * time.Second, 45 * time.Second, 90 * time.Second, 4 * time.Minute} {
		after(d)
		ticks <- time.Time{}
	}
	stop()
	assert.Len(t, bs.lines("beat"), 5, "every tick beat while the pass was held, the longest four minutes in")
	assert.Empty(t, bs.lines("finish"), "the finish is still held")
	assert.NotContains(t, out.String(), "STOPPED")
	release()
	<-done
	assert.Len(t, bs.lines("finish"), 1)
}

// A MEMBER WHOSE PASS IS HUNG STOPS BEATING. Past BeatStall with no advance the beat stops,
// said once, so the sprint marks the member down and deals its cards elsewhere; when the
// pass goes on again, the beat does, said once.
func TestAMemberWhosePassIsHungStopsBeating(t *testing.T) {
	t.Parallel()
	m, bs, after, release, done := heldPass(t)
	ticks, out, stop := beats(m)
	after(BeatStall + time.Second)
	ticks <- time.Time{}
	after(BeatStall + time.Minute)
	ticks <- time.Time{}
	ticks <- time.Time{} // taken only once the tick before it is done
	assert.Empty(t, bs.lines("beat"), "a hung pass sends no beat")
	assert.Equal(t, 1, strings.Count(out.String(), "MEMBER BEAT STOPPED: the work pass has not advanced for 5m1s (the bound is 5m0s)"), "said once")
	release()
	<-done // the finish answered: the pass advanced
	ticks <- time.Time{}
	stop()
	assert.NotEmpty(t, bs.lines("beat"), "the beat goes on once the pass does")
	assert.Contains(t, out.String(), "NOTE beat resumed: the work pass advanced\n")
}
