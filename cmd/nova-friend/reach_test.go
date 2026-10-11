package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reachClock advances only when the ladder sleeps. now returns the instant
// the last sleep reached, so a step's wait is the clock and not the wall.
type reachClock struct {
	at    time.Time
	slept time.Duration
}

func (c *reachClock) now() time.Time { return c.at }

func (c *reachClock) sleep(_ context.Context, d time.Duration) {
	c.at = c.at.Add(d)
	c.slept += d
}

type reachCapture struct{ args []string }

func (c *reachCapture) has(sub string) bool {
	for _, a := range c.args {
		if strings.Contains(a, sub) {
			return true
		}
	}
	return false
}

func reachWorld(t *testing.T, r *rig) (world, *reachClock) {
	t.Helper()
	w := r.world()
	clock := &reachClock{at: start}
	w.now = clock.now
	w.sleep = clock.sleep
	n := 0
	nonces := []string{"n1", "n2", "n3"}
	w.random = func() string {
		left := nonces[n:]
		assert.NotEmpty(t, left, "nonce %d past the three the ladder takes", n)
		if len(left) == 0 {
			return "nx"
		}
		s := nonces[n]
		n++
		return s
	}
	return w, clock
}

func reachRun(w world) testkit.Main {
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
}

// reachTmux is a pane that is idle on the odd capture and busy on the even
// one, so a delivery types once and returns without polling. pongWhen, when
// set, is answered with a pong for the nonce in that send-keys text.
func reachTmux(t *testing.T, store bus.Store, pongWhen string) (friend.Exec, *reachCapture) {
	t.Helper()
	got := &reachCapture{}
	var phase int
	exec := func(_ context.Context, _ string, name string, args []string, _ string) (string, int, error) {
		got.args = append(got.args, append([]string{name}, args...)...)
		if name == "tmux" && len(args) > 0 && args[0] == "capture-pane" {
			phase++
			if phase%2 == 1 {
				return "> \n", 0, nil
			}
			return "busy\n", 0, nil
		}
		if pongWhen != "" && name == "tmux" && len(args) > 0 && args[0] == "send-keys" && strings.Contains(strings.Join(args, " "), pongWhen) {
			nonce := nonceOf(strings.Join(args, " "))
			_, err := (&bus.Bus{Store: store}).Send(context.Background(), bus.Message{
				From: "bob", To: []string{"ada"}, Subject: friend.PongSubject, Body: friend.PongLine(nonce, 0, 0, 0) + "\n",
			})
			require.NoError(t, err)
		}
		return "", 0, nil
	}
	return exec, got
}

func nonceOf(text string) string {
	const key = "nonce="
	i := strings.Index(text, key)
	if i < 0 {
		return ""
	}
	rest := text[i+len(key):]
	if j := strings.IndexAny(rest, " \t\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func writeReachDaemon(t *testing.T, dir string, at time.Time) {
	t.Helper()
	require.NoError(t, friend.WriteStatus(dir, friend.Status{Friend: "bob", Harness: "tmux", At: at, Connection: "connected"}))
}

func reachCmd(dir string) []string {
	return []string{"reach", "--as", "ada", "--to", "bob", "--harness", "tmux", "--dir", dir, "--session", "friend-bob", "--state-dir", dir, "--step-timeout", "5s"}
}

func reachSend(t *testing.T, store bus.Store, subject, body string) {
	t.Helper()
	_, err := (&bus.Bus{Store: store}).Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: subject, Body: body})
	require.NoError(t, err)
}

