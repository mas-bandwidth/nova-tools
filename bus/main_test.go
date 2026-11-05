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

// fake is an in-memory store and clock: per stream, the entries, how many the
// group has been handed, and the handed-but-unacked ids; the presence keys
// with their deadlines on the fake clock. A read for new entries hands one at
// a time and moves the clock on by step. When there are none it lets quiet
// reads time out, each moving the clock on by its block, then calls idle (the
// test stops the hook). Like the real client, it refuses a done context.
type fake struct {
	entries   map[string][]Msg
	delivered map[string]int
	pending   map[string][]string
	failAck   int // fail this many acks, then succeed
	failRead  int // hand this many entries and then lose the reply
	idle      func()
	onWrite   func() // after each line is written
	quiet     int    // quiet reads to time out before idle
	step      time.Duration
	clock     time.Time
	blocks    []time.Duration
	afters    []time.Duration
	presence  map[string]string
	deadline  map[string]time.Time
	claims    []time.Time // the clock at each successful Claim
	trims     []string    // the id of each Trim
	log       []string    // the order of writes to stdout and acks
}

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

const testValue = "2026-11-05T12:00:00Z\ttoken"

func newFake() *fake {
	return &fake{entries: map[string][]Msg{}, delivered: map[string]int{}, pending: map[string][]string{},
		clock: t0, presence: map[string]string{}, deadline: map[string]time.Time{}, idle: func() {}}
}

func (f *fake) Now() time.Time { return f.clock }

func (f *fake) After(d time.Duration) <-chan time.Time {
	f.afters = append(f.afters, d)
	f.clock = f.clock.Add(d)
	c := make(chan time.Time, 1)
	c <- f.clock
	return c
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

func (f *fake) Read(ctx context.Context, stream string, pending bool, block time.Duration) ([]Msg, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
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
	f.clock = f.clock.Add(f.step)
	f.delivered[stream]++
	f.pending[stream] = append(f.pending[stream], fresh[0].ID)
	if f.failRead > 0 {
		f.failRead--
		return nil, errors.New("EOF")
	}
	return fresh[:1], nil
}

func (f *fake) Waiting(_ context.Context, stream string) ([]Msg, error) {
	var out []Msg
	for _, id := range f.pending[stream] {
		out = append(out, f.find(stream, id))
	}
	return append(out, f.entries[stream][f.delivered[stream]:]...), nil
}

func (f *fake) Trim(_ context.Context, stream, id string) error {
	f.trims = append(f.trims, stream+" "+id)
	return nil
}

func (f *fake) Count(ctx context.Context, stream string) (int64, error) {
	w, err := f.Waiting(ctx, stream)
	return int64(len(w)), err
}

func (f *fake) live(name string) bool {
	_, ok := f.presence[name]
	return ok && f.clock.Before(f.deadline[name])
}

func (f *fake) Claim(ctx context.Context, name, value string, ttl time.Duration) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if f.live(name) && f.presence[name] != value {
		return false, nil
	}
	f.presence[name], f.deadline[name] = value, f.clock.Add(ttl)
	f.claims = append(f.claims, f.clock)
	return true, nil
}

func (f *fake) Release(ctx context.Context, name, value string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if f.live(name) && f.presence[name] == value {
		delete(f.presence, name)
	}
	return nil
}

// Presence leaves out keys past their deadline, as Redis expiry does.
func (f *fake) Presence(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for n, v := range f.presence {
		if f.live(n) {
			out[n] = v
		}
	}
	return out, nil
}

func (f *fake) Ack(ctx context.Context, stream, id string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
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
	code := run(context.Background(), args, func(k string) string { return env[k] }, strings.NewReader(stdin), &out, &errOut, dial, f)
	return code, out.String(), errOut.String()
}

