package main

import (
	"context"
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

// reachRig is the rig with bob's session behind the reach ladder: it answers
// once the answerAt-th REACH message is on its stream (0: never), by the
// pong for the nonce or by a real message, and the tmux pane it is typed
// into answers when the window does; every tmux call is recorded.
type reachRig struct {
	*rig
	answerAt int
	real     bool // answer with a real message, not the pong
	windowOK bool // the window step's typing reaches the session
	app      error
	tmux     []string
	opens    int // the stores the verb opened
	done     bool
}

func newReachRig(t *testing.T) *reachRig {
	return &reachRig{rig: newRig(t, "ada", "bob")}
}

// daemonUp writes bob's daemon status file as a running daemon writes it.
func (r *reachRig) daemonUp(t *testing.T) {
	require.NoError(t, friend.WriteStatus(friend.DefaultStateDir(r.home, "bob"), friend.Status{Friend: "bob", At: r.now}))
}

func (r *reachRig) reaches() []bus.Message {
	es, err := r.store.Range(context.Background(), bus.StreamOf("bob"), "-", "+", 0)
	if err != nil {
		return nil // ignored: the store is up in every reach test
	}
	var ms []bus.Message
	for _, e := range es {
		if m := e.Message(); strings.HasPrefix(m.Subject, friend.ReachPrefix) {
			ms = append(ms, m)
		}
	}
	return ms
}

func (r *reachRig) answer() {
	if r.done {
		return
	}
	r.done = true
	m := bus.Message{From: "bob", To: []string{"ada"}, Subject: friend.PongSubject, Body: friend.PongLine("r4nd0m", 0, 0, 0) + "\n"}
	if r.real {
		m = bus.Message{From: "bob", To: []string{"ada"}, Subject: "back", Body: "here now\n"}
	}
	_, _ = (&bus.Bus{Store: r.store}).Send(context.Background(), m) // ignored: a pong that is not sent leaves the ladder climbing, which the test reads
}

func (r *reachRig) cli() testkit.Main {
	w := r.world()
	open, now := w.open, w.now
	w.open = func(ctx context.Context, addr string) (bus.Store, func(), error) {
		r.opens++
		return open(ctx, addr)
	}
	w.now = func() time.Time {
		if r.answerAt > 0 && len(r.reaches()) >= r.answerAt {
			r.answer()
		}
		return now()
	}
	w.exec = func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		r.tmux = append(r.tmux, name+" "+strings.Join(args, " "))
		if r.windowOK && args[len(args)-1] == "Enter" {
			r.answer()
		}
		return "", 0, nil
	}
	w.app = func(context.Context, friend.Exec, string, string) error { return r.app }
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
}

func TestReachClimbsTheLadderOnlyUntilProof(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		answerAt int
		real     bool
		step     string
		messages int
		lines    []string
	}{
		{"the bus step lands", 1, false, "bus", 1, []string{"REACH STEP step=bus sent=", " nonce=r4nd0m", "REACH PROOF step=bus after=", " by=pong"}},
		{"a real message is proof", 1, true, "bus", 1, []string{"REACH PROOF step=bus after=", " by=message"}},
		{"the push step lands", 2, false, "push", 2, []string{"REACH STEP step=bus", "REACH NONE step=bus waited=3s", "REACH STEP step=push sent=", "REACH PROOF step=push after="}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newReachRig(t)
			r.answerAt, r.real = c.answerAt, c.real
			r.daemonUp(t)
			got := r.cli().Do(t, "reach", "--as", "ada", "--to", "bob", "--step-timeout", "3s", "--tmux", "bob:0").Exit(0).
				Out(append(c.lines, "REACH OK friend=bob step="+c.step)...)
			assert.NotContains(t, got.Stdout, "step=window", "no step after the proof")
			assert.Empty(t, r.tmux, "the window was never typed into")
			ms := r.reaches()
			require.Len(t, ms, c.messages)
			assert.Equal(t, "ada", ms[0].From)
			assert.Equal(t, "REACH r4nd0m", ms[0].Subject)
			assert.Contains(t, ms[0].Body, "nova-friend pong --as bob --nonce r4nd0m --to ada")
		})
	}
}

func TestReachSkipsThePushWhenTheDaemonIsDown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status func(r *reachRig, t *testing.T)
		reason string
	}{
		{"no status file", func(*reachRig, *testing.T) {}, `REACH SKIP step=push reason="the daemon is not up: it has written no status file"`},
		{"a stale status file", func(r *reachRig, t *testing.T) {
			require.NoError(t, friend.WriteStatus(friend.DefaultStateDir(r.home, "bob"), friend.Status{Friend: "bob", At: r.now.Add(-time.Hour)}))
		}, `REACH SKIP step=push reason="the daemon is not up: its status file is 1h0m`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newReachRig(t)
			r.windowOK = true
			c.status(r, t)
			r.cli().Do(t, "reach", "--as", "ada", "--to", "bob", "--step-timeout", "3s", "--tmux", "bob:0").Exit(0).
				Out("REACH NONE step=bus waited=3s", c.reason, "REACH STEP step=window sent=tmux:bob:0 nonce=r4nd0m", "REACH PROOF step=window", "REACH OK friend=bob step=window")
			assert.Len(t, r.reaches(), 1, "the push step sent nothing")
			require.Len(t, r.tmux, 2)
			assert.True(t, strings.HasPrefix(r.tmux[0], "tmux send-keys -t bob:0 -l REACH r4nd0m: ada is trying to reach this session."), r.tmux[0])
			assert.Equal(t, "tmux send-keys -t bob:0 Enter", r.tmux[1])
		})
	}
}

