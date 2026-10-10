package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every lane refreshes independently (the owner, 2026-10-10: "this is a bad design. each
// lane should refresh independently"; docs/SPEC-FRIEND.md; tla/FriendLanes.tla): each card
// a fresh run of the harness in its own lane, a free lane taking the next ready card at
// once, the main session only talking.

// freshHarness is a OneShotHarness whose runs each wait for their card's release, then
// write the card's REPORT.md and RESULT.md; Deliver is her main session's turn.
type freshHarness struct {
	mu      sync.Mutex
	dir     string
	running map[string]bool
	most    int
	started []string // each run's card, in order
	texts   []string // each run's text
	walled  []bool   // each run's context was a lane's
	jobDirs []string // the job directory each run's context named
	main    []string // the main session's turns
	gates   map[string]chan struct{}
}

func newFreshHarness(dir string) *freshHarness {
	return &freshHarness{dir: dir, running: map[string]bool{}, gates: map[string]chan struct{}{}}
}

func (h *freshHarness) Deliver(_ context.Context, text string) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.main = append(h.main, text)
	return 0, nil
}

func (h *freshHarness) gate(id string) chan struct{} {
	if h.gates[id] == nil {
		h.gates[id] = make(chan struct{})
	}
	return h.gates[id]
}

// release lets card id's run end.
func (h *freshHarness) release(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	close(h.gate(id))
}

func (h *freshHarness) RunOneShot(ctx context.Context, text string) (LaneTurn, error) {
	id := cardOfText.FindStringSubmatch(text)[1]
	h.mu.Lock()
	h.running[id] = true
	h.most = max(h.most, len(h.running))
	h.started = append(h.started, id)
	h.texts = append(h.texts, text)
	h.walled = append(h.walled, InLane(ctx))
	h.jobDirs = append(h.jobDirs, LaneDirOf(ctx, ""))
	g := h.gate(id)
	h.mu.Unlock()
	select {
	case <-g:
	case <-ctx.Done():
	}
	h.mu.Lock()
	delete(h.running, id)
	h.mu.Unlock()
	out := filepath.Join(h.dir, "outbox", id+"~15")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return LaneTurn{}, err
	}
	for _, f := range []string{"REPORT.md", "RESULT.md"} {
		if err := os.WriteFile(filepath.Join(out, f), []byte("RESULT: "+id+"\n"), 0o644); err != nil {
			return LaneTurn{}, err
		}
	}
	return LaneTurn{}, nil
}

// now is the cards running now, sorted.
func (h *freshHarness) now() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ids []string
	for id := range h.running {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// freshRig is the daemon rig over a fresh harness in a fixture of cards c01..c<n>, all
// queued and delivered, her row saying batch at width: what Freddy's and Zhi's rows say.
func freshRig(t *testing.T, n, width int) (*rig, *freshHarness) {
	var tasks [][2]string
	var ids []string
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("c%02d", i)
		tasks, ids = append(tasks, [2]string{id, "queued"}), append(ids, id)
	}
	dir := cardDirFixture(t, tasks, ids, nil)
	h := newFreshHarness(dir)
	r := newRig(t)
	state := &LaneState{}
	r.d.Deliver, r.passive, r.d.Dir = h, true, dir
	r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
	r.d.Row = func() (string, int) { return ModeBatch, width }
	r.d.Coordinator = "ada"
	r.d.CardDone = func(card, to string) string {
		return "nova-bus send --as bob --to " + to + " --subject 'card " + card + " done'"
	}
	r.d.LoadLanes = func() (LaneState, error) { return *state, nil }
	r.d.SaveLanes = func(s LaneState) error { *state = s; return nil }
	return r, h
}

