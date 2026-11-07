package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recv and ack are the bridge for a receiver that cannot run the hook (a
// Codex child agent): recv prints the oldest unhandled message and leaves it
// pending, ack marks it handled. Their tests run on the in-memory store.

func TestRecvPrintsTheOldestUnhandledAndAckMarksIt(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "s1", "b1")
	send(t, f, "s2", "b2\nmore")
	code, out, errOut := call(t, f, testEnv, "", "recv", "--as", "beta")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "1-0\talpha\t-\ts1\tb1\n", out)
	assert.Empty(t, errOut)
	assert.Equal(t, []string{"1-0"}, f.pending["bus:beta"], "handed, not handled")
	assert.Equal(t, "2026-11-05T12:00:00Z\trecv\t1-0", f.presence["beta"], "recv records presence, holding the message it handed out")
	assert.Equal(t, f.clock.Add(checkoutTTL), f.deadline["beta"], "for as long as a relay may take")

	code, out, _ = call(t, f, testEnv, "", "recv", "--as", "beta")
	require.Equal(t, 0, code)
	assert.Equal(t, "1-0\talpha\t-\ts1\tb1\n", out, "unacked, it comes again")

	code, out, errOut = call(t, f, testEnv, "", "ack", "--as", "beta", "--id", "1-0")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "ACK OK id=1-0\n", out)
	assert.Empty(t, f.pending["bus:beta"])
	assert.Equal(t, "2026-11-05T12:00:00Z\trecv", f.presence["beta"], "the ack ends the checkout")
	assert.Equal(t, f.clock.Add(presenceTTL), f.deadline["beta"])

	code, _, errOut = call(t, f, testEnv, "", "ack", "--as", "beta", "--id", "1-0")
	assert.Equal(t, 1, code)
	assert.Equal(t, "nova-bus ack: ACK FAILED id=1-0 reason=not received by recv, or handled already; run: nova-bus pending --as beta --list\n", errOut)

	code, out, _ = call(t, f, testEnv, "", "recv", "--as", "beta")
	require.Equal(t, 0, code)
	assert.Equal(t, "2-0\talpha\t-\ts2\tb2\\nmore\n", out)
	code, out, _ = call(t, f, testEnv, "", "ack", "--as", "beta", "--id", "2-0")
	require.Equal(t, 0, code)
	assert.Equal(t, "ACK OK id=2-0\n", out)
	assert.Empty(t, f.pending["bus:beta"])
}

func TestRecvTimesOutWithNothing(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.quiet = 100
	var out, errOut bytes.Buffer
	code := recv(context.Background(), f, "beta", testAddr, 3*time.Second, f, &out, &errOut)
	assert.Equal(t, 1, code)
	assert.Empty(t, out.String())
	assert.Equal(t, "RECV NONE name=beta waited=3s\n", errOut.String())
	assert.Equal(t, []time.Duration{time.Second, time.Second, time.Second}, f.blocks, "each read waits at most readBlock, to the timeout")
}

func TestRecvWaitsForANewMessage(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.quiet = 2
	f.onWrite = nil
	go func() {}()
	// Two quiet reads pass, then a message is there.
	sent := false
	f.step = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.idle = func() {
		if !sent {
			sent = true
			send(t, f, "late", "b")
		} else {
			cancel()
		}
	}
	var out, errOut bytes.Buffer
	code := recv(ctx, f, "beta", testAddr, 0, f, &out, &errOut)
	assert.Equal(t, 0, code, errOut.String())
	assert.Equal(t, "1-0\talpha\t-\tlate\tb\n", out.String())
}

func TestRecvKeepsItsFirstPresenceAndLeavesAHooksAlone(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "s", "b")
	f.presence["beta"], f.deadline["beta"] = "2026-11-05T11:00:00Z\trecv", f.clock.Add(presenceTTL)
	code, _, _ := call(t, f, testEnv, "", "recv", "--as", "beta")
	require.Equal(t, 0, code)
	assert.Equal(t, "2026-11-05T11:00:00Z\trecv\t1-0", f.presence["beta"], "uptime since the first recv, holding the message")
	assert.Equal(t, f.clock.Add(checkoutTTL), f.deadline["beta"], "set again")

	f.presence["beta"], f.deadline["beta"] = testValue, f.clock.Add(presenceTTL)
	code, out, errOut := call(t, f, testEnv, "", "recv", "--as", "beta")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Equal(t, "nova-bus recv: name hooked: a hook holds beta and would take the same messages; run: nova-bus who\n", errOut)
	assert.Equal(t, testValue, f.presence["beta"], "a hook's key is not taken")
	assert.Equal(t, []string{"1-0"}, f.pending["bus:beta"], "and its message was not read")
}

