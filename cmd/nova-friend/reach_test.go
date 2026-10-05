package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clock is the ladder's time. It moves only when the ladder sleeps.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func (c *clock) sleep(_ context.Context, d time.Duration) { c.t = c.t.Add(d) }

// reachEnv is one ladder over a fake bus, a clock and counted side effects.
type reachEnv struct {
	t       *testing.T
	rig     *rig
	w       world
	clk     *clock
	pushes  int
	windows int
	opened  int
}

func newReach(t *testing.T) *reachEnv {
	t.Helper()
	r := newRig(t, "ada", "bob")
	e := &reachEnv{t: t, rig: r, clk: &clock{t: start}}
	w := r.world()
	w.now = e.clk.now
	w.sleep = e.clk.sleep
	n := 0
	w.random = func() string {
		n++
		return "n" + itoa(n)
	}
	w.open = func(context.Context, string) (bus.Store, func(), error) {
		e.opened++
		if r.store.Fail != nil {
			return nil, nil, r.store.Fail
		}
		return r.store, func() {}, nil
	}
	w.reachDaemonUp = func(string) (bool, string) { return true, "" }
	w.reachPush = func(_ context.Context, name, text string) error {
		e.pushes++
		assert.Equal(t, "bob", name)
		assert.NotContains(t, text, "PING")
		assert.Contains(t, text, "nova-friend pong --as bob --nonce ")
		return nil
	}
	w.reachWindow = func(_ context.Context, name, text string) error {
		e.windows++
		assert.Equal(t, "bob", name)
		assert.NotContains(t, text, "PING")
		return nil
	}
	e.w = w
	return e
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (e *reachEnv) cli() testkit.Main {
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, e.w)
	})
}

func (e *reachEnv) on(step string, plant func(nonce string)) {
	e.t.Helper()
	prev := e.w.reachOnStep
	e.w.reachOnStep = func(s, nonce string) {
		if prev != nil {
			prev(s, nonce)
		}
		if s == step {
			plant(nonce)
		}
	}
}

func (e *reachEnv) fromBob(subject, body string) {
	e.t.Helper()
	_, err := (&bus.Bus{Store: e.rig.store}).Send(context.Background(), bus.Message{
		From: "bob", To: []string{"ada"}, Subject: subject, Body: body,
	})
	require.NoError(e.t, err)
}

func (e *reachEnv) log() []bus.Message {
	e.t.Helper()
	es, err := e.rig.store.Range(context.Background(), bus.LogKey, "-", "+", 0)
	require.NoError(e.t, err)
	out := make([]bus.Message, 0, len(es))
	for _, ent := range es {
		out = append(out, ent.Message())
	}
	return out
}

func (e *reachEnv) reach(extra ...string) testkit.Ran {
	e.t.Helper()
	args := []string{"reach", "--as", "ada", "bob", "--step-timeout", "2s"}
	args = append(args, extra...)
	return e.cli().Do(e.t, args...)
}

func TestReachClimbsTheLadderOnlyUntilProof(t *testing.T) {
	t.Parallel()
	t.Run("a pong on the bus stops the ladder", func(t *testing.T) {
		t.Parallel()
		e := newReach(t)
		e.on(reachBus, func(nonce string) {
			e.fromBob(friend.PongSubject, friend.PongLine(nonce, 1, 0, 4)+"\n")
		})
		ran := e.reach()
		ran.Exit(0).Out(
			"REACH OK friend=bob step=bus",
			"REACH STEP step=bus sent=",
			"nonce=n1",
			"REACH PROOF step=bus after=0 by=pong",
		).NotOut("step=push", "step=window", "REACH NONE", "REACH FAILED")
		assert.Equal(t, 0, e.pushes)
		assert.Equal(t, 0, e.windows)
		var sent bus.Message
		for _, m := range e.log() {
			if strings.HasPrefix(m.Subject, "REACH ") {
				sent = m
			}
		}
		assert.Equal(t, "ada", sent.From)
		assert.Equal(t, []string{"bob"}, sent.To)
		assert.Equal(t, "REACH n1", sent.Subject)
		assert.Contains(t, sent.Body, "nova-friend pong --as bob --nonce n1 --to ada")
		assert.NotContains(t, sent.Body, "PING")
	})
	t.Run("a real message on the push stops before the window", func(t *testing.T) {
		t.Parallel()
		e := newReach(t)
		e.on(reachPush, func(string) {
			e.fromBob("note", "I am here\n")
		})
		ran := e.reach()
		ran.Exit(0).Out(
			"REACH OK friend=bob step=push",
			"REACH NONE step=bus waited=2s",
			"REACH STEP step=push",
			"REACH PROOF step=push after=0 by=message",
		).NotOut("step=window", "REACH FAILED")
		assert.Equal(t, 1, e.pushes)
		assert.Equal(t, 0, e.windows)
	})
	t.Run("a daemon pong is not the session", func(t *testing.T) {
		t.Parallel()
		e := newReach(t)
		e.on(reachBus, func(nonce string) {
			e.fromBob(friend.DaemonPongSubject, "daemon-pong "+nonce+"\n")
			e.fromBob("PING "+nonce, "PING "+nonce+"\n")
		})
		e.on(reachPush, func(nonce string) {
			e.fromBob(friend.PongSubject, friend.PongLine(nonce, 0, 0, 0)+"\n")
		})
		ran := e.reach()
		ran.Exit(0).Out("REACH OK friend=bob step=push", "REACH NONE step=bus waited=2s", "by=pong")
		assert.NotContains(t, ran.Stdout, "PROOF step=bus")
		assert.Equal(t, 0, e.windows)
	})
}

