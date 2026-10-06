package friend

import (
	"context"
	"errors"
	"os"
	"os/user"
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

func TestAClosedHarnessIsAdvisoryAndDoesNotHoldTheBeat(t *testing.T) {
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
	var mu sync.Mutex
	step := 0
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	watched := r.d.Beat
	r.d.Beat = func(ctx context.Context, active time.Time) error {
		mu.Lock()
		step++
		s := step
		mu.Unlock()
		switch s {
		case closeAt:
			ps.set("") // the window closes; the beat keeps going
		case reopenAt:
			ps.set(open)
		}
		err := watched(ctx, active)
		if s >= steps {
			cancel()
		}
		return err
	}
	r.stopAfter = 1 << 20
	require.NoError(t, r.d.Run(ctx))

	assert.Equal(t, steps, r.beats, "a closed harness does not hold the beat")
	down, _ := w.Down()
	assert.False(t, down)
	var sawNotRunning bool
	r.mu.Lock()
	for _, s := range r.status {
		sawNotRunning = sawNotRunning || s.BeatError == HarnessNotRunning
	}
	records := strings.Join(r.records, "\n")
	r.mu.Unlock()
	assert.False(t, sawNotRunning, "the beat's error is never harness not running")
	assert.Contains(t, records, "harness="+HarnessNotSeen+":")
	assert.Contains(t, records, "harness="+HarnessRunning+":")
	assert.LessOrEqual(t, ps.calls, steps/int(AliveEvery/BeatEvery)+1, "one ps per check, not one per beat")
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
	down, _ := w.Down()
	assert.False(t, down)
	assert.Contains(t, strings.Join(r.records, "\n"), "harness="+HarnessNotSeen+":")
	assert.Contains(t, strings.Join(r.records, "\n"), "claude has no adapter")
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
	table := strings.Join([]string{
		"glenn 501 /Applications/DeepSeek Harness.app/Contents/MacOS/DeepSeek Harness --flag",
		"glenn 502 /Applications/ChatGPT.app/Contents/Frameworks/ChatGPT Helper.app/Contents/MacOS/ChatGPT Helper",
		"rowan 503 /Applications/Antigravity.app/Contents/MacOS/Antigravity",
		"glenn 504 /Applications/ChatGPT.app/Contents/MacOS/ChatGPTX",
	}, "\n")
	assert.True(t, AppRunning(table, "glenn", DSHApp), "a path with spaces, then its arguments")
	assert.False(t, AppRunning(table, "glenn", CodexApp), "a helper, or a longer name, is not the app")
	assert.False(t, AppRunning(table, "glenn", AntigravityApp), "another user's app is not this friend's")
	assert.True(t, AppRunning(table, "rowan", AntigravityApp))
}

func TestAnAppCheckReadsTheProcessTableOnMacOSAndCannotTellElsewhere(t *testing.T) {
	t.Parallel()
	ps := &processTable{listing: "glenn 77 " + CodexApp + "\n"}
	l := appAlive(context.Background(), ps.run, "/d", "darwin", "glenn", "ChatGPT", CodexApp)
	assert.Equal(t, Liveness{Known: true, Running: true, Why: "the ChatGPT app runs (" + CodexApp + ")"}, l)
	ps.set("")
	l = appAlive(context.Background(), ps.run, "/d", "darwin", "glenn", "ChatGPT", CodexApp)
	assert.True(t, l.Known)
	assert.False(t, l.Running)
	assert.Contains(t, l.Why, "the ChatGPT app is not running")
	l = appAlive(context.Background(), ps.run, "/d", "linux", "glenn", "ChatGPT", CodexApp)
	assert.False(t, l.Known, "no app process to read off macOS: the session check alone")
	failing := func(context.Context, string, string, []string, string) (string, int, error) {
		return "", 1, errors.New("no ps")
	}
	l = appAlive(context.Background(), failing, "/d", "darwin", "glenn", "ChatGPT", CodexApp)
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

func TestACommandLineSessionIsAFirstClassHarness(t *testing.T) {
	t.Parallel()
	u, err := user.Current()
	require.NoError(t, err)
	ps := &processTable{}
	dsh := &DSH{Dir: t.TempDir(), Run: ps.run, Program: "dsh"}
	ps.set(u.Username + " 9 /usr/local/bin/dsh headless --session-id s -\n")
	l := dsh.Alive(context.Background())
	assert.True(t, l.Known && l.Running, l.Why)
	codex := &Codex{Dir: t.TempDir(), Run: ps.run}
	ps.set(u.Username + " 9 /opt/homebrew/bin/codex exec resume s\n")
	l = codex.Alive(context.Background())
	assert.True(t, l.Known && l.Running, l.Why)
	claude := Stub{Harness: "claude", Dir: t.TempDir(), Run: ps.run}
	ps.set(u.Username + " 9 /usr/local/bin/claude -p\n")
	l = claude.Alive(context.Background())
	assert.True(t, l.Known && l.Running, l.Why)
	assert.True(t, CLIRunning(u.Username+" 3 /bin/dsh headless", u.Username, "dsh", "headless"))
	assert.False(t, CLIRunning(u.Username+" 3 /bin/dsh", u.Username, "dsh", "headless"), "the program without its mark is not the session")
}
