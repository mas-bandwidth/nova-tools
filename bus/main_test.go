package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

var testEnv = map[string]string{"NOVA_BUS_REDIS": "127.0.0.1:1"}

func call(t *testing.T, f *fake, env map[string]string, stdin string, args ...string) (int, string, string) {
	t.Helper()
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

func send(t *testing.T, f *fake, subject, body string) {
	t.Helper()
	code, _, errOut := call(t, f, testEnv, "", "send", "--as", "alpha", "--to", "beta", subject, body)
	require.Equal(t, 0, code, errOut)
}

func TestSendToTwoNames(t *testing.T) {
	t.Parallel()
	f := newFake()
	code, out, errOut := call(t, f, testEnv, "", "send", "--as", "alpha", "--to", "beta,gamma", "--re", "9-0", "hello", "the body")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "beta 1-0\ngamma 1-0\n", out)
	for _, s := range []string{"bus:beta", "bus:gamma"} {
		require.Len(t, f.entries[s], 1, s)
		assert.Equal(t, map[string]string{"from": "alpha", "to": "beta,gamma", "subject": "hello", "body": "the body", "re": "9-0"}, f.entries[s][0].Fields, s)
	}
}

func TestSendBodyFromStdin(t *testing.T) {
	t.Parallel()
	f := newFake()
	code, _, errOut := call(t, f, testEnv, "from stdin\n", "send", "--as", "delta", "--to", "beta", "s", "-")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "from stdin\n", f.entries["bus:beta"][0].Fields["body"])
}

func TestHookPrintsPendingThenNew(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	_, err := f.Read(context.Background(), "bus:beta", false) // handed to an earlier hook, never acked
	require.NoError(t, err)
	code, _, errOut := call(t, f, testEnv, "", "send", "--as", "b", "--to", "beta", "--re", "1-0", "two", "2")
	require.Equal(t, 0, code, errOut)
	out, _ := runHookOnce(t, f, "beta")
	assert.Equal(t, "1-0\talpha\t-\tone\t1\n2-0\tb\t1-0\ttwo\t2\n", out)
	assert.Empty(t, f.pending["bus:beta"])
}

func TestRestartResumesWithoutReprintingAcked(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	send(t, f, "two", "2")
	first, _ := runHookOnce(t, f, "beta")
	send(t, f, "three", "3")
	second, _ := runHookOnce(t, f, "beta")
	assert.Equal(t, 2, strings.Count(first, "\n"), first)
	assert.Equal(t, "3-0\talpha\t-\tthree\t3\n", second)
}

func TestCrashBetweenPrintAndAckPrintsAgain(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	f.failAck = 1
	first, err := runHookOnce(t, f, "beta")
	require.Error(t, err)
	assert.Equal(t, "1-0\talpha\t-\tone\t1\n", first)
	second, _ := runHookOnce(t, f, "beta")
	assert.Equal(t, first, second, "at-least-once: the unacked line is printed again")
}

func TestBodyNewlinesEscaped(t *testing.T) {
	t.Parallel()
	got := line(Msg{ID: "1-0", Fields: map[string]string{"from": "a", "subject": "s\tt", "body": "x\ny\r\\z"}})
	assert.Equal(t, "1-0\ta\t-\ts\\tt\tx\\ny\\r\\\\z\n", got)
}

func TestRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"missing NOVA_BUS_REDIS", map[string]string{}, []string{"hook", "--as", "beta"}, "NOVA_BUS_REDIS is not set"},
		{"send without --as", testEnv, []string{"send", "--to", "beta", "s", "b"}, "give --as <name>"},
		{"hook without --as", testEnv, []string{"hook"}, "give --as <name>"},
		{"re of -", testEnv, []string{"send", "--as", "a", "--to", "beta", "--re", "-", "s", "b"}, "--re wants a message id"},
		{"send without a body", testEnv, []string{"send", "--as", "a", "--to", "beta", "s"}, "usage: nova-bus send"},
		{"send without --to", testEnv, []string{"send", "--as", "a", "s", "b"}, "usage: nova-bus send"},
		{"empty name", testEnv, []string{"send", "--as", "a", "--to", "a,,b", "s", "b"}, "empty name"},
		{"unknown verb", testEnv, []string{"recv"}, "unknown verb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			code, _, errOut := call(t, f, tc.env, "", tc.args...)
			assert.NotEqual(t, 0, code)
			assert.Contains(t, errOut, tc.want)
			assert.Empty(t, f.entries, "a refused send writes nothing")
		})
	}
}

func TestHelp(t *testing.T) {
	t.Parallel()
	code, out, _ := call(t, newFake(), nil, "", "help")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "nova-bus hook --as <name>")
	assert.Contains(t, out, "Run one hook per name; two hooks on the same name split its messages.")
}