// A friend 32 wide with 32 ready cards starts 32 lanes at once, each a fresh run of its
// own card in the lane's wall and its job directory, from her row saying batch: no batch
// turn, no "cards dealt" turn, no card text in her main session. A bus message goes to her
// main session as a turn of its own and rides in no lane. Each lane's run ends on its own,
// and every card is done.
func TestA32WideFriendStarts32LanesFrom32ReadyCardsWithNoBatchTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r, h := freshRig(t, 32, 32)
		r.send(t, "ada", "hello", "for her session, not a lane")
		var at3 []string
		r.at[3] = func() {
			at3 = h.now()
			for i := 1; i <= 32; i++ {
				h.release(fmt.Sprintf("c%02d", i))
			}
		}
		r.run(t, 45)

		require.Len(t, at3, 32, "32 lanes run at once, one card each")
		assert.Equal(t, 32, h.most)
		h.mu.Lock()
		started, texts, walled, jobs, main := slices.Clone(h.started), slices.Clone(h.texts), slices.Clone(h.walled), slices.Clone(h.jobDirs), slices.Clone(h.main)
		h.mu.Unlock()
		require.Len(t, started, 32, "each card run once")
		assert.Len(t, slices.Compact(slices.Sorted(slices.Values(started))), 32, "32 distinct cards")
		for i, text := range texts {
			assert.True(t, walled[i], "each run is a lane's child, inside the wall")
			assert.NotEmpty(t, jobs[i], "each run in its card's job directory")
			assert.True(t, strings.HasPrefix(text, "You are bob: one of 32 lanes of bob, this is lane "), text)
			assert.Contains(t, text, "Read "+filepath.Join(r.d.Dir, "bob", "AGENTS.md")+" and every file under "+filepath.Join(r.d.Dir, "bob", "memory")+"/ first")
			assert.Contains(t, text, "3. Send one bus line: nova-bus send --as bob --to ada --subject 'card "+started[i]+" done'")
			assert.NotContains(t, text, "for her session, not a lane", "no message rides in a lane")
		}
		require.Len(t, main, 1, "the main session took one turn: the message")
		assert.Contains(t, main[0], "for her session, not a lane")
		for _, m := range main {
			assert.NotContains(t, m, "one card this turn")
			assert.NotContains(t, m, "card(s) dealt")
		}
		records := strings.Join(r.records, "\n")
		assert.Equal(t, 32, strings.Count(records, " card=done"), records)
		assert.Contains(t, records, "mode: one-shot lanes at width 32, each refreshing on its own: the row says batch, and batch is retired")
		s := r.last()
		assert.Equal(t, ModeOneShot, s.Mode)
		assert.Equal(t, 32, s.Width)
	})
}

// Width 4 and six ready cards: when one lane's run ends, that lane starts the next card
// while the other three still run; a card dealt mid-run is started by the next lane to
// free, never at a turn's boundary.
func TestEachLaneRefreshesOnItsOwnAndADealtCardTakesTheNextFreeLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r, h := freshRig(t, 6, 4)
		snap := map[int][]string{}
		take := func(step int) { r.at[step] = func() { snap[step] = h.now() } }
		take(3)
		r.at[4] = func() { h.release("c01") }
		take(7)
		r.at[8] = func() {
			// c07 is dealt to her row mid-run: its brief written, the queue file gaining it
			dir := r.d.Dir
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c07~15"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c07~15", "BRIEF.md"), []byte("RESULT: c07\n"), 0o644))
			q := Queue{}
			for i := 1; i <= 7; i++ {
				q.Tasks = append(q.Tasks, Task{ID: fmt.Sprintf("c%02d", i), State: "queued"})
			}
			raw, err := json.Marshal(q)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), raw, 0o644))
		}
		r.at[9] = func() { h.release("c02") }
		take(12)
		r.at[13] = func() { h.release("c03") }
		take(16)
		r.run(t, 18)

		assert.Equal(t, []string{"c01", "c02", "c03", "c04"}, snap[3], "four lanes, four cards")
		assert.Equal(t, []string{"c02", "c03", "c04", "c05"}, snap[7], "c01's lane took c05 while the other three ran")
		assert.Equal(t, []string{"c03", "c04", "c05", "c06"}, snap[12], "c02's lane took c06")
		assert.Equal(t, []string{"c04", "c05", "c06", "c07"}, snap[16], "c07, dealt mid-run, taken by the next lane to free")
		h.mu.Lock()
		defer h.mu.Unlock()
		assert.Empty(t, h.main, "no turn in her main session")
		assert.Equal(t, 4, h.most, "never past her width")
	})
}

