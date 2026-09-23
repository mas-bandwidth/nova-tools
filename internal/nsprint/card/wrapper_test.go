package card_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/redis/go-redis/v9"
)

// The fake harness is this test binary, re-executed. TestMain turns it into
// the harness when fakeHarnessEnv is set; the wrapper strips NOVA_CARD_* and
// anything naming a token from the harness's environment, so the switch uses
// its own names.
const (
	fakeHarnessEnv = "WRAPPER_FAKE_HARNESS" // done, fail or hang
	fakeGateEnv    = "WRAPPER_FAKE_GATE"    // done and fail exit once this file exists
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeHarnessEnv); mode != "" {
		os.Exit(fakeHarness(mode))
	}
	os.Exit(m.Run())
}

// fakeHarness prints its environment (so a test can prove no token reached
// it), writes a RESULT line into its out dir, and then waits for its gate:
// done exits 0, fail exits 3, hang never exits on its own.
func fakeHarness(mode string) int {
	for _, kv := range os.Environ() {
		fmt.Println(kv)
	}
	out := os.Getenv("NOVA_CARD_OUT")
	if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: "+os.Getenv("NOVA_CARD")+" sha=000000000000\n"), 0o644); err != nil {
		fmt.Println("fake harness:", err)
		return 9
	}
	gate := os.Getenv(fakeGateEnv)
	for {
		if mode != "hang" {
			if _, err := os.Stat(gate); err == nil {
				break
			}
		}
		// Waits for the test's gate file; the test's bound is the assertion.
		time.Sleep(5 * time.Millisecond)
	}
	if mode == "fail" {
		return 3
	}
	return 0
}

