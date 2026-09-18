package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// THE LANDING, TESTED RED FIRST.
//
// Every test here drives `batch --land` through run(), against a REAL git and the bare
// fixture repository of helpers_test.go, and against the fake forge. Nothing opens a
// socket: the clone URL is Deps.RepoURL, which the lab points at its own bare repository,
// the forge is merge.FakeHost, and the queue's edge is the same fakeLandEnqueue `land`'s
// own tests drive.
//
// NO TEST HERE WAITS. The poll between forge reads is Deps.Sleep, which the lab implements
// by moving its own clock forward, so a landing that polls six times costs six additions
// and no wall time (Glenn's two-minute rule). A test that really slept would be a test
// nobody runs.

// landPRNumber is the number the fake forge gives the FIRST pull request it opens:
// NewFakeHost starts its counter at 9000 and OpenPR increments before it uses it.
const landPRNumber = 9001

// landDev is the merge commit the fake queue reports on the base.
var landDev = strings.Repeat("d", 40)

// landScript is the forge's answers about the BATCH's own head, read in order, the last
// answer repeating for ever. A landing is a sequence of reads of a forge that is changing
// underneath it -- a check that goes green, an entry that leaves the queue, a pull request
// that becomes merged -- and a test that could not say "on the third read" could not test
// any of it.
type landScript struct {
	*lab
	root string
	name string
	// ciOK is the conclusion the forge reports for ci-ok on the batch's head, one per read.
	ciOK []string
	// queue is the merge-queue state per read; "" is an entry the queue does not hold.
	queue []string
	// mergedAfter is how many reads of the BATCH's pull request happen before the forge
	// calls it merged. Zero never merges.
	mergedAfter int
	// headRun is what the run over the batch's head says when the ci-ok above is a failure.
	headRun merge.LandRun
	// queueRun is what the merge-group run says when the entry leaves the queue.
	queueRun merge.LandRun

	ciReads, prReads, queueReads int
}

// landLab is batchRepo with the landing side scripted. The two members are green on their
// own heads, exactly as batchRepo leaves them; everything the LANDING asks about is this
// script's.
func landLab(t *testing.T, name string, s *landScript) *landScript {
	t.Helper()
	l := batchRepo(t)
	s.lab, s.name = l, name
	s.root = filepath.Join(l.dir, "batch")

	// The batch's head is a commit no test can know before the run, so the forge's answers
	// about it are keyed by "not one of the members' heads" rather than by a sha written
	// here. A member's head falls through to what batchRepo set on it.
	members := map[string]bool{}
	for _, sha := range l.heads {
		members[sha] = true
	}
	l.host.OnChecks = func(oid string, _ int) (merge.Checks, bool) {
		if members[oid] {
			return merge.Checks{}, false
		}
		s.ciReads++
		var c merge.Checks
		c.AddRun(batchRequiredCheck, at(s.ciOK, s.ciReads, "success"), oid)
		return c, true
	}
	l.host.OnPR = func(n, _ int) (merge.PR, bool) {
		if n != landPRNumber {
			return merge.PR{}, false
		}
		s.prReads++
		pr := merge.PR{
			Number:  n,
			HeadRef: "rowan/" + name,
			HeadOID: s.head(),
			Base:    "dev",
			// MERGEABLE, because the one door refuses a CONFLICTING entry and this batch
			// was built on the base it is landing on.
			Mergeable: "MERGEABLE",
		}
		if s.mergedAfter > 0 && s.prReads > s.mergedAfter {
			pr.Merged, pr.MergeSHA = true, landDev
		}
		return pr, true
	}
	l.host.OnQueueState = func(_, _ int) (string, bool) {
		s.queueReads++
		return at(s.queue, s.queueReads, ""), true
	}
	l.host.OnHeadRun = func(string, int) (merge.LandRun, bool) { return s.headRun, true }
	l.host.QueueRuns[landPRNumber] = s.queueRun
	return s
}

