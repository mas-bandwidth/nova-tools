//go:build slow

// The pool tests whose cost is real worker processes killed and reaped: a launch that
// must be a transaction, a killed worker's revision, a reaped job's second run, a group
// finalized while alive, a requeue refused mid-run. Five tests were 41 s of this
// package's 71 s.
//
// These tests are behind the `slow` build tag: the PR test jobs do not build them and
// .github/workflows/nightly-slow.yml does (#516, Glenn's two-minute rule -- a package's
// tests answer in a minute). Nothing here is skipped or weakened; it runs nightly, whole.

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// F7: `--task` said it "wants the id of the finished task this one replaces" and accepted
// any state. A RUNNING task is refused by name: work in flight is ended by its deadline or
// by `stop`, never replaced under itself.
func TestRequeueRefusesATaskThatIsStillRunning(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker that is still working\nFAKE-SLEEP 5\nFAKE-FINDINGS 1\n")
	replacement := filepath.Join(b.dir, "replacement.md")
	write(t, replacement, "a replacement\n")
	refused := make(chan string, 1)
	go func() {
		// IT IS ASKED ONLY ONCE THE TASK IS RUNNING, and that wait is the fix this test
		// needed when the wall landed: `run` proves the sandbox before it starts the first
		// worker (docs/SPEC-SANDBOX.md rule 10), so the first poll of the old loop landed
		// while the task was still PENDING -- and a requeue of a pending task is a LEGAL
		// requeue, which quietly replaced the task this test was about and left every
		// later poll saying `no task in pool`. The wait ends on its own, like every wait
		// in this repository.
		for i := 0; i < 600; i++ {
			if _, err := os.Stat(filepath.Join(b.pool, "running", id+".task")); err == nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		// THE WINDOW IS THE RUN'S, NOT A NUMBER. `run` proves the wall before it starts
		// the first worker (docs/SPEC-SANDBOX.md rule 10), and that probe is five real
		// wrapped runs on a machine with a backend -- so a poll loop of 60 x 25ms, which
		// was longer than a launch before the wall landed, expired while the dispatcher
		// was still proving it and this test failed for a reason that was not about
		// requeue. The job sleeps 5s once it is running; the loop waits longer than the
		// probe and the launch together, and still ends on its own (a wait loop always
		// has a deadline).
		for i := 0; i < 600; i++ {
			exit, _, stderr := b.swarm("requeue", "--pool", b.pool, "--task", id,
				"--task-file", replacement, "--files", "5", "--tokens", "100")
			if exit == 1 && strings.Contains(stderr, "running") {
				refused <- stderr
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		refused <- ""
	}()
	if exit, stdout, stderr := b.run(); exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	got := <-refused
	if got == "" {
		t.Fatal("a requeue of a RUNNING task is refused, and it never was")
	}
	mustContain(t, "the refusal", got, "REQUEUE REFUSED")
	mustContain(t, "the refusal", got, "nova-swarm stop")
}

func TestAReapedJobRunsOnceMore(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("this one waits for two deadlines")
	}
	b := newBench(t)
	id := b.add("a worker that sleeps past its deadline\nFAKE-SLEEP 30\n", "--deadline", "3s")
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN KILLED id="+id)
	// A worker reaped at its deadline BROKE NO RULE: it is `RUN KILLED … survived=<bool>`
	// (SPEC-SWARM.md:1055), never rule 11's `RUN VIOLATION`. Nothing outlived this kill.
	mustContain(t, "the run", stdout, "survived=false")
	if strings.Contains(stdout, "RUN VIOLATION") {
		t.Errorf("a deadline kill is not a background violation:\n%s", stdout)
	}
	mustContain(t, "the run", stdout, "requeued=true reaped=1")
	if _, err := os.Stat(filepath.Join(b.pool, "failed", id+".task")); err != nil {
		t.Errorf("a reaped job's files belong in failed/: %v", err)
	}
	// The re-queued task is a NEW job id carrying from=<old-id>, and the same pass picks it
	// up, reaps it a second time, and does not queue a third: one automatic retry closes the
	// case where a worker was silent because the provider was, and never the case where the
	// task was too big, which a second identical run would only prove twice.
	mustContain(t, "the run", stdout, "requeued=false reaped=2")
	failed, err := os.ReadDir(filepath.Join(b.pool, "failed"))
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 4 { // two tasks, each a .task and a .json
		t.Errorf("failed/ wants the two attempts and nothing else, got %d files", len(failed))
	}
	pending, err := os.ReadDir(filepath.Join(b.pool, "pending"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("a job reaped twice is not re-queued again, got %d files in pending/", len(pending))
	}
	mustContain(t, "the run", stdout, "RUN OK started=2 done=0 failed=0 killed=2 pending=0")
	mustContain(t, "the remedy line", stdout, "RUN NOTE a worker was killed at its deadline twice")

	// RULE 7, VERBATIM (SPEC-SWARM.md:109-111): "A worker silent past its deadline is
	// reaped and its job is re-queued once, with `requeued=1` in the new task's sidecar; a
	// job reaped a second time goes to `failed/` with `reaped=2` and is not re-queued
	// again." IN THE SIDECAR. The line printed `reaped=2` and the durable record in
	// failed/ still said `reaped=1`, so the one thing that outlives the run -- the file a
	// person reads tomorrow -- did not carry the count the rule names. A line is not a
	// record.
	first := b.sidecar(id)
	if first.Reaped != 1 {
		t.Errorf("the first attempt's sidecar wants reaped=1, got %d", first.Reaped)
	}
	second := swarmSidecar{}
	for _, name := range mustReadDirNames(t, filepath.Join(b.pool, "failed")) {
		if !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, id) {
			continue
		}
		second = b.sidecar(strings.TrimSuffix(name, ".json"))
	}
	if second.ID == "" {
		t.Fatal("the second attempt has no sidecar in failed/")
	}
	if second.From != id {
		t.Errorf("the re-queued attempt carries from=%s, got %q", id, second.From)
	}
	if second.Requeued != 1 {
		t.Errorf("rule 7 wants requeued=1 in the new task's sidecar, got %d", second.Requeued)
	}
	if second.Reaped != 2 {
		t.Errorf("a job reaped a second time goes to failed/ with reaped=2; its sidecar says %d", second.Reaped)
	}
}

// Demanded test 12, the killed job: its row says `end=killed` and carries whatever partial
// usage the provider's accounting held when the deadline came, and its re-queue is a SECOND
// file with `attempt=2 from=<old-id>` that `cost` sums once each.
func TestAKilledJobsUsageSaysKilledAndItsRetrySumsOnce(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("this one waits for two deadlines")
	}
	b := newBench(t)
	id := b.add("a worker that spends and then sleeps past its deadline\nFAKE-USAGE 700 300 - - -\nFAKE-SLEEP 30\n", "--deadline", "3s")
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN KILLED id="+id)

	first := b.usageRow(id)
	if first["end"] != "killed" {
		t.Errorf("a job killed at its deadline wants end=killed, got %q", first["end"])
	}
	if first["attempt"] != "1" || first["from"] != "-" {
		t.Errorf("the first attempt wants attempt=1 from=-, got attempt=%q from=%q", first["attempt"], first["from"])
	}
	if first["tokens_in"] != "700" || first["tokens_out"] != "300" {
		t.Errorf("the killed row wants whatever partial usage the source held, got in=%q out=%q",
			first["tokens_in"], first["tokens_out"])
	}
	// The re-queue is a SECOND file, naming the attempt and where it came from.
	var retry map[string]string
	entries, err := os.ReadDir(filepath.Join(b.pool, "usage"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("two attempts want two usage files, got %d", len(entries))
	}
	for _, e := range entries {
		other := strings.TrimSuffix(e.Name(), ".tsv")
		if other == id {
			continue
		}
		retry = b.usageRow(other)
	}
	if retry["attempt"] != "2" || retry["from"] != id {
		t.Errorf("the re-queue wants attempt=2 from=%s, got attempt=%q from=%q", id, retry["attempt"], retry["from"])
	}
	if retry["end"] != "killed" {
		t.Errorf("the second attempt was killed too, got end=%q", retry["end"])
	}
	exit, stdout, stderr = b.swarm("cost", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("cost exited %d: %s%s", exit, stdout, stderr)
	}
	// Each attempt once: 700 and 700, never 1400 for one of them.
	mustContain(t, "cost", stdout, "COST OK tasks=2 in=1400 out=600")
}

// Demanded test 12, the last sentence: `finalize --task` on a job whose group is ALIVE is
// FINALIZE REFUSED. Finalizing under a live worker would copy a report that is still being
// written and write a usage row the job has not finished earning.
func TestFinalizeIsRefusedWhileTheGroupIsAlive(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker that is still working\nFAKE-SLEEP 5\nFAKE-FINDINGS 1\n")
	refused := make(chan string, 1)
	go func() {
		for i := 0; i < 200; i++ {
			exit, _, stderr := b.swarm("finalize", "--pool", b.pool, "--task", id)
			if exit == 1 && strings.Contains(stderr, "still alive") {
				refused <- stderr
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		refused <- ""
	}()
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	got := <-refused
	if got == "" {
		t.Fatal("`finalize --task` under a live process group is FINALIZE REFUSED, and it never was")
	}
	mustContain(t, "the refusal", got, "FINALIZE REFUSED id="+id)
	mustContain(t, "the refusal", got, "process group is still alive")
}

// DEMANDED TEST 16 (SPEC-SWARM.md:1350), the dispatcher's half: a worker KILLED with a
// RESULT.md.tmp on disk ends `RUN KILLED … unpublished=true`, and the tmp file is still
// there afterwards, UNREAD. A half-written revision is not a report: the machinery says it
// exists, leaves it exactly where the worker left it, and folds none of it.
func TestAKilledWorkerLeavesItsUnpublishedRevisionAlone(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker killed with a revision half written\nFAKE-PUBLISH-FIRST\nFAKE-UNPUBLISHED\nFAKE-FINDINGS 2\nFAKE-SLEEP 30\n",
		"--deadline", "5s")
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	// THIS job's own line, not any line: the one automatic re-queue prints a RUN KILLED of
	// its own, and a test that read the wrong one would pass while this one said nothing.
	killed := lineWith(t, stdout, "RUN KILLED id="+id)
	mustContain(t, "the RUN KILLED line", killed, "unpublished=true")
	// findings= is what was PUBLISHED, and nothing was: the tmp file is not a report.
	mustContain(t, "the RUN KILLED line", killed, "findings=0")

	tmp := filepath.Join(b.jobDir(id), "RESULT.md.tmp")
	before, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("the half-written revision is left where the worker left it: %v", err)
	}
	mustContain(t, "the unpublished revision", string(before), "- finding 1:")
	if _, err := os.Stat(filepath.Join(b.pool, "reports", id, "NO-RESULT")); err != nil {
		t.Errorf("a job that published nothing wants its NO-RESULT marker: %v", err)
	}

	// THE TRIPWIRE: nothing this tool does opens RESULT.md.tmp. A mode that refuses its
	// own owner turns any open of it into an error, and triage and `result` go on
	// working -- which they could not do if either of them read it.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmp, 0o000); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(tmp, 0o644) }()
	}
	if exit, stdout, stderr = b.swarm("triage", "--pool", b.pool); exit != 0 {
		t.Fatalf("triage opened something it must not: exit %d\n%s%s", exit, stdout, stderr)
	}
	// Two: this job, and the ONE automatic re-queue of it (rule 7), which published nothing
	// either. A tmp file is not a report for either of them.
	mustContain(t, "triage", stdout, "reports=0 findings=0")
	mustContain(t, "triage", stdout, "no_result=2")
	if strings.Contains(stdout, "TRIAGE FINDING") {
		t.Errorf("a revision that was never published holds no findings for a page:\n%s", stdout)
	}
	exit, stdout, stderr = b.swarm("result", "--pool", b.pool, "--id", id)
	if exit != 1 {
		t.Errorf("`result --id` on a job that published nothing is REFUSED, got exit %d:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "NO-RESULT")
}

// SLOW: 1.0 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
// TestNativeRunKillsAtDeadline: a child that sleeps past the wall is killed by it, and the
// run records a non-zero exit rather than hanging.
func TestNativeRunKillsAtDeadline(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	start := time.Now()
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card: []byte("FAKE-SLEEP 60\n"), slotDir: slot, root: root, deadline: time.Second, noWall: true,
	}, &errOut)
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("a deadline kill is not a refusal, got exit %d:\n%s", code, errOut.String())
	}
	if res.rc == 0 {
		t.Fatalf("the deadline killed the child, and the run records a non-zero exit")
	}
	if elapsed > 30*time.Second {
		t.Fatalf("the deadline should cut the run short, but it took %v", elapsed)
	}
}