// A recv that waits keeps the name present: it claims again every 10 s.
func TestRecvRefreshesPresenceWhileItWaits(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.quiet = 100
	var out, errOut bytes.Buffer
	code := recv(context.Background(), f, "beta", testAddr, 35*time.Second, f, &out, &errOut)
	assert.Equal(t, 1, code)
	assert.Equal(t, "RECV NONE name=beta waited=35s\n", errOut.String())
	assert.Len(t, f.claims, 4, "at the start and every 10 s")
	assert.True(t, f.live("beta"), "present to the end")
}

func TestRecvAndAckRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"recv"}, "nova-bus recv: missing --as <name>; run: nova-bus recv -h\n"},
		{[]string{"recv", "--as", "beta", "--timeout", "-1s"}, "nova-bus recv: --timeout -1s is negative; run: nova-bus recv -h\n"},
		{[]string{"recv", "--as", "beta", "x"}, "nova-bus recv: recv takes no arguments; run: nova-bus recv -h\n"},
		{[]string{"ack", "--as", "beta"}, "nova-bus ack: missing --id <id>; run: nova-bus ack -h\n"},
		{[]string{"ack", "--as", "beta", "--id", "x"}, "nova-bus ack: --id \"x\" is not a message id like 1730800000000-0 (as recv prints it); run: nova-bus ack -h\n"},
		{[]string{"ack", "--id", "1-0"}, "nova-bus ack: missing --as <name>; run: nova-bus ack -h\n"},
	} {
		code, out, errOut := call(t, newFake(), testEnv, "", tc.args...)
		assert.Equal(t, 2, code, tc.args)
		assert.Empty(t, out)
		assert.Equal(t, tc.want, errOut, tc.args)
	}
}

// A send to a name no hook has ever read is refused before anything is
// sent (a typo would sit unread forever), unless --new says so.
func TestSendRefusesANameNoHookHasHeld(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.strict = true
	hookedIn(t, f, "alpha")
	code, out, errOut := call(t, f, testEnv, "", "send", "--as", "alpha", "--to", "beta,bob", "s", "b")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Equal(t, "nova-bus send: no hook has ever held beta, so nothing would read it; run: nova-bus who, or send --new to a name about to hook in\n", errOut)
	assert.Empty(t, f.entries, "nothing sent to any name")
	require.NoError(t, f.Group(context.Background(), "bus:beta"))
	code, _, errOut = call(t, f, testEnv, "", "send", "--as", "alpha", "--to", "beta,bob", "s", "b")
	assert.Equal(t, 1, code, errOut)
	assert.Contains(t, errOut, "no hook has ever held bob")
	assert.Empty(t, f.entries, "one unknown name refuses the whole send")
	code, out, _ = call(t, f, testEnv, "", "send", "--as", "alpha", "--to", "beta", "s", "b")
	assert.Equal(t, 0, code)
	assert.Equal(t, "beta 1-0\n", out)
	code, out, _ = call(t, f, testEnv, "", "send", "--as", "alpha", "--to", "bob", "--new", "s", "b")
	assert.Equal(t, 0, code)
	assert.Equal(t, "bob 1-0\n", out)
}

// A hook whose stdout reader is gone (the Monitor, the session) ends within
// a read, exit 1, and frees the name.
func TestHookEndsWhenStdoutsReaderIsGone(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.quiet, f.gone = 10, 2
	var out, errOut bytes.Buffer
	code := runHook(context.Background(), f, "beta", testAddr, f, &out, &errOut, nil, "nova-bus hook --as beta", f.outGone)
	assert.Equal(t, 1, code)
	assert.Equal(t, "HOOK OK name=beta waiting=0\nnext: NOVA_BUS_REDIS=127.0.0.1:1 nova-bus send --as beta --to <name> <subject> <body>\nnova-bus hook: writing stdout: stdout closed: its reader is gone\n", errOut.String())
	assert.Equal(t, 2, f.reads, "seen on the read after the reader went")
	assert.Empty(t, f.presence, "the name is freed")
}

