package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fake is an in-memory inbox store: per stream, the entries, how many the
// group has been handed, and the handed-but-unacked ids; the presence keys
// with their deadlines on the fake clock. A read for new entries hands one at
// a time. When there are none it lets quiet reads time out, each moving the
// clock on by its block, then calls idle (the test stops the hook).
type fake struct {
	entries   map[string][]Msg
	delivered map[string]int
	pending   map[string][]string
	failAck   int // fail this many acks, then succeed
	idle      func()
	quiet     int // quiet reads to time out before idle
	clock     time.Time
	blocks    []time.Duration
	presence  map[string]string
	deadline  map[string]time.Time
	sets      []time.Time // the clock at each SetPresence
	log       []string    // the order of writes to stdout and acks
}

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func newFake() *fake {
	return &fake{entries: map[string][]Msg{}, delivered: map[string]int{}, pending: map[string][]string{},
		clock: t0, presence: map[string]string{}, deadline: map[string]time.Time{}}
}

func (f *fake) now() time.Time { return f.clock }

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

func (f *fake) Read(ctx context.Context, stream string, pending bool, block time.Duration) ([]Msg, error) {
	if pending {
		var out []Msg
		for _, id := range f.pending[stream] {
			out = append(out, f.find(stream, id))
		}
		return out, nil
	}
	f.blocks = append(f.blocks, block)
	fresh := f.entries[stream][f.delivered[stream]:]
	if len(fresh) == 0 {
		if f.quiet > 0 {
			f.quiet--
			f.clock = f.clock.Add(block)
			return nil, nil
		}
		f.idle()
		return nil, ctx.Err()
	}
	f.delivered[stream]++
	f.pending[stream] = append(f.pending[stream], fresh[0].ID)
	return fresh[:1], nil
}

func (f *fake) Waiting(_ context.Context, stream string) ([]Msg, error) {
	var out []Msg
	for _, id := range f.pending[stream] {
		out = append(out, f.find(stream, id))
	}
	return append(out, f.entries[stream][f.delivered[stream]:]...), nil
}

func (f *fake) SetPresence(_ context.Context, name, value string, ttl time.Duration) error {
	f.presence[name], f.deadline[name] = value, f.clock.Add(ttl)
	f.sets = append(f.sets, f.clock)
	return nil
}

func (f *fake) DelPresence(_ context.Context, name string) error {
	delete(f.presence, name)
	delete(f.deadline, name)
	return nil
}

// Presence leaves out keys past their deadline, as Redis expiry does.
func (f *fake) Presence(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for n, v := range f.presence {
		if f.clock.Before(f.deadline[n]) {
			out[n] = v
		}
	}
	return out, nil
}

func (f *fake) Ack(_ context.Context, stream, id string) error {
	f.log = append(f.log, "ack "+id)
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
	code := run(context.Background(), args, func(k string) string { return env[k] }, strings.NewReader(stdin), &out, &errOut, dial, f.now)
	return code, out.String(), errOut.String()
}

// runHookOnce runs hook until the store has nothing new, returning its output.
func runHookOnce(t *testing.T, f *fake, name string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.idle = cancel
	var out bytes.Buffer
	err := hook(ctx, f, name, f.clock, f.now, &logWriter{&out, f})
	cancel()
	return out.String(), err
}

// logWriter records each write in the fake's log, so a test sees whether a
// line was written before its ack.
type logWriter struct {
	w io.Writer
	f *fake
}

func (l *logWriter) Write(p []byte) (int, error) {
	l.f.log = append(l.f.log, "print "+strings.SplitN(string(p), "\t", 2)[0])
	return l.w.Write(p)
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
	_, err := f.Read(context.Background(), "bus:beta", false, time.Second) // handed to an earlier hook, never acked
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
		{"ack is not a verb", testEnv, []string{"ack", "--as", "beta"}, "unknown verb"},
		{"colon in a name", testEnv, []string{"send", "--as", "a", "--to", "presence:x", "s", "b"}, "may not contain ':'"},
		{"hook with --to", testEnv, []string{"hook", "--as", "beta", "--to", "x"}, "hook does not take --to"},
		{"who with --as", testEnv, []string{"who", "--as", "beta"}, "who does not take --as"},
		{"pending without --as", testEnv, []string{"pending"}, "give --as <name>"},
		{"pending with an argument", testEnv, []string{"pending", "--as", "beta", "x"}, "pending takes no arguments"},
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
	assert.Contains(t, out, "nova-bus who\n")
	assert.Contains(t, out, "nova-bus pending --as <name> [--list]")
}

