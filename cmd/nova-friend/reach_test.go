package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reachClock advances only when the ladder sleeps. now returns the instant
// the last sleep reached, so a step's wait is the clock and not the wall.
type reachClock struct {
	at       time.Time
	slept    time.Duration
	deadline time.Time
	expire   func()
}

func (c *reachClock) now() time.Time { return c.at }

func (c *reachClock) sleep(_ context.Context, d time.Duration) {
	if c.expire != nil && d > c.deadline.Sub(c.at) {
		d = c.deadline.Sub(c.at)
	}
	c.at = c.at.Add(d)
	c.slept += d
	if c.expire != nil && !c.at.Before(c.deadline) {
		c.expire()
	}
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
	w.reachStart = func(ctx context.Context, deliver func(context.Context) error) <-chan error {
		done := make(chan error, 1)
		done <- deliver(ctx)
		close(done)
		return done
	}
	w.now = clock.now
	w.sleep = clock.sleep
	w.reachTimeout = func(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
		bounded, cancel := context.WithCancelCause(ctx)
		clock.deadline = clock.at.Add(d)
		clock.expire = func() { cancel(context.DeadlineExceeded) }
		return bounded, func() { cancel(context.Canceled) }
	}
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
		friend.ComposerRemedy,
		"nova-friend reach --as ada --to bob --dry-run",
		"see also: nova-friend wake --as <me> ends that friend's own recorded sleep; reach is this ladder, because wake is that verb",
	} {
		require.Contains(t, help, text)
		require.Contains(t, doc, text, "docs/CLI.md carries the help's text")
	}
	newRig(t).cli().Do(t, "reach", "--as", "ada", "--to", "bob", "--dry-run").Exit(0).Out("REACH DRY-RUN", "step=bus", "step=push", "step=window")
}

// A delivery and the proof wait spend the same budget (Reach.EveryStepBounded).
func TestReachStepBudgetIncludesDelivery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		accept bool
	}{{"delivery takes part of budget", true}, {"delivery exhausts budget", false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			w, clock := reachWorld(t, r)
			dir := t.TempDir()
			captures := 0
			w.exec = func(ctx context.Context, _ string, name string, args []string, _ string) (string, int, error) {
				if err := ctx.Err(); err != nil {
					return "", 0, err
				}
				if name == "tmux" && args[0] == "capture-pane" {
					captures++
					if tc.accept && captures == 2 {
						clock.sleep(ctx, 2*time.Second)
						return "busy\n", 0, nil
					}
					return "> \n", 0, nil
				}
				return "", 0, nil
			}
			reachRun(w).Do(t, append(reachCmd(dir), "--from", "window")...).Exit(1).Err("REACH FAILED")
			assert.Equal(t, 5*time.Second, clock.slept, "delivery and proof polling share one budget")
		})
	}
}

func TestReachFindsProofBeyondOldLogPage(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w, clock := reachWorld(t, r)
	// Bus.Log caps a page at 10,000. An old full page must not hide a new proof.
	for i := 0; i < 10001; i++ {
		require.NoError(t, r.store.AddAll(context.Background(), []string{bus.LogKey}, map[string]string{"from": "bob", "subject": "old"}))
	}
	dir := t.TempDir()
	base := w.sleep
	w.sleep = func(ctx context.Context, d time.Duration) {
		base(ctx, d)
		reachSend(t, r.store, "hello", "I am here\n")
	}
	w.exec = func(context.Context, string, string, []string, string) (string, int, error) {
		t.Error("typed after bus proof")
		return "", 0, nil
	}
	reachRun(w).Do(t, reachCmd(dir)...).Exit(0).Out("REACH PROOF step=bus", "by=message").NotOut("step=push", "step=window")
	assert.Equal(t, time.Second, clock.slept)
}

func TestReachChecksCurrentDaemonStatusBeforePush(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		initial bool
		refresh bool
	}{
		{"keeps beating", true, true},
		{"starts during bus wait", false, true},
		{"stops beating", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, "ada", "bob")
			w, clock := reachWorld(t, r)
			dir := t.TempDir()
			if tc.initial {
				writeReachDaemon(t, dir, clock.at)
			}
			base := w.sleep
			w.sleep = func(ctx context.Context, d time.Duration) {
				base(ctx, d)
				if tc.refresh {
					writeReachDaemon(t, dir, clock.at)
				}
			}
			exec, got := reachTmux(t, r.store, "step=push")
			w.exec = exec
			args := []string{"reach", "--as", "ada", "--to", "bob", "--dir", dir, "--session", "friend-bob", "--state-dir", dir} // default 60s, beyond DaemonStale
			if tc.refresh {
				reachRun(w).Do(t, args...).Exit(0).Out("REACH PROOF step=push").NotOut("step=window")
				assert.Equal(t, time.Minute, clock.slept)
			} else {
				reachRun(w).Do(t, args...).Exit(1).Err("daemon down", "step=window")
			}
			assert.Equal(t, tc.refresh, got.has("step=push"))
		})
	}
}