// SLOW: 25.2 s on hetzner at dev 64b9bec48, over the five-second line.
// TestNativeSamplerNeverOverlapsAndNeverDelaysTheDeadline: rule 13d, "no sample starts while
// one is unanswered", and "a usage reader that never returns does not move the deadline, and
// the run still ends inside the bound the deadline's own test holds (issue #779)".
//
// THE READER THAT NEVER RETURNS is a `sqlite3` on PATH that sleeps past every bound. The
// sampler gives one read 5 seconds and abandons it; the card's deadline is 3 seconds and is
// the thing under test, so a sampler that could hold the ending open would show here as a
// run that outlived its own deadline.
func TestNativeSamplerNeverOverlapsAndNeverDelaysTheDeadline(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	// A database must EXIST for the reader to be run at all: an absent one is an absence
	// and never a read.
	db := filepath.Join(slot, "data", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(swarm.SQLiteBinary, db)
	cmd.Stdin = strings.NewReader("CREATE TABLE message (id INTEGER PRIMARY KEY, data TEXT NOT NULL, time_created INTEGER NOT NULL);\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fixture store: %v\n%s", err, out)
	}
	// A `sqlite3` on PATH that never answers, ahead of the real one.
	slow := t.TempDir()
	stall := filepath.Join(slow, swarm.SQLiteBinary)
	if err := testbin.WriteExecutable(stall, []byte("#!/bin/sh\nsleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", slow+string(os.PathListSeparator)+os.Getenv("PATH"))

	card := filepath.Join(root, "card.md")
	if err := os.WriteFile(card, []byte("a card\nFAKE-IGNORE-TERM\nFAKE-SLEEP 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(budgetNativeArgs(t, bin, card, slot, root, "50000"), "--usage-interval", "1s")
	for i := range args {
		if args[i] == "--deadline" {
			args[i+1] = "3s"
		}
	}
	// THE EVENT, NOT THE CLOCK. The reader sleeps ten minutes and the card's deadline is
	// three seconds. What is asserted is that the run RETURNS and that its DEADLINE is what
	// ended the card: `rc=-1` with NO `stopped=` field, which is the deadline's own shape and
	// not a sampler's. A sampler that could hold the ending open would not reach either
	// assertion at all -- the go test timeout is this repo's bound on a hang, and it is a
	// better one than a number written here, which is why the repo refuses the number.
	var stdout, stderr bytes.Buffer
	run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	line := nativeOKLine(t, stdout.String())
	if got := fieldOf(line, "rc"); got != "-1" {
		t.Fatalf("the DEADLINE ended this card, so the line prints rc=-1; got %q:\n%s", got, line)
	}
	if got := fieldOf(line, "stopped"); got != "" {
		t.Fatalf("a reader that never answers is not three FAILED reads while the card still had time; the deadline ended it and the line carries no stopped= field, got %q:\n%s", got, line)
	}
}

// SLOW: 11.1 s on hetzner at dev 64b9bec48, over the five-second line.
// TestNativeThreeFailedReadsEndTheCardUnverifiable, and two then an answer end nothing.
//
// A READ THAT FAILS IS NOT A SOURCE THAT REPORTED NOTHING: the first ends a card, because a
// numeric budget the tool has stopped being able to see is a budget the caller believes is
// enforced and is not; the second leaves the budget unable to fire and the deadline to end
// the job.
func TestNativeThreeFailedReadsEndTheCardUnverifiable(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	bin := nativeHarness(t)
	for _, tc := range []struct {
		name     string
		failures int // how many refusals the reader gives before it answers
		wantRC   int
		wantStop string
		wantEnd  string
	}{
		{"three_in_a_row_ends_it", 1000, 1, "unverifiable", swarm.EndUnverifiable},
		{"twice_then_an_answer_ends_nothing", 2, 0, "", swarm.EndDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			// A DATABASE MUST EXIST for a read to be attempted at all: an absent one is an
			// absence, and rule 13d keeps the two apart.
			db := filepath.Join(slot, "data", "opencode", "opencode.db")
			if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(db, []byte("a database\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			// A reader that refuses its first `failures` calls and answers after that,
			// counting in a file of its own so the count survives across processes.
			dir := t.TempDir()
			counter := filepath.Join(dir, "calls")
			script := "#!/bin/sh\n" +
				"n=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
				"n=$((n+1)); echo $n > " + counter + "\n" +
				"if [ \"$n\" -le " + strconv.Itoa(tc.failures) + " ]; then echo 'Error: file is not a database' >&2; exit 1; fi\n" +
				"exit 0\n"
			if err := testbin.WriteExecutable(filepath.Join(dir, swarm.SQLiteBinary), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			card := filepath.Join(root, "card.md")
			if err := os.WriteFile(card, []byte("a card\nFAKE-SLEEP 8\nFAKE-FINDINGS 1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			args := append(budgetNativeArgs(t, bin, card, slot, root, "100000"), "--usage-interval", "1s")
			for i := range args {
				if args[i] == "--deadline" {
					args[i+1] = "60s"
				}
			}
			var stdout, stderr strings.Builder
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			if rc != tc.wantRC {
				t.Fatalf("this card exits %d, got %d\nstdout:\n%s\nstderr:\n%s", tc.wantRC, rc, stdout.String(), stderr.String())
			}
			if got := fieldOf(nativeOKLine(t, stdout.String()), "stopped"); got != tc.wantStop {
				t.Errorf("stopped= is %q, want %q:\n%s", got, tc.wantStop, stdout.String())
			}
			head, rows := usageRows(t, filepath.Join(slot, "jobs", "lbl"))
			if got := cell(t, head, rows[len(rows)-1], "end"); got != tc.wantEnd {
				t.Errorf("the last row carries end=%s, got %q", tc.wantEnd, got)
			}
		})
	}
}
