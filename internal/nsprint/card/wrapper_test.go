package card_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/redis/go-redis/v9"
)

// The fake harness is this test binary, re-executed. TestMain turns it into
// the harness when fakeHarnessEnv is set; the wrapper strips NOVA_CARD_* and
// anything naming a token from the harness's environment, so the switch uses
// its own names.
const (
	fakeHarnessEnv = "WRAPPER_FAKE_HARNESS" // done, fail or hang
	fakeGateEnv    = "WRAPPER_FAKE_GATE"    // done and fail exit once this file exists
	fakeOriginEnv  = "WRAPPER_FAKE_ORIGIN"  // repo mode clones this into out/repo
	fakeSlotEnv    = "WRAPPER_FAKE_SLOT"    // native modes run the card in <slot>/jobs/<label>
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeHarnessEnv); mode != "" {
		os.Exit(fakeHarness(mode))
	}
	if os.Getenv(fakeRunnerEnv) != "" {
		os.Exit(fakeRunner())
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
	if mode == "refuse" || mode == "refuse-identity" {
		return fakeRefusal(mode)
	}
	out := os.Getenv("NOVA_CARD_OUT")
	if strings.HasPrefix(mode, "native") {
		if code := fakeNative(mode, out); code != 0 {
			return code
		}
	} else if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: "+os.Getenv("NOVA_CARD")+" sha=000000000000\n"+saidLine2[mode]), 0o644); err != nil {
		fmt.Println("fake harness:", err)
		return 9
	}
	if mode == "repo" {
		// The #2932 commit step's input: a clone of the test's origin with
		// uncommitted work and card scratch in it.
		repo := filepath.Join(out, "repo")
		if msg, err := exec.Command("git", "clone", "-q", os.Getenv(fakeOriginEnv), repo).CombinedOutput(); err != nil {
			fmt.Println("fake harness clone:", err, string(msg))
			return 9
		}
		for name, body := range map[string]string{"work.txt": "the card's work\n", "notes.txt": "scratch\n"} {
			if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
				fmt.Println("fake harness:", err)
				return 9
			}
		}
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

// saidLine2 is the model's line 2 the said-* modes write under line 1 (the
// quack-0925d no-commit cards: DONE and nothing committed).
var saidLine2 = map[string]string{
	"said-done":    "DONE\n",
	"said-abstain": "ABSTAIN out of scope\n",
	"said-blocked": "BLOCKED deps missing\n",
}