func TestReachWindowAppRefusedWithoutThePermissionSaysTheRemedy(t *testing.T) {
	t.Parallel()
	r := newReachRig(t)
	r.app = friend.ErrNoAccessibility
	r.cli().Do(t, "reach", "--as", "ada", "--to", "bob", "--step-timeout", "2s", "--from", "window", "--app", "com.example.chat").Exit(2).
		Err("REACH REFUSED", "step window: the accessibility permission is not granted", "Privacy & Security > Accessibility")
	assert.Empty(t, r.reaches())
}

func TestReachFailedSaysSoOnceOnTheBus(t *testing.T) {
	t.Parallel()
	r := newReachRig(t)
	r.daemonUp(t)
	got := r.cli().Do(t, "reach", "--as", "ada", "--to", "bob", "--step-timeout", "2s", "--tmux", "bob:0").Exit(1).
		Out("REACH NONE step=bus waited=2s", "REACH NONE step=push waited=2s", "REACH NONE step=window waited=2s").
		Err("REACH FAILED friend=bob tried=bus,push,window skipped=- id=")
	assert.NotContains(t, got.Stdout, "REACH OK")
	es, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 0)
	require.NoError(t, err)
	var failed []bus.Message
	for _, e := range es {
		if m := e.Message(); strings.HasPrefix(m.Subject, "REACH FAILED") {
			failed = append(failed, m)
		}
	}
	require.Len(t, failed, 1, "one message on the coordinator's own stream")
	assert.Equal(t, "REACH FAILED bob", failed[0].Subject)
	assert.Equal(t, "ada", failed[0].From)
	assert.Contains(t, failed[0].Body, "REACH FAILED friend=bob tried=bus,push,window skipped=- nonce=r4nd0m")

	j := testkit.JSON[map[string]any](t, newReachRig(t).cli().Do(t, "reach", "--as", "ada", "--to", "bob", "--step-timeout", "1s", "--json").Exit(1).Stdout)
	assert.Equal(t, map[string]any{"friend": "bob", "tried": "bus", "skipped": "push,window", "id": j["facts"].(map[string]any)["id"]}, j["facts"])
	items := j["items"].([]any)
	require.Len(t, items, 4)
	assert.Equal(t, "skip", items[2].(map[string]any)["kind"])
}

func TestReachDryRunPrintsTheLadder(t *testing.T) {
	t.Parallel()
	r := newReachRig(t)
	r.daemonUp(t)
	r.cli().Do(t, "reach", "--as", "ada", "--to", "bob", "--tmux", "bob:0", "--dry-run").Exit(0).
		Out("REACH OK friend=bob nonce=r4nd0m dry_run=true",
			"REACH PLAN step=bus: send REACH r4nd0m to bob on the bus",
			"REACH PLAN step=push: send REACH r4nd0m to bob on the bus, which the friend's daemon pushes into the session as a turn",
			"REACH PLAN step=window: type the message into tmux:bob:0 and submit it",
			"REACH NOTE nothing was sent; the message: REACH r4nd0m: ada is trying to reach this session.")
	assert.Zero(t, r.opens, "no store opened")
	assert.Empty(t, r.tmux)

	down := newReachRig(t)
	down.cli().Do(t, "reach", "--as", "ada", "--to", "bob", "--from", "push", "--dry-run").Exit(0).
		Out("REACH PLAN step=push: skip: the daemon is not up", "REACH PLAN step=window: skip: no window named").NotOut("step=bus")
}

func TestReachRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()
	newReachRig(t).cli().Do(t, "reach").Exit(2).Err("--as is required", "--to is required")
	newReachRig(t).cli().Do(t, "reach", "--as", "ada", "--to", "bob", "--from", "phone", "--step-timeout", "0s", "--tmux", "a", "--app", "b").Exit(2).
		Err(`--from "phone" is no step; it wants bus, push or window`, "--step-timeout wants a positive duration", "--tmux and --app each name the friend's window; give one")
}

func TestReachHelpExampleRunsAsWritten(t *testing.T) {
	t.Parallel()
	r := newReachRig(t)
	r.answerAt = 1
	help := r.cli().Do(t, "reach", "-h").Exit(0).Stdout
	var example string
	for _, line := range strings.Split(help, "\n") {
		if rest, ok := strings.CutPrefix(line, "example: nova-friend "); ok {
			example = rest
		}
	}
	require.Equal(t, "reach --as ada --to bob --tmux bob:0 --step-timeout 30s", example)
	r.cli().Do(t, strings.Fields(example)...).Exit(0).Out("REACH STEP step=bus", "REACH PROOF step=bus", "REACH OK friend=bob step=bus")
}