// A verb that acts as a name runs only while that name is hooked in; the
// refusal carries the hook-in recipe of the harness in the environment.
func TestVerbsRefuseUntilHookedIn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		environ []string
		want    string
	}{
		{nil, "run `NOVA_BUS_REDIS=127.0.0.1:1 nova-bus hook --as alpha` as the Monitor tool's command; run: nova-bus show agents"},
		{[]string{"HOME=/h", "TERM_PROGRAM=Muse"}, "run `NOVA_BUS_REDIS=127.0.0.1:1 nova-bus hook --as alpha` as the Monitor tool's command; run: nova-bus show agents"},
		{[]string{"CODEX_THREAD_ID=t1", "ANTIGRAVITY_CSRF_TOKEN=1"}, "`NOVA_BUS_REDIS=127.0.0.1:1 nova-bus hook --as alpha --codex-thread $CODEX_THREAD_ID --service`; run: nova-bus show codex-agents"},
		{[]string{"GROK_HOME=/Users/me/.grok"}, "Monitor: tail -n 0 -F $GROK_HOME/nova-hook.wake, then `NOVA_BUS_REDIS=127.0.0.1:1 nova-bus hook --as alpha --harness grok --grok-home $GROK_HOME --service`; run: nova-bus hook -h"},
		{[]string{"GROK_HOME=/x", "ANTIGRAVITY_AGENT_PORT=1"}, "`NOVA_BUS_REDIS=127.0.0.1:1 nova-bus hook --as alpha --harness antigravity --antigravity-dir <workspace> --antigravity-session <conversation id> --antigravity-events-dir ~/.gemini/antigravity/nova-bus-alpha --service`; run: nova-bus hook -h"},
		{[]string{"GEMINI_API_KEY=k", "CLAUDECODE=1", "ANTIGRAVITY_AGENT_PORT=1"}, "run `NOVA_BUS_REDIS=127.0.0.1:1 nova-bus hook --as alpha` as the Monitor tool's command; run: nova-bus show agents"},
		{[]string{"CLAUDECODE=1", "CODEX_THREAD_ID=t1"}, "`NOVA_BUS_REDIS=127.0.0.1:1 nova-bus hook --as alpha --codex-thread $CODEX_THREAD_ID --service`; run: nova-bus show codex-agents"},
		{[]string{"GROK_HOME=/g", "CLAUDECODE=1"}, "run `NOVA_BUS_REDIS=127.0.0.1:1 nova-bus hook --as alpha` as the Monitor tool's command; run: nova-bus show agents"},
		{[]string{"GEMINI_API_KEY=k"}, "run `NOVA_BUS_REDIS=127.0.0.1:1 nova-bus hook --as alpha` as the Monitor tool's command; run: nova-bus show agents"},
		{[]string{"GEMINI_CLI_CSRF_TOKEN=1"}, "`NOVA_BUS_REDIS=127.0.0.1:1 nova-bus hook --as alpha --harness antigravity --antigravity-dir <workspace> --antigravity-session <conversation id> --antigravity-events-dir ~/.gemini/antigravity/nova-bus-alpha --service`; run: nova-bus hook -h"},
	} {
		s := sys{environ: func() []string { return tc.environ }}
		for _, args := range [][]string{{"send", "--as", "alpha", "--to", "beta", "--new", "s", "b"}, {"ack", "--as", "alpha", "--id", "1-0"}, {"pending", "--as", "alpha"}} {
			code, out, errOut := callSys(t, s, testEnv, args...)
			assert.Equal(t, 1, code, "%v %v", tc.environ, args)
			assert.Empty(t, out)
			assert.Equal(t, "nova-bus "+args[0]+": alpha is not hooked in, so nothing would read an answer; hook in first: "+tc.want+"\n", errOut, "%v %v", tc.environ, args)
		}
	}
	f := newFake()
	hookedIn(t, f, "beta")
	for _, args := range [][]string{{"who"}, {"show", "agents"}, {"pending", "--as", "beta"}} {
		code, _, errOut := call(t, f, testEnv, "", args...)
		assert.Equal(t, 0, code, "%v: %s", args, errOut)
	}
	// A Codex child mid-loop: recv claimed the name, so its ack and send run.
	send(t, f, "s", "b")
	delete(f.presence, "beta") // the planted hook is gone; recv claims the name itself
	code, out, _ := call(t, f, testEnv, "", "recv", "--as", "beta")
	require.Equal(t, 0, code)
	assert.Equal(t, "1-0\talpha\t-\ts\tb\n", out)
	assert.Equal(t, "2026-11-05T12:00:00Z\trecv\t1-0", f.presence["beta"], "held, naming the message out")
	code, out, errOut := call(t, f, testEnv, "", "send", "--as", "beta", "--to", "alpha", "--re", "1-0", "re", "ok")
	assert.Equal(t, 0, code, errOut)
	assert.Equal(t, "alpha 1-0\n", out)
	code, out, errOut = call(t, f, testEnv, "", "ack", "--as", "beta", "--id", "1-0")
	assert.Equal(t, 0, code, errOut)
	assert.Equal(t, "ACK OK id=1-0\n", out)
}

