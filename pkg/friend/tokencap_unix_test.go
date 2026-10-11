//go:build unix

package friend

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tokenCapChild is set when this test binary is started again as a lane's fake harness
// (TestTokenCapFakeHarness): it then reports usage and runs, and never the tests.
const tokenCapChild = "NOVA_FRIEND_TOKENCAP_CHILD"

// TestTokenCapFakeHarness is the fake harness a capped lane runs, this test binary
// started again with tokenCapChild set: in mode claude it prints TOKENCAP_MSGS
// stream-json assistant messages, each two lines (one per content block) with the
// message's usage so far, 999000 input tokens and 1000 output each, so a cap of a
// million tokens a message is reached only at the last line; then with
// TOKENCAP_DONE set it writes TOKENCAP_OUTBOX's REPORT.md and RESULT.md and exits, else
// it runs until it is stopped. In mode run (an opencode turn) it does the same with no
// output. Run as a test, it does nothing.
func TestTokenCapFakeHarness(t *testing.T) {
	t.Parallel()
	if os.Getenv(tokenCapChild) == "" {
		return
	}
	if os.Getenv("TOKENCAP_MODE") == "claude" {
		n, _ := strconv.Atoi(os.Getenv("TOKENCAP_MSGS")) // ignored: the test sets it; none is zero messages
		for i := 1; i <= n; i++ {
			fmt.Printf(`{"type":"assistant","message":{"id":"msg_%d","usage":{"input_tokens":999000,"output_tokens":1}}}`+"\n", i)
			fmt.Printf(`{"type":"assistant","message":{"id":"msg_%d","usage":{"input_tokens":999000,"output_tokens":1000}}}`+"\n", i)
		}
		_ = os.Stdout.Sync() // a pipe is fully buffered; the lane counts the lines before this process blocks
	}
	if os.Getenv("TOKENCAP_DONE") == "" {
		r, w, err := os.Pipe()
		if err == nil {
			_, _ = io.Copy(io.Discard, r) // ignored: it blocks until the lane stops this process
		}
		runtime.KeepAlive(w)
		os.Exit(3)
	}
	out := os.Getenv("TOKENCAP_OUTBOX")
	if os.MkdirAll(out, 0o755) != nil || os.WriteFile(filepath.Join(out, "REPORT.md"), []byte("Verdict: LAND\n"), 0o644) != nil || os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: c1\n"), 0o644) != nil {
		os.Exit(4)
	}
	fmt.Println(`{"type":"result","total_cost_usd":0.5}`)
	os.Exit(0)
}

// fakeHarnessExec runs this test binary as the fake harness through RealExec (the
// lane's own process group, its writes handed to the lane's tail), whatever command the
// lane names; opencode's export is answered by export when set, and started is closed
// when the turn's run begins.
func fakeHarnessExec(t *testing.T, env []string, export func() string, started chan<- struct{}) Exec {
	t.Helper()
	bin, err := os.Executable()
	require.NoError(t, err)
	return func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		if export != nil && len(args) > 0 && args[0] == "export" {
			return export(), 0, nil
		}
		if started != nil {
			close(started)
		}
		argv := append(append([]string{tokenCapChild + "=1"}, env...), bin, "-test.run=^TestTokenCapFakeHarness$")
		return RealExec(ctx, dir, "/usr/bin/env", argv, stdin)
	}
}

