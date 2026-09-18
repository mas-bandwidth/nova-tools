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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// EDGE 17. `nova-merge react` said `REACT enqueue pr=N`, exited 0, and enqueued nothing
// anybody could reach: react.go passed a nil Enqueue, NewReactor installed `SAdd
// merge:queue`, and nothing in this tree reads `merge:queue`. THE PROOF IS THAT THE
// ENTRY IS VISIBLE AFTERWARDS to the verb a person would look at it with.
func TestReactEnqueuesIntoTheLanesQueueWhereQueueStatusCanSeeIt(t *testing.T) {
	// Its lane, its bare repository, its fake host and its own miniredis are all this
	// test's: nothing here is shared, so nothing here may hold another test up.
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	if q := l.loadQueue(); !eqInts(q.Queued, []int{951}) {
		t.Fatalf("the lane's own order before react is %v, want [951]", q.Queued)
	}

	reactOnce(t, l, `{"number":952,"head":"a1b2","conclusion":"SUCCESS"}`, func(stdout string) {
		contains(t, stdout, "REACT enqueue pr=952")
	})

	// THE DOOR IS THE LANE'S QUEUE FILE, and `queue status` is where a person sees it.
	// Before this, the entry went into a redis set and `queue status` -- and `run`, and
	// every other verb in this tree -- would never have heard of it.
	exit, stdout, stderr := l.run("queue", "--lane", l.lane, "status")
	if exit != 0 {
		t.Fatalf("queue status: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "QUEUE ENTRY pos=2 entry=952 state=queued")
	contains(t, stdout, "queued=2")
	if q := l.loadQueue(); !eqInts(q.Queued, []int{951, 952}) {
		t.Errorf("the queue is %v, want [951 952]", q.Queued)
	}
}

// EDGE 18. Two hold mechanisms and two skip sets wearing the same words: with the lane
// held and the pull request in the lane's skip list, react still printed `REACT enqueue`,
// and only `enqueue:hold` in redis stopped it. ONE HOLD, ONE SKIP SET.
func TestReactObeysTheLanesOwnHoldAndSkipSet(t *testing.T) {
	// Its lane, its bare repository, its fake host and its own miniredis are all this
	// test's: nothing here is shared, so nothing here may hold another test up.
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	if exit, _, errb := l.run("queue", "--lane", l.lane, "skip", "952"); exit != 0 {
		t.Fatalf("skip: exit %d: %s", exit, errb)
	}
	reactOnce(t, l, `{"number":952,"head":"a1b2","conclusion":"SUCCESS"}`, func(stdout string) {
		contains(t, stdout, "REACT skip pr=952")
		absent(t, stdout, "REACT enqueue")
	})
	if q := l.loadQueue(); hasInt(q.Queued, 952) {
		t.Errorf("a pull request in the lane's skip set was enqueued: %v", q.Queued)
	}

	// Now the hold, over a pull request nothing skips.
	if exit, _, errb := l.run("queue", "--lane", l.lane, "hold", "the base is frozen", "--who", "rowan"); exit != 0 {
		t.Fatalf("hold: exit %d: %s", exit, errb)
	}
	reactOnce(t, l, `{"number":953,"head":"c3d4","conclusion":"SUCCESS"}`, func(stdout string) {
		contains(t, stdout, "REACT hold pr=953")
		contains(t, stdout, `the\x20base\x20is\x20frozen`)
		absent(t, stdout, "REACT enqueue")
	})
	if q := l.loadQueue(); hasInt(q.Queued, 953) {
		t.Errorf("a pull request was enqueued over the lane's hold: %v", q.Queued)
	}
}

// EDGE 19. `react` with neither --once nor --deadline ran a 60 s loop and exited 0,
// although its own help says one of them is required. It is refused BY NAME, the way
// `nova-work events` refuses the same shape.
func TestReactRefusesTheLoopFormWithNoDeadline(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	exit, stdout, stderr := l.run("react", "--redis", "127.0.0.1:0", "--lane", l.lane)
	if exit != 2 {
		t.Fatalf("a loop with no deadline is exit 2, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "--deadline")
	absent(t, stdout, "REACT OK")
}

// EDGE 17, the other half: --lane is the door, so it is required. A reactor with no lane
// has nowhere to put a pull request.
func TestReactRefusesWithNoLane(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	exit, stdout, stderr := l.run("react", "--redis", "127.0.0.1:0", "--once")
	if exit != 2 {
		t.Fatalf("react with no lane is exit 2, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "--lane is required")
	absent(t, stdout, "REACT OK")
}

// EDGE 7. `queue status` names the hold and the skip set, which nothing did: a lane
// standing still under a hold read exactly like a lane with nothing to do.
func TestQueueStatusNamesTheHoldAndTheSkipSet(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	writeQueue(t, l, merge.Queue{Queued: []int{951}, Skipped: []int{404}, Parked: []merge.Park{{PR: 404, Test: "TestFlake"}}})

	exit, stdout, stderr := l.run("queue", "--lane", l.lane, "status")
	if exit != 0 {
		t.Fatalf("queue status: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "QUEUE ENTRY pos=1 entry=951 state=queued")
	contains(t, stdout, "QUEUE ENTRY pos=- entry=404 state=parked")
	contains(t, stdout, "queued=1 skipped=1 parked=1 hold=-")

	if exit, _, errb := l.run("queue", "--lane", l.lane, "hold", "the base is frozen", "--who", "rowan"); exit != 0 {
		t.Fatalf("hold: exit %d: %s", exit, errb)
	}
	exit, stdout, stderr = l.run("queue", "--lane", l.lane, "status")
	if exit != 0 {
		t.Fatalf("queue status under a hold: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "hold=the\\x20base\\x20is\\x20frozen by=rowan")
}

// EDGE 11. `--who` was undocumented and optional, and a hold without it said
// `by=unknown` -- a hold whose owner nobody can ask.
func TestQueueHoldRefusesWithNoWho(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	exit, stdout, stderr := l.run("queue", "--lane", l.lane, "hold", "the base is frozen")
	if exit != 2 {
		t.Fatalf("a hold with no --who is exit 2, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "--who is required")
	if _, err := os.Stat(merge.HoldPath(l.lane)); err == nil {
		t.Error("the refusal wrote a hold file on the way past")
	}
}

// EDGE 8. `merge.ReadHold` was called in exactly one place -- the sweep -- so a hold
// stopped the ENQUEUE and not the pass that lands what is already queued, which is the
// half a person holding a lane actually means.
func TestRunRefusesWhileAHoldStands(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	if exit, _, errb := l.run("queue", "--lane", l.lane, "hold", "the base is frozen", "--who", "rowan"); exit != 0 {
		t.Fatalf("hold: exit %d: %s", exit, errb)
	}
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 2 {
		t.Fatalf("a held lane lands nothing: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "RUN REFUSED: a hold is standing")
	contains(t, stderr, "the\\x20base\\x20is\\x20frozen")
	contains(t, stderr, "release")
	absent(t, stdout, "MERGE OK")

	// And it runs again once the hold is released, so the refusal is the hold and not
	// a wall this verb cannot get past.
	if exit, _, errb := l.run("queue", "--lane", l.lane, "release"); exit != 0 {
		t.Fatalf("release: exit %d: %s", exit, errb)
	}
	if exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("after the release: exit %d\n%s\n%s", exit, stdout, stderr)
	}
}

// EDGE 9. `pass.go` swallowed LoadQueue's error, so a `queue.json` that does not parse
// left Order nil -- and a nil Order means "walk every entry in the lane's own order".
// A corrupt file SILENTLY UN-SKIPPED EVERYTHING, including every parked poison.
func TestACorruptQueueFileRefusesRatherThanUnSkippingEverything(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	path := filepath.Join(l.lane, merge.QueueName)
	if err := os.WriteFile(path, []byte("{not a queue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"run", "dry-run"} {
		args := []string{verb, "--lane", l.lane}
		if verb == "run" {
			args = append(args, "--once")
		}
		exit, stdout, stderr := l.run(args...)
		if exit != 2 {
			t.Errorf("%s over a corrupt queue is exit 2, got %d\n%s\n%s", verb, exit, stdout, stderr)
		}
		contains(t, stderr, merge.QueueName)
		absent(t, stdout, "MERGE OK")
	}
	// And `queue status` names it too rather than reporting an empty queue.
	exit, _, stderr := l.run("queue", "--lane", l.lane, "status")
	if exit != 2 {
		t.Errorf("queue status over a corrupt queue is exit 2, got %d: %s", exit, stderr)
	}
	contains(t, stderr, merge.QueueName)
}

// EDGE 6. `p.Order` was set only in the run path, so `dry-run` -- the verb that exists to
// print what a pass would do -- walked a DIFFERENT order from the pass and listed a
// skipped pull request in its plan.
func TestDryRunWalksTheQueuesOrderLikeRunDoes(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	if exit, _, errb := l.run("queue", "--lane", l.lane, "skip", "951"); exit != 0 {
		t.Fatalf("skip: exit %d: %s", exit, errb)
	}
	exit, stdout, stderr := l.run("dry-run", "--lane", l.lane)
	if exit != 0 {
		t.Fatalf("dry-run REPORTS: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	// The entry is skipped, so the survey must not plan it. `run` would not walk it
	// either, and the whole point of the survey is that the two agree.
	if strings.Contains(stdout, "entry=951") {
		t.Errorf("dry-run planned a skipped entry; run would not walk it:\n%s", stdout)
	}
}

// EDGE 10. `front` only reorders numbers in a file, and it read the pull request and its
// checks from the forge first -- so the one local verb of this family refused on a bench
// with no gh, over a judgement `run` makes again on every pass anyway.
func TestQueueFrontIsLocalLikeSkipAndUnskip(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	setupBranchEntry(t, l, 952, "feature-b")
	setupBranchEntry(t, l, 953, "feature-c")
	// THE FORGE IS BROKEN for the whole of this test. front must not notice.
	l.host.Err = errAnyHost{}

	exit, stdout, stderr := l.run("queue", "--lane", l.lane, "front", "953")
	if exit != 0 {
		t.Fatalf("front is local: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "QUEUE FRONT entry=953 position=1")
	if q := l.loadQueue(); !eqInts(q.Queued, []int{953, 951, 952}) {
		t.Errorf("the order is %v, want [953 951 952]", q.Queued)
	}
	// It still refuses a pull request this lane does not hold, which is the one thing
	// it can know by itself.
	if exit, _, errb := l.run("queue", "--lane", l.lane, "front", "404"); exit != 2 {
		t.Errorf("front on an entry not in the lane is exit 2, got %d: %s", exit, errb)
	}
}

// EDGE 1. With go1.22 on PATH and a go.mod asking for 1.26, the whole gate ran and the
// failure surfaced as `step=build reason="go: downloading go1.26 (linux/amd64)"` -- a
// progress NOTICE naming nothing to fix. The notice is never the news.
func TestGoNoticesAreNotTheFailure(t *testing.T) {
	t.Parallel()
	out := "go: downloading go1.26 (linux/amd64)\ngo: downloading golang.org/x/mod v0.1.0\n# example.com/batch/pkg/c\npkg/c/c.go:3: undefined: X\n"
	if got := firstLine(dropGoNotices(out), nil); got != "# example.com/batch/pkg/c" {
		t.Errorf("the reason is %q; a `go: downloading` line is a notice and never the failure", got)
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
	contains(t, stdout, "members=none")
	contains(t, stdout, "dropped=1,3")
	contains(t, stdout, "checks=required")
	absent(t, stderr, "BATCH MERGED")
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
	src, err := os.ReadFile("react.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	silence := strings.Index(body, "silenceRedis()")
	dial := strings.Index(body, "deps.Dial(")
	switch {
	case silence < 0:
		t.Fatal("react.go no longer silences go-redis's own logger; five raw pool.go lines came out ahead of a one-line refusal the day it did not")
	case dial < 0:
		t.Fatal("react.go no longer dials; if the verb moved, move this check with it")
	case silence > dial:
		t.Error("react.go dials before it silences the logger; the chatter this exists to stop is written while the connection is being made")
	}
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

// reactOnce runs `react --once` against a miniredis of this test's own, republishing the
// payload until the reactor has consumed it -- which is what a durable bus would do with
// the message instead -- and then hands the check the verb's stdout.
func reactOnce(t *testing.T, l *lab, payload string, check func(stdout string)) {
	t.Helper()
	mr := miniredis.RunT(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	type result struct {
		exit   int
		stdout string
		stderr string
	}
	done := make(chan result, 1)
	go func() {
		exit, stdout, stderr := l.run("react", "--redis", mr.Addr(), "--lane", l.lane, "--once", "--deadline", "30")
		done <- result{exit, stdout, stderr}
	}()
	stop := time.After(reactWait())
	for {
		select {
		case r := <-done:
			if r.exit != 0 {
				t.Fatalf("react --once: exit %d\n%s\n%s", r.exit, r.stdout, r.stderr)
			}
			check(r.stdout)
			return
		case <-stop:
			t.Fatalf("react never consumed the message within %s", reactWait())
		default:
		}
		if err := rdb.Publish(ctx, ci.ChannelPRChecksDone, payload).Err(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