func TestHookAcksEachLineAfterPrintingIt(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	send(t, f, "two", "2")
	out, _ := runHookOnce(t, f, "beta")
	assert.Equal(t, 2, strings.Count(out, "\n"))
	assert.Equal(t, []string{"print 1-0", "ack 1-0", "print 2-0", "ack 2-0"}, f.log)
}

func TestHookSetsPresenceAtStartAndRefreshes(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.quiet = 30                     // a quiet read waits readBlock: 30 s in all
	_, _ = runHookOnce(t, f, "beta") // ends on the fake's cancel
	assert.Equal(t, t0.Format(time.RFC3339), f.presence["beta"], "the value is the hook's start time")
	assert.Equal(t, []time.Time{t0, t0.Add(10 * time.Second), t0.Add(20 * time.Second), t0.Add(30 * time.Second)}, f.sets)
	assert.Equal(t, t0.Add(60*time.Second), f.deadline["beta"], "each set holds the key for 30 s")
	for _, b := range f.blocks {
		assert.LessOrEqual(t, b, readBlock, "no read waits past the next refresh, or long enough to delay an exit")
		assert.Positive(t, b, "a block of 0 would wait forever")
	}
}

func TestHookDeletesPresenceOnCleanExit(t *testing.T) {
	t.Parallel()
	f := newFake()
	ctx, cancel := context.WithCancel(context.Background())
	f.idle = func() {
		assert.Contains(t, f.presence, "beta", "present while it runs")
		cancel()
	}
	var out, errOut bytes.Buffer
	code := runHook(ctx, f, "beta", f.now, &out, &errOut)
	assert.Equal(t, 0, code, errOut.String())
	assert.NotContains(t, f.presence, "beta")
}

func TestCrashedHookLapsesWithinTTL(t *testing.T) {
	t.Parallel()
	f := newFake()
	_, _ = runHookOnce(t, f, "beta") // stops without deleting: a crash, as far as Redis knows
	f.clock = t0.Add(29 * time.Second)
	present, _ := f.Presence(context.Background())
	assert.Contains(t, present, "beta")
	f.clock = t0.Add(30 * time.Second)
	present, _ = f.Presence(context.Background())
	assert.NotContains(t, present, "beta")
}

func TestWhoSortsAndShowsUptime(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.clock = t0.Add(-90 * time.Minute)
	require.NoError(t, f.SetPresence(context.Background(), "gamma", f.clock.Format(time.RFC3339), time.Hour*2))
	f.clock = t0.Add(-5 * time.Second)
	require.NoError(t, f.SetPresence(context.Background(), "alpha", f.clock.Format(time.RFC3339), presenceTTL))
	require.NoError(t, f.SetPresence(context.Background(), "beta", "not a time", presenceTTL))
	f.clock = t0.Add(500 * time.Millisecond)
	code, out, errOut := call(t, f, testEnv, "", "who")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "alpha\t5s\nbeta\t?\ngamma\t1h30m0s\n", out)
}

func TestWhoWithNobody(t *testing.T) {
	t.Parallel()
	code, out, _ := call(t, newFake(), testEnv, "", "who")
	assert.Equal(t, 0, code)
	assert.Empty(t, out)
}

func TestPendingCountsHandedAndUnhandedWithoutConsuming(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	send(t, f, "two\tx", "2")
	send(t, f, "three", "3")
	_, err := f.Read(context.Background(), "bus:beta", false, time.Second) // one handed, not acked
	require.NoError(t, err)
	code, out, errOut := call(t, f, testEnv, "", "pending", "--as", "beta")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "3\n", out)
	code, out, errOut = call(t, f, testEnv, "", "pending", "--as", "beta", "--list")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "3\n1-0\tone\n2-0\ttwo\\tx\n3-0\tthree\n", out)
	assert.Equal(t, 1, f.delivered["bus:beta"], "pending consumes nothing")
	assert.Equal(t, []string{"1-0"}, f.pending["bus:beta"])
	code, out, _ = call(t, f, testEnv, "", "pending", "--as", "nobody")
	assert.Equal(t, 0, code)
	assert.Equal(t, "0\n", out)
}
