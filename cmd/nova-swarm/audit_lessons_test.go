package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE NEW-USER AUDIT (2026-09-11). Each test below is one footgun or one stumble a person
// meeting this tool for the first time actually hit, written as the assertion that would
// have stopped it.

// F3: the PROMPT.md path did not resolve from the harness's cwd. `supervise` sets the
// child's directory to the SLOT and hands it a job path built from the dispatcher's own
// cwd, so a RELATIVE `worker_dir` -- the style the README teaches -- gave the harness a
// path that does not exist from where it stands. Two jobs, rc=0, `result=no-result
// dest=failed`, under a RUN OK byte-identical to a successful pass. The suite could not see
// it because every test used an absolute t.TempDir().
func TestARelativeWorkerDirWorksFromTheSlot(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker under a relative worker_dir\nFAKE-FINDINGS 1\n")
	b.rewriteWorker(func(d map[string]any) { d["worker_dir"] = "worker-home" })

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN DONE id="+id)
	mustContain(t, "the run", stdout, "result=ok findings=1")
	mustContain(t, "the run", stdout, "dest=done")
	if _, err := os.Stat(filepath.Join(b.pool, "reports", id, "RESULT.md")); err != nil {
		t.Errorf("the worker's report belongs beside the pool: %v", err)
	}
}

// F6 / lesson 83: "a negative ceiling is refused (0 already means all; a negative number is
// a typo with two readings)". `--max -1` listed a whole pool with no MORE line and exit 0 --
// a typo on the one flag whose job is to bound output un-bounding it, on the largest state.
func TestANegativeCeilingIsRefusedOnEveryListing(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.add("a task to list\n")
	for _, verb := range []string{"status", "triage", "cost", "reclaim"} {
		args := []string{verb, "--pool", b.pool, "--max", "-1"}
		if verb == "reclaim" {
			args = append(args, "--done")
		}
		exit, stdout, stderr := b.swarm(args...)
		if exit != 2 {
			t.Errorf("`%s --max -1` exits %d, want 2:\n%s%s", verb, exit, stdout, stderr)
			continue
		}
		mustContain(t, verb+"'s refusal", stderr, "--max")
		mustContain(t, verb+"'s refusal", stderr, "0 already means all")
	}
}

