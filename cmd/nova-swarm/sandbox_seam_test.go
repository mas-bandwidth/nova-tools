package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// THE LAUNCH SEAM: every job runs inside nova-sandbox (docs/SPEC-SANDBOX.md, "the two
// callers": nova-swarm, at its launch seam). These are the tests that spec demands of the
// DISPATCHER caller, and each one names the platform it runs on and is skipped with a
// reason, never silently.
//
// Two kinds of test live here, and the difference matters:
//
//   - the SEAM tests, which are about the argv the dispatcher builds, the probe it runs
//     before the first worker and the one loud line of --no-sandbox. They run on every
//     platform against the fake sandbox on PATH, because the seam is the same argv on a
//     machine whose body is built and on one whose is not.
//   - the WALL tests, which ask the operating system whether a job can write outside its
//     job directory or read the key file. They run against the REAL nova-sandbox, on
//     darwin, which is the platform whose body this repository has built.

// wallOnly skips a test that needs a real wall, by name, on a platform whose body is not
// built. Rule 1 of SPEC-SANDBOX is that such a platform REFUSES rather than pretends, and a
// test that asserted a denial there would be asserting the refusal of the run, not the wall.
func wallOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skipf("the wall is asked about the operating system itself, and only the darwin body (sandbox-exec) is built in this repository; on %s nova-sandbox REFUSES and there is no wall to question", runtime.GOOS)
	}
}