// TestWrapperOwnsOneCardEndToEnd is the DONE-WHEN control of #3059. Against
// the real ns_card_* functions: a harness that exits DONE, one that exits
// non-zero and one that hangs past the clock each produce exactly one
// launched record, beats at the configured interval while running, one end
// record of the right class, and no job directory left behind; a wrapper
// started for a card not dealt to this bench exits 4 and writes nothing.
func TestWrapperOwnsOneCardEndToEnd(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		mode    string
		outcome string
		reason  string
		exit    string
	}{
		{"done", "DONE", "done", "0"},
		{"fail", "FAILED", "crash", "3"},
		{"hang", "FAILED", "timeout", "-1"},
	}
	for i, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			ctx := context.Background()
			st, client := newSprint(t)
			id := card.Identity{Sprint: "control-wrap", Label: "card-" + tc.mode, BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
			token := attemptToken(1, fmt.Sprintf("%032x", i+1))
			seedCard(t, ctx, client, id, "dealt", token)
			gate := filepath.Join(t.TempDir(), "gate")
			t.Setenv(fakeHarnessEnv, tc.mode)
			t.Setenv(fakeGateEnv, gate)
			// A token in the wrapper's own environment must not reach the harness.
			t.Setenv("NOVA_CARD_TOKEN", token)

			h := newHarnessRun(t, id, self)
			ledger := &observed{inner: &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token}, events: make(chan string, 64)}
			got := make(chan card.WrapperReport, 1)
			go func() { got <- card.RunWrapper(ctx, h.cfg, ledger) }()

			h.waitFor(ledger, "launched")
			h.waitFor(ledger, "beat") // the start acknowledgement
			for n := 0; n < 2; n++ {
				h.ticks <- time.Time{}
				h.waitFor(ledger, "beat")
			}
			if tc.mode == "hang" {
				// The harness is up (it wrote its RESULT line) and hangs; the clock runs out.
				h.waitFile(filepath.Join(card.WrapperJobDir(h.cfg.JobsRoot, id.Sprint, id.Label, 1), "out", "RESULT.md"))
				h.clock <- time.Time{}
			} else if err := os.WriteFile(gate, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			rep := h.report(got)

			if rep.Code != card.WrapperExitEnded || rep.Outcome != tc.outcome || rep.Reason != tc.reason || rep.Beats != 3 {
				t.Fatalf("report %s why=%q; want outcome=%s reason=%s beats=3 code=0", rep.Line(), rep.Why, tc.outcome, tc.reason)
			}
			if h.interval != h.cfg.BeatEvery {
				t.Fatalf("beat ticker at %s, want the configured %s", h.interval, h.cfg.BeatEvery)
			}
			log := logBy(t, ctx, client, id.Sprint)
			if n := len(log["launched"]); n != 1 {
				t.Fatalf("%d launched records, want 1: %v", n, log)
			}
			if n := len(log["card-beat"]); n != 3 {
				t.Fatalf("%d beat records, want 3 (the start ack and two ticks): %v", n, log)
			}
			if n := len(log["ended"]); n != 1 || log["ended"][0]["reason"] != tc.reason {
				t.Fatalf("end records %v, want one with reason %s", log["ended"], tc.reason)
			}
			hash := hashOf(t, ctx, client, id.Sprint, id.Label)
			if hash["state"] != "ended" || hash["outcome"] != tc.outcome || hash["reason"] != tc.reason || hash["exit"] != tc.exit {
				t.Fatalf("card hash %v, want ended %s %s exit %s", hash, tc.outcome, tc.reason, tc.exit)
			}
			if hash["branch"] != card.WrapperBranch(id.Sprint, id.Label, 1) {
				t.Fatalf("branch %q", hash["branch"])
			}
			rec, err := card.ReadEndRecord(h.results)
			if err != nil || rec.Identity != id || rec.Outcome != tc.outcome || rec.Reason != tc.reason || rec.TokenSHA != card.TokenSHA(token) {
				t.Fatalf("end record %+v err %v", rec, err)
			}
			for _, name := range []string{"harness.log", "RESULT.md", "wrapper.line"} {
				if _, err := os.Stat(filepath.Join(h.results, name)); err != nil {
					t.Fatalf("results dir lacks %s: %v", name, err)
				}
			}
			harnessLog, _ := os.ReadFile(filepath.Join(h.results, "harness.log"))
			if strings.Contains(string(harnessLog), token) || !strings.Contains(string(harnessLog), "NOVA_CARD_JOB=") {
				t.Fatalf("harness environment carried the token or no job dir:\n%s", harnessLog)
			}
			for _, entry := range log["all"] {
				if strings.Contains(fmt.Sprint(entry), token) {
					t.Fatalf("log entry carries the raw token: %v", entry)
				}
			}
			h.assertNoJobDir()
		})
	}

	// Not dealt to this bench: another bench's card, and a card still queued.
	for _, tc := range []struct {
		name, state, bench string
	}{
		{"other-bench", "dealt", "someone-else"},
		{"queued", "queued", "wrap-bench"},
	} {
		t.Run("not-dealt-"+tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, client := newSprint(t)
			id := card.Identity{Sprint: "control-wrap", Label: "card-" + tc.name, BaseSHA: "0123abcd", Bench: tc.bench, Attempt: 1}
			token := attemptToken(1, "abcdefabcdefabcdefabcdefabcdefab")
			seedCard(t, ctx, client, id, tc.state, token)
			before := hashOf(t, ctx, client, id.Sprint, id.Label)
			t.Setenv(fakeHarnessEnv, "done")

			h := newHarnessRun(t, card.Identity{Sprint: id.Sprint, Label: id.Label, BaseSHA: id.BaseSHA, Bench: "wrap-bench", Attempt: 1}, self)
			ledger := &observed{inner: &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token}, events: make(chan string, 64)}
			rep := card.RunWrapper(ctx, h.cfg, ledger)
			if rep.Code != card.WrapperExitNotDealt || rep.Outcome != "" {
				t.Fatalf("report %s, want exit 4 and no outcome", rep.Line())
			}
			if calls := drain(ledger.events); len(calls) != 0 {
				t.Fatalf("a wrapper for a card not dealt here called %v", calls)
			}
			if n := xlen(t, ctx, client, id.Sprint); n != 0 {
				t.Fatalf("a wrapper for a card not dealt here wrote %d log entries", n)
			}
			if after := hashOf(t, ctx, client, id.Sprint, id.Label); fmt.Sprint(after) != fmt.Sprint(before) {
				t.Fatalf("card hash changed: %v -> %v", before, after)
			}
			h.assertNoJobDir()
			if entries, _ := os.ReadDir(h.cfg.ResultsRoot); len(entries) != 0 {
				t.Fatalf("results root not empty: %v", entries)
			}
		})
	}
}

// harnessRun is one wrapper's configuration with its clock and ticker as
// channels the test drives.
type harnessRun struct {
	t        *testing.T
	cfg      card.WrapperConfig
	results  string
	clock    chan time.Time
	ticks    chan time.Time
	interval time.Duration
}