// S5: `--pool` on a missing directory named no remedy, and `quickstart` is exactly the
// verb that makes one.
func TestAMissingPoolNamesTheVerbThatMakesOne(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	missing := filepath.Join(b.dir, "nopool")
	exit, stdout, stderr := b.swarm("status", "--pool", missing)
	if exit != 2 {
		t.Fatalf("status on a missing pool exits %d, want 2:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "nova-swarm quickstart --pool "+missing)
}

// S7: `ADD OK` echoed `tokens=` and not `files=`, so the half a caller cannot re-derive
// from the line was the half not on it. Both budgets are required; both are printed.
func TestAddEchoesBothBudgets(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	file := filepath.Join(b.dir, "a-task.md")
	write(t, file, "a task\n")
	exit, stdout, stderr := b.swarm("add", "--pool", b.pool, "--task", file, "--files", "5", "--tokens", "100000")
	if exit != 0 {
		t.Fatalf("add exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "ADD OK", stdout, "files=5")
	mustContain(t, "ADD OK", stdout, "tokens=100000")
}

// S6 / lesson 119: "Two shapes may never share one token." `TRIAGE BATCH … reports=6`
// printed three lines above `TRIAGE OK reports=2`, counting two different things.
func TestOneTokenHasOneMeaningOnAPage(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.add("a job that is folded\nFAKE-FINDINGS 1\n")
	b.add("a job with no report at all\nFAKE-NORESULT\n")
	if exit, stdout, stderr := b.run("--workers", "2"); exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	exit, stdout, stderr := b.swarm("triage", "--pool", b.pool)
	if exit != 0 {
		t.Fatalf("triage exited %d: %s%s", exit, stdout, stderr)
	}
	batch := lineWith(t, stdout, "TRIAGE BATCH")
	ok := lineWith(t, stdout, "TRIAGE OK")
	if !strings.Contains(batch, "reports=1") {
		t.Errorf("TRIAGE BATCH counts the jobs that HAVE a report:\n%s", batch)
	}
	if strings.Contains(ok, "reports=") {
		t.Errorf("`reports=` already means one thing on this page; TRIAGE OK counts what it FOLDED:\n%s", ok)
	}
	mustContain(t, "TRIAGE OK", ok, "folded=1")
}

// F5 / lesson 23: the only diagnosis of a failed job was printed NOWHERE and then deleted.
// The harness's own words -- `401 unauthorized` -- land in <job>/harness.log, no verb
// printed them, and `reclaim` removes the log with the directory. A line whose key is wrong
// had no printed route to the word `unauthorized`.
func TestAFailedJobPrintsTheHarnesssOwnWords(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add("a worker whose provider refuses its key\nFAKE-SAY error: 401 unauthorized\nFAKE-NORESULT\n")
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	// One bounded, escaped field on the event line: the run says what the harness said.
	run := lineWith(t, stdout, "RUN DONE id="+id)
	mustContain(t, "the RUN DONE line of a job with no result", run, "401 unauthorized")
	if strings.Count(run, "\n") != 0 {
		t.Errorf("an event line is ONE line:\n%q", run)
	}

	// And `result --id` hands over the transcript itself, raw, beneath its one event line.
	exit, stdout, stderr = b.swarm("result", "--pool", b.pool, "--id", id)
	if exit != 1 {
		t.Fatalf("`result --id` on a job that published nothing is REFUSED, got %d", exit)
	}
	mustContain(t, "the refusal", stderr, "RESULT REFUSED")
	mustContain(t, "the transcript beneath it", stderr, "401 unauthorized")
}

// F9: the failure path leaked disk. `reclaim --done` swept only done/, so a pass where
// everything failed needed one `reclaim --task <id>` per failure.
func TestReclaimSweepsTheFailedJobsToo(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	ok := b.add("a job that finishes\nFAKE-FINDINGS 1\n")
	bad := b.add("a job that publishes nothing\nFAKE-NORESULT\n")
	if exit, stdout, stderr := b.run("--workers", "2"); exit != 0 {
		t.Fatalf("run exited %d: %s%s", exit, stdout, stderr)
	}
	exit, stdout, stderr := b.swarm("reclaim", "--pool", b.pool, "--failed")
	if exit != 0 {
		t.Fatalf("`reclaim --failed` exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "reclaim --failed", stdout, "RECLAIM OK id="+bad)
	if strings.Contains(stdout, ok) {
		t.Errorf("`--failed` sweeps failed/ and nothing else:\n%s", stdout)
	}
	exit, stdout, stderr = b.swarm("reclaim", "--pool", b.pool, "--all")
	if exit != 0 {
		t.Fatalf("`reclaim --all` exited %d: %s%s", exit, stdout, stderr)
	}
	mustContain(t, "reclaim --all", stdout, "RECLAIM OK id="+ok)
}

// S1 and S2: the one file a first run cannot start without was the one with no template.
// `template --name worker` prints a complete worker description, and the tool runs the one
// it prints.
func TestTemplateWorkerPrintsADescriptionThisToolAccepts(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	exit, stdout, stderr := b.swarm("template", "--name", "worker")
	if exit != 0 {
		t.Fatalf("`template --name worker` exited %d: %s%s", exit, stdout, stderr)
	}
	for _, field := range []string{"name", "provider", "model", "env_var", "key_file", "usage",
		"harness", "harness_args", "{model}", "{prompt}", "worker_dir", "deadline"} {
		mustContain(t, "the worker template", stdout, field)
	}
	// It is not prose: it is a description this tool reads. Filled in with this bench's own
	// fake harness and key, `run` accepts it.
	filled := strings.NewReplacer(
		"<the harness command on PATH>", "fake-harness",
		"<the model id>", "fake-model",
		"<the NAME of the variable the provider reads>", "FAKE_KEY",
		"<the path of a file holding one line, mode 0600>", b.keyFile,
		"<the home copy of this worker's own directory>", filepath.Join(b.dir, "worker-home"),
	).Replace(stdout)
	path := filepath.Join(b.dir, "from-template.json")
	write(t, path, filled)
	b.add("a task the template's worker runs\nFAKE-FINDINGS 1\n")
	exit, stdout, stderr = b.swarm("run", "--pool", b.pool, "--workers", "1", "--hours", "0.25", "--worker", path)
	if exit != 0 {
		t.Fatalf("the description this tool PRINTS is one it reads; run exited %d:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN DONE")
}

// S3: the harness contract was undocumented -- cwd, argv, NOVA_SWARM_JOB, RESULT.md -- and
// the audit learned it by dumping the fake harness's own environment. The README says it,
// and names the fake harness that already demonstrates it.
func TestTheReadmeCarriesTheHarnessContract(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{"### The harness contract", "NOVA_SWARM_JOB", "RESULT.md",
		"cmd/nova-swarm/testdata/fakeharness", "harness_args"} {
		if !strings.Contains(body, want) {
			t.Errorf("the README's harness contract wants %q", want)
		}
	}
}
