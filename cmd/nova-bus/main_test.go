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
	onRead    func() // after each quiet read
	quiet     int    // quiet reads to time out before idle
	step      time.Duration
	clock     time.Time
	blocks    []time.Duration
	afters    []time.Duration
	presence  map[string]string
	deadline  map[string]time.Time
	claims    []time.Time // the clock at each successful Claim
	down      int         // fail this many Add, Count and Presence calls: refused
	log       []string    // the order of writes to stdout and acks
	groups    map[string]bool
	strict    bool // Hooked answers from groups
	gone      int  // reads after which stdout's reader is gone (0: never)
	reads     int
}

// outGone is the fake's poll of stdout: its reader is gone after gone reads.
func (f *fake) outGone() bool { return f.gone > 0 && f.reads >= f.gone }

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
	if f.isDown() {
		return "", errRefused
	}
	id := fmt.Sprintf("%d-0", len(f.entries[stream])+1)
	copied := map[string]string{}
	for k, v := range fields {
		copied[k] = v
	}
	f.entries[stream] = append(f.entries[stream], Msg{ID: id, Fields: copied})
	return id, nil
}

func (f *fake) Group(_ context.Context, stream string) error {
	if f.groups == nil {
		f.groups = map[string]bool{}
	}
	f.groups[stream] = true
	return nil
}

