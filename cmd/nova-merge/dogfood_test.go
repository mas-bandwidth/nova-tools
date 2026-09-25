package main

// dogfood_test.go is the red-test half of the merge lane's dogfood pass, 2026-09-18: a
// non-author drove `nova-merge batch`, `nova-merge queue` and `nova-merge react` against
// this repository and wrote down every edge they fell off. Each test below names the
// edge it pins and the thing that went wrong, so the day one comes back a reader knows
// what it cost the first time.
//
// Every test here drives run() with the lab's deps: a real git against a bare fixture
// repository, a fake host, a fake clock and -- for react -- miniredis. Nothing opens a
// socket to anything.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// EDGE 1. With go1.22 on PATH and a go.mod asking for 1.26, the whole gate ran and the
// failure surfaced as `step=build reason="go: downloading go1.26 (linux/amd64)"` -- a
// progress NOTICE naming nothing to fix. The notice is never the news.
func TestGoNoticesAreNotTheFailure(t *testing.T) {
	t.Parallel()
	out := "go: downloading go1.26 (linux/amd64)\ngo: downloading golang.org/x/mod v0.1.0\n# example.com/batch/pkg/c\npkg/c/c.go:3: undefined: X\n"
	if got := firstLine(dropGoNotices(out), nil); got != "# example.com/batch/pkg/c" {
		t.Errorf("the reason is %q; a `go: downloading` line is a notice and never the failure", got)
	}
	_, _, reason := stepFailure(batchStep{name: "build", command: "go build ./..."}, out, nil)
	if strings.Contains(reason, "go: downloading") {
		t.Errorf("BATCH FAIL reason = %q; a `go: downloading` line is a notice and never the failure", reason)
	}
	if !strings.Contains(reason, "undefined: X") {
		t.Errorf("BATCH FAIL reason = %q; the compiler line must survive once the notices are dropped (#2499 item 3)", reason)
	}
	// A step whose output is ONLY notices still says what it said, rather than nothing.
	only := "go: downloading go1.26 (linux/amd64)\n"
	if got := firstLine(dropGoNotices(only), nil); got == "" || got == "(no output)" {
		t.Errorf("an output of nothing but notices printed %q; a reader must still see what the step said", got)
	}
}

// EDGE 1, the version comparison behind the refusal: go1.9 is older than go1.22, which a
// string compare gets backwards.
func TestGoVersionsCompareNumerically(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		have, want string
		older      bool
	}{
		{"1.22", "1.26", true},
		{"1.9", "1.22", true},
		{"1.26.5", "1.26", false},
		{"1.26", "1.26.5", true},
		{"1.26.5", "1.26.5", false},
		{"2.0", "1.26", false},
	} {
		if got := olderThan(c.have, c.want); got != c.older {
			t.Errorf("olderThan(%q, %q) = %v, want %v", c.have, c.want, got, c.older)
		}
	}
}

