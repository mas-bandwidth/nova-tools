package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
)

// fakeClock moves only when the code sleeps; at fires once its instant passes.
type fakeClock struct {
	now   time.Time
	at    time.Time
	fire  func()
	slept time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(d time.Duration) {
	c.now = c.now.Add(d)
	c.slept += d
	if c.fire != nil && !c.now.Before(c.at) {
		f := c.fire
		c.fire = nil
		f()
	}
}

type fakeWaker struct {
	lines []string
	err   error
	after func()
}

func (w *fakeWaker) Via() string { return "codex-window" }
func (w *fakeWaker) Wake(_ context.Context, line string) error {
	w.lines = append(w.lines, line)
	if w.after != nil {
		w.after()
	}
	return w.err
}

type rig struct {
	t     *testing.T
	store *bustest.Fake
	clock *fakeClock
	waker *fakeWaker
	out   strings.Builder
	ask   *Ask
}

var t0 = time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)

func newRig(t *testing.T) *rig {
	r := &rig{t: t, store: bustest.NewFake(t0, "rowan", "stella", "emma"), clock: &fakeClock{now: t0}, waker: &fakeWaker{}}
	r.ask = &Ask{Bus: &bus.Bus{Store: r.store}, Clock: r.clock, Waker: r.waker, As: "rowan", To: "stella",
		Subject: "q", Body: "are you there?", Deadline: 60 * time.Second, Out: &r.out}
	return r
}

// sentID is the id of the ask, read off the log.
func (r *rig) sentID() string {
	es, err := r.ask.Bus.Log(context.Background(), "-")
	if err != nil || len(es) == 0 {
		r.t.Fatalf("no ask on the log: %v", err)
	}
	return es[0].Message().ID
}

func (r *rig) reply(from, re string) {
	if _, err := r.ask.Bus.Send(context.Background(), bus.Message{From: from, To: []string{"rowan"}, Subject: "re", Re: re, Body: "here"}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) run() int { return r.ask.Run(context.Background()) }

func TestAnsweredBeforeDeadlineNoWake(t *testing.T) {
	r := newRig(t)
	r.clock.at, r.clock.fire = t0.Add(10*time.Second), func() { r.reply("stella", r.sentID()) }
	if code := r.run(); code != ExitAnswered {
		t.Fatalf("exit %d, want 0\n%s", code, r.out.String())
	}
	if len(r.waker.lines) != 0 {
		t.Fatalf("woke %v before the deadline", r.waker.lines)
	}
	if !strings.Contains(r.out.String(), "ANSWERED stella "+r.sentID()+" after 10s") {
		t.Fatalf("out: %s", r.out.String())
	}
}

func TestTimeoutWakeAnswered(t *testing.T) {
	r := newRig(t)
	r.waker.after = func() { r.reply("stella", r.sentID()) }
	if code := r.run(); code != ExitAnswered {
		t.Fatalf("exit %d, want 0\n%s", code, r.out.String())
	}
	id := r.sentID()
	if len(r.waker.lines) != 1 || r.waker.lines[0] != "Rowan: please read your psychic link (bus message "+id+") and reply with --re." {
		t.Fatalf("wake lines %q", r.waker.lines)
	}
	out := r.out.String()
	if !strings.Contains(out, "WOKE-ATTEMPT stella via codex-window") || !strings.Contains(out, "ANSWERED stella "+id+" after 60s") {
		t.Fatalf("out: %s", out)
	}
}

func TestTimeoutWakeNoAnswer(t *testing.T) {
	r := newRig(t)
	if code := r.run(); code != ExitNoAnswer {
		t.Fatalf("exit %d, want 1\n%s", code, r.out.String())
	}
	if !strings.Contains(r.out.String(), "NO ANSWER stella after wake") {
		t.Fatalf("out: %s", r.out.String())
	}
	if r.clock.slept != 2*r.ask.Deadline {
		t.Fatalf("waited %s, want exactly 2 x deadline", r.clock.slept)
	}
}

func TestWakeRefused(t *testing.T) {
	r := newRig(t)
	r.waker.err = &GrantNeeded{Process: `"Claude"`, Detail: "not allowed assistive access"}
	if code := r.run(); code != ExitRefused {
		t.Fatalf("exit %d, want 2\n%s", code, r.out.String())
	}
	out := r.out.String()
	if !strings.Contains(out, `WAKE REFUSED stella via codex-window: "Claude" needs the one-time Accessibility grant (System Settings > Privacy & Security > Accessibility)`) {
		t.Fatalf("out: %s", out)
	}
	if r.clock.slept != r.ask.Deadline {
		t.Fatalf("waited %s after a refused wake", r.clock.slept)
	}
}

func TestReplyWithoutReDoesNotCount(t *testing.T) {
	r := newRig(t)
	r.clock.at, r.clock.fire = t0.Add(4*time.Second), func() {
		r.reply("stella", "")       // no --re
		r.reply("emma", r.sentID()) // not the friend asked
	}
	if code := r.run(); code != ExitNoAnswer {
		t.Fatalf("exit %d, want 1\n%s", code, r.out.String())
	}
	if len(r.waker.lines) != 1 {
		t.Fatalf("wake lines %q", r.waker.lines)
	}
}

func TestWaitTakesNothing(t *testing.T) {
	r := newRig(t)
	r.waker.after = func() { r.reply("stella", r.sentID()) }
	r.run()
	// the answer is still new on rowan's stream for rowan's own receiver
	pending, fresh, err := r.ask.Bus.Peek(context.Background(), "rowan")
	if err != nil || len(pending) != 0 || len(fresh) != 1 {
		t.Fatalf("pending %d new %d err %v", len(pending), len(fresh), err)
	}
}

func TestGrantNeededParse(t *testing.T) {
	g := grantNeeded("128:132: execution error: System Events got an error: Claude is not allowed assistive access. (-1728)")
	if g == nil || !strings.HasPrefix(g.Process, `"Claude"`) {
		t.Fatalf("got %+v", g)
	}
	if grantNeeded("execution error: no chat input (-2700)") != nil {
		t.Fatal("a script error read as a missing grant")
	}
}

func TestCodexWindowArgs(t *testing.T) {
	var got []string
	w := &CodexWindow{Bundle: "com.openai.codex", Window: "ChatGPT", run: func(_ context.Context, args ...string) (string, string, error) {
		got = args
		return "", "System Events got an error: nova-ask is not allowed assistive access. (-1728)", errors.New("exit status 1")
	}}
	err := w.Wake(context.Background(), "hello")
	var g *GrantNeeded
	if !errors.As(err, &g) || !strings.HasPrefix(g.Process, `"nova-ask"`) {
		t.Fatalf("err %v", err)
	}
	if len(got) != 5 || got[0] != "-e" || got[2] != "hello" || got[3] != "com.openai.codex" || got[4] != "ChatGPT" {
		t.Fatalf("args %q", got)
	}
}