// Hooked is true for every stream unless strict, so a test's sends need no
// hook first; the refusal has its own test.
func (f *fake) Hooked(_ context.Context, stream string) (bool, error) {
	if f.isDown() {
		return false, errRefused
	}
	return !f.strict || f.groups[stream], nil
}

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
	f.reads++
	fresh := f.entries[stream][f.delivered[stream]:]
	if len(fresh) == 0 {
		if f.quiet > 0 {
			f.quiet--
			f.clock = f.clock.Add(block)
			if f.onRead != nil {
				f.onRead()
			}
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
		return nil, io.EOF
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

var errRefused = errors.New("dial tcp 127.0.0.1:1: connect: connection refused")

func (f *fake) isDown() bool {
	f.down--
	return f.down >= 0
}

func (f *fake) Count(ctx context.Context, stream string) (int64, error) {
	if f.isDown() {
		return 0, errRefused
	}
	w, err := f.Waiting(ctx, stream)
	return int64(len(w)), err
}

func (f *fake) live(name string) bool {
	_, ok := f.presence[name]
	return ok && f.clock.Before(f.deadline[name])
}

func (f *fake) Claim(ctx context.Context, name, value string, ttl time.Duration) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if f.live(name) && f.presence[name] != value {
		return f.presence[name], nil
	}
	f.presence[name], f.deadline[name] = value, f.clock.Add(ttl)
	f.claims = append(f.claims, f.clock)
	return "", nil
}

// claim is Claim for a test's setup: it must succeed.
func (f *fake) claim(t *testing.T, name, value string, ttl time.Duration) {
	t.Helper()
	held, err := f.Claim(context.Background(), name, value, ttl)
	require.NoError(t, err)
	require.Empty(t, held)
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
	if f.isDown() {
		return nil, errRefused
	}
	out := map[string]string{}
	for n, v := range f.presence {
		if f.live(n) {
			out[n] = v
		}
	}
	return out, nil
}

func (f *fake) Ack(ctx context.Context, stream, id string) (int64, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	f.log = append(f.log, "ack "+id)
	if f.failAck > 0 {
		f.failAck--
		return 0, errors.New("connection reset")
	}
	p := f.pending[stream]
	for i, x := range p {
		if x == id {
			f.pending[stream] = append(p[:i:i], p[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

const testAddr = "127.0.0.1:1"

var testEnv = map[string]string{"NOVA_BUS_REDIS": testAddr}

func call(t *testing.T, f *fake, env map[string]string, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	dial := func(string, string, string) Store { return f }
	code := run(context.Background(), args, func(k string) string { return env[k] }, strings.NewReader(stdin), &out, &errOut, dial, f, sys{})
	return code, out.String(), errOut.String()
}

// runHookOnce runs hook until the store has nothing new, returning its output.
func runHookOnce(t *testing.T, f *fake, name string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.idle = cancel
	var out bytes.Buffer
	err := hook(ctx, f, name, testValue, f, &logWriter{&out, f}, io.Discard, func(int64) {}, nil, f.outGone)
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

// hookedIn plants name's presence, as a hook of its own would, for a day of
// the fake clock: a verb that acts as name runs only while it is hooked in.
func hookedIn(t *testing.T, f *fake, name string) {
	t.Helper()
	if !f.live(name) { // planted, not claimed: the claims a test counts are the hook's own
		f.presence[name], f.deadline[name] = "2026-11-05T11:00:00Z", f.clock.Add(24*time.Hour)
	}
}

func send(t *testing.T, f *fake, subject, body string) {
	t.Helper()
	hookedIn(t, f, "alpha")
	code, _, errOut := call(t, f, testEnv, "", "send", "--as", "alpha", "--to", "beta", subject, body)
	require.Equal(t, 0, code, errOut)
}

func TestSendToTwoNames(t *testing.T) {
	t.Parallel()
	f := newFake()
	hookedIn(t, f, "alpha")
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
	hookedIn(t, f, "delta")
	code, _, errOut := call(t, f, testEnv, "from stdin\n\n", "send", "--as", "delta", "--to", "beta", "s", "-")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "from stdin\n", f.entries["bus:beta"][0].Fields["body"], "exactly one trailing newline trimmed")
}

// A body is text: one holding a NUL byte is refused in one line and nothing
// is sent.
func TestSendRefusesABodyWithANUL(t *testing.T) {
	t.Parallel()
	f := newFake()
	hookedIn(t, f, "delta")
	code, out, errOut := call(t, f, testEnv, "a\x00b\n", "send", "--as", "delta", "--to", "beta", "s", "-")
	assert.Equal(t, 2, code)
	assert.Empty(t, out)
	assert.Equal(t, "nova-bus send: the body holds a NUL byte, and a body is text: encode binary data (base64, say) and send that; run: nova-bus send -h\n", errOut)
	assert.Empty(t, f.entries, "a refused send writes nothing")
}

func TestHookPrintsPendingThenNew(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	_, err := f.Read(context.Background(), "bus:beta", false, time.Second) // handed to an earlier hook, never acked
	require.NoError(t, err)
	hookedIn(t, f, "b")
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
		{"re of -", testEnv, []string{"send", "--as", "a", "--to", "beta", "--re", "-", "s", "b"}, `--re "-" is not a message id`},
		{"send without a body", testEnv, []string{"send", "--as", "a", "--to", "beta", "s"}, "missing <body>;"},
		{"send with three arguments", testEnv, []string{"send", "--as", "a", "--to", "beta", "s", "b", "c"}, "not 3 arguments"},
		{"send without --to", testEnv, []string{"send", "--as", "a", "s", "b"}, "missing --to <name>;"},
		{"empty name", testEnv, []string{"send", "--as", "a", "--to", "a,,b", "s", "b"}, "empty name"},
		{"unknown verb", testEnv, []string{"peek"}, "nova-bus: unknown verb \"peek\"; verbs: send, hook, who, pending, show, help; run: nova-bus help\n"},
		{"delete is not a verb", testEnv, []string{"delete", "--as", "beta"}, "unknown verb \"delete\""},
		{"help for an unknown verb", testEnv, []string{"help", "delete"}, "unknown verb \"delete\""},
		{"unknown flag", testEnv, []string{"hook", "--as", "beta", "--nope"}, "nova-bus hook: unknown flag --nope; run: nova-bus hook -h\n"},
		{"flag without a value", testEnv, []string{"hook", "--as"}, "nova-bus hook: --as needs a value; run: nova-bus hook -h\n"},
		{"bad bool", testEnv, []string{"pending", "--as", "b", "--list=x"}, "nova-bus pending: bad value for --list; run: nova-bus pending -h\n"},
		{"colon in a name", testEnv, []string{"send", "--as", "a", "--to", "presence:x", "s", "b"}, `bad name "presence:x"`},
		{"space in a name", testEnv, []string{"hook", "--as", "a b"}, `bad name "a b"`},
		{"control char in --as", testEnv, []string{"send", "--as", "a\x01", "--to", "b", "s", "b"}, `bad name "a\x01"`},
		{"re not an id", testEnv, []string{"send", "--as", "a", "--to", "b", "--re", "hello", "s", "b"}, `--re "hello" is not a message id`},
		{"re half an id", testEnv, []string{"send", "--as", "a", "--to", "b", "--re", "12-", "s", "b"}, "is not a message id"},
		{"user without password", map[string]string{"NOVA_BUS_REDIS": testAddr, "NOVA_BUS_USER": "bus"}, []string{"who"},
			"nova-bus who: NOVA_BUS_USER is set but NOVA_BUS_PASSWORD is empty: a named user needs its password; run: nova-bus who -h\n"},
		{"empty subject", testEnv, []string{"send", "--as", "a", "--to", "b", " ", "b"}, "nova-bus send: empty subject; run: nova-bus send -h\n"},
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
		return "", io.EOF
	}
	return f.fake.Add(ctx, stream, fields)
}

func TestSendFailedIsNotRetried(t *testing.T) {
	t.Parallel()
	f := newFake()
	hookedIn(t, f, "a")
	var out, errOut bytes.Buffer
	dial := func(string, string, string) Store { return failingAdd{f} }
	code := run(context.Background(), []string{"send", "--as", "a", "--to", "beta,gamma,delta", "s", "b"},
		func(k string) string { return testEnv[k] }, nil, &out, &errOut, dial, f, sys{})
	assert.Equal(t, 1, code)
	assert.Equal(t, "beta 1-0\n", out.String(), "the names before the failure were sent")
	assert.Equal(t, "nova-bus send: SEND FAILED to gamma: cannot reach redis at 127.0.0.1:1 (NOVA_BUS_REDIS): connection lost; check that NOVA_BUS_REDIS=127.0.0.1:1 is the fleet's Redis and that it is up\n", errOut.String())
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
	for _, v := range []string{"send", "hook", "who", "pending", "show"} {
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
	err := hook(ctx, f, "beta", testValue, f, &logWriter{&out, f}, io.Discard, func(int64) {}, nil, nil)
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
	code := runHook(ctx, f, "beta", testAddr, f, &out, &errOut, nil, "again", nil)
	assert.Equal(t, 0, code, errOut.String())
	assert.Equal(t, "HOOK WAIT name=beta held_since=2026-11-05T12:00:00Z ttl=30s budget=40s\nHOOK OK name=beta waiting=1\nnext: NOVA_BUS_REDIS=127.0.0.1:1 nova-bus send --as beta --to <name> <subject> <body>\n", errOut.String())
	assert.Len(t, f.afters, 25, "polled each second from 5 s to 30 s")
	assert.Equal(t, t0.Add(30*time.Second), f.claims[len(f.claims)-1], "claimed as the key lapsed")
	assert.Equal(t, "1-0\talpha\t-\tone\t1\n", out.String())
}

// A live duplicate keeps its key: the second hook gives up after 40 s and
// leaves the first's presence alone.
func TestSecondHookOnALiveNameIsRefusedAfterWaiting(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.claim(t, "beta", "first\ttoken", time.Hour) // refreshed throughout
	var out, errOut bytes.Buffer
	code := runHook(context.Background(), f, "beta", testAddr, f, &out, &errOut, nil, "again", nil)
	assert.Equal(t, 1, code)
	assert.Equal(t, "HOOK WAIT name=beta held_since=first ttl=30s budget=40s\nnova-bus hook: name already hooked: beta after 40s; run: nova-bus who\n", errOut.String())
	assert.Len(t, f.afters, 40)
	assert.Equal(t, t0.Add(40*time.Second), f.clock)
	assert.Equal(t, "first\ttoken", f.presence["beta"])
	assert.Empty(t, out.String())
}

func TestWaitingHookStopsOnSignal(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.claim(t, "beta", "first\ttoken", time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	assert.Equal(t, 0, runHook(ctx, f, "beta", testAddr, f, &out, &errOut, nil, "again", nil))
	assert.Equal(t, "first\ttoken", f.presence["beta"], "the release leaves another hook's key")
}

func TestReleaseLeavesAnotherHooksPresence(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.claim(t, "beta", "other\tvalue", presenceTTL)
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
	code := runHook(ctx, f, "beta", testAddr, f, &out, &errOut, nil, "again", nil)
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
	code := runHook(context.Background(), f, "beta", testAddr, f, brokenPipe{}, &errOut, nil, "again", nil)
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
	code := runHook(ctx, f, "beta", testAddr, f, &out, &errOut, nil, "again", nil)
	assert.Equal(t, 0, code)
	assert.Equal(t, "HOOK OK name=beta waiting=1\nnext: NOVA_BUS_REDIS=127.0.0.1:1 nova-bus send --as beta --to <name> <subject> <body>\nnova-bus hook: cannot reach redis at 127.0.0.1:1 (NOVA_BUS_REDIS): connection lost; check that NOVA_BUS_REDIS=127.0.0.1:1 is the fleet's Redis and that it is up; reconnecting\n", errOut.String())
	assert.Equal(t, []time.Duration{time.Second}, f.afters, "the wait is on the injected clock")
	assert.Equal(t, "HOOK RECONNECTED name=beta waiting=1\n1-0\talpha\t-\tone\t1\n", out.String(), "the batch whose reply was lost is printed after the reconnect, which the Monitor sees")
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
	claim := func(name, value string, ttl time.Duration) { f.claim(t, name, value, ttl) }
	f.clock = t0.Add(-90 * time.Minute)
	claim("gamma", f.clock.Format(time.RFC3339)+"\tg", 2*time.Hour)
	f.clock = t0.Add(-5 * time.Second)
	claim("alpha", f.clock.Format(time.RFC3339)+"\ta", presenceTTL)
	claim("beta", "not a time", presenceTTL)
	claim("delta", "", presenceTTL) // what Presence gives for a key that is not a string
	f.clock = t0.Add(500 * time.Millisecond)
	code, out, errOut := call(t, f, testEnv, "", "who")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "WHO OK present=4\nalpha\t5s\ta\nbeta\t?\ndelta\t?\ngamma\t1h30m0s\tg\n", out)
}

func TestWhoPrintsSessionID(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	f.failRead = 1 // forces a reconnect inside the first hook process
	var firstWho string
	ctx1, cancel1 := context.WithCancel(context.Background())
	f.idle = func() {
		f.clock = t0.Add(5 * time.Second)
		code, out, errOut := call(t, f, testEnv, "", "who")
		require.Equal(t, 0, code, errOut)
		firstWho = out
		cancel1()
	}
	var out1, err1 bytes.Buffer
	require.Equal(t, 0, runHook(ctx1, f, "beta", testAddr, f, &out1, &err1, nil, "again", nil))
	assert.Len(t, f.claims, 2, "claimed at start and again after reconnect")
	lines1 := strings.Split(strings.TrimSuffix(firstWho, "\n"), "\n")
	require.Equal(t, []string{"WHO OK present=2", "alpha\t1h0m5s"}, lines1[:2])
	fields1 := strings.Split(lines1[2], "\t")
	require.Len(t, fields1, 3)
	assert.Equal(t, []string{"beta", "5s"}, fields1[:2])
	assert.Regexp(t, "^[0-9a-f]{32}$", fields1[2], "16-byte random hex session id")

	// A restarted hook process mints a new session id.
	f.clock = t0.Add(10 * time.Second)
	var secondWho string
	ctx2, cancel2 := context.WithCancel(context.Background())
	f.idle = func() {
		f.clock = t0.Add(12 * time.Second)
		code, out, errOut := call(t, f, testEnv, "", "who")
		require.Equal(t, 0, code, errOut)
		secondWho = out
		cancel2()
	}
	require.Equal(t, 0, runHook(ctx2, f, "beta", testAddr, f, &out1, &err1, nil, "again", nil))
	lines2 := strings.Split(strings.TrimSuffix(secondWho, "\n"), "\n")
	fields2 := strings.Split(lines2[2], "\t")
	require.Len(t, fields2, 3)
	assert.Equal(t, []string{"beta", "2s"}, fields2[:2])
	assert.Regexp(t, "^[0-9a-f]{32}$", fields2[2])
	assert.NotEqual(t, fields1[2], fields2[2], "a restarted hook gets a new session id")
}

func TestWhoWithNobody(t *testing.T) {
	t.Parallel()
	code, out, _ := call(t, newFake(), testEnv, "", "who")
	assert.Equal(t, 0, code)
	assert.Equal(t, "WHO OK present=0\n", out)
}

func TestPendingCountsHandedAndUnhandedWithoutConsuming(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "one", "1")
	send(t, f, "two\tx", "2")
	send(t, f, "three", "3")
	_, err := f.Read(context.Background(), "bus:beta", false, time.Second) // one handed, not acked
	require.NoError(t, err)
	hookedIn(t, f, "beta")
	code, out, errOut := call(t, f, testEnv, "", "pending", "--as", "beta")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "PENDING OK waiting=3\n", out)
	code, out, errOut = call(t, f, testEnv, "", "pending", "--as", "beta", "--list")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "PENDING OK waiting=3\n1-0\talpha\tone\n2-0\talpha\ttwo\\tx\n3-0\talpha\tthree\n", out)
	assert.Equal(t, 1, f.delivered["bus:beta"], "pending consumes nothing")
	assert.Equal(t, []string{"1-0"}, f.pending["bus:beta"])
	hookedIn(t, f, "nobody")
	code, out, _ = call(t, f, testEnv, "", "pending", "--as", "nobody")
	assert.Equal(t, 0, code)
	assert.Equal(t, "PENDING OK waiting=0\n", out)
}

// pending is the README's remedy after a hook ends on a failed wake: it
// runs with no hook holding the name; a name no hook has read is refused.
func TestPendingRunsAfterTheHookEnds(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.strict = true
	require.NoError(t, f.Group(context.Background(), "bus:beta"))
	send(t, f, "one", "1")
	code, out, errOut := call(t, f, testEnv, "", "pending", "--as", "beta", "--list")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "PENDING OK waiting=1\n1-0\talpha\tone\n", out)
	code, out, errOut = call(t, f, testEnv, "", "pending", "--as", "betta")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Equal(t, "nova-bus pending: betta is not hooked in, and no hook has ever held it; run: nova-bus who\n", errOut)
}

func TestSendDedupesRecipients(t *testing.T) {
	t.Parallel()
	f := newFake()
	hookedIn(t, f, "a")
	code, out, errOut := call(t, f, testEnv, "", "send", "--as", "a", "--to", "beta, beta,gamma,beta", "s", "b")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "beta 1-0\ngamma 1-0\n", out)
	assert.Len(t, f.entries["bus:beta"], 1)
	assert.Equal(t, "beta,gamma", f.entries["bus:gamma"][0].Fields["to"])
}

func TestRedisOptions(t *testing.T) {
	t.Parallel()
	o := redisOptions("h:6379", "bus", "pw")
	assert.Equal(t, "h:6379", o.Addr)
	assert.Equal(t, "bus", o.Username)
	assert.Equal(t, "pw", o.Password)
	assert.Equal(t, 1, o.PoolSize)
	assert.Equal(t, -1, o.MaxRetries)
	assert.Empty(t, redisOptions("h:6379", "", "pw").Username, "no user: Redis's default user")
}

func TestUnreachableRedisIsSaidPlainly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"send", "--as", "a", "--to", "beta", "s", "b"}, "nova-bus send: SEND FAILED to beta: cannot reach redis at 127.0.0.1:1 (NOVA_BUS_REDIS): connection refused; check that NOVA_BUS_REDIS=127.0.0.1:1 is the fleet's Redis and that it is up\n"},
		{[]string{"who"}, "nova-bus who: cannot reach redis at 127.0.0.1:1 (NOVA_BUS_REDIS): connection refused; check that NOVA_BUS_REDIS=127.0.0.1:1 is the fleet's Redis and that it is up\n"},
		{[]string{"pending", "--as", "beta"}, "nova-bus pending: cannot reach redis at 127.0.0.1:1 (NOVA_BUS_REDIS): connection refused; check that NOVA_BUS_REDIS=127.0.0.1:1 is the fleet's Redis and that it is up\n"},
	} {
		f := newFake()
		f.down = 1
		code, out, errOut := call(t, f, testEnv, "", tc.args...)
		assert.Equal(t, 1, code, tc.args)
		assert.Empty(t, out, tc.args)
		assert.Equal(t, tc.want, errOut, tc.args)
	}
	check := "; check that NOVA_BUS_REDIS=h:1 is the fleet's Redis and that it is up"
	assert.Equal(t, "cannot reach redis at h:1 (NOVA_BUS_REDIS): no such host"+check, describe(errors.New("dial tcp: lookup h: no such host"), "h:1"))
	assert.Equal(t, "cannot reach redis at h:1 (NOVA_BUS_REDIS): timed out"+check, describe(errors.New("dial tcp 10.0.0.1:1: i/o timeout"), "h:1"))
	assert.Equal(t, "cannot reach redis at h:1 (NOVA_BUS_REDIS): connection lost"+check, describe(errors.New("read tcp 1.2.3.4:5->h:1: read: connection reset by peer"), "h:1"))
	// Redis's own words are mapped to the variable to set, never shown raw.
	assert.Equal(t, "redis at h:1 refused the login: check NOVA_BUS_USER and NOVA_BUS_PASSWORD", describe(errors.New("WRONGPASS invalid username-password pair or user is disabled."), "h:1"))
	assert.Equal(t, "redis at h:1 wants a login: set NOVA_BUS_USER and NOVA_BUS_PASSWORD", describe(errors.New("NOAUTH Authentication required."), "h:1"))
	assert.Equal(t, "redis at h:1 wants a login: set NOVA_BUS_USER and NOVA_BUS_PASSWORD", describe(errors.New("ERR Protocol error: unauthenticated multibulk length"), "h:1"))
	assert.Equal(t, "h:1 is not talking Redis protocol: check that NOVA_BUS_REDIS=h:1 is the fleet's Redis", describe(errors.New("ERR Protocol error: expected '$', got 'H'"), "h:1"))
	assert.Equal(t, "h:1 is not talking Redis protocol: check that NOVA_BUS_REDIS=h:1 is the fleet's Redis", describe(errors.New("redis: can't parse map reply: \"SSH-2.0-OpenSSH_9.9\""), "h:1"), "an SSH port, say")
	assert.Equal(t, "the login lacks permission on this stream at h:1: ask whoever runs Redis for the bus ACL (see the README)", describe(errors.New("NOPERM User lim has no permissions to run the 'xadd' command"), "h:1"))
	assert.Equal(t, "redis at h:1 (NOVA_BUS_REDIS) answered with an error; redis: something else", describe(errors.New("something else"), "h:1"), "the fallback names its source")
}