// EDGE 1, the refusal itself: a tree whose go.mod asks for a go this machine has not got
// is ONE refusal with the remedy, before the first merge, and not a red build step.
func TestBatchRefusesAToolchainThisMachineHasNot(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	// The fixture's base asks for a go nobody has. It is read out of the CLONE, so it
	// is set at the remote and the batch finds it after the checkout.
	l.git(l.work, "checkout", "-q", "dev")
	l.write("go.mod", "module example.com/batch\n\ngo 99.1\n")
	l.commit("a go nobody has")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")
	l.git(l.work, "checkout", "-q", "main")

	exit, stdout, stderr := l.run("batch", "--name", "integration-tc", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "dev", "--timeout", "5m")
	if exit != 2 {
		t.Fatalf("a toolchain this machine has not is exit 2, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH REFUSED")
	contains(t, stderr, "go99.1")
	contains(t, stderr, "go.mod")
	// It refuses BEFORE the merges, so no member was ever merged.
	absent(t, stderr, "BATCH MERGED")
	absent(t, stdout, "BATCH OK")
}

// EDGE 2. `BATCH SKIP lisp reason="sbcl is not on this machine"` went to stderr and
// `BATCH OK` said nothing about it, so the one line a caller parses claimed a green gate
// over a suite that ran three of its four steps. --require-lisp is for the caller who
// needs that step RUN.
func TestBatchRequireLispFailsWhenTheStepCannotRun(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	exit, stdout, stderr := l.run("batch", "--name", "integration-lisp", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "dev",
		"--timeout", "5m", "--require-lisp")
	if exit != 1 {
		t.Fatalf("--require-lisp over a checkout with no lisp suite is exit 1, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH FAIL")
	contains(t, stdout, "step=lisp")
	contains(t, stdout, "skipped=lisp")
	contains(t, stdout, "--require-lisp")
	absent(t, stdout, "BATCH OK")
	// It refused the SUITE, not the merge: the members still merged, so a caller who
	// installs sbcl and runs it again is judging the same tree.
	contains(t, stderr, "BATCH MERGED #1")
}

// EDGE 25 (batch 7). Three members were green under this gate on linux and red on CI's
// windows legs, and the batch pull request went red after the gate had said OK. A member
// whose OWN head has no green ci-ok is dropped BEFORE the merge, by name.
func TestBatchDropsAMemberWhoseOwnHeadIsNotGreen(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	// #1's own head is red on ci-ok; #3's has no ci-ok at all. Neither may be merged.
	l.host.SetCheckRuns(l.heads[1], merge.CheckDetail{Name: "ci-ok", Conclusion: "failure", SHA: l.heads[1]})
	l.host.SetCheckRuns(l.heads[3], merge.CheckDetail{Name: "build", Conclusion: "success", SHA: l.heads[3]})

	exit, stdout, stderr := l.run("batch", "--name", "integration-checks", "--pr", "1,3",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "dev", "--timeout", "5m")
	if exit != 0 {
		t.Fatalf("a batch whose members were all dropped still runs its gate: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+l.heads[1]+" has no green ci-ok (state=failure)\"")
	contains(t, stderr, "BATCH DROP #3 reason=\"head "+l.heads[3]+" has no green ci-ok (state=none)\"")
	contains(t, stderr, "check=ci-ok")
	contains(t, stdout, "members=none")
	contains(t, stdout, "dropped=1,3")
	contains(t, stdout, "checks=required")
	contains(t, stdout, "check=ci-ok")
	absent(t, stderr, "BATCH MERGED")
}

// #2499 / #2508. Schema's required check is named `tests`, not `ci-ok`. A member whose
// own head is green on tests and has no ci-ok is DROPPED today, because the gate looks
// for nova-tools' rollup. After the patch, --check-name tests (or .nova-merge
// required-check=tests) keeps it, and the name is on the BATCH OK / DROP receipt so a
// lane script can parse it. Default remains ci-ok.
func TestBatchDropsAMemberGreenOnTestsWhenTheRequiredCheckIsCiOk(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	l.host.SetCheckRuns(l.heads[1], merge.CheckDetail{Name: "tests", Conclusion: "success", SHA: l.heads[1]})

	exit, stdout, stderr := l.run("batch", "--name", "integration-schema-drop", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "dev", "--timeout", "5m")
	if exit != 0 {
		t.Fatalf("a batch whose members were all dropped still runs its gate: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+l.heads[1]+" has no green ci-ok (state=none)\"")
	contains(t, stderr, "check=ci-ok")
	contains(t, stdout, "members=none")
	contains(t, stdout, "dropped=1")
	contains(t, stdout, "check=ci-ok")
	absent(t, stderr, "BATCH MERGED")
}

func TestBatchCheckNameKeepsAMemberGreenOnThatCheck(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	l.host.SetCheckRuns(l.heads[1], merge.CheckDetail{Name: "tests", Conclusion: "success", SHA: l.heads[1]})

	exit, stdout, stderr := l.run("batch", "--name", "integration-schema-flag", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "dev", "--timeout", "5m",
		"--check-name", "tests")
	if exit != 0 {
		t.Fatalf("--check-name tests keeps a member green on tests: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH MERGED #1")
	contains(t, stdout, "members=1")
	contains(t, stdout, "dropped=none")
	contains(t, stdout, "checks=required")
	contains(t, stdout, "check=tests")
	absent(t, stdout, "check=ci-ok")
	absent(t, stderr, "BATCH DROP #1")
}

func TestBatchNovaMergeFileNamesTheRequiredCheck(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	l.host.SetCheckRuns(l.heads[1], merge.CheckDetail{Name: "tests", Conclusion: "success", SHA: l.heads[1]})
	l.git(l.work, "checkout", "-q", "dev")
	l.write(".nova-merge", "required-check=tests\n")
	l.commit("schema required-check")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")

	exit, stdout, stderr := l.run("batch", "--name", "integration-schema-file", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "dev", "--timeout", "5m")
	if exit != 0 {
		t.Fatalf(".nova-merge required-check=tests keeps a member green on tests: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH MERGED #1")
	contains(t, stdout, "members=1")
	contains(t, stdout, "dropped=none")
	contains(t, stdout, "check=tests")
	absent(t, stdout, "check=ci-ok")
	absent(t, stderr, "BATCH DROP #1")
}

func TestBatchCheckNameOverridesTheNovaMergeFile(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	l.host.SetCheckRuns(l.heads[1], merge.CheckDetail{Name: "tests", Conclusion: "success", SHA: l.heads[1]})
	l.git(l.work, "checkout", "-q", "dev")
	l.write(".nova-merge", "required-check=ci-ok\n")
	l.commit("nova-tools required-check")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")

	exit, stdout, stderr := l.run("batch", "--name", "integration-schema-override", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "dev", "--timeout", "5m",
		"--check-name", "tests")
	if exit != 0 {
		t.Fatalf("--check-name wins over .nova-merge: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH MERGED #1")
	contains(t, stdout, "check=tests")
	absent(t, stdout, "check=ci-ok")
}

func TestBatchRefusesAnEmptyRequiredCheckInNovaMerge(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	l.git(l.work, "checkout", "-q", "dev")
	l.write(".nova-merge", "required-check=\n")
	l.commit("empty required-check")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")

	exit, stdout, stderr := l.run("batch", "--name", "integration-empty-check", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "dev", "--timeout", "5m")
	if exit != 2 {
		t.Fatalf("an empty required-check is BATCH REFUSED at exit 2, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH REFUSED")
	contains(t, stderr, "required-check")
	absent(t, stdout, "BATCH OK")
}

// EDGE 25, the override: --no-require-checks merges whatever the caller named and SAYS
// SO on the verdict line, so a green batch never hides which admission it used.
func TestBatchNoRequireChecksIsSaidOutLoud(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	l.host.SetCheckRuns(l.heads[1], merge.CheckDetail{Name: "ci-ok", Conclusion: "failure", SHA: l.heads[1]})

	exit, stdout, stderr := l.run("batch", "--name", "integration-waived", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "dev",
		"--timeout", "5m", "--no-require-checks")
	if exit != 0 {
		t.Fatalf("a waived batch that goes green is exit 0, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH NOTE checks=waived")
	contains(t, stderr, "BATCH MERGED #1")
	contains(t, stdout, "checks=waived")
	contains(t, stdout, "members=1")
}

// EDGE 25, the exemption a batch's own pull request needs (#1347's receipt, read here).
// The gate must not refuse a member that IS a gated tree: a batch branch is the gate's own
// evidence, and so is a BATCH OK line naming that member's head. Without this, a batch
// pull request whose CI is still running -- which is every batch pull request in the
// minutes after it is opened -- could never be a member of the next batch.
func TestBatchAdmitsAMemberTheGateItselfVouchedFor(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	// #1's own head is red on ci-ok, and it arrives on a batch's own branch.
	l.host.SetCheckRuns(l.heads[1], merge.CheckDetail{Name: "ci-ok", Conclusion: "failure", SHA: l.heads[1]})
	pr := l.host.PRs[1]
	pr.HeadRef = merge.BatchBranchPrefix + "6"
	l.host.PRs[1] = pr

	exit, stdout, stderr := l.run("batch", "--name", "integration-branch", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "dev", "--timeout", "5m")
	if exit != 0 {
		t.Fatalf("a member on a batch branch is admitted: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH NOTE #1 checks=batch-branch")
	contains(t, stdout, "members=1")
	contains(t, stdout, "checks=required")

	// And the receipt, for a member that is not on a batch branch: the same BATCH OK line
	// `nova-merge land` takes, read by the same parser.
	l2 := batchRepo(t)
	l2.host.SetCheckRuns(l2.heads[1], merge.CheckDetail{Name: "ci-ok", Conclusion: "failure", SHA: l2.heads[1]})
	receipt := filepath.Join(l2.dir, "receipt.txt")
	body := "BATCH STEP build command=\"go build ./...\"\n" +
		"BATCH OK name=integration-6 base=" + strings.Repeat("b", 40) + " head=" + l2.heads[1] + " members=7 dropped=none\n"
	if err := os.WriteFile(receipt, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr = l2.run("batch", "--name", "integration-receipt", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l2.dir, "batch"), "--base", "dev",
		"--timeout", "5m", "--receipt-file", receipt)
	if exit != 0 {
		t.Fatalf("a member a receipt vouches for is admitted: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH NOTE #1 checks=receipt")
	contains(t, stdout, "members=1")

	// A --receipt-file that holds no receipt at all is a refusal, not a silent empty set:
	// a caller who presented evidence and had it ignored would read a ci-ok refusal and
	// have no idea why.
	empty := filepath.Join(l2.dir, "not-a-receipt.txt")
	if err := os.WriteFile(empty, []byte("BATCH FAIL name=x step=test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if exit, _, errb := l2.run("batch", "--name", "integration-empty", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l2.dir, "batch"), "--base", "dev",
		"--timeout", "5m", "--receipt-file", empty); exit != 2 {
		t.Errorf("a --receipt-file with no BATCH OK line is exit 2, got %d: %s", exit, errb)
	}
}

// EDGE 3. The help said the test step runs `go test -json -count=1 -timeout 5m` and the
// step has carried no -timeout since integration-4: the Makefile's target does not set
// one, so neither does the gate. A help that describes a command the tool does not run is
// a document that sends a reader looking for a flag that is not there.
func TestTheHelpDescribesTheCommandTheGateRuns(t *testing.T) {
	t.Parallel()
	command := strings.Join(ciTestArgs(), " ")
	if !strings.Contains(usage, command) {
		t.Errorf("the help does not carry the gate's own test command %q; it describes a command this tool does not run", command)
	}
	if strings.Contains(usage, "-timeout 5m") {
		t.Error("the help still says the test step runs with -timeout 5m, and it has not since integration-4")
	}
}

// EDGE 16. Five raw `redis: ... pool.go` lines landed on stderr ahead of this verb's own
// clean refusal. The library's diagnostics are not this tool's output grammar, and the
// silencer is installed before the first dial of the verb that dials.
func TestTheRedisLoggerIsSilencedBeforeTheFirstDial(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("verbs.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	silence := strings.Index(body, "silenceRedis()")
	dial := strings.Index(body, "deps.Dial(")
	switch {
	case silence < 0:
		t.Fatal("verbs.go (read --redis) no longer silences go-redis's own logger; five raw pool.go lines came out ahead of a one-line refusal the day it did not")
	case dial < 0:
		t.Fatal("verbs.go (read --redis) no longer dials; if the verb moved, move this check with it")
	case silence > dial:
		t.Error("verbs.go (read --redis) dials before it silences the logger; the chatter this exists to stop is written while the connection is being made")
	}
}

// #1609. `silenceRedis` was `redis.SetLogger(quietRedis{})` run on every `react`, and
// `redis.SetLogger` writes a package-level variable inside go-redis. Two `react` runs in
// one process -- which is what this package's own tests are, `reactOnce` starting the
// verb in a goroutine while the package runs in parallel -- both write it, and
// `go test -race ./cmd/nova-merge/` caught it one run in six on vision. `-race` is a
// certification leg, and ten tests failed behind that one race.
//
// The property is the tool's, not the harness's: NO TWO VERBS IN ONE PROCESS MAY RACE ON
// THE LIBRARY'S LOGGER. This drives the silencer itself from several goroutines at once,
// so the detector fires on every -race run rather than one in six.
func TestSilencingTheRedisLoggerIsNotARaceBetweenTwoVerbs(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			silenceRedis()
		}()
	}
	wg.Wait()
}

// --- the fixtures these tests share -------------------------------------------------

// errAnyHost is a host error a test installs to prove a verb never asked the host.
type errAnyHost struct{}

func (errAnyHost) Error() string {
	return "this test broke the forge on purpose; a verb that reached it was not supposed to"
}

// writeQueue puts an exact queue on disk, which is the state under test.
func writeQueue(t *testing.T, l *lab, q merge.Queue) {
	t.Helper()
	if err := merge.SaveQueue(l.lane, &q); err != nil {
		t.Fatal(err)
	}
}

// setupBranchEntry adds one more pull request entry to a lane setupPR already made.
func setupBranchEntry(t *testing.T, l *lab, n int, branch string) {
	t.Helper()
	oid := l.branch(branch, branch+".txt", "the "+branch+" change\n", "a change on "+branch)
	l.host.PRs[n] = merge.PR{Number: n, Author: "pat", Base: "main", HeadRef: branch,
		HeadOID: oid, Mergeable: "MERGEABLE"}
	l.host.SetChecks(oid, 3, 0)
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", itoa(n)); exit != 0 {
		t.Fatalf("add %d: exit %d: %s", n, exit, errb)
	}
}