// head is the commit the gate built, read off the branch in the clone the gate built it in.
// The test learns it the way a caller does rather than being told: a fixture that knew the
// sha in advance would be a fixture that could not notice the gate building another one.
func (s *landScript) head() string {
	return s.git(filepath.Join(s.root, s.name, "repo"), "rev-parse", "refs/heads/rowan/"+s.name)
}

// run drives the gate with --land over the two-member fixture: #1 merges and #2 conflicts,
// so the landing lands one member and closes one.
func (s *landScript) run(extra ...string) (int, string, string) {
	args := append([]string{"batch", "--name", s.name, "--pr", "1,2", "--repo", "o/n",
		"--root", s.root, "--base", "dev", "--timeout", "5m", "--interval", "30s", "--land"}, extra...)
	return s.lab.run(args...)
}

// at is the scripted answer for one read: the nth entry, the last one for every read after
// it, and fallback for a script that named none at all.
func at(list []string, n int, fallback string) string {
	switch {
	case len(list) == 0:
		return fallback
	case n > len(list):
		return list[len(list)-1]
	default:
		return list[n-1]
	}
}

// flakeFile writes a --flakes list for one test.
func flakeFile(t *testing.T, entries ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flakes.txt")
	if err := os.WriteFile(path, []byte(strings.Join(entries, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// THE HAPPY PATH, which is the procedure Rowan ran eight times by hand on 2026-09-18: the
// branch reaches the forge, the pull request is opened with the receipt as its body, the
// forge's ci-ok is green, the one door takes it at the FRONT, the queue merges it, and the
// member is closed pointing at the batch that carried it.
func TestBatchLandPushesOpensEnqueuesWatchesAndClosesTheMembers(t *testing.T) {
	s := landLab(t, "integration-9", &landScript{
		ciOK:        []string{"success"},
		queue:       []string{"QUEUED"},
		mergedAfter: 2,
	})
	exit, stdout, stderr := s.run()
	if exit != 0 {
		t.Fatalf("a landed batch is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}

	// THE VERDICT IS THE LAST LINE ON STDOUT, so a caller who pipes this run into a file
	// and takes its last line takes the verdict -- which is exactly what `land
	// --receipt-file` does.
	want := fmt.Sprintf("BATCH LAND OK name=integration-9 dev=%s members=1", landDev)
	if last := lastLine(stdout); last != want {
		t.Errorf("the last line of stdout is\n\t%s\nwant\n\t%s\nall of it:\n%s", last, want, stdout)
	}
	// 1: the branch reached the forge, and it is the only ref the landing created.
	contains(t, remoteRefs(s.lab), "refs/heads/rowan/integration-9")
	contains(t, stderr, "BATCH LAND STEP push branch=rowan/integration-9 ")
	contains(t, stderr, "state=created")
	// 2: the pull request carries the RECEIPT, so a reader of it can see which tree was
	// gated and what was left out.
	if len(s.host.Opened) != 1 {
		t.Fatalf("want one pull request opened, got %v", s.host.Opened)
	}
	contains(t, s.host.Opened[0], "base=dev")
	contains(t, s.host.Opened[0], "BATCH OK name=integration-9")
	contains(t, s.host.Opened[0], "members: 1")
	contains(t, s.host.Opened[0], "dropped: 2")
	// 3 and 5: green, then the ONE DOOR, at the front.
	contains(t, stderr, "BATCH LAND STEP pr-ci check=ci-ok state=green")
	if len(s.enqueue.enqueued) != 1 || !s.enqueue.jumps[0] {
		t.Fatalf("want one enqueue at the front, got enqueued=%v jumps=%v", s.enqueue.enqueued, s.enqueue.jumps)
	}
	contains(t, stdout, "LAND OK pr=9001")
	// 6: the queue was watched to the merge, not assumed.
	contains(t, stderr, "BATCH LAND STEP queue pr=9001 state=QUEUED")
	contains(t, stderr, "state=merged dev="+landDev)
	// 7: the member is closed, and it is closed pointing at the batch.
	if len(s.host.Closed) != 1 {
		t.Fatalf("want the one member closed, got %v", s.host.Closed)
	}
	contains(t, s.host.Closed[0], "pr=1 ")
	contains(t, s.host.Closed[0], "integration-9 (#9001)")
	// And the structured line is beside the human one, for every step (SPEC-LOGS.md Part 2).
	contains(t, stderr, `"verb":"batch-land"`)
	contains(t, stderr, `"event":"push"`)
	contains(t, stderr, `"event":"done"`)
}

// A RED ci-ok WHOSE EVERY FAILING TEST IS A KNOWN FLAKE BUYS ONE RERUN, and the landing
// then carries on when the rerun goes green. The two on the list on 2026-09-18 cost a batch
// a whole CI round each, by hand, every time.
func TestBatchLandRerunsAKnownFlakeOnceAndThenLands(t *testing.T) {
	s := landLab(t, "integration-10", &landScript{
		// red, then the rollup recomputing after the rerun, then green.
		ciOK:        []string{"failure", "pending", "success"},
		queue:       []string{"QUEUED"},
		mergedAfter: 2,
		headRun: merge.LandRun{ID: 5150, Jobs: []merge.LandJob{
			{
				Name:       "test (ubuntu-latest, cmd/nova-pulse)",
				Conclusion: "failure",
				Package:    "cmd/nova-pulse",
				Tests:      []string{"TestPowerRunnerTimeoutKillsProcessGroup"},
				Lines:      []string{"--- FAIL: TestPowerRunnerTimeoutKillsProcessGroup (2.01s)"},
			},
		}},
	})
	flakes := flakeFile(t, "cmd/nova-pulse TestPowerRunnerTimeoutKillsProcessGroup the kill races the child's own exit on a loaded bench")

	exit, stdout, stderr := s.run("--flakes", flakes)
	if exit != 0 {
		t.Fatalf("a flake rerun that goes green lands: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH LAND rerun=flake tests=TestPowerRunnerTimeoutKillsProcessGroup")
	// ONCE, and the FAILED jobs -- never the whole run, which is minutes of the fleet's
	// runners spent re-proving the legs that were already green.
	if len(s.host.Reruns) != 1 || s.host.Reruns[0] != 5150 {
		t.Fatalf("want exactly one rerun of run 5150, got %v", s.host.Reruns)
	}
	contains(t, stdout, "BATCH LAND OK name=integration-10")
	if len(s.enqueue.enqueued) != 1 {
		t.Fatalf("want one enqueue, got %v", s.enqueue.enqueued)
	}
}

// A FAILURE THAT IS NOT ON THE LIST STOPS THE LANDING, names the job and the tests on one
// line, prints the failing lines under it, and NEVER TOUCHES THE QUEUE. This is the whole
// point of the flake list being a list rather than a policy of rerunning.
func TestBatchLandStopsOnAFailureThatIsNotAKnownFlake(t *testing.T) {
	s := landLab(t, "integration-11", &landScript{
		ciOK: []string{"failure"},
		headRun: merge.LandRun{ID: 61, Jobs: []merge.LandJob{
			{
				Name:       "test (windows-latest, internal/merge)",
				Conclusion: "failure",
				Package:    "internal/merge",
				Tests:      []string{"TestTheLeaseSpellingHasOneCallSite"},
				Lines: []string{
					"--- FAIL: TestTheLeaseSpellingHasOneCallSite (0.01s)",
					"    guard_test.go:110: the lease is built at 2 call sites, want exactly 1",
				},
			},
		}},
	})
	flakes := flakeFile(t, "cmd/nova-pulse TestPowerRunnerTimeoutKillsProcessGroup the kill races the child's own exit on a loaded bench")

	exit, stdout, stderr := s.run("--flakes", flakes)
	if exit != 1 {
		t.Fatalf("a red landing is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH LAND FAIL step=pr-ci")
	contains(t, stdout, "job=test\\x20(windows-latest,\\x20internal/merge)")
	contains(t, stdout, "tests=TestTheLeaseSpellingHasOneCallSite")
	contains(t, stdout, "is not on the flake list")
	// THE LINES ARE PRINTED, so a caller does not have to open the run to find out what
	// broke -- which is the minute this step exists to save.
	contains(t, stderr, "--- FAIL: TestTheLeaseSpellingHasOneCallSite")
	contains(t, stderr, "the lease is built at 2 call sites")
	// And nothing was rerun and nothing was queued.
	if len(s.host.Reruns) != 0 {
		t.Errorf("a failure that is not a flake was rerun: %v", s.host.Reruns)
	}
	if len(s.enqueue.enqueued) != 0 {
		t.Errorf("a red batch reached the queue: %v", s.enqueue.enqueued)
	}
	if len(s.host.Closed) != 0 {
		t.Errorf("a batch that did not land closed its members: %v", s.host.Closed)
	}
}

// THE ONE DEQUEUE WORTH A SECOND GO: the merge group's darwin shards were CANCELLED, which
// is the fleet's two darwin runners being busy behind another group rather than a red tree.
// The batch goes back to the front of the queue ONCE, and then lands.
func TestBatchLandReenqueuesOnceWhenTheMergeGroupsDarwinShardsWereCancelled(t *testing.T) {
	s := landLab(t, "integration-12", &landScript{
		ciOK: []string{"success"},
		// in the queue, then gone, then gone -- two empty reads are what counts as a
		// dequeue -- and in the queue again after the re-enqueue.
		queue:       []string{"QUEUED", "", "", "QUEUED"},
		mergedAfter: 5,
		queueRun: merge.LandRun{ID: 909, Jobs: []merge.LandJob{
			{Name: "test (darwin-arm64, shard 1)", Conclusion: "cancelled"},
			{Name: "test (darwin-arm64, shard 2)", Conclusion: "cancelled"},
			{Name: "test (ubuntu-latest, shard 1)", Conclusion: "success"},
		}},
	})
	exit, stdout, stderr := s.run()
	if exit != 0 {
		t.Fatalf("a cancelled darwin shard is a re-enqueue, not a failure: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH LAND requeue=cancelled jobs=test\\x20(darwin-arm64,\\x20shard\\x201),test\\x20(darwin-arm64,\\x20shard\\x202)")
	if len(s.enqueue.enqueued) != 2 {
		t.Fatalf("want the batch enqueued twice -- once, then once more after the cancellation -- got %v", s.enqueue.enqueued)
	}
	for i, jump := range s.enqueue.jumps {
		if !jump {
			t.Errorf("enqueue %d did not jump; a batch is the whole of a session's landing and goes to the front", i+1)
		}
	}
	contains(t, stdout, "BATCH LAND OK name=integration-12")
	contains(t, stderr, "state=requeue")
}

// ANY OTHER DEQUEUE IS A RED TREE AND STOPS. A failed merge-group job is the queue telling
// us the batch is red on the base it would land on, and a verb that re-enqueued that would
// spend the fleet's runners proving the same failure again.
func TestBatchLandStopsWhenTheMergeGroupFailed(t *testing.T) {
	s := landLab(t, "integration-13", &landScript{
		ciOK:  []string{"success"},
		queue: []string{"QUEUED", "", ""},
		queueRun: merge.LandRun{ID: 910, Jobs: []merge.LandJob{
			{
				Name:       "test (ubuntu-latest, internal/work)",
				Conclusion: "failure",
				Package:    "internal/work",
				Tests:      []string{"TestTheWorkSchemaRoundTrips"},
				Lines:      []string{"--- FAIL: TestTheWorkSchemaRoundTrips (0.00s)"},
			},
			{Name: "test (darwin-arm64, shard 1)", Conclusion: "cancelled"},
		}},
	})
	exit, stdout, stderr := s.run()
	if exit != 1 {
		t.Fatalf("a failed merge group is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH LAND FAIL step=queue")
	contains(t, stdout, "job=test\\x20(ubuntu-latest,\\x20internal/work)")
	contains(t, stdout, "tests=TestTheWorkSchemaRoundTrips")
	contains(t, stderr, "--- FAIL: TestTheWorkSchemaRoundTrips")
	// ONE enqueue: the failure was not a cancellation, so there was no second go.
	if len(s.enqueue.enqueued) != 1 {
		t.Fatalf("want exactly one enqueue, got %v", s.enqueue.enqueued)
	}
	if len(s.host.Closed) != 0 {
		t.Errorf("a batch that did not land closed its members: %v", s.host.Closed)
	}
}

// EDGE 25 CARRIED INTO THE LANDING (#1363): a member whose OWN head has no green ci-ok is
// dropped BEFORE the gate merges it, so it is not in the batch, not in the pull request's
// members, and not closed at the end. A landing that closed a pull request it did not carry
// would be the worst failure in this file: the author's work is gone and nothing has it.
func TestBatchLandNeverClosesAMemberTheGateDropped(t *testing.T) {
	s := landLab(t, "integration-14", &landScript{
		ciOK:        []string{"success"},
		queue:       []string{"QUEUED"},
		mergedAfter: 2,
	})
	// #1's own head is red on ci-ok, so it is dropped before the merge. #2 then has nothing
	// ahead of it and merges cleanly -- it only ever conflicted WITH #1 -- so this batch
	// lands one member and has dropped another, which is the case that matters: the
	// landing must close exactly the one it carried.
	s.host.SetCheckRuns(s.heads[1], merge.CheckDetail{Name: "ci-ok", Conclusion: "failure", SHA: s.heads[1]})

	exit, stdout, stderr := s.run()
	if exit != 0 {
		t.Fatalf("a batch with one member dropped still lands the rest: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+s.heads[1]+" has no green ci-ok (state=failure)\"")
	contains(t, stdout, "BATCH OK name=integration-14")
	contains(t, stdout, "members=2 dropped=1")
	contains(t, stdout, "BATCH LAND OK name=integration-14 dev="+landDev+" members=2")
	// EXACTLY THE MEMBER IT CARRIED. Closing a pull request the gate dropped is the worst
	// failure in this file: the author's work never landed and nothing points at it.
	if len(s.host.Closed) != 1 {
		t.Fatalf("want exactly the carried member closed, got %v", s.host.Closed)
	}
	contains(t, s.host.Closed[0], "pr=2 ")
	contains(t, s.host.Opened[0], "members: 2")
	contains(t, s.host.Opened[0], "dropped: 1")
}

// --flakes without --land is a list that would be read and never used, which is a caller
// expecting a rerun that is not going to happen.
func TestBatchRefusesAFlakeListWithNothingToLand(t *testing.T) {
	l := batchRepo(t)
	flakes := flakeFile(t, "cmd/nova-pulse TestSomething because it races")
	exit, stdout, stderr := l.run("batch", "--name", "integration-15", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--flakes", flakes)
	if exit != 2 {
		t.Fatalf("exit %d, want 2 (could not run)\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "--flakes is the list --land reruns a known flake from")
	absent(t, stdout, "BATCH")
}

// An entry with no reason is a test nobody can ever take off the list, so the list refuses
// it where it is read rather than dropping it quietly.
func TestBatchRefusesAFlakeListEntryWithNoReason(t *testing.T) {
	l := batchRepo(t)
	flakes := flakeFile(t, "cmd/nova-pulse TestSomething")
	exit, _, stderr := l.run("batch", "--name", "integration-16", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--land", "--flakes", flakes)
	if exit != 2 {
		t.Fatalf("exit %d, want 2 (could not run)\n%s", exit, stderr)
	}
	contains(t, stderr, "an entry with no reason")
}

// lastLine is the last line of an output that says anything -- the same reading `land
// --receipt-file` does, so a test of the verdict's position is a test of what that reader
// would take.
func lastLine(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}
