package pulse

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// THE FIXTURE IS THE REAL BINARY. The launch fixture used to be a three-line stub that
// exited 0 for any argv, so no test could see that launch called a form of `nova-swarm
// batch` that cannot run a card (issue #630: the pool form wants --files and --tokens and
// has no runner). Every test here builds nova-swarm from this tree and asserts against what
// that binary actually accepts.

var (
	swarmOnce sync.Once
	swarmBin  string
	swarmErr  error
)

// realSwarm builds cmd/nova-swarm once per test binary and returns its path.
func realSwarm(t *testing.T) string {
	t.Helper()
	swarmOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-swarm-bin")
		if err != nil {
			swarmErr = err
			return
		}
		out := filepath.Join(dir, "nova-swarm")
		cmd := exec.Command("go", "build", "-o", out, "github.com/mas-bandwidth/nova-tools/cmd/nova-swarm")
		if b, err := cmd.CombinedOutput(); err != nil {
			swarmErr = fmt.Errorf("go build nova-swarm: %v: %s", err, b)
			return
		}
		swarmBin = out
	})
	if swarmErr != nil {
		t.Fatal(swarmErr)
	}
	return swarmBin
}

// fakeRunner writes a runner script of the shape `nova-swarm batch --runner` starts: one
// process per card, given label slot model card-path root. It writes the card's own line 1
// into RESULT.md, which is what a done card looks like.
func fakeRunner(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "runner.sh")
	script := `#!/bin/sh
label="$1"; slot="$2"; card="$4"; root="$5"
job="$root/$slot/jobs/$label"
mkdir -p "$job"
head -1 "$card" > "$job/RESULT.md"
echo "BRANCH rowan/$label" >> "$job/RESULT.md"
echo "REPO mas-bandwidth/nova-tools" >> "$job/RESULT.md"
echo "RUNNER OK label=$label slot=$slot"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeCards writes n card files and a cards.tsv naming them, each old enough to admit.
func writeCards(t *testing.T, root string, n int) (string, []string) {
	t.Helper()
	cardsDir := filepath.Join(root, "src")
	if err := os.MkdirAll(cardsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	paths := make([]string, n)
	old := time.Now().Add(-time.Hour)
	for i := 0; i < n; i++ {
		label := fmt.Sprintf("card-%d", i)
		path := filepath.Join(cardsDir, label+".md")
		body := "RESULT " + label + " sha=000000000000\nYou are a worker.\nSTEP 1. do the thing.\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		paths[i] = path
		sb.WriteString(label + "\t-\topencode/deepseek-v4-flash\t" + path + "\n")
	}
	cards := filepath.Join(root, "cards.tsv")
	if err := os.WriteFile(cards, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return cards, paths
}

func runLaunch(t *testing.T, in LaunchInput) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	in.Stdout = &out
	in.Stderr = &errb
	code := Launch(in)
	return code, out.String(), errb.String()
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestLaunchCallsTheCardFormOfBatchAndTheCardRuns is issue #630's red test. Without the
// change it fails on the first assertion: launch called `nova-swarm batch --pool --tasks`,
// whose own refusal (`--files is required and is at least 1`) came back as
// `PULSE REFUSED`, exit 2, and no card ever started. The non-test line it needs is
// launch.go's startBatch, which passes --id --cards --deadline --root --runner --slots.
func TestLaunchCallsTheCardFormOfBatchAndTheCardRuns(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCards(t, root, 2)
	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Deadline: "60", Slots: "1-4",
		Runner: fakeRunner(t, root), Swarm: realSwarm(t), Check: 0,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.HasPrefix(out, "LAUNCH OK id=") || !strings.Contains(out, "cards=2 slots=1-4 free=4") {
		t.Fatalf("LAUNCH OK line wrong: %q", out)
	}
	// The real batch ran the cards: each card's job directory holds the RESULT.md the
	// runner wrote, under a slot of the range.
	for i := 0; i < 2; i++ {
		label := fmt.Sprintf("card-%d", i)
		waitFor(t, 30*time.Second, label+" RESULT.md", func() bool {
			jd := findJobDir(root, label)
			if jd == "" {
				return false
			}
			_, err := os.Stat(filepath.Join(jd, "RESULT.md"))
			return err == nil
		})
	}
	// And the batch's own packet is in the root beside it.
	waitFor(t, 30*time.Second, "the BATCH line", func() bool {
		raw, err := os.ReadFile(filepath.Join(root, "batch-"+launchID(t, out)+".out"))
		return err == nil && strings.Contains(string(raw), "BATCH ")
	})
}

func launchID(t *testing.T, out string) string {
	t.Helper()
	for _, f := range strings.Fields(out) {
		if v, ok := strings.CutPrefix(f, "id="); ok {
			return v
		}
	}
	t.Fatalf("no id= in %q", out)
	return ""
}

// TestLaunchRecordsEveryLaunchedCard: launch.tsv is the row harvest and check fold from.
// Red without recordLaunch in launch.go: there is no launch.tsv at all.
func TestLaunchRecordsEveryLaunchedCard(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCards(t, root, 2)
	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Deadline: "60", Slots: "1-4", ID: "TP9",
		Runner: fakeRunner(t, root), Swarm: realSwarm(t),
	})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, errb)
	}
	rows, err := readLaunchTSV(filepath.Join(root, "launch.tsv"), "TP9")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("launch.tsv holds %d rows, want 2", len(rows))
	}
	if rows[0].Label != "card-0" || rows[0].Model != "opencode/deepseek-v4-flash" || rows[0].SHA == "-" || rows[0].Stamp == "" {
		t.Fatalf("row 0 does not carry label, model, sha and stamp: %+v", rows[0])
	}
	if !strings.Contains(out, "id=TP9") {
		t.Fatalf("the named id is not on the LAUNCH line: %q", out)
	}
}

// TestLaunchFillsOnlyFreeSlotsByTheBatchLock is the measured cause of ten wasted cards:
// the launcher decided busy by a running process, the batch by its own lock, and during a
// clone there is no process. Red without swarm.FreeSlots in launch.go's freeSlotCount:
// a launcher that probed processes counts four free slots here and launches four cards.
func TestLaunchFillsOnlyFreeSlotsByTheBatchLock(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCards(t, root, 4)
	// Slots 1, 2 and 3 hold a live BATCH lock (this test's own pid); no process of theirs
	// is running, which is exactly the clone/scp window that cost the cards.
	for _, n := range []int{1, 2, 3} {
		dir := filepath.Join(root, fmt.Sprintf("%d", n))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf("id=other pid=%d at=2026-09-16T00:00:00Z\n", os.Getpid())
		if err := os.WriteFile(filepath.Join(dir, "BATCH"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Deadline: "60", Slots: "1-4",
		Runner: fakeRunner(t, root), Swarm: realSwarm(t),
	})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, errb)
	}
	if !strings.Contains(out, "cards=1 slots=1-4 free=1") {
		t.Fatalf("launch did not stop at the one free slot: %q", out)
	}
}

// TestLaunchRefusesWhenEverySlotIsHeld: nothing is free, so nothing is launched, and the
// refusal names the range. Red without the free=0 branch: launch handed every card to a
// batch that refused them one by one at admission.
func TestLaunchRefusesWhenEverySlotIsHeld(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCards(t, root, 2)
	for n := 1; n <= 2; n++ {
		dir := filepath.Join(root, fmt.Sprintf("%d", n))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf("id=other pid=%d at=2026-09-16T00:00:00Z\n", os.Getpid())
		if err := os.WriteFile(filepath.Join(dir, "BATCH"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Deadline: "60", Slots: "1-2",
		Runner: fakeRunner(t, root), Swarm: realSwarm(t),
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2; stdout=%q", code, out)
	}
	if !strings.Contains(errb, "LAUNCH REFUSED reason=no-free-slot slots=1-2 cards=2") {
		t.Fatalf("refusal wrong: %q", errb)
	}
}

// TestLaunchRefusesASecondLaunchInOneRoot: one launch per root, the holder named by pid.
// Red without takePID: two launches race over one root's free slots.
func TestLaunchRefusesASecondLaunchInOneRoot(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCards(t, root, 1)
	if err := os.WriteFile(filepath.Join(root, "pulse.pid"), []byte(fmt.Sprintf("%d", os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Deadline: "60", Slots: "1-2",
		Runner: fakeRunner(t, root), Swarm: realSwarm(t),
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	if !strings.Contains(errb, fmt.Sprintf("LAUNCH REFUSED reason=running pid=%d", os.Getpid())) {
		t.Fatalf("refusal does not name the holder: %q", errb)
	}
}

// TestLaunchSkipsAnEmptyCardAndAYoungOne: a card still being written admits under the wrong
// line 1. Red without cardNotReady: both are handed to the batch.
func TestLaunchSkipsAnEmptyCardAndAYoungOne(t *testing.T) {
	root := t.TempDir()
	cards, paths := writeCards(t, root, 3)
	if err := os.WriteFile(paths[0], nil, 0o644); err != nil { // empty
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(paths[1], now, now); err != nil { // younger than five seconds
		t.Fatal(err)
	}
	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Deadline: "60", Slots: "1-4",
		Runner: fakeRunner(t, root), Swarm: realSwarm(t),
		Now: func() time.Time { return now },
	})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, errb)
	}
	if !strings.Contains(errb, "LAUNCH SKIPPED label=card-0 reason=card-empty") ||
		!strings.Contains(errb, "LAUNCH SKIPPED label=card-1 reason=card-young") {
		t.Fatalf("skips not named: %q", errb)
	}
	if !strings.Contains(out, "cards=1") {
		t.Fatalf("one card should have launched: %q", out)
	}
}

// TestCheckCountsStartedCardsByJobDirectory is the shim's launch_check: the job directory is
// made before the harness's first line, so its absence is a dead launch and its presence is
// not. Red without Check in launch.go: there is no check verb at all.
func TestCheckCountsStartedCardsByJobDirectory(t *testing.T) {
	root := t.TempDir()
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	rows := fmt.Sprintf("TP1\tcard-a\t-\t-\tm\tabc123\t%s\nTP1\tcard-b\t-\t-\tm\tdef456\t%s\n", stamp, stamp)
	if err := os.WriteFile(filepath.Join(root, "launch.tsv"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Check(CheckInput{ID: "TP1", Root: root, Stdout: &out, Stderr: &errb})
	if code != 1 || !strings.Contains(out.String(), "LAUNCH-DEAD id=TP1") || !strings.Contains(out.String(), "cards=2 started=0") {
		t.Fatalf("no job dir should be LAUNCH-DEAD: exit=%d out=%q", code, out.String())
	}

	if err := os.MkdirAll(filepath.Join(root, "3", "jobs", "card-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	code = Check(CheckInput{ID: "TP1", Root: root, Stdout: &out, Stderr: &errb})
	if code != 0 || !strings.Contains(out.String(), "LAUNCH-OK id=TP1 bench=- started=1/2") {
		t.Fatalf("one started card should be LAUNCH-OK 1/2: exit=%d out=%q", code, out.String())
	}
}
