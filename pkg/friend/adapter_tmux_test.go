package friend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTmux is a tmux behind the Exec seam: each capture-pane answers the next
// scripted screen (the last one repeats), every call is recorded as argv, and
// a fake clock moves only when the adapter sleeps.
type fakeTmux struct {
	mu      sync.Mutex
	screens []string
	next    int
	calls   [][]string
	exists  bool   // has-session answers 0
	missing string // when set, capture-pane exits 1 printing this
	now     time.Time
}

func newFakeTmux(screens ...string) *fakeTmux {
	return &fakeTmux{screens: screens, exists: true, now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeTmux) run(_ context.Context, dir, name string, args []string, _ string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{dir, name}, args...))
	switch args[0] {
	case "capture-pane":
		if f.missing != "" {
			return f.missing, 1, nil
		}
		s := f.screens[min(f.next, len(f.screens)-1)]
		f.next++
		return s, 0, nil
	case "has-session":
		if f.exists {
			return "", 0, nil
		}
		return "can't find session", 1, nil
	}
	return "", 0, nil
}

func (f *fakeTmux) clock() func() time.Time {
	return func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
}

func (f *fakeTmux) sleep(_ context.Context, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *fakeTmux) tmux() *Tmux {
	return &Tmux{Session: "friend-bob", Dir: "/w/bob", Prompt: PromptPattern(`^\s*>\s*$`), Run: f.run, Now: f.clock(), Sleep: f.sleep}
}

func (f *fakeTmux) argv() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c[2:], " "))
	}
	return out
}

const idleScreen = "some earlier output\n\n> \n\n"
const busyScreen = "some earlier output\nthinking about it...\n"

func TestTmuxTypesIntoAnIdlePaneAndDefersDuringATurn(t *testing.T) {
	t.Parallel()
	f := newFakeTmux(idleScreen, busyScreen, busyScreen, busyScreen)
	d := f.tmux()

	exit, err := d.Deliver(context.Background(), "PING n1\nrun: nova-friend pong --as bob")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Equal(t, []string{
		"capture-pane -p -t friend-bob",
		"send-keys -t friend-bob -l PING n1 ⏎ run: nova-friend pong --as bob",
		"send-keys -t friend-bob Enter",
		"capture-pane -p -t friend-bob",
	}, f.argv(), "the text is typed literally as one line, Enter is its own call, then the pane is read once more")

	// the turn runs now: the prompt is absent, so a second delivery types nothing
	before := len(f.calls)
	_, err = d.Deliver(context.Background(), "second")
	var def Deferred
	require.ErrorAs(t, err, &def)
	assert.Contains(t, def.Reason, "turn")
	assert.Equal(t, []string{"capture-pane -p -t friend-bob"}, f.argv()[before:], "a turn runs: nothing is typed")
	busy, err := d.Busy(context.Background())
	require.NoError(t, err)
	assert.True(t, busy, "Busy says a turn runs")
}