// runHookOnce runs hook until the store has nothing new, returning its output.
func runHookOnce(t *testing.T, f *fake, name string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.idle = cancel
	var out bytes.Buffer
	err := hook(ctx, f, name, testValue, f, &logWriter{&out, f}, io.Discard)
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
	n, err := l.w.Write(p)
	if l.f.onWrite != nil {
		l.f.onWrite()
	}
	return n, err
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
		{"missing everything", map[string]string{}, []string{"send"}, "nova-bus send: missing --as <name>, --to <name>, <subject> <body>, NOVA_BUS_REDIS (host:port); run: nova-bus send -h\n"},
		{"missing NOVA_BUS_REDIS", map[string]string{}, []string{"hook", "--as", "beta"}, "nova-bus hook: missing NOVA_BUS_REDIS (host:port); run: nova-bus hook -h\n"},
		{"send without --as", testEnv, []string{"send", "--to", "beta", "s", "b"}, "nova-bus send: missing --as <name>; run: nova-bus send -h\n"},
		{"hook without --as", testEnv, []string{"hook"}, "nova-bus hook: missing --as <name>; run: nova-bus hook -h\n"},
		{"re of -", testEnv, []string{"send", "--as", "a", "--to", "beta", "--re", "-", "s", "b"}, "--re wants a message id"},
		{"send without a body", testEnv, []string{"send", "--as", "a", "--to", "beta", "s"}, "missing <body>;"},
		{"send with three arguments", testEnv, []string{"send", "--as", "a", "--to", "beta", "s", "b", "c"}, "not 3 arguments"},
		{"send without --to", testEnv, []string{"send", "--as", "a", "s", "b"}, "missing --to <name>;"},
		{"empty name", testEnv, []string{"send", "--as", "a", "--to", "a,,b", "s", "b"}, "empty name"},
		{"unknown verb", testEnv, []string{"recv"}, "nova-bus: unknown verb \"recv\"; verbs: send, hook, who, pending, help; run: nova-bus help\n"},
		{"ack is not a verb", testEnv, []string{"ack", "--as", "beta"}, "unknown verb \"ack\""},
		{"help for an unknown verb", testEnv, []string{"help", "ack"}, "unknown verb \"ack\""},
		{"unknown flag", testEnv, []string{"hook", "--as", "beta", "--nope"}, "nova-bus hook: unknown flag --nope; run: nova-bus hook -h\n"},
		{"flag without a value", testEnv, []string{"hook", "--as"}, "nova-bus hook: --as needs a value; run: nova-bus hook -h\n"},
		{"bad bool", testEnv, []string{"pending", "--as", "b", "--list=x"}, "nova-bus pending: bad value for --list; run: nova-bus pending -h\n"},
		{"colon in a name", testEnv, []string{"send", "--as", "a", "--to", "presence:x", "s", "b"}, "may not contain ':'"},
		{"hook with --to", testEnv, []string{"hook", "--as", "beta", "--to", "x"}, "nova-bus hook: unknown flag --to; run: nova-bus hook -h\n"},
		{"who with --as", testEnv, []string{"who", "--as", "beta"}, "unknown flag --as"},
		{"pending without --as", testEnv, []string{"pending"}, "missing --as <name>"},
		{"pending with an argument", testEnv, []string{"pending", "--as", "beta", "x"}, "pending takes no arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake()
			code, out, errOut := call(t, f, tc.env, "", tc.args...)
			assert.Equal(t, 2, code)
			assert.Empty(t, out)
			assert.Contains(t, errOut, tc.want)
			assert.Equal(t, 1, strings.Count(errOut, "\n"), "one line")
			assert.NotContains(t, errOut, "flag provided", "no flag-package text")
			assert.Empty(t, f.entries, "a refused send writes nothing")
		})
	}
}

// failingAdd is a store whose XADD fails for one name.
type failingAdd struct{ *fake }

func (f failingAdd) Add(ctx context.Context, stream string, fields map[string]string) (string, error) {
	if stream == "bus:gamma" {
		return "", errors.New("EOF")
	}
	return f.fake.Add(ctx, stream, fields)
}

func TestSendFailedIsNotRetried(t *testing.T) {
	t.Parallel()
	f := newFake()
	var out, errOut bytes.Buffer
	dial := func(string, string) Store { return failingAdd{f} }
	code := run(context.Background(), []string{"send", "--as", "a", "--to", "beta,gamma,delta", "s", "b"},
		func(k string) string { return testEnv[k] }, nil, &out, &errOut, dial, f)
	assert.Equal(t, 1, code)
	assert.Equal(t, "beta 1-0\n", out.String(), "the names before the failure were sent")
	assert.Contains(t, errOut.String(), "SEND FAILED to gamma: EOF")
	assert.Empty(t, f.entries["bus:delta"], "nothing after the failure")
}

func TestHelp(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}, {}} {
		code, out, errOut := call(t, newFake(), nil, "", args...)
		assert.Equal(t, 0, code, args)
		assert.Empty(t, errOut)
		for _, v := range verbOrder {
			assert.Contains(t, out, "  nova-bus "+v, args)
		}
	}
	for _, v := range []string{"send", "hook", "who", "pending"} {
		for _, args := range [][]string{{v, "-h"}, {v, "--help"}, {"help", v}} {
			code, out, errOut := call(t, newFake(), nil, "", args...)
			assert.Equal(t, 0, code, args)
			assert.Empty(t, errOut, args)
			assert.True(t, strings.HasPrefix(out, "  nova-bus "+v+" ") || strings.HasPrefix(out, "  nova-bus "+v+"\n"), "%v: %q", args, out)
			assert.Equal(t, 1, strings.Count(out, "  nova-bus "), "only that verb: %v", args)
		}
	}
	_, out, _ := call(t, newFake(), nil, "", "help")
	assert.Contains(t, out, "One hook per name: a hook that finds the name held waits up to 40 s")
	assert.Contains(t, out, "nova-bus pending --as <name> [--list]")
}

