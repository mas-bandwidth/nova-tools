package friend

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTmux replays captured screens behind the Exec seam and records every
// tmux argv. Enter moves the pane to the next screen of afterEnter, one per
// capture-pane after it, the last repeating: a TUI that took the line.
type fakeTmux struct {
	mu         sync.Mutex
	screen     string
	afterEnter []string
	entered    bool
	gone       bool // no such session
	calls      [][]string
	typed      []string
	now        time.Time
}

func (f *fakeTmux) run(_ context.Context, dir, name string, args []string, _ string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{dir, name}, args...))
	if f.gone {
		return "can't find session: friend-bob\n", 1, nil
	}
	switch args[0] {
	case "capture-pane":
		if f.entered && len(f.afterEnter) > 0 {
			f.screen = f.afterEnter[0]
			if len(f.afterEnter) > 1 {
				f.afterEnter = f.afterEnter[1:]
			}
		}
		return f.screen, 0, nil
	case "send-keys":
		if args[len(args)-1] == "Enter" {
			f.entered = true
		} else {
			f.typed = append(f.typed, args[len(args)-1])
		}
	}
	return "", 0, nil
}

func (f *fakeTmux) sleep(_ context.Context, d time.Duration) { f.now = f.now.Add(d) }
func (f *fakeTmux) clock() time.Time                         { return f.now }

func (f *fakeTmux) adapter() *Tmux {
	return &Tmux{Session: "friend-bob", Prompt: regexp.MustCompile(`^>\s*$`), Dir: "/w/bob", Run: f.run, Now: f.clock, Sleep: f.sleep}
}

var clock0 = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

const (
	idleScreen = "welcome to the harness\n\nanswer: done\n\n>\n\n"
	busyScreen = "welcome to the harness\n\nthinking...\n\n"
)

func TestTmuxTypesIntoAnIdlePaneAndDefersDuringATurn(t *testing.T) {
	t.Parallel()
	f := &fakeTmux{screen: idleScreen, afterEnter: []string{busyScreen}, now: clock0}
	d := f.adapter()

	exit, err := d.Deliver(context.Background(), "PING n1\nrun: nova-friend pong --as bob --nonce n1\n")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Equal(t, [][]string{
		{"/w/bob", "tmux", "capture-pane", "-p", "-t", "=friend-bob:"},
		{"/w/bob", "tmux", "send-keys", "-t", "=friend-bob:", "-l", "--", "PING n1 ⏎ run: nova-friend pong --as bob --nonce n1"},
		{"/w/bob", "tmux", "send-keys", "-t", "=friend-bob:", "Enter"},
		{"/w/bob", "tmux", "capture-pane", "-p", "-t", "=friend-bob:"},
	}, f.calls, "one capture, the text as one literal line, Enter as a second call, one capture to see the turn start")

	// a turn runs: the prompt is absent, so nothing is typed and the answer is Deferred
	f = &fakeTmux{screen: busyScreen, now: clock0}
	d = f.adapter()
	_, err = d.Deliver(context.Background(), "hello")
	var def Deferred
	require.ErrorAs(t, err, &def)
	assert.Empty(t, def.Remedy, "a turn ends by itself; no remedy")
	assert.Contains(t, def.Reason, "friend-bob")
	assert.Empty(t, f.typed)
	require.Len(t, f.calls, 1, "only the capture ran: no second turn lands beside one")
	busy, err := d.Busy(context.Background())
	require.NoError(t, err)
	assert.True(t, busy)

	f.screen = idleScreen
	busy, err = d.Busy(context.Background())
	require.NoError(t, err)
	assert.False(t, busy)
}

func TestTmuxAcceptsOnceThePromptLineIsGone(t *testing.T) {
	t.Parallel()
	// the prompt stays for two polls, then the turn starts: accepted at the third
	f := &fakeTmux{screen: idleScreen, afterEnter: []string{idleScreen, idleScreen, busyScreen}, now: clock0}
	exit, err := f.adapter().Deliver(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Equal(t, 1500*time.Millisecond, f.now.Sub(clock0), "polled each half second")
	assert.Equal(t, []string{"hello"}, f.typed)

	// the prompt never goes: typed once, no retype, an error after a minute
	f = &fakeTmux{screen: idleScreen, afterEnter: []string{idleScreen}, now: clock0}
	_, err = f.adapter().Deliver(context.Background(), "hello")
	require.Error(t, err)
	var def Deferred
	assert.False(t, errors.As(err, &def), "text was typed: a deferral would type it again")
	assert.Equal(t, time.Minute, f.now.Sub(clock0))
	assert.Equal(t, []string{"hello"}, f.typed)
}

func TestTmuxMissingSessionDefersWithTheHostLine(t *testing.T) {
	t.Parallel()
	f := &fakeTmux{gone: true, now: clock0}
	_, err := f.adapter().Deliver(context.Background(), "hello")
	var def Deferred
	require.ErrorAs(t, err, &def)
	assert.Contains(t, def.Reason, "nova-friend host --as bob --harness <h> --dir /w/bob -- <launch command...>")
	assert.Empty(t, def.Remedy, "starting the session fixes it: the daemon retries, nothing counts toward a failure")
	assert.Empty(t, f.typed)

	// a prompt the friend never saved is the same line, never a guess
	f = &fakeTmux{screen: idleScreen, now: clock0}
	d := f.adapter()
	d.Prompt = nil
	_, err = d.Deliver(context.Background(), "hello")
	require.ErrorAs(t, err, &def)
	assert.Contains(t, def.Reason, "nova-friend host --as bob")
	assert.False(t, strings.Contains(def.Reason, "<nil>"))
}