func TestReachClimbsTheLadderOnlyUntilProof(t *testing.T) {
	t.Parallel()
	t.Run("pong on the push", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		w, clock := reachWorld(t, r)
		dir := t.TempDir()
		writeReachDaemon(t, dir, clock.at)
		var sleeps int
		base := w.sleep
		w.sleep = func(ctx context.Context, d time.Duration) {
			base(ctx, d)
			sleeps++
			if sleeps == 1 {
				reachSend(t, r.store, friend.DaemonPongSubject, "daemon-pong n1\n")
			}
		}
		exec, got := reachTmux(t, r.store, "step=push")
		w.exec = exec
		reachRun(w).Do(t, reachCmd(dir)...).Exit(0).Out("REACH OK", "REACH PROOF step=push", "by=pong", "REACH NONE step=bus").NotOut("step=window")
		require.Equal(t, 5*time.Second, clock.slept)
		require.False(t, got.has("step=window"))
	})
	t.Run("message on the bus", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		w, clock := reachWorld(t, r)
		dir := t.TempDir()
		var sleeps int
		base := w.sleep
		w.sleep = func(ctx context.Context, d time.Duration) {
			base(ctx, d)
			sleeps++
			if sleeps == 1 {
				reachSend(t, r.store, "hello", "I am here\n")
			}
		}
		w.exec = func(context.Context, string, string, []string, string) (string, int, error) {
			t.Error("the ladder typed after a proof on the bus")
			return "", 0, nil
		}
		reachRun(w).Do(t, reachCmd(dir)...).Exit(0).Out("REACH PROOF step=bus", "by=message").NotOut("step=push", "step=window")
		require.Equal(t, time.Second, clock.slept)
	})
}

// TestReachProofPastTheLogCap is a fresh proof after more than the log
// read's oldest window (pkg/bus logLimit, 10000). A poll that re-reads
// Log from the start never sees that pong and the ladder returns REACH FAILED.
func TestReachProofPastTheLogCap(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	const prior = 10001
	ctx := context.Background()
	for i := 0; i < prior; i++ {
		require.NoError(t, r.store.AddAll(ctx, []string{bus.LogKey}, map[string]string{
			"from": "ada", "to": "bob", "subject": "old", "body": "prior\n",
		}))
	}
	require.Equal(t, prior, r.store.Len(bus.LogKey))
	w, clock := reachWorld(t, r)
	dir := t.TempDir()
	var sleeps int
	base := w.sleep
	w.sleep = func(ctx context.Context, d time.Duration) {
		base(ctx, d)
		sleeps++
		if sleeps == 1 {
			reachSend(t, r.store, friend.PongSubject, friend.PongLine("n1", 0, 0, 0)+"\n")
		}
	}
	w.exec = func(context.Context, string, string, []string, string) (string, int, error) {
		t.Error("the ladder typed after a proof past the log cap")
		return "", 0, nil
	}
	reachRun(w).Do(t, reachCmd(dir)...).Exit(0).Out("REACH PROOF step=bus", "by=pong").NotOut("REACH FAILED", "step=push", "step=window")
	require.Equal(t, time.Second, clock.slept)
}

func TestReachSkipsThePushWhenTheDaemonIsDown(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w, clock := reachWorld(t, r)
	dir := t.TempDir()
	exec, got := reachTmux(t, r.store, "")
	w.exec = exec
	reachRun(w).Do(t, reachCmd(dir)...).Exit(1).Err("daemon down", "step=window")
	require.False(t, got.has("step=push"))
	require.Equal(t, 10*time.Second, clock.slept)
}

func TestReachWindowRefusesWithoutAccessibilityPermission(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w, clock := reachWorld(t, r)
	dir := t.TempDir()
	w.reachPermitted = func(context.Context) (bool, error) { return false, nil }
	w.exec = func(context.Context, string, string, []string, string) (string, int, error) {
		t.Error("the window step typed without the accessibility permission")
		return "", 0, nil
	}
	reachRun(w).Do(t, "reach", "--as", "ada", "--to", "bob", "--from", "window", "--harness", "codex", "--dir", dir, "--state-dir", dir, "--step-timeout", "5s").
		Exit(2).Err(friend.AccessibilityRemedy)
	require.Equal(t, time.Duration(0), clock.slept)
	es, err := (&bus.Bus{Store: r.store}).Log(context.Background(), "-")
	require.NoError(t, err)
	for _, e := range es {
		require.NotContains(t, e.Message().Body, "REACH FAILED")
	}
}