// A recv's checkout outlasts a slow relay: the ack (and the parent's reply)
// pass the hook-in gate at 31 s and at 14 min; a reader that died leaves the
// claim to lapse at 15 min, and the next recv hands the message out again.
func TestRecvCheckoutOutlastsASlowRelay(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		after time.Duration
		acked bool
	}{{31 * time.Second, true}, {14 * time.Minute, true}, {16 * time.Minute, false}} {
		f := newFake()
		send(t, f, "s", "b")
		code, out, _ := call(t, f, testEnv, "", "recv", "--as", "beta")
		require.Equal(t, 0, code)
		assert.Equal(t, "1-0\talpha\t-\ts\tb\n", out)
		_, out, _ = call(t, f, testEnv, "", "who")
		assert.Equal(t, "WHO OK present=2\nalpha\t1h0m0s\nbeta\t0s\trecv holding 1-0\n", out)
		f.clock = f.clock.Add(tc.after)
		code, out, errOut := call(t, f, testEnv, "", "send", "--as", "beta", "--to", "alpha", "--re", "1-0", "re", "ok")
		assert.Equal(t, map[bool]int{true: 0, false: 1}[tc.acked], code, "%s: the reply to the parent: %s", tc.after, errOut)
		code, out, errOut = call(t, f, testEnv, "", "ack", "--as", "beta", "--id", "1-0")
		if tc.acked {
			require.Equal(t, 0, code, "%s: %s", tc.after, errOut)
			assert.Equal(t, "ACK OK id=1-0\n", out)
			assert.Empty(t, f.pending["bus:beta"])
			continue
		}
		assert.Equal(t, 1, code, tc.after)
		assert.Contains(t, errOut, "beta is not hooked in", "the claim lapsed with the reader")
		code, out, _ = call(t, f, testEnv, "", "recv", "--as", "beta")
		require.Equal(t, 0, code)
		assert.Equal(t, "1-0\talpha\t-\ts\tb\n", out, "handed out again")
	}
}

// A hook started while a recv holds the name (waiting, or with a message
// checked out) says so, with the message and the time its claim has left.
func TestHookNamesTheRecvThatHoldsItsName(t *testing.T) {
	t.Parallel()
	f := newFake()
	send(t, f, "s", "b")
	code, _, _ := call(t, f, testEnv, "", "recv", "--as", "beta")
	require.Equal(t, 0, code)
	f.clock = f.clock.Add(5 * time.Minute)
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	f.idle = cancel
	code = runHook(ctx, f, "beta", testAddr, f, &out, &errOut, nil, "again", nil)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut.String(), "nova-bus hook: name already hooked by a recv: beta after 40s; a recv holds message 1-0 checked out, for 9m20s more unless it is acked: stop that reader first, or ack; run: nova-bus who\n")

	f = newFake()
	f.presence["beta"], f.deadline["beta"] = "2026-11-05T12:00:00Z\trecv", f.clock.Add(presenceTTL)
	f.onWrite = nil
	errOut.Reset()
	// the waiting recv keeps claiming: plant it far ahead so the hook gives up
	f.deadline["beta"] = f.clock.Add(time.Hour)
	ctx, cancel = context.WithCancel(context.Background())
	f.idle = cancel
	code = runHook(ctx, f, "beta", testAddr, f, &out, &errOut, nil, "again", nil)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut.String(), "a recv is waiting on it and claims it every 10 s: stop that reader first, or ack; run: nova-bus who\n")
}
