package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeWork is the open work of a repo: no gh, no network.
type fakeWork struct {
	prs    []OpenPR
	issues []OpenIssue
}

func (f *fakeWork) OpenPRs(repo string) ([]OpenPR, error)       { return f.prs, nil }
func (f *fakeWork) OpenIssues(repo string) ([]OpenIssue, error) { return f.issues, nil }

// wiredFixture is a queue and a bench the size of a first tick: one card waiting, one
// approval that merged, one launched card whose job is gone, one runner stuck busy, one PR
// owed a read and one issue owed a fix.
func wiredFixture(t *testing.T, now time.Time) (queue, root string, work *fakeWork, restarter *fakeRestarter) {
	t.Helper()
	queue, root = t.TempDir(), t.TempDir()

	write := func(rel, body string) string {
		t.Helper()
		p := filepath.Join(queue, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// The configuration the tick runs on: one bench, two slots, two cards a tick.
	write(ConfigFile, "[slots]\nstudio = 2\nspace = 0\nlocal = 0\n\n[headroom]\nstudio = 2\nspace = 0\nlocal = 0\n\nrefill.cadence = 1\n")
	write("WORKSET", "# the work this bench is allowed to touch\n812\n601\n")
	write("ROUTES-code", "opencode/deepseek-v4-flash\n")
	write("pending/card-5.md", "RESULT: CARD-5 nova-tools #777 fixed with its red test first: a card already cut\nSTEP 1. do the thing\n")

	// An approval that landed while the loop was not looking: the sweep closes it and
	// writes the day's MERGED record.
	if err := AppendLedger(queue, LedgerRow{PR: 812, Head: "deadbeef", Card: "card-4.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	// A launched card whose job directory is gone, older than the deadline: the reaper
	// requeues it once.
	orphan := write("launched/card-3.md", "RESULT: CARD-3 nova-tools #778 fixed with its red test first: an orphan\n")
	age(t, orphan, 2*time.Hour, now)
	// A runner busy with nothing running since twenty minutes ago: rule E2 restarts it.
	writeBusySince(queue, map[string]time.Time{"space-nova-1": now.Add(-20 * time.Minute)})

	work = &fakeWork{
		prs:    []OpenPR{{Number: 812, Head: "abcd1234ef567890", Title: "the PR owed a read"}},
		issues: []OpenIssue{{Number: 601, Title: "the issue owed a fix", Body: "what is wrong"}},
	}
	return queue, root, work, &fakeRestarter{}
}

// TestWiredOnceTickRunsEverySeam is the claim of this card: `nova-pulse run --once` on a
// fixture queue is the hand loop's tick -- gate, harvest, sweep, reap, refill, launch -- and
// every one of them is the shipped verb, not a stub. The mutation that matters: a seam left
// nil is named on stderr and counted zero, and a tick that names a seam is a tick that did
// not do that step.
func TestWiredOnceTickRunsEverySeam(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Log: filepath.Join(t.TempDir(), "swarm.log"), Default: fakeRule{Stdout: "BATCH OK\n"}})

	now := time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)
	queue, root, work, restarter := wiredFixture(t, now)

	prs := &fakeSource{calls: map[int]int{}, views: map[int][]PRView{
		812: {{State: "MERGED", Head: "deadbeef", Title: "the PR owed a read"}},
	}}
	table := &fakeRunnerTable{running: 0, runners: []Runner{{Name: "space-nova-1", Busy: true}}}

	var out, errs bytes.Buffer
	in := RunInput{
		Queue: queue, Roots: root, Repo: "mas-bandwidth/nova-tools", Branch: "dev",
		Once: true, GateEvery: 1, Stdout: &out, Stderr: &errs,
		Now:   func() time.Time { return now },
		Sleep: func(time.Duration) { t.Fatal("--once must not sleep") },
	}
	cfg := DefaultConfig()
	in.Configured = func(c Config) { cfg = c }
	Wire(&in, NewWiring(WiringInput{
		Queue: queue, Roots: root, Repo: "mas-bandwidth/nova-tools", Branch: "dev",
		Now: func() time.Time { return now }, Config: func() Config { return cfg },
		TempGlob:  filepath.Join(t.TempDir(), "*swarmtest*"),
		Runs:      &fakeRuns{runs: []CIRun{{ID: 77, Status: "completed", Conclusion: "success", HeadSHA: "0123456789abcdef", Workflow: "ci", Event: "push"}}},
		PRs:       prs,
		Enqueuer:  &fakeEnqueuer{},
		Procs:     &fakeProcs{live: map[int]bool{}},
		Runners:   table,
		Restarter: restarter,
		Work:      work,
	}))
	if exit := Run(in); exit != 0 {
		t.Fatalf("exit %d:\n%s%s", exit, out.String(), errs.String())
	}

	// The console is what it was: one WIDTH line and one verdict. The verbs' own lines are
	// in the queue's log, where the hand loop wrote them.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "PULSE WIDTH tick=1 ") {
		t.Fatalf("stdout is %d lines, want the WIDTH line and RUN OK:\n%s", len(lines), out.String())
	}
	if strings.Contains(errs.String(), "seam=") {
		t.Errorf("a seam is still unwired:\n%s", errs.String())
	}

	log := strings.Join(readLines(filepath.Join(queue, "pulse.log")), "\n")
	for _, want := range []string{"GATE GREEN", "SWEEP repo=", "REAP roots=", "REFILL reads=", "PULSE OK"} {
		if !strings.Contains(log, want) {
			t.Errorf("pulse.log does not carry %q:\n%s", want, log)
		}
	}

	// 2. sweep: the merge is recorded where status --oneline counts it.
	if merged := readLines(filepath.Join(queue, "MERGED")); len(merged) != 1 || !strings.Contains(merged[0], "#812") {
		t.Errorf("MERGED = %v, want the merged PR", merged)
	}
	// 3. reap: the stuck runner was restarted, and the orphan card was requeued.
	if len(restarter.names) != 1 {
		t.Errorf("restarted %v, want exactly one stuck runner", restarter.names)
	}
	if !strings.Contains(log, "requeued=1") {
		t.Errorf("the orphaned launched card was not requeued:\n%s", log)
	}
	// 4. refill: a read card at this PR's head and a fix card for the issue, each cut by
	// cut --kind, which is the only numberer.
	cut := strings.Join(cardTexts(t, queue, "pending", "launched"), "\n")
	if !strings.Contains(cut, "read of nova-tools PR812 at abcd1234") {
		t.Errorf("no read card at PR812's head was cut:\n%s", cut)
	}
	if !strings.Contains(cut, "nova-tools #601 fixed with its red test first: the issue owed a fix") {
		t.Errorf("no fix card was cut for issue 601:\n%s", cut)
	}
	// 5. launch: the bench's two slots took two cards, and every launched card left pending.
	if got := field2(lines[0], "launched="); got != "2" {
		t.Errorf("WIDTH launched=%s, want 2 (two free slots, two cards)", got)
	}
	launched, _ := filepath.Glob(filepath.Join(queue, "launched", "card-*.md"))
	if len(launched) != 2 {
		t.Errorf("launched holds %d cards, want 2: a card is in exactly one place", len(launched))
	}
	if _, err := os.Stat(filepath.Join(root, "cards.tsv")); err != nil {
		t.Errorf("the bench has no cards.tsv for the harvest to fold: %v", err)
	}
	// 6. the counters are a file, and they carry the tick and the refill that ran on it.
	state, err := LoadState(queue)
	if err != nil {
		t.Fatal(err)
	}
	if state.Tick != 1 || state.RefillTick != 1 || state.Gate != 1 {
		t.Errorf("state = %+v, want tick=1 refill_tick=1 gate=1", state)
	}
	if state.NextCard < 3 {
		t.Errorf("state next_card=%d, want the cutter's next number after two cards", state.NextCard)
	}
}