func TestReachFailedSaysSoOnceOnTheBus(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w, clock := reachWorld(t, r)
	dir := t.TempDir()
	writeReachDaemon(t, dir, clock.at)
	exec, got := reachTmux(t, r.store, "")
	w.exec = exec
	reachRun(w).Do(t, reachCmd(dir)...).Exit(1)
	es, err := (&bus.Bus{Store: r.store}).Log(context.Background(), "-")
	require.NoError(t, err)
	n := 0
	for _, e := range es {
		m := e.Message()
		if !strings.Contains(m.Body, "REACH FAILED") {
			continue
		}
		n++
		require.Equal(t, "ada", m.From)
		require.Equal(t, []string{"ada"}, m.To)
		require.Contains(t, m.Body, "tried=bus,push,window")
	}
	require.Equal(t, 1, n)
	require.True(t, got.has("step=push"))
	require.True(t, got.has("step=window"))
	require.False(t, got.has("PING"))
	require.Equal(t, 15*time.Second, clock.slept)
}

func TestReachDryRunPrintsTheLadder(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w, clock := reachWorld(t, r)
	opened := false
	w.open = func(context.Context, string) (bus.Store, func(), error) {
		opened = true
		return r.store, func() {}, nil
	}
	w.exec = func(context.Context, string, string, []string, string) (string, int, error) {
		t.Error("a dry run typed")
		return "", 0, nil
	}
	reachRun(w).Do(t, "reach", "--as", "ada", "--to", "bob", "--dry-run").Exit(0).Out("REACH DRY-RUN", "step=bus", "step=push", "step=window")
	require.False(t, opened)
	require.Equal(t, time.Duration(0), clock.slept)
}

func TestReachHelp(t *testing.T) {
	t.Parallel()
	help := newRig(t).cli().Do(t, "reach", "-h").Exit(0).Stdout
	doc := testkit.ReadFile(t, "../../docs/CLI.md")
	for _, text := range []string{
		"--as", "--to", "--step-timeout", "--from", "--harness", "--dir", "--session", "--state-dir", "--redis", "--dry-run", "--json",
		"REACH STEP step=bus sent=<id> nonce=<n>",
		"REACH NONE step=push waited=0s",
		"REACH PROOF step=<s> after=<duration> by=<pong|message>",
		"REACH OK friend=<f> step=<s>",
		"REACH FAILED friend=<f> tried=<steps>",
		"REACH DRY-RUN",
		"Exit 0", "Exit 1", "Exit 2",
		friend.AccessibilityRemedy,
		"nova-friend reach --as ada --to bob --dry-run",
		"see also: nova-friend ping --wake is the coordinator's periodic wake check; reach is this escalation ladder",
	} {
		require.Contains(t, help, text)
		require.Contains(t, doc, text, "docs/CLI.md carries the help's text")
	}
	newRig(t).cli().Do(t, "reach", "--as", "ada", "--to", "bob", "--dry-run").Exit(0).Out("REACH DRY-RUN", "step=bus", "step=push", "step=window")
}

func TestReachStepBudgetIncludesDelivery(t *testing.T) {
	t.Parallel()
	for _, step := range []string{reachBus, reachPush, reachWindow} {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			clock := &reachClock{at: start}
			bounded := func(ctx context.Context) {
				_, ok := ctx.Deadline()
				assert.True(t, ok, "the side effect needs a bounded context")
			}
			fx := reachFX{
				Arm: func(ctx context.Context) error { bounded(ctx); return nil },
				Send: func(ctx context.Context, _, _ string) (string, error) {
					bounded(ctx)
					clock.at = clock.at.Add(4 * time.Second)
					return "sent", nil
				},
				DaemonUp: func() (bool, string) { return true, "" },
				Push: func(ctx context.Context, _ string) error {
					bounded(ctx)
					clock.at = clock.at.Add(4 * time.Second)
					return nil
				},
				Window: func(ctx context.Context, _ string) error {
					bounded(ctx)
					clock.at = clock.at.Add(4 * time.Second)
					return nil
				},
				Proof: func(ctx context.Context, _ string) (string, bool, error) { bounded(ctx); return "", false, nil },
				Tell:  func(context.Context, string) error { return nil },
				Now:   clock.now, Sleep: clock.sleep, Nonce: func() string { return "nonce" },
			}
			out := climb(context.Background(), fx, "friend", 5*time.Second, []string{step})
			assert.Equal(t, 1, out.Exit)
			assert.Equal(t, time.Second, clock.slept, "delivery and proof share one budget")
		})
	}
}

