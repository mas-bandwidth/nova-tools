package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// processTable is a fake ps: the listing it answers is whatever the test
// last set, so a harness opens and closes by a line.
type processTable struct {
	mu      sync.Mutex
	listing string
	calls   int
}

func (p *processTable) set(listing string) { p.mu.Lock(); p.listing = listing; p.mu.Unlock() }

func (p *processTable) run(_ context.Context, _, name string, _ []string, _ string) (string, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name != "ps" {
		return "", 1, errors.New("the check runs ps only, not " + name)
	}
	p.calls++
	return p.listing, 0, nil
}

// TestAClosedHarnessIsSaidAndNeverHoldsTheBeat: the harness check is
// advisory (the finding of 2026-10-05). A grok window that closes and opens
// again is said on the record each time and kept on the status
// (HarnessSeen); every beat goes out the whole time, since presence is the
// session's, decided in front of the beat (SessionCheck), never here.
func TestAClosedHarnessIsSaidAndNeverHoldsTheBeat(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "active_sessions.json"), []byte(`[{"pid":4242,"cwd":"`+r.d.Dir+`"}]`), 0o644))
	const open = "4242 1 grok\n"
	ps := &processTable{listing: open}
	grok := &Grok{Dir: r.d.Dir, Run: ps.run, Home: home}

	w := WatchHarness(r.d, grok) // the rig delivers itself; the check is the real Grok adapter over the fake table
	w.Now = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }

	const closeAt, reopenAt, steps = 32, 80, 130
	r.at[closeAt] = func() { ps.set("") }
	r.at[reopenAt] = func() { ps.set(open) }
	r.run(t, steps)

	assert.Equal(t, steps, r.beats, "every beat goes out, the window open or closed")
	r.mu.Lock()
	var sawNotSeen, sawRunning bool
	for _, s := range r.status {
		sawNotSeen = sawNotSeen || s.HarnessSeen == HarnessNotSeen
		sawRunning = sawRunning || s.HarnessSeen == HarnessRunning
		assert.Empty(t, s.BeatError, "the check never fails a beat")
	}
	records := strings.Join(r.records, "\n")
	r.mu.Unlock()
	assert.True(t, sawNotSeen && sawRunning, "the status carries what the check read")
	assert.Equal(t, 2, strings.Count(records, "harness: running: a grok window is open in "+r.d.Dir), "said at the start and when it opens again")
	assert.Equal(t, 1, strings.Count(records, "harness: not seen: no grok window is open in "+r.d.Dir), "said once when it closes")
	assert.Contains(t, records, "advisory: presence is the session's answer")
	assert.LessOrEqual(t, ps.calls, steps/int(AliveEvery/BeatEvery)+1, "one ps per check, not one per beat")
}

func TestNoBeatStaysNoBeat(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Beat = nil
	w := WatchHarness(r.d, Stub{Harness: "claude"})
	assert.Nil(t, r.d.Beat, "a daemon that beats nothing is not given a call")
	require.NoError(t, w.Beat(context.Background(), time.Time{}))
	assert.Equal(t, HarnessUnknown, watchSeen(w))
	assert.Contains(t, strings.Join(r.records, "\n"), "harness check: cannot tell")
}

func TestAnAdapterThatCannotTellLeavesTheSessionCheckAlone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Deliver = Stub{Harness: "claude"}
	r.passive = true
	w := WatchHarness(r.d, nil)
	require.NotNil(t, w.Alive, "every adapter answers the check, a stub with cannot tell")
	r.run(t, 40)
	assert.Equal(t, 40, r.beats, "every beat goes out")
	assert.Equal(t, HarnessUnknown, watchSeen(w))
	assert.Contains(t, strings.Join(r.records, "\n"), "harness check: cannot tell: claude has no adapter")
}

func TestTheWatchReadsTheBareAdapterNotTheGateInFrontOfIt(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	adapter := &OpenCode{Dir: r.d.Dir}
	sc := &SessionCheck{}
	r.d.Deliver = sc.Gate(adapter)
	_, gateAnswers := r.d.Deliver.(Aliver)
	require.False(t, gateAnswers, "a gate forwards no Alive, so the daemon's Deliver cannot be asked")
	assert.Nil(t, WatchHarness(r.d, nil).Alive, "the gated Deliver: no check")
	assert.Equal(t, Aliver(adapter), WatchHarness(r.d, adapter).Alive, "the bare adapter: its check")
}

func TestEveryAdapterAnswersTheHarnessCheck(t *testing.T) {
	t.Parallel()
	for _, h := range Harnesses {
		d, err := NewDeliverer(h, t.TempDir(), "", nil, nil)
		require.NoError(t, err, h)
		_, ok := d.(Aliver)
		assert.True(t, ok, "%s answers Alive", h)
	}
}

