//go:build unix

package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tokenCapFake names the fake harness this test binary runs as: claude or
// opencode. It runs from init, before the test flags are parsed, since a
// harness's arguments are not the test binary's.
const tokenCapFake = "NOVA_FRIEND_TOKEN_CAP_FAKE"

func init() {
	if h := os.Getenv(tokenCapFake); h != "" {
		os.Exit(fakeTokenHarness(h, os.Args[1:]))
	}
}

// fakeTokenHarness reports growing usage, the way its harness records it:
// FAKE_STEPS messages of FAKE_EACH input tokens each (as claude -p's
// stream-json assistant events, each printed twice as a message's blocks
// are, or as rows of the opencode session record in FAKE_DIR/usage, which
// its export reads). Then it writes FAKE_OUTBOX's REPORT.md and RESULT.md and
// exits when FAKE_FINISH is set, and otherwise works on until it is stopped,
// and marks FAKE_DIR/stopped.
func fakeTokenHarness(harness string, args []string) int {
	steps, _ := strconv.Atoi(os.Getenv("FAKE_STEPS"))
	each, _ := strconv.ParseInt(os.Getenv("FAKE_EACH"), 10, 64)
	usage := filepath.Join(os.Getenv("FAKE_DIR"), "usage")
	if harness == "opencode" && len(args) > 0 && args[0] == "export" {
		raw, _ := os.ReadFile(usage) // ignored: no file is a session with no assistant message yet
		var msgs []string
		for _, n := range strings.Fields(string(raw)) {
			msgs = append(msgs, `{"info":{"role":"assistant","cost":0,"tokens":{"input":`+n+`,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}},"parts":[]}`)
		}
		fmt.Printf("Exporting session: %s\n{\"info\":{\"id\":%q},\"messages\":[%s]}\n", args[1], args[1], strings.Join(msgs, ","))
		return 0
	}
	for i := 1; i <= steps; i++ {
		switch harness {
		case "claude":
			line := fmt.Sprintf(`{"type":"assistant","message":{"id":"msg_%d","usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`, i, each)
			fmt.Println(line)
			fmt.Println(line)
		case "opencode":
			raw, _ := os.ReadFile(usage) // ignored: no file is a session with no assistant message yet
			tmp := usage + ".tmp"
			if os.WriteFile(tmp, append(raw, []byte(fmt.Sprintf("%d\n", each))...), 0o644) != nil || os.Rename(tmp, usage) != nil {
				return 3
			}
		}
	}
	if os.Getenv("FAKE_FINISH") == "" {
		// working on until the lane stops it, then the stop is marked for the test
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		<-stop
		if os.WriteFile(filepath.Join(os.Getenv("FAKE_DIR"), "stopped"), nil, 0o644) != nil {
			return 3
		}
		return 143
	}
	if outbox := os.Getenv("FAKE_OUTBOX"); outbox != "" {
		if os.MkdirAll(outbox, 0o755) != nil || os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: LAND\nHead: "+strings.Repeat("a", 40)+"\n"), 0o644) != nil ||
			os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("RESULT: c1\n"), 0o644) != nil {
			return 3
		}
	}
	fmt.Println(`{"type":"result","total_cost_usd":0.01}`)
	return 0
}

// tickingClock is an injected clock that ticks whenever the guard waits on
// it, until the test is done: no test waits on the wall clock.
func tickingClock(t *testing.T) func(time.Duration) <-chan time.Time {
	ticks, done := make(chan time.Time), make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		at := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
		for {
			at = at.Add(TokenCapEvery)
			select {
			case ticks <- at:
			case <-done:
				return
			}
		}
	}()
	return func(time.Duration) <-chan time.Time { return ticks }
}