// Streams from an older build hold acked history: it is removed the first
// time a hook finds unacked entries pending, below the first of them.
func TestHookTrimsAckedHistoryOnce(t *testing.T) {
	t.Parallel()
	f := newFake()
	for _, s := range []string{"one", "two", "three", "four"} {
		send(t, f, s, "b")
	}
	f.delivered["bus:beta"] = 3 // one and two acked; three handed, not acked
	f.pending["bus:beta"] = []string{"3-0"}
	out, _ := runHookOnce(t, f, "beta")
	assert.Equal(t, 2, strings.Count(out, "\n"))
	assert.Equal(t, []string{"bus:beta 3-0"}, f.trims, "trimmed below the oldest unacked entry, once")

	g := newFake()
	for _, s := range []string{"one", "two", "three"} {
		send(t, g, s, "b")
	}
	g.delivered["bus:beta"] = 2 // nothing pending: nothing is trimmed
	_, _ = runHookOnce(t, g, "beta")
	assert.Empty(t, g.trims, "a new entry is never the trim point")
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

// A signal between writing a line and acking it does not leave the line
// unacked: the ack runs on a context the signal does not cancel, and nothing
// more is printed.
func TestSignalAfterWriteStillAcks(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	send(t, f, "two", "2")
	ctx, cancel := context.WithCancel(context.Background())
	f.onWrite = cancel
	var out bytes.Buffer
	err := hook(ctx, f, "beta", testValue, f, &logWriter{&out, f}, io.Discard)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{"print 1-0", "ack 1-0"}, f.log)
	assert.Empty(t, f.pending["bus:beta"])
}

func TestHookClaimsPresenceAtStartAndRefreshes(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.quiet = 30                     // a quiet read waits readBlock: 30 s in all
	_, _ = runHookOnce(t, f, "beta") // ends on the fake's cancel
	assert.Equal(t, testValue, f.presence["beta"])
	assert.Equal(t, []time.Time{t0, t0.Add(10 * time.Second), t0.Add(20 * time.Second), t0.Add(30 * time.Second)}, f.claims)
	assert.Equal(t, t0.Add(60*time.Second), f.deadline["beta"], "each claim holds the key for 30 s")
	for _, b := range f.blocks {
		assert.LessOrEqual(t, b, readBlock, "no read waits past the next refresh, or long enough to delay an exit")
		assert.Positive(t, b, "a block of 0 would wait forever")
	}
}

func TestHookRefreshesUnderContinuousTraffic(t *testing.T) {
	t.Parallel()
	f := newFake()
	for i := 0; i < 25; i++ {
		send(t, f, "s", "b")
	}
	f.step = time.Second // every read returns a message and takes a second
	out, _ := runHookOnce(t, f, "beta")
	assert.Equal(t, 25, strings.Count(out, "\n"))
	assert.Equal(t, []time.Time{t0, t0.Add(10 * time.Second), t0.Add(20 * time.Second)}, f.claims)
}

// A supervised restart after a crash: the dead hook's key is still there, so
// the new hook waits, polling each second on its clock, and claims it once it
// has lapsed.
func TestRestartWaitsForACrashedHooksKey(t *testing.T) {
	t.Parallel()
	f := newFake()
	_, _ = runHookOnce(t, f, "beta") // the crashed hook: its key holds to t0+30s
	f.clock = t0.Add(5 * time.Second)
	send(t, f, "one", "1")
	ctx, cancel := context.WithCancel(context.Background())
	f.idle = func() {
		assert.NotEqual(t, testValue, f.presence["beta"], "the new hook holds the name")
		cancel()
	}
	var out, errOut bytes.Buffer
	code := runHook(ctx, f, "beta", f, &out, &errOut)
	assert.Equal(t, 0, code, errOut.String())
	assert.Equal(t, "HOOK WAIT name=beta ttl=30\n", errOut.String())
	assert.Len(t, f.afters, 25, "polled each second from 5 s to 30 s")
	assert.Equal(t, t0.Add(30*time.Second), f.claims[len(f.claims)-1], "claimed as the key lapsed")
	assert.Equal(t, "1-0\talpha\t-\tone\t1\n", out.String())
}

