package card_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
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
	// native-done-already writes `ABSTAIN done-already <this sha>` (#3919)
	fakeDoneAlreadyEnv = "WRAPPER_FAKE_DONE_ALREADY"
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
	case "native-done-already":
		// #3919: the swarm-0925a cards, whose work had landed minutes before.
		body = "RESULT: " + os.Getenv("NOVA_CARD") + " sha=000000000000\nABSTAIN done-already " + os.Getenv(fakeDoneAlreadyEnv) + "\n"
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
	// onEnd, when set, sees the end the wrapper hands the ledger, before
	// the ledger writes it and before the job dir is deleted.
	onEnd func(card.WrapperEnd)
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
	if o.onEnd != nil {
		o.onEnd(end)
	}
	code, err := o.inner.End(ctx, end)
	o.events <- "end"
	return code, err
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