func newHarnessRun(t *testing.T, id card.Identity, harness string) *harnessRun {
	root := t.TempDir()
	h := &harnessRun{t: t, clock: make(chan time.Time), ticks: make(chan time.Time)}
	h.cfg = card.WrapperConfig{
		Sprint: id.Sprint, Label: id.Label, Attempt: id.Attempt, Bench: id.Bench,
		Harness:     harness,
		JobsRoot:    filepath.Join(root, "jobs"),
		ResultsRoot: filepath.Join(root, "results"),
		Clock:       45 * time.Minute,
		BeatEvery:   42 * time.Second,
		After:       func(time.Duration) <-chan time.Time { return h.clock },
		Tick: func(d time.Duration) (<-chan time.Time, func()) {
			h.interval = d
			return h.ticks, func() {}
		},
	}
	for _, dir := range []string{h.cfg.JobsRoot, h.cfg.ResultsRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.results = filepath.Join(h.cfg.ResultsRoot, filepath.FromSlash(id.String()))
	return h
}

func (h *harnessRun) waitFor(l *observed, want string) {
	h.t.Helper()
	select {
	case got := <-l.events:
		if got != want {
			h.t.Fatalf("ledger call %s, want %s", got, want)
		}
	case <-time.After(testWait()):
		h.t.Fatalf("no %s call within %s", want, testWait())
	}
}

// waitFile polls for a file the harness writes, up to the test bound.
func (h *harnessRun) waitFile(path string) {
	h.t.Helper()
	bound := time.Now().Add(testWait())
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(bound) {
			h.t.Fatalf("%s did not appear within %s", path, testWait())
		}
		// Waits only for the next probe, never as the assertion.
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harnessRun) report(got chan card.WrapperReport) card.WrapperReport {
	h.t.Helper()
	select {
	case rep := <-got:
		return rep
	case <-time.After(testWait()):
		h.t.Fatalf("the wrapper did not return within %s", testWait())
	}
	return card.WrapperReport{}
}

func (h *harnessRun) assertNoJobDir() {
	h.t.Helper()
	entries, err := os.ReadDir(h.cfg.JobsRoot)
	if err != nil {
		h.t.Fatal(err)
	}
	if len(entries) != 0 {
		h.t.Fatalf("job directory left behind under %s: %v", h.cfg.JobsRoot, entries)
	}
}

// observed passes every call to the Redis ledger and reports its name, so the
// test waits on events rather than a clock.
type observed struct {
	inner  card.WrapperLedger
	events chan string
}

func (o *observed) Card(ctx context.Context) (card.WrapperCard, error) { return o.inner.Card(ctx) }

func (o *observed) Launched(ctx context.Context, branch, job string) (int, error) {
	code, err := o.inner.Launched(ctx, branch, job)
	o.events <- "launched"
	return code, err
}

func (o *observed) Beat(ctx context.Context) (int, error) {
	code, err := o.inner.Beat(ctx)
	o.events <- "beat"
	return code, err
}

func (o *observed) End(ctx context.Context, end card.WrapperEnd) (int, error) {
	code, err := o.inner.End(ctx, end)
	o.events <- "end"
	return code, err
}

func drain(ch chan string) []string {
	var out []string
	for {
		select {
		case s := <-ch:
			out = append(out, s)
		default:
			return out
		}
	}
}

// logBy groups the sprint log: "launched" and "ended" by the to field,
// "card-beat" by actor, and "all" holds every entry.
func logBy(t *testing.T, ctx context.Context, client *redis.Client, sprint string) map[string][]map[string]any {
	t.Helper()
	msgs, err := client.XRange(ctx, card.LogKey(sprint), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]map[string]any{}
	for _, m := range msgs {
		out["all"] = append(out["all"], m.Values)
		switch {
		case m.Values["actor"] == "card-beat":
			out["card-beat"] = append(out["card-beat"], m.Values)
		case m.Values["to"] == "launched":
			out["launched"] = append(out["launched"], m.Values)
		case m.Values["to"] == "ended":
			out["ended"] = append(out["ended"], m.Values)
		}
	}
	return out
}

// testWait is the generous bound for an event, read from NOVA_TEST_WAIT.
func testWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}
