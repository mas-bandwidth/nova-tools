//go:build functional

package swarm

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// These tests run a batch or a pull against fake ssh and scp programs placed
// first on PATH: exec of whole programs is the functional tier's, not a unit
// test (Glenn 2026-09-26, nova-tools#4328).

// TestBatchPinsSlotToCore: slot 3 on cores=1-15 runs under taskset -c 3, and every remote
// argv carries taskset. The fake ssh records each argv; none of them run native.
//
// The two cards run at once, so the order their argv lands in ssh.log is the order two
// concurrent slots happened to reach the fake -- not a fact about pinning. Reading runs[0]
// as card a's line made this test red 5 runs in 8 on this bench; the assertion is over the
// SET of run lines, each matched by its own label, which is what pinning actually claims.
func TestBatchPinsSlotToCore(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t1-15\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	sshLog, _ := fakeBin(t, dir)
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	b := writeCard(t, dir, "b.card", "RESULT: b\ndone and clean")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:3\tmodel\t"+a+"\nb\tb2:4\tmodel\t"+b+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		Tokens: "unmetered",
		ID:     "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
		SlotsStore: aBenchSlotStore(t), SlotOwner: "fake-1",
	})
	if code == 2 {
		t.Fatalf("the batch refused admission: %s", errb.String())
	}
	// The RUN argv lines, which are the ones that pin: an ssh that asks the bench a
	// question -- the pull's `test -f`, the idle watch's `stat` -- runs no card and pins
	// nothing. Selecting them by the command they carry is what keeps this assertion about
	// pinning rather than about how many other things a batch asks a bench.
	var runs []string
	for _, l := range readLines(t, sshLog) {
		if strings.Contains(l, "nova-swarm native") {
			runs = append(runs, l)
		}
	}
	if len(runs) == 0 {
		t.Fatalf("the fake ssh saw no run; the remote card never ran")
	}
	// The two remote cards' ssh writes land in ssh.log in whichever order the scheduler
	// ran them, so the RUN lines are not ordered by slot on return (#583). Sorting by the
	// pin they carry gives the loop below and any failure message a stable order.
	//
	// It is NOT what makes the slot->core assertions order-independent: sorting the lines
	// by the very core the test then names cannot tell card a on core 3 from card a on
	// core 4, so the pairing is asserted by matching each line's own --label instead.
	sort.Slice(runs, func(i, j int) bool {
		return coreIn(runs[i]) < coreIn(runs[j])
	})
	for _, l := range runs {
		if !strings.Contains(l, "taskset") {
			t.Fatalf("every remote run argv carries taskset, got %q", l)
		}
	}
	if len(runs) != 2 {
		t.Fatalf("two cards run, the fake ssh saw %d runs:\n%v", len(runs), runs)
	}
	pin := map[string]string{}
	for _, l := range runs {
		for _, label := range []string{"a", "b"} {
			if strings.Contains(l, "--label "+label+" ") {
				pin[label] = l
			}
		}
	}
	if !strings.Contains(pin["a"], "taskset -c 3") {
		t.Fatalf("card a on slot 3 of 1-15 runs under taskset -c 3, got %q", pin["a"])
	}
	if !strings.Contains(pin["b"], "taskset -c 4") {
		t.Fatalf("card b on slot 4 of 1-15 runs under taskset -c 4, got %q", pin["b"])
	}
}

// TestBatchCopiesCardOnly: exactly one file crosses before the run, and it is the card.
func TestBatchCopiesCardOnly(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	_, rsyncLog := fakeBin(t, dir)
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		Tokens: "unmetered",
		ID:     "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
		SlotsStore: aBenchSlotStore(t), SlotOwner: "fake-1",
	})
	if code == 2 {
		t.Fatalf("the batch refused admission: %s", errb.String())
	}
	lines := readLines(t, rsyncLog)
	if len(lines) != 1 {
		t.Fatalf("exactly one file crosses before the run, rsync saw %d:\n%v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], a+" ") {
		t.Fatalf("the one file copied is the card: %q", lines[0])
	}
	if strings.Contains(lines[0], "runner") || strings.Contains(lines[0], "auth") || strings.Contains(lines[0], "config") {
		t.Fatalf("no runner script, config or key crosses: %q", lines[0])
	}
}

// TestRemoteRunSlotIsTheSlotDir: remoteRun hands native the slot directory, not the job
// directory, as --slot: native computes the job itself as <slot>/jobs/<label>, so a --slot
// of <root>/<n>/jobs/<label> made the job <root>/<n>/jobs/<label>/jobs/<label> instead of
// the <root>/<n>/jobs/<label> the local path is (#656). The fake ssh records argv; the
// --slot value has no /jobs/ component and the job directory the pull reads the card's
// files from equals slotDir/jobs/label.
func TestRemoteRunSlotIsTheSlotDir(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t-\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	sshLog, _ := fakeBin(t, dir)
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:1\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		Tokens: "unmetered",
		ID:     "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Stdout: &out, Stderr: &errb,
		SlotsStore: aBenchSlotStore(t), SlotOwner: "fake-1",
	})
	if code == 2 {
		t.Fatalf("the batch refused admission: %s", errb.String())
	}
	var runs []string
	for _, l := range readLines(t, sshLog) {
		if strings.Contains(l, "nova-swarm native") {
			runs = append(runs, l)
		}
	}
	if len(runs) != 1 {
		t.Fatalf("one remote card runs, the fake ssh saw %d runs:\n%v", len(runs), runs)
	}
	fields := strings.Fields(runs[0])
	slot := ""
	for i, f := range fields {
		if f == "--slot" && i+1 < len(fields) {
			slot = fields[i+1]
		}
	}
	if slot == "" {
		t.Fatalf("the run argv carries a --slot value: %q", runs[0])
	}
	if rel := strings.TrimPrefix(slot, benchRoot); strings.Contains(rel, "/jobs/") {
		t.Fatalf("--slot is the slot directory, not the job directory: got %q", slot)
	}
	if want := filepath.Join(benchRoot, "1"); slot != want {
		t.Fatalf("--slot is <root>/<n>: got %q, want %q", slot, want)
	}
	wantJob := filepath.Join(slot, "jobs", "a")
	// The pull reads the card's files from the job directory on the bench, which native
	// computes as <slot>/jobs/<label>: the two must name one path.
	found := false
	for _, l := range readLines(t, sshLog) {
		if _, after, ok := strings.Cut(l, "test -f "); ok && strings.HasSuffix(after, "/RESULT.md") {
			if job := strings.TrimSuffix(after, "/RESULT.md"); job == wantJob {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the job directory is <slot>/jobs/<label>, and the pull reads the card from it: want %s, run %q", wantJob, runs[0])
	}
}

// coreIn returns the taskset core a run argv pins: the number that follows "taskset -c ".
func coreIn(run string) int {
	i := strings.Index(run, "taskset -c ")
	if i < 0 {
		return -1
	}
	rest := run[i+len("taskset -c "):]
	j := strings.IndexAny(rest, " \t")
	if j < 0 {
		j = len(rest)
	}
	n := 0
	for _, c := range rest[:j] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