func TestReachProofCancelsRunningDeliveryWithoutEscalation(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w, clock := reachWorld(t, r)
	dir := t.TempDir()
	writeReachDaemon(t, dir, clock.at)
	proofSent := make(chan struct{})
	stopped := make(chan struct{})
	cleanupEntered := make(chan struct{})
	releaseCleanup := make(chan struct{})
	var release sync.Once
	releaseWorker := func() { release.Do(func() { close(releaseCleanup) }) }
	t.Cleanup(func() { releaseWorker(); <-stopped })
	baseTimeout := w.reachTimeout
	w.reachTimeout = func(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
		if d == reachCleanupWithin {
			close(cleanupEntered)
			releaseWorker()
		}
		return baseTimeout(ctx, d)
	}
	w.reachStart = func(ctx context.Context, deliver func(context.Context) error) <-chan error {
		done := startReachDelivery(ctx, deliver)
		<-proofSent // controlled schedule: the effect remains active after its proof
		return done
	}
	w.exec = func(ctx context.Context, _ string, _ string, args []string, _ string) (string, int, error) {
		if args[0] == "capture-pane" {
			return "> \n", 0, nil
		}
		text := strings.Join(args, " ")
		assert.NotContains(t, text, "step=window", "window typed after proof")
		if strings.Contains(text, "step=push") {
			reachSend(t, r.store, friend.PongSubject, friend.PongLine(nonceOf(text), 0, 0, 0)+"\n")
			close(proofSent)
			<-ctx.Done()
			<-releaseCleanup // cancellation noticed; adapter cleanup is still pending
			close(stopped)
			return "", 0, ctx.Err()
		}
		return "", 0, nil
	}
	reachRun(w).Do(t, append(reachCmd(dir), "--from", "push")...).Exit(0).Out("REACH PROOF step=push", "REACH OK").NotOut("step=window")
	select {
	case <-stopped:
	default:
		assert.Fail(t, "run returned before delivery cleanup completed")
	}
	assert.Zero(t, clock.slept)
	es, err := (&bus.Bus{Store: r.store}).Log(context.Background(), "-")
	require.NoError(t, err)
	for _, e := range es {
		assert.NotContains(t, e.Message().Body, "REACH FAILED")
	}
}

func TestReachGUIRefusesWithoutVerifiedComposer(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w, clock := reachWorld(t, r)
	w.reachPermitted = func(context.Context) (bool, error) { return true, nil }
	w.exec = func(context.Context, string, string, []string, string) (string, int, error) {
		t.Error("an unverified GUI target received a command")
		return "", 0, nil
	}
	reachRun(w).Do(t, "reach", "--as", "ada", "--to", "bob", "--from", "window", "--harness", "codex", "--session", "chat-id").Exit(2).Err("GUI composer is not verified", friend.ComposerRemedy)
	assert.Zero(t, clock.slept)
	es, err := (&bus.Bus{Store: r.store}).Log(context.Background(), "-")
	require.NoError(t, err)
	for _, e := range es {
		assert.NotContains(t, e.Message().Body, "REACH FAILED")
	}
}

func TestReachProofWinsCoincidentDeliveryError(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w, clock := reachWorld(t, r)
	dir := t.TempDir()
	writeReachDaemon(t, dir, clock.at)
	base, got := reachTmux(t, r.store, "step=push")
	w.exec = func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		out, code, err := base(ctx, dir, name, args, stdin)
		if strings.Contains(strings.Join(args, " "), "step=push") {
			return "", 0, context.Canceled
		}
		return out, code, err
	}
	reachRun(w).Do(t, append(reachCmd(dir), "--from", "push")...).Exit(0).Out("REACH PROOF step=push").NotOut("step=window")
	assert.False(t, got.has("step=window"))
	assert.Zero(t, clock.slept)
}

func TestReachRefusesWhenDeliveryCleanupCannotFinish(t *testing.T) {
	t.Parallel()
	fx := reachFX{Timeout: func(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
		assert.Equal(t, reachCleanupWithin, d)
		bounded, cancel := context.WithCancel(ctx)
		cancel() // deterministic expiry; no worker or wall-clock timer
		return bounded, cancel
	}}
	err := finishReachDelivery(context.Background(), fx, make(chan error))
	assert.ErrorContains(t, err, "delivery cleanup did not finish")
}

func TestReachCleanupFailurePreservesProofAndRefusesEscalation(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w, _ := reachWorld(t, r)
	dir := t.TempDir()
	writeReachDaemon(t, dir, start)
	exec, got := reachTmux(t, r.store, "step=push")
	w.exec = exec
	w.reachStart = func(ctx context.Context, deliver func(context.Context) error) <-chan error {
		require.NoError(t, deliver(ctx))
		return make(chan error) // unsafe starter never acknowledges cleanup; no actual worker
	}
	baseTimeout := w.reachTimeout
	w.reachTimeout = func(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
		if d == reachCleanupWithin {
			bounded, cancel := context.WithCancel(ctx)
			cancel()
			return bounded, cancel
		}
		return baseTimeout(ctx, d)
	}
	reachRun(w).Do(t, append(reachCmd(dir), "--from", "push")...).Exit(2).Err("delivery cleanup did not finish", "REACH PROOF step=push").NotOut("step=window")
	assert.False(t, got.has("step=window"))
	es, err := (&bus.Bus{Store: r.store}).Log(context.Background(), "-")
	require.NoError(t, err)
	for _, e := range es {
		assert.NotContains(t, e.Message().Body, "REACH FAILED")
	}
}