func TestAnAppIsRunningWhenItsMainExecutableIsInTheTableForThatUser(t *testing.T) {
	t.Parallel()
	dsh := App{Bundle: "/Applications/DeepSeek Harness.app", Name: "DeepSeek Harness"}
	chat := App{Bundle: "/Applications/ChatGPT.app", Name: "ChatGPT"}
	table := strings.Join([]string{
		"glenn 501 /Applications/DeepSeek Harness.app/Contents/MacOS/DeepSeek Harness --flag",
		"glenn 502 /Applications/ChatGPT.app/Contents/Frameworks/ChatGPT Helper.app/Contents/MacOS/ChatGPT Helper",
		"rowan 503 /Applications/Antigravity.app/Contents/MacOS/Antigravity",
		"glenn 504 /Applications/ChatGPT.app/Contents/MacOS/ChatGPTX",
	}, "\n")
	assert.True(t, AppRunning(table, "glenn", dsh), "a path with spaces, then its arguments")
	assert.False(t, AppRunning(table, "glenn", chat), "a helper, or a longer name, is not the app")
	assert.False(t, AppRunning(table, "glenn", AntigravityApp), "another user's app is not this friend's")
	assert.True(t, AppRunning(table, "rowan", AntigravityApp))
}

// The finding of 2026-10-06: the app ran as Contents/Resources/../MacOS/<name>
// and the literal path read it as not running for a morning. The app is its
// bundle and its name, the path cleaned; a process outside the bundle with
// the same name is not it.
func TestAnAppLaunchedThroughAnotherSpellingOfItsPathIsRunning(t *testing.T) {
	t.Parallel()
	dsh := App{Bundle: "/Applications/DeepSeek Harness.app", Name: "DeepSeek Harness"}
	assert.True(t, AppRunning("zhi 9 /Applications/DeepSeek Harness.app/Contents/Resources/../MacOS/DeepSeek Harness", "zhi", dsh))
	assert.True(t, AppRunning("zhi 9 /Applications/DeepSeek Harness.app/Contents/Resources/../MacOS/DeepSeek Harness --hidden", "zhi", dsh))
	assert.True(t, AppRunning("zhi 9 /Applications/DeepSeek Harness.app/Contents/MacOS/DeepSeek Harness", "zhi", dsh))
	assert.False(t, AppRunning("zhi 9 /tmp/DeepSeek Harness.app/Contents/MacOS/DeepSeek Harness", "zhi", dsh), "outside the bundle")
	assert.False(t, AppRunning("zhi 9 /Applications/DeepSeek Harness.app/Contents/Resources/runtime/cli/bin/dsh headless --session-id s -", "zhi", dsh), "the CLI inside the bundle is not the app")
	assert.False(t, AppRunning("zhi 9 /Applications/DeepSeek Harness.app/Contents/MacOS/DeepSeek HarnessX", "zhi", dsh), "a longer name is not the app")
}

func TestAnAppCheckReadsTheProcessTableOnMacOSAndCannotTellElsewhere(t *testing.T) {
	t.Parallel()
	ps := &processTable{listing: "glenn 77 /Applications/Antigravity.app/Contents/MacOS/Antigravity\n"}
	l := appAlive(context.Background(), ps.run, "/d", "darwin", "glenn", AntigravityApp)
	assert.Equal(t, Liveness{Known: true, Running: true, Why: "the Antigravity app runs (/Applications/Antigravity.app)", Rule: RuleApp}, l)
	ps.set("")
	l = appAlive(context.Background(), ps.run, "/d", "darwin", "glenn", AntigravityApp)
	assert.True(t, l.Known)
	assert.False(t, l.Running)
	assert.Contains(t, l.Why, "the Antigravity app is not running")
	l = appAlive(context.Background(), ps.run, "/d", "linux", "glenn", AntigravityApp)
	assert.False(t, l.Known, "no app process to read off macOS: the session check alone")
	failing := func(context.Context, string, string, []string, string) (string, int, error) {
		return "", 1, errors.New("no ps")
	}
	l = appAlive(context.Background(), failing, "/d", "darwin", "glenn", AntigravityApp)
	assert.False(t, l.Known, "a listing that cannot be read is no evidence the app closed")
}

func TestARunnerThatCannotBeFoundIsNotRunning(t *testing.T) {
	t.Parallel()
	missing := func(string) (string, error) { return "", errors.New("executable file not found in $PATH") }
	found := func(p string) (string, error) { return "/usr/local/bin/" + p, nil }
	l := runnerAlive(missing, "opencode")
	assert.True(t, l.Known)
	assert.False(t, l.Running)
	assert.Contains(t, l.Why, "the runner opencode is not found")
	l = runnerAlive(found, "opencode")
	assert.False(t, l.Known, "a runner on the path says nothing about the session")
}

