//go:build race

package main

// The two invariants run's output keeps (the finding of the v1.2.14 cut,
// certification run 38205033396): nothing writes run's output from two
// goroutines at once, and nothing writes it after run returns. These tests are
// race-tagged because they hold real goroutines and wall-clock windows to make
// the dev bug reproduce every run; they pass against the fix.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/hook/hooktest"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// holdingCmd is a redis.Cmdable whose first Eval or EvalSha blocks until
// release is closed, so the daemon hook's first step is held in flight; the
// rest of the interface is the hooktest client's.
type holdingCmd struct {
	redis.Cmdable
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (h *holdingCmd) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	h.once.Do(func() { close(h.entered) })
	<-h.release
	return h.Cmdable.Eval(ctx, script, keys, args...)
}

func (h *holdingCmd) EvalSha(ctx context.Context, sha string, keys []string, args ...interface{}) *redis.Cmd {
	h.once.Do(func() { close(h.entered) })
	<-h.release
	return h.Cmdable.EvalSha(ctx, sha, keys, args...)
}

// overlapWriter fails the test when two goroutines are inside Write at once:
// it holds each write open long enough that a second writer overlaps it. With
// the fix every write goes through the one locked writer, so no two overlap.
type overlapWriter struct {
	mu      sync.Mutex
	active  bool
	overlap atomic.Bool
	inner   strings.Builder
}

func (o *overlapWriter) Write(p []byte) (int, error) {
	o.mu.Lock()
	if o.active {
		o.overlap.Store(true)
	}
	o.active = true
	o.mu.Unlock()
	time.Sleep(5 * time.Millisecond) // keep the write open so a concurrent writer overlaps it
	o.mu.Lock()
	n, err := o.inner.Write(p)
	o.active = false
	o.mu.Unlock()
	return n, err
}

func (o *overlapWriter) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.inner.String()
}

// noLateWriter fails the test when a write reaches it after returned is set:
// the test flips returned as run returns, so any later write is the bug.
type noLateWriter struct {
	returned atomic.Bool
	late     atomic.Bool
	inner    strings.Builder
}

func (w *noLateWriter) Write(p []byte) (int, error) {
	if w.returned.Load() {
		w.late.Store(true)
		return len(p), nil
	}
	return w.inner.Write(p)
}

// TestRunNeverWritesItsOutputFromTwoGoroutinesAtOnce runs a one-shot claude
// lane whose account line is written from the lane goroutine while the beat
// loop writes the record from its own; on dev the two reach c.Stdout with no
// shared lock and overlap, and with the fix both go through the one locked
// writer.
func TestRunNeverWritesItsOutputFromTwoGoroutinesAtOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"c1","state":"queued"}]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), []byte("RESULT: c1\n"), 0o644))
	w.wall = func(_ friend.Wall, _ friend.Exec) friend.Exec {
		return func(ctx context.Context, _, _ string, _ []string, _ string) (string, int, error) {
			if !friend.InLane(ctx) {
				return "", 0, nil
			}
			out := filepath.Join(dir, "outbox", "c1~15")
			if err := os.MkdirAll(out, 0o755); err != nil {
				return "", 0, err
			}
			for _, f := range []string{"REPORT.md", "RESULT.md"} {
				if err := os.WriteFile(filepath.Join(out, f), []byte("done\n"), 0o644); err != nil {
					return "", 0, err
				}
			}
			return `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.0125,"session_id":"s1","result":"ok"}` + "\n", 0, nil
		}
	}
	beats := 0
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		if beats == 12 {
			cancel()
		}
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=one-shot row_width=1 row_config_dir=/accounts/heavy-a", nil
	}
	var out overlapWriter
	var errb strings.Builder
	code := run([]string{"run", "--as", "bob", "--harness", "claude", "--dir", dir}, strings.NewReader(""), &out, &errb, w)
	require.Equal(t, 0, code, errb.String())
	require.False(t, out.overlap.Load(), "two goroutines wrote run's output at once\n%s", out.String())
}

// TestRunNeverWritesItsOutputAfterItReturns holds the daemon hook's first
// step in flight, cancels the run, and checks nothing reaches the output once
// the run returns. On dev the hook loop is never joined, so its post-cancel
// "hook: ..." line lands after run returned; with the fix run cancels then
// joins it before returning.
func TestRunNeverWritesItsOutputAfterItReturns(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	var cancel context.CancelFunc
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel = context.WithCancel(ctx)
		return ctx, cancel
	}
	hr := hooktest.New(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	w.hookStore = func(bus.Store) (hook.Store, error) {
		return hook.Store{C: &holdingCmd{Cmdable: hr.Client, entered: entered, release: release}}, nil
	}
	beats := 0
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		if beats == 3 {
			cancel()
		}
		return "FRIEND-BEAT OK bob at=2026-10-04T03:00:00Z row_mode=batch row_width=2", nil
	}

	var out noLateWriter
	var errb strings.Builder
	done := make(chan struct{})
	go func() {
		run([]string{"run", "--as", "bob", "--harness", "opencode", "--dir", t.TempDir()},
			strings.NewReader(""), &out, &errb, w)
		out.returned.Store(true)
		close(done)
	}()

	<-entered // the hook's first step is in flight, held here
	cancel()  // the run ends: its daemon loop returns at once

	// With the fix the run cancels then joins the hook loop before it returns,
	// so it is still blocked here; without the fix it has already returned. Give
	// an unjoined run a moment to return, then let the step finish either way.
	grace := time.Second
	select {
	case <-done:
		// run returned while its hook step still ran: the bug is about to write.
	case <-time.After(grace):
		// run is waiting on the hook step: the fix.
	}
	close(release)
	<-done

	require.False(t, out.late.Load(), "a write reached run's output after it returned")
}