// Redis down for 70 s: the hook says so once, again after a minute, and
// announces HOOK OK once when it is back.
func TestReconnectIsLoggedOnceAMinute(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.down = 70
	ctx, cancel := context.WithCancel(context.Background())
	f.idle = cancel
	var out, errOut bytes.Buffer
	code := runHook(ctx, f, "beta", testAddr, f, &out, &errOut, nil, "again", nil)
	assert.Equal(t, 0, code)
	line := "nova-bus hook: cannot reach redis at 127.0.0.1:1 (NOVA_BUS_REDIS): connection refused; check that NOVA_BUS_REDIS=127.0.0.1:1 is the fleet's Redis and that it is up; reconnecting\n"
	assert.Equal(t, line+line+"HOOK OK name=beta waiting=0\nnext: NOVA_BUS_REDIS=127.0.0.1:1 nova-bus send --as beta --to <name> <subject> <body>\n", errOut.String())
	assert.Len(t, f.afters, 70)
	assert.Empty(t, out.String(), "the first claim is HOOK OK, not a reconnect")

	f = newFake()
	send(t, f, "s", "b")
	f.failAck = 1 // the connection drops after the first line is printed
	ctx, cancel = context.WithCancel(context.Background())
	f.idle = cancel
	out.Reset()
	errOut.Reset()
	code = runHook(ctx, f, "beta", testAddr, f, &out, &errOut, nil, "again", nil)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1-0\talpha\t-\ts\tb\nHOOK RECONNECTED name=beta waiting=1\n1-0\talpha\t-\ts\tb\n", out.String(), "the Monitor sees the hook is back, and the unacked line again")
	assert.Equal(t, 1, strings.Count(errOut.String(), "HOOK OK"), "HOOK OK once")
}