func TestReachSkipsThePushWhenTheDaemonIsDown(t *testing.T) {
	t.Parallel()
	e := newReach(t)
	e.w.reachDaemonUp = nil // the status file is absent: the real guard
	e.w.reachPush = func(context.Context, string, string) error {
		t.Fatal("the push step ran while the daemon was down")
		return nil
	}
	e.on(reachWindow, func(string) {
		e.fromBob("note", "at the window\n")
	})
	ran := e.reach()
	ran.Exit(0).Out(
		"REACH SKIP step=push reason=daemon-down",
		"REACH NONE step=bus waited=2s",
		"REACH PROOF step=window after=0 by=message",
		"REACH OK friend=bob step=window",
	).NotOut("STEP step=push", "REACH FAILED")
	assert.Equal(t, 0, e.pushes)
}

func TestReachWindowRefusesWithoutAccessibilityPermission(t *testing.T) {
	t.Parallel()
	e := newReach(t)
	e.w.reachWindow = func(context.Context, string, string) error {
		e.windows++
		return friend.ErrNoAccessibility
	}
	ran := e.reach("--from", "window")
	ran.Exit(2).Refused("accessibility permission is not granted to this binary")
	assert.Contains(t, ran.Stderr, "System Settings > Privacy & Security > Accessibility")
	assert.NotContains(t, ran.Stderr, "REACH FAILED")
	assert.Empty(t, ran.Stdout)
	for _, m := range e.log() {
		assert.NotEqual(t, "REACH FAILED", m.Subject)
	}
	assert.Equal(t, 1, e.windows)
	assert.Equal(t, 0, e.pushes)
}

func TestReachFailedSaysSoOnceOnTheBus(t *testing.T) {
	t.Parallel()
	e := newReach(t)
	ran := e.reach()
	ran.Exit(1).Err(
		"REACH FAILED friend=bob tried=bus,push,window",
		"REACH NONE step=bus waited=2s",
		"REACH NONE step=push waited=2s",
		"REACH NONE step=window waited=2s",
	)
	assert.Empty(t, ran.Stdout)
	assert.Equal(t, 1, e.pushes)
	assert.Equal(t, 1, e.windows)
	n := 0
	for _, m := range e.log() {
		if m.Subject != "REACH FAILED" {
			continue
		}
		n++
		assert.Equal(t, "ada", m.From)
		assert.Equal(t, []string{"ada"}, m.To)
		assert.Equal(t, "REACH FAILED friend=bob tried=bus,push,window\n", m.Body)
	}
	assert.Equal(t, 1, n, "the failure was said once")
}

func TestReachDryRunPrintsTheLadder(t *testing.T) {
	t.Parallel()
	e := newReach(t)
	ran := e.reach("--dry-run")
	ran.Exit(0).Out(
		"REACH OK friend=bob step=- dry_run=true",
		"REACH STEP step=bus sent=- nonce=n1",
		"REACH STEP step=push sent=- nonce=n2",
		"REACH STEP step=window sent=- nonce=n3",
	).NotOut("REACH PROOF", "REACH NONE", "REACH FAILED", "REACH SKIP")
	assert.Empty(t, ran.Stderr)
	assert.Equal(t, 0, e.opened, "a dry run opened the store")
	assert.Equal(t, 0, e.pushes)
	assert.Equal(t, 0, e.windows)
	assert.Empty(t, e.log())

	e = newReach(t)
	raw := e.reach("--dry-run", "--json")
	raw.Exit(0)
	var body struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
		Facts map[string]any `json:"facts"`
		Items []struct {
			Kind   string         `json:"kind"`
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(raw.Stdout)), &body))
	assert.Equal(t, "reach", body.Result.Verb)
	assert.Equal(t, "ok", body.Result.Status)
	assert.Equal(t, 0, body.Result.Exit)
	assert.Equal(t, "bob", body.Facts["friend"])
	assert.Equal(t, "-", body.Facts["step"])
	assert.Equal(t, true, body.Facts["dry_run"])
	require.Len(t, body.Items, 3)
	assert.Equal(t, []string{"bus", "push", "window"}, []string{
		body.Items[0].Fields["step"].(string),
		body.Items[1].Fields["step"].(string),
		body.Items[2].Fields["step"].(string),
	})
	for _, it := range body.Items {
		assert.Equal(t, "step", it.Kind)
		assert.Equal(t, "-", it.Fields["sent"])
	}
	assert.Equal(t, 0, e.opened)
}

func TestReachWindowTypesIntoAnIdleTmuxPane(t *testing.T) {
	t.Parallel()
	e := newReach(t)
	e.w.reachWindow = nil
	var calls []string
	e.w.exec = func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch args[0] {
		case "list-panes":
			return "%3\tbob\topencode\n", 0, nil
		case "display-message":
			return "0\n", 0, nil
		default:
			return "", 0, nil
		}
	}
	dir := t.TempDir()
	require.NoError(t, friend.WriteStatus(dir, friend.Status{Friend: "bob", Harness: "opencode", At: start}))
	e.on(reachWindow, func(nonce string) {
		e.fromBob(friend.PongSubject, friend.PongLine(nonce, 0, 0, 0)+"\n")
	})
	ran := e.reach("--from", "window", "--state-dir", dir)
	ran.Exit(0).Out("REACH OK friend=bob step=window", "REACH PROOF step=window after=0 by=pong")
	assert.Contains(t, calls, "tmux send-keys -t %3 -l -- "+reachText("ada", "bob", "n1"))
	assert.Contains(t, calls, "tmux send-keys -t %3 Enter")
}