func TestGrokIsRunningWhileAWindowIsOpenInItsDirectory(t *testing.T) {
	t.Parallel()
	home, dir := t.TempDir(), t.TempDir()
	ps := &processTable{listing: "9 1 grok\n"}
	g := &Grok{Dir: dir, Run: ps.run, Home: home}
	l := g.Alive(context.Background())
	assert.Equal(t, Liveness{Known: true, Why: l.Why}, l, "no active_sessions.json: no window was ever opened")
	require.NoError(t, os.WriteFile(filepath.Join(home, "active_sessions.json"), []byte(`[{"pid":9,"cwd":"/elsewhere"}]`), 0o644))
	l = g.Alive(context.Background())
	assert.True(t, l.Known)
	assert.False(t, l.Running, "a window in another directory is another friend's")
	require.NoError(t, os.WriteFile(filepath.Join(home, "active_sessions.json"), []byte(`[{"pid":9,"cwd":"`+dir+`"}]`), 0o644))
	assert.True(t, g.Alive(context.Background()).Running, "a window with no monitor still runs")
	ps.set("")
	assert.False(t, g.Alive(context.Background()).Running, "a stale record whose pid is gone is no window")
	require.NoError(t, os.WriteFile(filepath.Join(home, "active_sessions.json"), []byte(`not json`), 0o644))
	assert.False(t, g.Alive(context.Background()).Known, "a session file that cannot be read cannot tell")
}

// TestAHeadlessFriendIsAliveWithNoAppProcess: the finding of 2026-10-06. Zhi's
// deliveries through dsh headless into her named session answered (a fresh
// session in 4 s) while the DeepSeek Harness app was closed, and the harness
// check, reading the app, said "harness not running". A harness with a
// headless program is alive on its session's word: the last delivery or
// session check into the named session ended exit 0 within AliveWithin. No
// process table is read, and the record and the status say alive=session.
func TestAHeadlessFriendIsAliveWithNoAppProcess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var mu sync.Mutex
	now, exit := t0, 0
	var ps, turns int
	var records []string
	run := func(_ context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		mu.Lock()
		defer mu.Unlock()
		if name == "ps" {
			ps++
			return "", 0, nil // no app at all in the table
		}
		turns++
		assert.Equal(t, []string{"/w/zhi", "dsh", "headless", "--session-id", "session-1", "-"}, append([]string{dir, name}, args...))
		return "pong sent\n", exit, nil
	}
	dsh := &DSH{Dir: "/w/zhi", Session: "session-1", Program: "dsh", Run: run}
	d := &Daemon{Friend: "zhi", Harness: "dsh", Dir: "/w/zhi",
		Now:    func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		Record: func(l string) { mu.Lock(); records = append(records, l); mu.Unlock() },
		Beat:   func(context.Context, time.Time) error { return nil }}
	w := WatchHarness(d, dsh)
	step := func(by time.Duration) {
		mu.Lock()
		now = now.Add(by)
		mu.Unlock()
		require.NoError(t, d.Beat(ctx, time.Time{}))
	}

	step(0)
	assert.Equal(t, HarnessUnknown, watchSeen(w), "no turn yet: the session check alone")

	got, err := dsh.Deliver(ctx, SessionCheckPrefix+"z1")
	require.NoError(t, err)
	require.Zero(t, got)
	step(AliveEvery)
	assert.Equal(t, HarnessRunning, watchSeen(w), "the session check ended exit 0 with no app running: alive")
	_, rule := w.Status()
	assert.Equal(t, RuleSession, rule)
	step(AliveWithin - AliveEvery)
	assert.Equal(t, HarnessRunning, watchSeen(w), "still within the window")
	step(AliveEvery)
	assert.Equal(t, HarnessUnknown, watchSeen(w), "quiet past the window: a one-shot harness is seen by its next turn, never not seen")

	_, err = dsh.Deliver(ctx, "a card")
	require.NoError(t, err)
	step(AliveEvery)
	assert.Equal(t, HarnessRunning, watchSeen(w), "a delivery counts as the check does")

	mu.Lock()
	exit = 1
	mu.Unlock()
	_, _ = dsh.Deliver(ctx, "a card the session cannot take") // ignored: the exit is what is read
	step(AliveEvery)
	assert.Equal(t, HarnessNotSeen, watchSeen(w), "the last turn failed")

	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, ps, "no process table is read for a headless harness")
	assert.Equal(t, 3, turns)
	all := strings.Join(records, "\n")
	assert.Contains(t, all, "harness: running: alive=session the last turn into the dsh session session-1 ended exit 0")
	assert.Contains(t, all, "harness: not seen: alive=session the last turn into the dsh session session-1 ended exit=1")
	assert.NotContains(t, all, "app is not running")
}

// A deferred turn ran nothing and moves no liveness; a session that refused
// the turn is a failed one.
func TestADeferredTurnIsNoTurnAndARefusedOneIsAFailure(t *testing.T) {
	t.Parallel()
	var s SessionTurns
	s.clock(func() time.Time { return t0 })
	s.saw("s1", 0, nil)
	s.saw("s1", 0, Deferred{Reason: "the chat is busy"})
	assert.True(t, s.alive("codex").Running, "a deferral is no turn")
	s.saw("s1", 0, SessionRefused{Session: "s1", Reason: "an agent preset"})
	l := s.alive("dsh")
	assert.True(t, l.Known)
	assert.False(t, l.Running)
	assert.Contains(t, l.Why, "an agent preset")
}

// watchSeen is what the last check read: HarnessRunning, HarnessNotSeen, or
// HarnessUnknown when the adapter cannot tell or nothing was asked yet.
func watchSeen(w *HarnessWatch) string {
	s, _ := w.Status()
	return s
}