// An entry send did not write (no from or subject) is not mail: the hook
// says so on stdout and acks it.
func TestMalformedEntryIsSaidAndAckedNotDelivered(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.entries["bus:beta"] = append(f.entries["bus:beta"], Msg{ID: "1-0", Fields: map[string]string{"body": "x"}}, Msg{ID: "2-0", Fields: map[string]string{"from": "a", "body": "y"}})
	send(t, f, "real", "z")
	out, _ := runHookOnce(t, f, "beta")
	assert.Equal(t, "HOOK BAD id=1-0 missing=from,subject\nHOOK BAD id=2-0 missing=subject\n3-0\talpha\t-\treal\tz\n", out)
	assert.Empty(t, f.pending["bus:beta"], "acked away")
	assert.Equal(t, []string{"print HOOK BAD id=1-0 missing=from,subject\n", "ack 1-0", "print HOOK BAD id=2-0 missing=subject\n", "ack 2-0", "print 3-0", "ack 3-0"}, f.log)
}

// refusing answers every Claim with err; after stop Claims it ends the
// context, so a hook that retries anyway comes to an end in the test.
type refusing struct {
	*fake
	err      error
	stop     func()
	tries    int
	released int
}

func (r *refusing) Claim(context.Context, string, string, time.Duration) (string, error) {
	if r.tries++; r.tries >= 3 {
		r.stop()
	}
	return "", r.err
}