// fakeNative is today's native route (quack test, 2026-09-24): the card runs in
// <slot>/jobs/<label>, a slot the bench harness picks outside the wrapper's job, its
// STEP 1 clones there as repo (one committed base), its fix leaves one changed file,
// and its RESULT.md is written beside the repo. mode native then makes the same
// hand-off call nova-swarm native makes under NOVA_CARD_OUT; native-nohandoff is the
// layout before the fix, with nothing under out.
func fakeNative(mode, out string) int {
	parts := strings.Split(os.Getenv("NOVA_CARD"), "/")
	job := filepath.Join(os.Getenv(fakeSlotEnv), "jobs", parts[1])
	repo := filepath.Join(job, "repo")
	if msg, err := exec.Command("git", "clone", "-q", os.Getenv(fakeOriginEnv), repo).CombinedOutput(); err != nil {
		fmt.Println("fake native clone:", err, string(msg))
		return 9
	}
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\nthe card's fix\n"), 0o644); err != nil {
		fmt.Println("fake native:", err)
		return 9
	}
	body := "RESULT: " + os.Getenv("NOVA_CARD") + " sha=000000000000\nfixed; tests pass\n"
	switch mode {
	case "native-two":
		// #3689: the model's whole contract, two lines and a note.
		body = "RESULT: " + os.Getenv("NOVA_CARD") + " sha=000000000000\nDONE\nthe retry path is still owed\n"
	case "native-wrongbranch":
		// #3689: the quack cards' shape, the BRANCH the card told the model.
		body = "RESULT: " + os.Getenv("NOVA_CARD") + " sha=000000000000\nDONE\nBRANCH: rowan/" + parts[1] + "\n"
	}
	if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(body), 0o644); err != nil {
		fmt.Println("fake native:", err)
		return 9
	}
	if mode != "native" && mode != "native-nohandoff" {
		// The bench harness's START line (rowan-tools' nova-card-harness):
		// names only, never a key's value.
		start := "2026-09-24T23:59:00Z START " + os.Getenv("NOVA_CARD") + " bench=wrap-bench tier=flash route=opencode-flash model=opencode/kimi-k3 key=OPENCODE_API_KEY sha=0123456789ab\n"
		if err := os.WriteFile(filepath.Join(out, "harness.log"), []byte(start), 0o644); err != nil {
			fmt.Println("fake native:", err)
			return 9
		}
	}
	if mode != "native-nohandoff" {
		if _, err := swarm.HandOffCardOut(job, out); err != nil {
			fmt.Println("fake native hand-off:", err)
			return 9
		}
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

// Claim passes through without an event: the claim is not a ledger record
// the end-to-end control counts, and a card not dealt here never reaches it
// (its hash is compared unchanged).
func (o *observed) Claim(ctx context.Context, nonce string) (int, error) {
	return claimOf(o.inner).Claim(ctx, nonce)
}

func (o *observed) Launched(ctx context.Context, branch, job string, wallMax time.Duration) (int, error) {
	code, err := o.inner.Launched(ctx, branch, job, wallMax)
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

// claimer is the wrapper's Redis claim (#3328). It is asserted, not named in
// the interface, so this control compiles at dev and fails there on what
// the wrapper does rather than on a missing symbol.
type claimer interface {
	Claim(ctx context.Context, nonce string) (int, error)
}

func claimOf(l card.WrapperLedger) claimer {
	if c, ok := l.(claimer); ok {
		return c
	}
	return noClaim{}
}

type noClaim struct{}

func (noClaim) Claim(context.Context, string) (int, error) { return 0, nil }

// TestWrapperClaimIsRedisNotMkdir is the DONE-WHEN control of #3328: the
// attempt claim is Redis, not a job-dir Mkdir. Two RunWrapper calls race for
// one dealt attempt (same token, same bench, same jobs root): exactly one gets
// ns_card_launched 0; the other exits with the Redis refusal and creates no
// directory under JobsRoot; the job dir does not exist until launched returned
// 0. A leftover job dir from an earlier run no longer refuses a launched card:
// it is cleared under JobsRoot through safepath.
func TestWrapperClaimIsRedisNotMkdir(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("race", func(t *testing.T) {
		ctx := context.Background()
		st, client := newSprint(t)
		id := card.Identity{Sprint: "control-claim", Label: "card-race", BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
		token := attemptToken(1, fmt.Sprintf("%032x", 77))
		seedCard(t, ctx, client, id, "dealt", token)
		gate := filepath.Join(t.TempDir(), "gate")
		t.Setenv(fakeHarnessEnv, "done")
		t.Setenv(fakeGateEnv, gate)

		h := newHarnessRun(t, id, self)
		job := card.WrapperJobDir(h.cfg.JobsRoot, id.Sprint, id.Label, id.Attempt)
		race := &raceLedger{
			job:      job,
			bothRead: make(chan struct{}),
			won:      make(chan struct{}),
			release:  make(chan struct{}),
		}
		reports := make(chan card.WrapperReport, 2)
		for i := 0; i < 2; i++ {
			inner := &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token}
			go func() { reports <- card.RunWrapper(ctx, h.cfg, &racer{inner: inner, race: race}) }()
		}

		// The loser returns while the winner is held just after launched
		// returned 0, so any directory under JobsRoot now is the loser's.
		var loser card.WrapperReport
		select {
		case loser = <-reports:
		case <-time.After(testWait()):
			t.Fatalf("no wrapper returned within %s; launched 0 calls: %d", testWait(), race.zeros())
		}
		if loser.Code != 4 || loser.Outcome != "" || !strings.Contains(loser.Why, "claim refused code=4") {
			t.Fatalf("loser %s why=%q; want the Redis claim refusal (code 4, CONFLICT) and no outcome", loser.Line(), loser.Why)
		}
		if entries, _ := os.ReadDir(h.cfg.JobsRoot); len(entries) != 0 {
			t.Fatalf("%v under the jobs root while the winner is held just after launched 0: the loser made a directory, or one was made before the claim", entries)
		}
		select {
		case <-race.won:
		case <-time.After(testWait()):
			t.Fatalf("no wrapper got ns_card_launched 0 within %s; loser %s why=%q", testWait(), loser.Line(), loser.Why)
		}
		close(race.release)
		if err := os.WriteFile(gate, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		winner := h.report(reports)

		if n := race.zeros(); n != 1 {
			t.Fatalf("%d wrappers got ns_card_launched 0, want exactly 1", n)
		}
		if why := race.dirBeforeLaunched(); why != "" {
			t.Fatal(why)
		}
		if winner.Code != card.WrapperExitEnded || winner.Outcome != "DONE" {
			t.Fatalf("winner %s why=%q; want ENDED DONE code=0", winner.Line(), winner.Why)
		}
		log := logBy(t, ctx, client, id.Sprint)
		if n := len(log["launched"]); n != 1 {
			t.Fatalf("%d launched records, want 1: %v", n, log["launched"])
		}
		if n := len(log["ended"]); n != 1 {
			t.Fatalf("%d end records, want 1: %v", n, log["ended"])
		}
		hash := hashOf(t, ctx, client, id.Sprint, id.Label)
		if hash["state"] != "ended" || hash["jobdir"] != job {
			t.Fatalf("card hash %v, want ended with jobdir %s", hash, job)
		}
		if strings.Contains(hash["claim"], token) {
			t.Fatalf("the claim carries the raw token: %q", hash["claim"])
		}
		h.assertNoJobDir()
	})

	t.Run("leftover-job-dir", func(t *testing.T) {
		ctx := context.Background()
		st, client := newSprint(t)
		id := card.Identity{Sprint: "control-claim", Label: "card-leftover", BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
		token := attemptToken(1, fmt.Sprintf("%032x", 78))
		seedCard(t, ctx, client, id, "dealt", token)
		gate := filepath.Join(t.TempDir(), "gate")
		if err := os.WriteFile(gate, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv(fakeHarnessEnv, "done")
		t.Setenv(fakeGateEnv, gate)

		h := newHarnessRun(t, id, self)
		job := card.WrapperJobDir(h.cfg.JobsRoot, id.Sprint, id.Label, id.Attempt)
		if err := os.MkdirAll(filepath.Join(job, "out"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(job, "out", "stale.txt"), []byte("an earlier run\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rep := card.RunWrapper(ctx, h.cfg, &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token})
		if rep.Code != card.WrapperExitEnded || rep.Outcome != "DONE" {
			t.Fatalf("report %s why=%q; a leftover job dir must not refuse a launched card", rep.Line(), rep.Why)
		}
		if _, err := os.Stat(filepath.Join(h.results, "stale.txt")); err == nil {
			t.Fatal("the leftover job dir's output was copied into this attempt's results")
		}
		if hash := hashOf(t, ctx, client, id.Sprint, id.Label); hash["state"] != "ended" {
			t.Fatalf("card hash %v, want ended", hash)
		}
		h.assertNoJobDir()
	})
}

// raceLedger is shared by the two racing wrappers. Card holds each caller
// until both have read the card as dealt, so both reach the claim; the first
// launched 0 is held until the test releases it.
type raceLedger struct {
	job      string
	mu       sync.Mutex
	reads    int
	bothRead chan struct{}
	zero     int
	dirSeen  string
	won      chan struct{}
	release  chan struct{}
}

func (r *raceLedger) zeros() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.zero
}

func (r *raceLedger) dirBeforeLaunched() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dirSeen
}

type racer struct {
	inner card.WrapperLedger
	race  *raceLedger
}

func (r *racer) Card(ctx context.Context) (card.WrapperCard, error) {
	c, err := r.inner.Card(ctx)
	r.race.mu.Lock()
	r.race.reads++
	if r.race.reads == 2 {
		close(r.race.bothRead)
	}
	r.race.mu.Unlock()
	select {
	case <-r.race.bothRead:
	case <-time.After(testWait()):
	}
	return c, err
}

func (r *racer) Claim(ctx context.Context, nonce string) (int, error) {
	return claimOf(r.inner).Claim(ctx, nonce)
}

func (r *racer) Launched(ctx context.Context, branch, job string, wallMax time.Duration) (int, error) {
	r.race.mu.Lock()
	if _, err := os.Stat(r.race.job); err == nil && r.race.dirSeen == "" {
		r.race.dirSeen = "the job dir " + r.race.job + " existed before ns_card_launched returned 0"
	}
	r.race.mu.Unlock()
	code, err := r.inner.Launched(ctx, branch, job, wallMax)
	if err != nil || code != 0 {
		return code, err
	}
	r.race.mu.Lock()
	r.race.zero++
	first := r.race.zero == 1
	r.race.mu.Unlock()
	if first {
		close(r.race.won)
		select {
		case <-r.race.release:
		case <-time.After(testWait()):
		}
	}
	return code, err
}

func (r *racer) Beat(ctx context.Context) (int, error) { return r.inner.Beat(ctx) }

func (r *racer) End(ctx context.Context, end card.WrapperEnd) (int, error) {
	return r.inner.End(ctx, end)
}
