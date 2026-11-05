package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fake is an in-memory inbox store: per stream, the entries, how many the
// group has been handed, and the handed-but-unacked ids. When a hook asks for
// new entries and there are none, it calls idle (the test stops the hook).
type fake struct {
	entries   map[string][]Msg
	delivered map[string]int
	pending   map[string][]string
	failAck   int // fail this many acks, then succeed
	idle      func()
}

func newFake() *fake {
	return &fake{entries: map[string][]Msg{}, delivered: map[string]int{}, pending: map[string][]string{}}
}

func (f *fake) Add(_ context.Context, stream string, fields map[string]string) (string, error) {
	id := fmt.Sprintf("%d-0", len(f.entries[stream])+1)
	copied := map[string]string{}
	for k, v := range fields {
		copied[k] = v
	}
	f.entries[stream] = append(f.entries[stream], Msg{ID: id, Fields: copied})
	return id, nil
}

func (f *fake) Group(context.Context, string) error { return nil }

func (f *fake) find(stream, id string) Msg {
	for _, m := range f.entries[stream] {
		if m.ID == id {
			return m
		}
	}
	panic("no entry " + id)
}

func (f *fake) Read(ctx context.Context, stream string, pending bool) ([]Msg, error) {
	if pending {
		var out []Msg
		for _, id := range f.pending[stream] {
			out = append(out, f.find(stream, id))
		}
		return out, nil
	}
	fresh := f.entries[stream][f.delivered[stream]:]
	if len(fresh) == 0 {
		f.idle()
		return nil, ctx.Err()
	}
	f.delivered[stream] = len(f.entries[stream])
	for _, m := range fresh {
		f.pending[stream] = append(f.pending[stream], m.ID)
	}
	return fresh, nil
}

func (f *fake) Ack(_ context.Context, stream, id string) error {
	if f.failAck > 0 {
		f.failAck--
		return errors.New("connection reset")
	}
	p := f.pending[stream]
	for i, x := range p {
		if x == id {
			f.pending[stream] = append(p[:i:i], p[i+1:]...)
		}
	}
	return nil
}

var testEnv = map[string]string{"NOVA_BUS_REDIS": "127.0.0.1:1", "NOVA_BUS_AS": "alpha"}

func call(t *testing.T, f *fake, env map[string]string, stdin string, args ...string) (int, string, string) {
	t.Helper()
	now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	var out, errOut bytes.Buffer
	dial := func(string, string) Store { return f }
	code := run(context.Background(), args, func(k string) string { return env[k] }, strings.NewReader(stdin), &out, &errOut, dial)
	return code, out.String(), errOut.String()
}

// runHookOnce runs hook until the store has nothing new, returning its output.
func runHookOnce(t *testing.T, f *fake, name string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.idle = cancel
	var out bytes.Buffer
	err := hook(ctx, f, "bus:"+name, &out)
	cancel()
	return out.String(), err
}

func TestSendToTwoNames(t *testing.T) {
	f := newFake()
	code, out, errOut := call(t, f, testEnv, "", "send", "--to", "beta,gamma", "--re", "9-0", "hello", "the body")
	if code != 0 || out != "beta 1-0\ngamma 1-0\n" {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	for _, s := range []string{"bus:beta", "bus:gamma"} {
		got := f.entries[s][0].Fields
		want := map[string]string{"from": "alpha", "to": "beta,gamma", "subject": "hello", "body": "the body", "re": "9-0", "time": "2026-01-02T03:04:05Z"}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: %v", s, got)
		}
	}
}

func TestSendBodyFromStdinAndAsFlag(t *testing.T) {
	f := newFake()
	if code, _, e := call(t, f, testEnv, "from stdin\n", "send", "--as", "delta", "--to", "beta", "s", "-"); code != 0 {
		t.Fatal(e)
	}
	if m := f.entries["bus:beta"][0].Fields; m["body"] != "from stdin\n" || m["from"] != "delta" {
		t.Fatalf("%v", m)
	}
}

func TestHookPrintsPendingThenNew(t *testing.T) {
	f := newFake()
	f.Add(context.Background(), "bus:beta", map[string]string{"from": "a", "subject": "one", "body": "1"})
	f.Read(context.Background(), "bus:beta", false) // handed to an earlier hook, never acked
	f.Add(context.Background(), "bus:beta", map[string]string{"from": "b", "subject": "two", "body": "2", "re": "1-0"})
	out, _ := runHookOnce(t, f, "beta")
	if out != "1-0\ta\t-\tone\t1\n2-0\tb\t1-0\ttwo\t2\n" {
		t.Fatalf("%q", out)
	}
	if len(f.pending["bus:beta"]) != 0 {
		t.Fatalf("unacked: %v", f.pending["bus:beta"])
	}
}

func TestRestartResumesWithoutReprintingAcked(t *testing.T) {
	f := newFake()
	call(t, f, testEnv, "", "send", "--to", "beta", "one", "1")
	call(t, f, testEnv, "", "send", "--to", "beta", "two", "2")
	first, _ := runHookOnce(t, f, "beta")
	call(t, f, testEnv, "", "send", "--to", "beta", "three", "3")
	second, _ := runHookOnce(t, f, "beta")
	if strings.Count(first, "\n") != 2 || second != "3-0\talpha\t-\tthree\t3\n" {
		t.Fatalf("first %q second %q", first, second)
	}
}

func TestCrashBetweenPrintAndAckPrintsAgain(t *testing.T) {
	f := newFake()
	call(t, f, testEnv, "", "send", "--to", "beta", "one", "1")
	f.failAck = 1
	first, err := runHookOnce(t, f, "beta")
	if err == nil || first != "1-0\talpha\t-\tone\t1\n" {
		t.Fatalf("first %q err %v", first, err)
	}
	second, _ := runHookOnce(t, f, "beta")
	if second != first {
		t.Fatalf("at-least-once: second %q", second)
	}
}

func TestBodyNewlinesEscaped(t *testing.T) {
	got := line(Msg{ID: "1-0", Fields: map[string]string{"from": "a", "subject": "s\tt", "body": "x\ny\r\\z"}})
	if got != "1-0\ta\t-\ts\\tt\tx\\ny\\r\\\\z\n" {
		t.Fatalf("%q", got)
	}
}

func TestMissingRedisRefused(t *testing.T) {
	code, _, errOut := call(t, newFake(), map[string]string{"NOVA_BUS_AS": "a"}, "", "hook", "--as", "beta")
	if code == 0 || !strings.Contains(errOut, "NOVA_BUS_REDIS is not set") {
		t.Fatalf("code %d err %q", code, errOut)
	}
}

func TestRefusals(t *testing.T) {
	for _, args := range [][]string{{"send", "--to", "beta", "only-subject"}, {"send", "s", "b"}, {"send", "--to", "a,,b", "s", "b"}, {"hook"}, {"recv"}} {
		env := testEnv
		if args[0] == "hook" {
			env = map[string]string{"NOVA_BUS_REDIS": "x:1"}
		}
		if code, _, _ := call(t, newFake(), env, "", args...); code == 0 {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestHelp(t *testing.T) {
	code, out, _ := call(t, newFake(), nil, "", "help")
	if code != 0 || !strings.Contains(out, "nova-bus hook --as <name>") {
		t.Fatalf("%d %q", code, out)
	}
}