func (r *refusing) Release(context.Context, string, string) error {
	r.released++
	return r.err
}

// A login Redis refuses, a missing permission, or an address that is not
// Redis is never cleared by reconnecting: the hook refuses at once, exit 1,
// with the remedy, and says nothing about presence it never held.
func TestHookRefusesAPermanentRedisError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ err, want string }{
		{"WRONGPASS invalid username-password pair or user is disabled.", "redis at 127.0.0.1:1 refused the login: check NOVA_BUS_USER and NOVA_BUS_PASSWORD"},
		{"NOAUTH Authentication required.", "redis at 127.0.0.1:1 wants a login: set NOVA_BUS_USER and NOVA_BUS_PASSWORD"},
		{"NOPERM User lim has no permissions to run the 'set' command", "the login lacks permission on this stream at 127.0.0.1:1: ask whoever runs Redis for the bus ACL (see the README)"},
		{"ERR Protocol error: expected '$', got 'H'", "127.0.0.1:1 is not talking Redis protocol: check that NOVA_BUS_REDIS=127.0.0.1:1 is the fleet's Redis"},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		f := newFake()
		r := &refusing{fake: f, err: errors.New(tc.err), stop: cancel}
		var out, errOut bytes.Buffer
		code := runHook(ctx, r, "beta", testAddr, f, &out, &errOut, nil, "nova-bus hook --as beta", nil)
		cancel()
		assert.Equal(t, 1, code, tc.err)
		assert.Empty(t, out.String(), tc.err)
		assert.Equal(t, "nova-bus hook: "+tc.want+"; once that is fixed, run: nova-bus hook --as beta\n", errOut.String(), tc.err)
		assert.Equal(t, 1, r.tries, "never retried: %s", tc.err)
		assert.Zero(t, r.released, "nothing was claimed: %s", tc.err)
	}
}