// A one-shot lane counts a card's tokens from the harness's own usage record as the
// card runs, input, cache read and write, output and reasoning summed; when the sum
// reaches the friend row's cap (DefaultTokenCap where none is set, 0 none) the lane
// stops its own child and writes the card's REPORT.md as a hold naming `token cap
// <cap> reached at <n> tokens` and the usage so far; under the cap the card finishes as
// it does today. Both the Claude lane (stream-json usage, one count per message id)
// and the OpenCode lane (its session export, polled on an injected clock) are held to it.
func TestOneShotLaneStopsAtTheTokenCapAndHoldsWithTheReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		lane     string // claude or opencode
		row      func() int64
		msgs     int  // claude: messages printed, 999000 input and 1000 output each
		done     bool // the harness finishes the card by itself
		wantHold string
		wantAt   Tokens
	}{
		{name: "claude over the default cap", lane: "claude", msgs: 6, wantHold: "token cap 6000000 reached at 6000000 tokens", wantAt: Tokens{Input: 5994000, Output: 6000}},
		{name: "claude over the row's cap", lane: "claude", row: func() int64 { return 3000000 }, msgs: 3, wantHold: "token cap 3000000 reached at 3000000 tokens", wantAt: Tokens{Input: 2997000, Output: 3000}},
		{name: "claude under the cap", lane: "claude", msgs: 2, done: true},
		{name: "claude with the row's cap 0, none", lane: "claude", row: func() int64 { return 0 }, msgs: 8, done: true},
		{name: "opencode over the default cap", lane: "opencode", wantHold: "token cap 6000000 reached at 6000000 tokens", wantAt: Tokens{Input: 3000000, CacheRead: 1500000, CacheWrite: 600000, Output: 600000, Reasoning: 300000}},
		{name: "opencode under the cap", lane: "opencode", done: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			c := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
			require.NoError(t, os.MkdirAll(filepath.Dir(c.Brief), 0o755))
			require.NoError(t, os.WriteFile(c.Brief, []byte("STATUS: nova-sprint card c1, epoch 15\n"), 0o644))
			env := []string{"TOKENCAP_MODE=" + map[string]string{"claude": "claude", "opencode": "run"}[tc.lane], "TOKENCAP_MSGS=" + strconv.Itoa(tc.msgs), "TOKENCAP_OUTBOX=" + c.Outbox}
			if tc.done {
				env = append(env, "TOKENCAP_DONE=1")
			}
			var out strings.Builder
			ctx := context.Background()
			var lt LaneTurn
			var err error
			switch tc.lane {
			case "claude":
				cl := &Claude{Stub: Stub{Harness: "claude"}, Friend: "bob", Dir: dir, Run: fakeHarnessExec(t, env, nil, nil), Out: &out, ConfigDir: func() string { return "/accounts/heavy-a" }, TokenCap: tc.row}
				lt, err = cl.RunCard(ctx, c)
			case "opencode":
				// the session holds 5000000 input tokens of earlier cards, and each export it grew
				// by 2000000 tokens: the turn's read before it is the base, and each tick of the
				// injected clock one read
				var reads atomic.Int64
				export := func() string {
					n := reads.Add(1)
					per := map[bool]int64{true: 100, false: 1000000}[tc.done]
					return fmt.Sprintf(`{"messages":[{"info":{"role":"user"}},{"info":{"role":"assistant","cost":0.01,"tokens":{"input":%d,"output":%d,"reasoning":%d,"cache":{"read":%d,"write":%d}}}}]}`,
						5000000+n*per, n*per/5, n*per/10, n*per/2, n*per/5)
				}
				ticks := make(chan time.Time)
				started, stop := make(chan struct{}), make(chan struct{})
				p := &OpenCodePriced{OpenCode: &OpenCode{Dir: dir, Run: fakeHarnessExec(t, env, export, started), Out: &out}, Friend: "bob", TokenCap: tc.row,
					Tick: func(d time.Duration) (<-chan time.Time, func()) {
						assert.Equal(t, TokenPoll, d)
						return ticks, func() {}
					}}
				go func() {
					<-started // the clock ticks once the turn's harness runs
					for at := time.Unix(0, 0); ; at = at.Add(TokenPoll) {
						select {
						case ticks <- at:
						case <-stop:
							return
						}
					}
				}()
				lt, err = p.DeliverTo(ctx, "ses_1", CardText(LaneJob{Card: c}, 1, 1, "nova-bus send ...", "", "", "", nil))
				close(stop)
			}
			require.NoError(t, ctx.Err(), "the lane's own context is cancelled, never its caller's")
			report, rerr := os.ReadFile(c.Report())
			require.NoError(t, rerr, "the card's REPORT.md is written either way")
			if tc.wantHold == "" {
				assert.NoError(t, err)
				assert.Equal(t, 0, lt.Exit)
				assert.Equal(t, "Verdict: LAND\n", string(report), "under the cap the card finishes as the harness wrote it")
				assert.NotContains(t, out.String(), "token cap")
				return
			}
			var capped TokenCapped
			require.ErrorAs(t, err, &capped)
			assert.EqualError(t, err, tc.wantHold)
			assert.Equal(t, tc.wantAt, capped.At, "the usage at the stop, each count from the harness's own record")
			assert.True(t, capped.Wrote)
			assert.NotEqual(t, 0, lt.Exit, "the child was stopped, not finished")
			assert.True(t, strings.HasPrefix(string(report), "Verdict: HOLD\n"), "the report: %s", report)
			assert.Contains(t, string(report), "stopped card c1: "+tc.wantHold+"; the usage so far: "+tc.wantAt.String())
			assert.NoFileExists(t, c.Result(), "the lane claims no result")
			assert.Contains(t, out.String(), tc.wantHold+"; its run is stopped, usage "+tc.wantAt.String()+"; REPORT.md written as HOLD", "the record says the stop")
		})
	}
}

// The row's cap rides her beat's answer as row_token_cap=<n>; anything else is no cap read.
func TestTheTokenCapIsReadOffTheRow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		answer string
		want   int64
		ok     bool
	}{
		{answer: "BEAT OK row_mode=one-shot row_width=2 row_token_cap=3000000", want: 3000000, ok: true},
		{answer: "BEAT OK row_token_cap=0", want: 0, ok: true},
		{answer: "BEAT OK row_mode=one-shot"},
		{answer: "BEAT OK row_token_cap=-1"},
		{answer: "BEAT OK row_token_cap=6M"},
	} {
		t.Run(tc.answer, func(t *testing.T) {
			t.Parallel()
			got, ok := TokenCapOf(tc.answer)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
	assert.Equal(t, int64(6000000), tokenCap(nil), "a row that names none is capped at the default")
}

// An opencode export whose messages carry no tokens is no count: the turn runs uncapped
// and its price stands as it was.
func TestAnExportWithNoTokenShapeLeavesThePriceAlone(t *testing.T) {
	t.Parallel()
	export := `{"messages":[{"info":{"role":"assistant","cost":0.25}}]}`
	_, err := SessionTokens(export)
	assert.ErrorIs(t, err, errNoTokenShape)
	cost, err := SessionCost(export)
	require.NoError(t, err)
	assert.Equal(t, 0.25, cost)
}