func TestTmuxAcceptsOnceThePromptLineIsGone(t *testing.T) {
	t.Parallel()
	f := newFakeTmux(idleScreen, idleScreen, idleScreen, busyScreen)
	d := f.tmux()
	start := f.now
	exit, err := d.Deliver(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Equal(t, time.Second, f.now.Sub(start), "polled each half second until the prompt line went")

	// a prompt that never goes is typed text that started no turn: not accepted, not a deferral, and not typed twice
	f = newFakeTmux(idleScreen)
	start = f.now
	_, err = f.tmux().Deliver(context.Background(), "hello")
	require.Error(t, err)
	assert.False(t, errors.As(err, new(Deferred)))
	assert.GreaterOrEqual(t, f.now.Sub(start), time.Minute, "the wait is a minute")
	typed := 0
	for _, a := range f.argv() {
		if strings.HasPrefix(a, "send-keys") && strings.Contains(a, " -l ") {
			typed++
		}
	}
	assert.Equal(t, 1, typed)
}

func TestTmuxMissingSessionDefersWithTheHostLine(t *testing.T) {
	t.Parallel()
	f := newFakeTmux()
	f.missing = "can't find session: friend-bob"
	_, err := f.tmux().Deliver(context.Background(), "hello")
	var def Deferred
	require.ErrorAs(t, err, &def)
	assert.Contains(t, def.Reason, "friend-bob")
	assert.Contains(t, def.Reason, "nova-friend host --as bob --harness <h> --dir /w/bob -- <launch command...>")
	assert.Equal(t, []string{"capture-pane -p -t friend-bob"}, f.argv(), "nothing is typed")
}

func TestHostRefusesASessionThatRuns(t *testing.T) {
	t.Parallel()
	f := newFakeTmux()
	_, err := Host(context.Background(), f.run, HostSpec{Name: "bob", Dir: "/w/bob", Command: []string{"aider"}})
	var refused HostRefused
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "friend-bob", refused.Session)
	assert.Equal(t, []string{"has-session -t friend-bob"}, f.argv(), "no second session is started")

	f = newFakeTmux()
	f.exists = false
	got, err := Host(context.Background(), f.run, HostSpec{Name: "bob", Dir: "/w/bob", Command: []string{"aider", "--no-auto-commits"}})
	require.NoError(t, err)
	assert.Equal(t, "friend-bob", got.Session)
	assert.Equal(t, "tmux attach -t friend-bob", got.Attach)
	assert.Equal(t, []string{"has-session -t friend-bob", "new-session -d -s friend-bob -c /w/bob -- aider --no-auto-commits"}, f.argv())
}

func TestHostDryRunPrintsTheTmuxCommand(t *testing.T) {
	t.Parallel()
	f := newFakeTmux()
	got, err := Host(context.Background(), f.run, HostSpec{Name: "bob", Dir: "/w/bob", Command: []string{"aider"}, DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, "tmux new-session -d -s friend-bob -c /w/bob -- aider", got.Line)
	assert.Empty(t, f.calls, "a dry run runs no tmux")
}

func TestHostPromptIsDataPerHarnessAndOverridable(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"opencode", "grok", "aider"} {
		re, _, err := HostPrompt(h, "")
		require.NoError(t, err, h)
		assert.NotNil(t, re, h)
	}
	_, _, err := HostPrompt("nosuch", "")
	require.Error(t, err)
	re, src, err := HostPrompt("nosuch", `^\$ $`)
	require.NoError(t, err)
	assert.Equal(t, `^\$ $`, src)
	assert.True(t, re.MatchString("$ "))
	_, _, err = HostPrompt("aider", `(`)
	require.Error(t, err)
}

func TestTmuxIsAHarnessWithADeliverCommand(t *testing.T) {
	t.Parallel()
	assert.True(t, Known("tmux"))
	assert.Contains(t, Pushing(), "tmux")
	d, err := NewDeliverer("tmux", "/w/bob", "friend-bob", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "friend-bob", d.(*Tmux).Session)
}

func TestHostStateNamesTheSessionAndPromptForRunAndInstall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d, err := NewDeliverer("tmux", "/w/bob", "", nil, nil)
	require.NoError(t, err)
	require.NoError(t, TmuxFor(d, "bob", dir)) // no host state yet: the session is friend-<name>, the generic prompt
	assert.Equal(t, "friend-bob", d.(*Tmux).Session)
	require.NoError(t, WriteHost(dir, Hosted{Session: "friend-bob", Harness: "aider", Prompt: `^aider> $`}))
	d, err = NewDeliverer("tmux", "/w/bob", "", nil, nil)
	require.NoError(t, err)
	require.NoError(t, TmuxFor(d, "bob", dir))
	assert.True(t, d.(*Tmux).Prompt.MatchString("aider> "))
}

func TestTmuxAliveIsTheHostedSession(t *testing.T) {
	t.Parallel()
	f := newFakeTmux()
	assert.True(t, f.tmux().Alive(context.Background()).Running)
	f.exists = false
	l := f.tmux().Alive(context.Background())
	assert.True(t, l.Known)
	assert.False(t, l.Running)
	_, ok := Deliverer(f.tmux()).(Aliver)
	assert.True(t, ok)
}