// A harness that runs no lane is delivered in batch only as the named fallback: said on
// the record once and told to the seat once, whatever her row says.
func TestTheBatchFallbackIsAJudgmentToTheSeat(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Coordinator = "ada"
	r.d.Row = func() (string, int) { return ModeBatch, 3 }
	r.send(t, "ada", "hello", "x")
	r.run(t, 4)
	require.Len(t, r.delivered, 1, "the batch turn")
	assert.Equal(t, ModeBatch, r.last().Mode)
	var told []string
	for _, m := range r.adaGot(t) {
		if strings.HasPrefix(m, "friend bob: batch fallback: fake runs no one-shot lane") {
			told = append(told, m)
		}
	}
	assert.Len(t, told, 1, "%v", r.adaGot(t))
}

// run --mode is taken as given, for a test: batch stays batch on a harness that runs lanes.
func TestRunModeOverridesTheRowAsGiven(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r, h := freshRig(t, 2, 2)
		r.d.Mode = ModeBatch
		r.send(t, "ada", "hello", "x")
		r.run(t, 6)
		h.mu.Lock()
		defer h.mu.Unlock()
		assert.Empty(t, h.started, "no lane in batch")
		assert.NotEmpty(t, h.main)
		assert.Equal(t, ModeBatch, r.last().Mode)
	})
}

// Each harness a friend runs has its one-shot run: a fresh session, no resume, in the
// card's job directory, the text whole.
func TestEveryFriendHarnessRunsAFreshOneShotInTheJobDirectory(t *testing.T) {
	t.Parallel()
	type call struct {
		dir, name string
		args      []string
		stdin     string
	}
	var got []call
	run := func(_ context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		got = append(got, call{dir, name, args, stdin})
		return "done\n", 0, nil
	}
	ctx := WithLaneDir(LaneContext(t.Context()), "/f/jobs/card~1")
	for _, tc := range []struct {
		h     OneShotHarness
		name  string
		args  []string
		stdin string
	}{
		{&DSH{Dir: "/f", Run: run}, DSHProgram, []string{"headless", "-"}, "the card"},
		{&OpenCode{Dir: t.TempDir(), Run: run}, "opencode", []string{"run", "the card"}, ""},
		{&Codex{Dir: "/f", Run: run}, "codex", []string{"exec", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "-"}, "the card"},
		{&Gemini{Dir: "/f", Run: run}, "gemini", []string{"--skip-trust", "--approval-mode", "yolo", "--prompt=the card"}, ""},
		{&Grok{Dir: "/f", Run: run}, "grok", []string{"--cwd", "/f/jobs/card~1", "--always-approve", "--single", "the card"}, ""},
	} {
		got = nil
		lt, err := tc.h.RunOneShot(ctx, "the card")
		require.NoError(t, err, tc.name)
		assert.Zero(t, lt.Exit)
		require.Len(t, got, 1, tc.name)
		assert.Equal(t, "/f/jobs/card~1", got[0].dir, tc.name)
		assert.Equal(t, tc.name, got[0].name)
		assert.Equal(t, tc.args, got[0].args, tc.name)
		assert.Equal(t, tc.stdin, got[0].stdin, tc.name)
	}
	for _, h := range []string{"dsh", "opencode", "codex", "gemini", "grok"} {
		assert.True(t, RunsOneShot(h), h)
	}
	assert.False(t, RunsOneShot("antigravity"))
}

// The gates keep a one-shot harness one: its lanes' runs go straight through, holding no
// turn of her session, and the main session's turns stay gated.
func TestTheGatesKeepAOneShotHarnessAndHoldNoTurnForALane(t *testing.T) {
	t.Parallel()
	h := newFreshHarness(t.TempDir())
	lim := (&Limits{Now: time.Now}).Gate(h)
	_, ok := lim.(OneShotHarness)
	require.True(t, ok, "the limit gate keeps a one-shot harness one")
	sc := &SessionCheck{Now: time.Now}
	gated := sc.Gate(lim)
	oh, ok := gated.(OneShotHarness)
	require.True(t, ok, "the check's gate keeps it one")
	sc.turn.Lock() // a session check in her session: a lane's run does not wait on it
	defer sc.turn.Unlock()
	h.release("c01")
	_, err := oh.RunOneShot(t.Context(), "nova-friend: lane 1 of 1: one card this turn, c01. Do exactly") // under the gate's turn it would never return
	require.NoError(t, err)
}