// TestWiredRunRereadsTheConfigAndSaysWhatChanged is class A, bugs 2 and 3: a value used to
// need a restart, and the restart lost the counters. The second tick here takes an edited
// pulse.toml with no restart, says on ONE line what changed, and the tick counter carries
// on from the file rather than starting again.
func TestWiredRunRereadsTheConfigAndSaysWhatChanged(t *testing.T) {
	queue := t.TempDir()
	root := t.TempDir()
	now := time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(queue, ConfigFile), []byte("[slots]\nstudio = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	once := func() string {
		var out, errs bytes.Buffer
		in := RunInput{
			Queue: queue, Roots: root, Repo: "o/n", Branch: "dev", Once: true, GateEvery: 99,
			Stdout: &out, Stderr: &errs, Now: func() time.Time { return now },
		}
		cfg := DefaultConfig()
		in.Configured = func(c Config) { cfg = c }
		Wire(&in, NewWiring(WiringInput{
			Queue: queue, Roots: root, Repo: "o/n", Branch: "dev",
			Now: func() time.Time { return now }, Config: func() Config { return cfg },
			TempGlob: filepath.Join(t.TempDir(), "*swarmtest*"),
			PRs:      &fakeSource{calls: map[int]int{}}, Enqueuer: &fakeEnqueuer{},
			Procs: &fakeProcs{live: map[int]bool{}}, Work: &fakeWork{},
		}))
		if exit := Run(in); exit != 0 {
			t.Fatalf("exit %d:\n%s%s", exit, out.String(), errs.String())
		}
		return out.String()
	}

	// The first tick is the shift's ground: the configuration is what it is, and a line
	// about it would be a line every tick.
	first := once()
	if strings.Contains(first, "CONFIG") {
		t.Errorf("the first tick printed a CONFIG line:\n%s", first)
	}
	if !strings.HasPrefix(first, "PULSE WIDTH tick=1 ") {
		t.Errorf("first tick = %q", strings.SplitN(first, "\n", 2)[0])
	}

	// Somebody widens the bench between the ticks. No restart, and the loop says so once.
	if err := os.WriteFile(filepath.Join(queue, ConfigFile), []byte("[slots]\nstudio = 6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := once()
	if !strings.Contains(second, "CONFIG OK") || !strings.Contains(second, "keys=slots.studio") {
		t.Errorf("the changed value was not named on one CONFIG line:\n%s", second)
	}
	// And the counters survived the restart: this is tick 2, not tick 1 again.
	if !strings.Contains(second, "PULSE WIDTH tick=2 ") {
		t.Errorf("the tick counter did not survive the restart:\n%s", second)
	}
}

// cardTexts is every card in the named queue directories, read.
func cardTexts(t *testing.T, queue string, dirs ...string) []string {
	t.Helper()
	var out []string
	for _, dir := range dirs {
		for _, card := range cardsIn(filepath.Join(queue, dir)) {
			raw, err := os.ReadFile(card)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%s: %s", filepath.Base(card), string(raw)))
		}
	}
	return out
}

// headroom-decimal-caps-the-tick (issue #869): headroom is a ratio of cores, so 2.5 is a
// value a person writes, and the arithmetic that caps a tick's take must read it as 2.5 and
// not refuse it or round it up. Half a card is not a card: the cap is the whole cards that
// fit under the headroom.
func TestHeadroomTakeReadsADecimal(t *testing.T) {
	for _, c := range []struct {
		open     int
		headroom float64
		want     int
	}{
		{open: 8, headroom: 2.5, want: 2},
		{open: 8, headroom: 3, want: 3},
		{open: 2, headroom: 2.5, want: 2},
		{open: 8, headroom: 0, want: 8}, // no headroom is no cap, as it always was
		{open: 1, headroom: 0.5, want: 0},
	} {
		if got := headroomTake(c.open, c.headroom); got != c.want {
			t.Errorf("headroomTake(%d, %v) = %d, want %d", c.open, c.headroom, got, c.want)
		}
	}
}