func TestReachTmuxDeliveryStopsAtTheStepBudget(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w, clock := reachWorld(t, r)
	dir := t.TempDir()
	writeReachDaemon(t, dir, clock.at)
	var captures int
	w.exec = func(ctx context.Context, _, name string, args []string, _ string) (string, int, error) {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		if name == "tmux" && args[0] == "capture-pane" {
			captures++
			return "> \n", 0, nil
		}
		return "", 0, nil
	}
	args := append(reachCmd(dir), "--from", "push")
	reachRun(w).Do(t, args...).Exit(1).Err("REACH FAILED", "waited=5s")
	assert.Equal(t, 10*time.Second, clock.slept, "each delivery stops after its own five seconds")
	assert.LessOrEqual(t, captures, 24, "the pane cannot consume the adapter's one minute default")
}

func TestReachReadsDaemonStatusWhenThePushBegins(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		initiallyRunning bool
	}{
		{"daemon remains healthy during bus wait", true},
		{"daemon starts during bus wait and supplies its default harness", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			w, clock := reachWorld(t, r)
			dir := t.TempDir()
			if tc.initiallyRunning {
				writeReachDaemon(t, dir, clock.at)
			}
			base := w.sleep
			w.sleep = func(ctx context.Context, d time.Duration) { base(ctx, d); writeReachDaemon(t, dir, clock.at) }
			exec, got := reachTmux(t, r.store, "step=push")
			w.exec = exec
			args := []string{"reach", "--as", "ada", "--to", "bob", "--dir", dir, "--state-dir", dir, "--session", "friend-bob", "--step-timeout", "60s"}
			if tc.initiallyRunning {
				args = append(args, "--harness", "tmux")
			}
			reachRun(w).Do(t, args...).Exit(0).Out("REACH PROOF step=push", "REACH OK").NotOut("step=window")
			assert.True(t, got.has("step=push"), "the current daemon remains eligible after the bus's default budget")
			assert.Equal(t, time.Minute, clock.slept)
		})
	}
}

func TestReachProofRequiresFreshFriendEvidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, from, subject, body, want string
		fresh                           bool
	}{
		{"old message", "bob", "hello", "I was here", "", false},
		{"wrong nonce", "bob", friend.PongSubject, friend.PongLine("wrong", 0, 0, 0), "", true},
		{"daemon pong", "bob", friend.DaemonPongSubject, "daemon-pong n1", "", true},
		{"daemon pong case", "bob", strings.ToUpper(friend.DaemonPongSubject), "daemon-pong n1", "", true},
		{"malformed pong", "bob", friend.PongSubject, "not a pong", "", true},
		{"other sender", "ada", "hello", "I am here", "", true},
		{"message", "bob", "hello", "I am here", "message", true},
		{"matching pong", "bob", friend.PongSubject, friend.PongLine("n1", 0, 0, 0), "pong", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			b := &bus.Bus{Store: r.store}
			m := bus.Message{From: tc.from, To: []string{"ada"}, Subject: tc.subject, Body: tc.body}
			if !tc.fresh {
				_, err := b.Send(context.Background(), m)
				require.NoError(t, err)
			}
			cursor, err := b.LogCursor(context.Background())
			require.NoError(t, err)
			if tc.fresh {
				_, err = b.Send(context.Background(), m)
				require.NoError(t, err)
			}
			by, ok, err := reachProof(context.Background(), b, &cursor, "bob", "n1")
			require.NoError(t, err)
			assert.Equal(t, tc.want != "", ok)
			assert.Equal(t, tc.want, by)
		})
	}
}