// A one-shot lane counts a card's tokens as it runs, from its harness's own
// usage record (claude -p's stream-json usage events; opencode's session
// record, what the session gained during the card's turn), summed: when the
// sum reaches the friend row's cap (6000000 unless the row says; 0 is none)
// the lane stops its own child, and the card's REPORT.md is a HOLD with the
// reason `token cap <cap> reached at <n> tokens` and the usage so far, so the
// lane's end finishes the card and the lane takes the next. A card under the
// cap runs to its end untouched (docs/SPEC-FRIEND.md, friend-token-cap-b.w1).
func TestOneShotLaneStopsAtTheTokenCapAndHoldsWithTheReason(t *testing.T) {
	t.Parallel()
	bin, err := os.Executable()
	require.NoError(t, err)
	none := int64(0)
	for _, tc := range []struct {
		name, harness string
		cap           *int64 // the row's token_cap; nil: the row says none
		before        int64  // the session's tokens before the card's turn (earlier cards)
		steps         int
		each          int64
		finish        bool
		want          string // the HOLD's reason; empty: the run ends untouched
	}{
		{name: "claude over the default cap", harness: "claude", steps: 3, each: 2_500_000, want: "token cap 6000000 reached at 7500000 tokens"},
		{name: "claude under the cap", harness: "claude", steps: 2, each: 1_000, finish: true},
		{name: "claude with no cap", harness: "claude", cap: &none, steps: 3, each: 2_500_000, finish: true},
		{name: "opencode over the default cap", harness: "opencode", before: 5_000_000, steps: 3, each: 2_500_000, want: "token cap 6000000 reached at 7500000 tokens"},
		{name: "opencode under the cap after earlier cards", harness: "opencode", before: 5_999_000, steps: 2, each: 1_000, finish: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			c := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
			require.NoError(t, os.MkdirAll(filepath.Dir(c.Brief), 0o755))
			require.NoError(t, os.WriteFile(c.Brief, []byte("STATUS: nova-sprint card c1, epoch 15\n"), 0o644))
			if tc.before > 0 {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "usage"), []byte(strconv.FormatInt(tc.before, 10)+"\n"), 0o644))
			}
			env := []string{tokenCapFake + "=" + tc.harness, "FAKE_DIR=" + dir, "FAKE_OUTBOX=" + c.Outbox, "FAKE_STEPS=" + strconv.Itoa(tc.steps), "FAKE_EACH=" + strconv.FormatInt(tc.each, 10)}
			if tc.finish {
				env = append(env, "FAKE_FINISH=1")
			}
			run := func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
				return RealExec(ctx, dir, "/usr/bin/env", append(append(append([]string{}, env...), name), args...), stdin)
			}
			guard := TokenGuard{After: tickingClock(t)}
			if tc.cap != nil {
				guard.Cap = func() int64 { return *tc.cap }
			}
			var out strings.Builder
			var lt LaneTurn
			var err error
			if tc.harness == "claude" {
				cl := &Claude{Stub: Stub{Harness: "claude"}, Friend: "bob", Dir: dir, Run: run, Program: bin, Out: &out, ConfigDir: func() string { return "/accounts/a" }, Guard: guard}
				lt, err = cl.RunCard(t.Context(), c)
			} else {
				p := &OpenCodePriced{OpenCode: &OpenCode{Dir: dir, Run: run, Program: bin, Out: &out}, Guard: guard}
				lt, err = p.DeliverTo(t.Context(), "ses_a", CardText(c, 1, 1, "nova-bus send", "", "", nil))
			}
			report, rerr := os.ReadFile(c.Report())
			require.NoError(t, rerr, "the card's REPORT.md is written either way")
			if tc.want == "" {
				require.NoError(t, err)
				assert.Equal(t, 0, lt.Exit)
				assert.True(t, strings.HasPrefix(string(report), "Verdict: LAND\n"), "the run's own report stands: %s", report)
				assert.NotContains(t, out.String(), "stopped")
				return
			}
			var capped TokenCapped
			require.ErrorAs(t, err, &capped)
			assert.EqualError(t, capped, tc.want)
			assert.Equal(t, Tokens{Input: tc.each * int64(tc.steps)}, capped.Tokens, "the card's own tokens: what the session gained in its turn")
			lines := strings.Split(string(report), "\n")
			require.GreaterOrEqual(t, len(lines), 4)
			assert.Equal(t, "Verdict: HOLD", lines[0])
			assert.Equal(t, "Head: none", lines[1])
			assert.True(t, strings.HasPrefix(lines[3], tc.want+": "), "the reason leads the paragraph: %s", lines[3])
			assert.Contains(t, lines[3], "usage so far: input=7500000 cached_input=0 output=0 reasoning=0 total=7500000")
			assert.NoFileExists(t, c.Result(), "the run was stopped before it wrote its result")
			assert.FileExists(t, filepath.Join(dir, "stopped"), "the lane stopped its own child")
			assert.Contains(t, out.String(), "stopped: "+tc.want)
		})
	}
}

// The cap is a friend-row setting read off the beat's answer, and a lane
// turn's text names the outbox its HOLD goes to; a report the run wrote is
// never overwritten by one.
func TestTheTokenCapIsReadFromTheRowAndTheHoldNeverOverwritesAReport(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		answer string
		n      int64
		ok     bool
	}{
		{"BEAT OK row_mode=one-shot row_width=4 row_token_cap=2500000", 2_500_000, true},
		{"BEAT OK row_token_cap=0", 0, true},
		{"BEAT OK row_token_cap=lots", 0, false},
		{"BEAT OK row_mode=one-shot", 0, false},
	} {
		t.Run(tc.answer, func(t *testing.T) {
			t.Parallel()
			n, ok := ParseTokenCap(tc.answer)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.n, n)
		})
	}
	t.Run("the outbox a turn names", func(t *testing.T) {
		t.Parallel()
		c := Card{ID: "c1", Brief: "/w/inbox/c1~15/BRIEF.md", Outbox: "/w/outbox/c1~15"}
		assert.Equal(t, c.Outbox, cardOutbox(CardText(c, 2, 4, "nova-bus send", "", "", nil)))
		assert.Empty(t, cardOutbox("You are bob."))
	})
	t.Run("a written report stands", func(t *testing.T) {
		t.Parallel()
		outbox := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: LAND\n"), 0o644))
		require.NoError(t, HoldAtCap(outbox, "claude -p", TokenCapped{Cap: 10, Tokens: Tokens{Input: 12}}))
		got, err := os.ReadFile(filepath.Join(outbox, "REPORT.md"))
		require.NoError(t, err)
		assert.Equal(t, "Verdict: LAND\n", string(got))
	})
	t.Run("claude stream-json usage", func(t *testing.T) {
		t.Parallel()
		var s streamUsage
		line := func(id string, in, cw, cr, out int) string {
			b, err := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"id": id, "usage": map[string]int{"input_tokens": in, "cache_creation_input_tokens": cw, "cache_read_input_tokens": cr, "output_tokens": out}}})
			require.NoError(t, err)
			return string(b) + "\n"
		}
		all := line("m1", 10, 100, 1000, 1) + line("m1", 10, 100, 1000, 5) + `{"type":"result","usage":{"input_tokens":99999}}` + "\n" + line("m2", 20, 0, 2000, 7)
		s.Write([]byte(all[:17])) // a write ends mid-line
		s.Write([]byte(all[17:]))
		got, err := s.Tokens(t.Context())
		require.NoError(t, err)
		assert.Equal(t, Tokens{Input: 30, CachedInput: 3100, Output: 12}, got, "each message once, at its last usage; the result's total is not added again")
	})
}