// DEMANDED (SPEC-SANDBOX.md, the dispatcher caller; test 3's write half). A job writes only
// under its job directory. The worker asks for a file one directory above its own job --
// inside the slot, which is in the READ set -- and the operating system refuses it. The
// same write outside the wall is the control: --no-sandbox on the same task text lands the
// file, so no line of this test can pass by the write being impossible.
func TestAJobCannotWriteOutsideItsJobDir(t *testing.T) {
	wallOnly(t)
	b := newBench(t)
	outside := filepath.Join(b.dir, "outside-every-list")
	id := b.add("FAKE-TOUCH " + outside + "\nFAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("the pass exited %d, and a refused write is not a failed job:\n%s%s", exit, stdout, stderr)
	}
	log := harnessLog(t, b, id)
	if !strings.Contains(log, "touch "+outside+":") {
		t.Fatalf("the worker never tried the write this test is about:\n%s", log)
	}
	if strings.Contains(log, "touch "+outside+": ok") {
		t.Errorf("the job wrote outside its job directory and the wall did not stop it:\n%s", log)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("%s exists: the write landed", outside)
	}
	// The control, outside the wall: the same task text, --no-sandbox, and the file lands.
	id2 := b.add("FAKE-TOUCH " + outside + "\nFAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
	if exit, stdout, stderr := b.run("--no-sandbox"); exit != 0 {
		t.Fatalf("the control pass exited %d:\n%s%s", exit, stdout, stderr)
	}
	if log := harnessLog(t, b, id2); !strings.Contains(log, "touch "+outside+": ok") {
		t.Errorf("the CONTROL write outside the wall did not land, so the denial above proves nothing:\n%s", log)
	}
}

// DEMANDED (SPEC-SANDBOX.md rule 6 and the dispatcher caller's "No key is readable"). The
// key file is in neither list, so the job cannot read it -- while the key's VALUE, which the
// dispatcher read as data before the wrap, is in the child's environment and arrives. Both
// halves in one test, because either alone is the wrong shape: a job that cannot read the
// file and never got the key cannot work, and a job that got the key and can read the file
// is the failure #69 is about.
func TestAJobCannotReadTheKeyFile(t *testing.T) {
	wallOnly(t)
	b := newBench(t)
	id := b.add("FAKE-CAT " + b.keyFile + "\nFAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("the pass exited %d:\n%s%s", exit, stdout, stderr)
	}
	log := harnessLog(t, b, id)
	if !strings.Contains(log, "cat "+b.keyFile+":") {
		t.Fatalf("the worker never tried the read this test is about:\n%s", log)
	}
	if strings.Contains(log, "cat "+b.keyFile+": ok") {
		t.Errorf("the job read the key file from inside the wall:\n%s", log)
	}
	if !strings.Contains(log, "the key is present, length") {
		t.Errorf("the key did not reach the child by environment, which is rule 6's other half:\n%s", log)
	}
	if strings.Contains(log, fakeKey) {
		t.Errorf("the key VALUE is in the harness log")
	}
}

// DEMANDED (SPEC-SANDBOX.md test 23). `run` runs the probe ONCE before the first worker and
// refuses the pass when the wall is not there: a probe that fails is
// RUN REFUSED reason=sandbox_probe, a machine with no backend is
// RUN REFUSED reason=no_sandbox, and in both cases the worker count is zero -- not just the
// line. This runs on every platform, against the fake sandbox, because the refusal is the
// dispatcher's own and not the operating system's.
func TestRunRefusesWhenTheWallIsNotThere(t *testing.T) {
	for _, c := range []struct{ mode, reason string }{
		{"probefail", "sandbox_probe"},
		{"none", "no_sandbox"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			b := newBench(t)
			b.extraEnv = append(b.extraEnv, "NOVA_FAKE_SANDBOX="+c.mode)
			id := b.add("FAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
			exit, stdout, stderr := b.run("--sandbox", b.fakeSandbox)
			if exit == 0 {
				t.Errorf("a run with no wall exited 0:\n%s%s", stdout, stderr)
			}
			if want := "RUN REFUSED reason=" + c.reason; !strings.Contains(stdout+stderr, want) {
				t.Errorf("no %q in:\n%s%s", want, stdout, stderr)
			}
			if strings.Contains(stdout, "RUN START") {
				t.Errorf("a worker started under a refused run:\n%s", stdout)
			}
			// The task is still pending, and no job directory was ever made for it: the
			// count is zero, not just the line.
			if _, err := os.Stat(filepath.Join(b.pool, "pending", id+".task")); err != nil {
				t.Errorf("the task did not stay pending: %v", err)
			}
		})
	}
}

// DEMANDED (SPEC-SANDBOX.md rule 5 and test 23, against the REAL wall). The pool as a person
// types it is relative -- `--pool ./pool` is the line in README.md and in TESTS.md -- and
// `Pool.Dir` keeps it as typed. The probe's `--write` is built from it, so before the fix the
// wall refused `pool/sandbox-probe` as relative, `run` answered
// `RUN REFUSED reason=sandbox_probe` and the documented invocation started NO WORKER
// (DeepSeek's read of #88 at d0c1841, HIGH 1). This is the wall test that says NO: the same
// task, the same worker, the pool named relative to the caller's own directory.
func TestARelativePoolStillProvesTheWall(t *testing.T) {
	wallOnly(t)
	b := newBench(t)
	id := b.add("FAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
	// b.swarm runs with its working directory at b.dir, which is what a person has when
	// they type ./pool: the pool is b.dir/pool.
	exit, stdout, stderr := b.swarm(withSandbox([]string{
		"run", "--pool", "./pool", "--workers", "1", "--hours", "0.25", "--worker", b.worker,
	})...)
	if strings.Contains(stdout+stderr, "RUN REFUSED reason=sandbox_probe") {
		t.Fatalf("the wall refused its own probe because --pool was typed relative:\n%s%s", stdout, stderr)
	}
	if exit != 0 {
		t.Fatalf("the pass exited %d:\n%s%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "RUN START id="+id) {
		t.Errorf("no worker started under a relative pool:\n%s%s", stdout, stderr)
	}
}

// DEMANDED (SPEC-SANDBOX.md rule 11, "the one loud workaround"). --no-sandbox runs the jobs
// with no wall and says so ONCE PER JOB, on stderr, before the job starts. It is never a
// default and no environment variable turns it on.
func TestNoSandboxSaysSoOncePerJob(t *testing.T) {
	b := newBench(t)
	b.extraEnv = append(b.extraEnv, "NOVA_SWARM_NO_SANDBOX=1", "NOVA_NO_SANDBOX=1")
	first := b.add("FAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
	second := b.add("FAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
	exit, stdout, stderr := b.run("--no-sandbox")
	if exit != 0 {
		t.Fatalf("the pass exited %d:\n%s%s", exit, stdout, stderr)
	}
	loud := 0
	for _, line := range strings.Split(stdout+stderr, "\n") {
		if strings.HasPrefix(line, "RUN UNSANDBOXED ") {
			loud++
			if !strings.Contains(line, "no OS containment") {
				t.Errorf("the loud line does not say what is missing: %s", line)
			}
		}
	}
	if loud != 2 {
		t.Errorf("two jobs ran with no wall and the tool said so %d times:\n%s%s", loud, stdout, stderr)
	}
	for _, id := range []string{first, second} {
		if !strings.Contains(stdout, "id="+id) {
			t.Errorf("job %s never ran:\n%s", id, stdout)
		}
	}
	// No environment variable switches the wall off (rule 11): the same bench, with both
	// plausible names set and the flag GONE, wraps.
	if exit, stdout, stderr := b.swarm("run", "--pool", b.pool, "--workers", "1", "--hours", "0.1",
		"--worker", b.worker, "--sandbox", b.fakeSandbox); strings.Contains(stdout+stderr, "RUN UNSANDBOXED") {
		t.Errorf("an environment variable turned the wall off (exit %d):\n%s%s", exit, stdout, stderr)
	}
}

// DEMANDED (SPEC-SANDBOX.md test 24's argv half, and the dispatcher caller's two lists).
// The argv the dispatcher builds for a worker carries the worker home as --read, the job
// directory FIRST in --write with the data home beside it, the job directory as --cwd, and
// --net-deny nowhere -- the provider's API is the work. A TASK TEXT NAMING A DIRECTORY DOES
// NOT CHANGE IT: the argv is built by the dispatcher from the job it created, and this test
// plants the directory in the task text and compares.
func TestTheWorkerArgvIsTheDispatchersAndNotTheTasks(t *testing.T) {
	b := newBench(t)
	planted := filepath.Join(b.dir, "a-directory-the-task-named")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	id := b.add("write your scratch into " + planted + " and use --write " + planted + "\nFAKE-FINDINGS 1\nFAKE-USAGE 100 50 - - -\n")
	exit, stdout, stderr := b.run("--sandbox", b.fakeSandbox)
	if exit != 0 {
		t.Fatalf("the pass exited %d:\n%s%s", exit, stdout, stderr)
	}
	// The wrap recorded the argv it was handed, in the job directory it was pointed at.
	jobDir := filepath.Join(b.dir, "worker-home-1", "jobs", id)
	raw, err := os.ReadFile(filepath.Join(jobDir, "sandbox-argv"))
	if err != nil {
		t.Fatalf("the sandbox recorded no argv: %v", err)
	}
	var wrap string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, " -- ") {
			wrap = line
		}
	}
	if wrap == "" {
		t.Fatalf("no wrapped run in the recorded argv:\n%s", raw)
	}
	slotDir := filepath.Join(b.dir, "worker-home-1")
	for _, want := range []string{
		"--read " + slotDir,
		"--write " + jobDir,
		"--write " + filepath.Join(jobDir, "data"),
		"--cwd " + jobDir,
		"-- ",
	} {
		if !strings.Contains(wrap, want) {
			t.Errorf("the wrap argv does not carry %q:\n%s", want, wrap)
		}
	}
	if strings.Contains(wrap, planted) {
		t.Errorf("the task text put a directory in the worker's argv:\n%s", wrap)
	}
	if strings.Contains(wrap, "--net-deny") {
		t.Errorf("--net-deny is in the argv, and the provider's API is the work:\n%s", wrap)
	}
	if i, j := strings.Index(wrap, "--write "+jobDir), strings.Index(wrap, "--write "+filepath.Join(jobDir, "data")); i < 0 || j < 0 || i > j {
		t.Errorf("the job directory is not the FIRST --write, which is what the cwd and the temp directory default to:\n%s", wrap)
	}
}

// harnessLog is what the worker said, which is where a refused read or write appears.
func harnessLog(t *testing.T, b *bench, id string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(b.dir, "worker-home-*", "jobs", id, "harness.log"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no harness log for job %s: %v", id, err)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("reading %s: %v", matches[0], err)
	}
	return string(raw)
}