// A live duplicate keeps its key: the second hook gives up after 40 s and
// leaves the first's presence alone.
func TestSecondHookOnALiveNameIsRefusedAfterWaiting(t *testing.T) {
	t.Parallel()
	f := newFake()
	ok, err := f.Claim(context.Background(), "beta", "first\ttoken", time.Hour) // refreshed throughout
	require.True(t, ok)
	require.NoError(t, err)
	var out, errOut bytes.Buffer
	code := runHook(context.Background(), f, "beta", f, &out, &errOut)
	assert.Equal(t, 1, code)
	assert.Equal(t, "HOOK WAIT name=beta ttl=30\nnova-bus: name already hooked: beta\n", errOut.String())
	assert.Len(t, f.afters, 40)
	assert.Equal(t, t0.Add(40*time.Second), f.clock)
	assert.Equal(t, "first\ttoken", f.presence["beta"])
	assert.Empty(t, out.String())
}

func TestWaitingHookStopsOnSignal(t *testing.T) {
	t.Parallel()
	f := newFake()
	ok, _ := f.Claim(context.Background(), "beta", "first\ttoken", time.Hour)
	require.True(t, ok)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	assert.Equal(t, 0, runHook(ctx, f, "beta", f, &out, &errOut))
	assert.Equal(t, "first\ttoken", f.presence["beta"], "the release leaves another hook's key")
}

func TestReleaseLeavesAnotherHooksPresence(t *testing.T) {
	t.Parallel()
	f := newFake()
	ok, err := f.Claim(context.Background(), "beta", "other\tvalue", presenceTTL)
	require.True(t, ok)
	require.NoError(t, err)
	require.NoError(t, f.Release(context.Background(), "beta", testValue))
	assert.Equal(t, "other\tvalue", f.presence["beta"])
}

func TestHookReleasesPresenceOnCleanExit(t *testing.T) {
	t.Parallel()
	f := newFake()
	ctx, cancel := context.WithCancel(context.Background())
	f.idle = func() {
		assert.Contains(t, f.presence, "beta", "present while it runs")
		cancel()
	}
	var out, errOut bytes.Buffer
	code := runHook(ctx, f, "beta", f, &out, &errOut)
	assert.Equal(t, 0, code, errOut.String())
	assert.NotContains(t, f.presence, "beta")
}

type brokenPipe struct{}

func (brokenPipe) Write([]byte) (int, error) { return 0, errors.New("write |1: broken pipe") }

func TestHookReleasesPresenceWhenStdoutFails(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	var errOut bytes.Buffer
	code := runHook(context.Background(), f, "beta", f, brokenPipe{}, &errOut)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut.String(), "writing stdout: write |1: broken pipe")
	assert.NotContains(t, f.presence, "beta")
	assert.Equal(t, []string{"1-0"}, f.pending["bus:beta"], "the unprinted message stays pending")
}

// A read whose reply is lost after the server handed the entry: the hook
// waits a second on its clock, reconnects and prints it from the pending list.
func TestLostReadReplyIsPrintedAfterReconnect(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	f.failRead = 1
	ctx, cancel := context.WithCancel(context.Background())
	f.idle = cancel
	var out, errOut bytes.Buffer
	code := runHook(ctx, f, "beta", f, &out, &errOut)
	assert.Equal(t, 0, code)
	assert.Contains(t, errOut.String(), "EOF; reconnecting")
	assert.Equal(t, []time.Duration{time.Second}, f.afters, "the wait is on the injected clock")
	assert.Equal(t, "1-0\talpha\t-\tone\t1\n", out.String())
	assert.Empty(t, f.pending["bus:beta"])
}

func TestCrashedHookLapsesWithinTTL(t *testing.T) {
	t.Parallel()
	f := newFake()
	_, _ = runHookOnce(t, f, "beta") // stops without releasing: a crash, as far as Redis knows
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
	claim := func(name, value string, ttl time.Duration) {
		ok, err := f.Claim(context.Background(), name, value, ttl)
		require.True(t, ok)
		require.NoError(t, err)
	}
	f.clock = t0.Add(-90 * time.Minute)
	claim("gamma", f.clock.Format(time.RFC3339)+"\tg", 2*time.Hour)
	f.clock = t0.Add(-5 * time.Second)
	claim("alpha", f.clock.Format(time.RFC3339)+"\ta", presenceTTL)
	claim("beta", "not a time", presenceTTL)
	claim("delta", "", presenceTTL) // what Presence gives for a key that is not a string
	f.clock = t0.Add(500 * time.Millisecond)
	code, out, errOut := call(t, f, testEnv, "", "who")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "alpha\t5s\nbeta\t?\ndelta\t?\ngamma\t1h30m0s\n", out)
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
