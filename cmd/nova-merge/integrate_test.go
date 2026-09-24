package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// THE LANDING VERB'S OWN TESTS. Issue #1845, L6 of #1725.
//
// They drive cmdIntegrate through run(), against a REAL git and the bare fixture
// repository batchRepo builds, with the forge, the merge queue's door and the run reader
// all fakes. Nothing here opens a socket.
//
// Each test is one step of the eleven the hand loop ran on 2026-09-19, and each asserts
// the two things a landing verb is for: the TYPED LINE, and the REFUSAL THAT NAMES THE
// MEMBER. A refusal that does not name the member is the refusal the hand loop's reader
// could not act on.

// integrateLab is batchRepo plus the three things integrate needs and batch does not: a
// lane directory for the read records, a reviewer file the hold fold resolves through,
// and a basis file for the pull request body.
type integrateLab struct {
	*lab
	lane      string
	reviewers string
	basis     string
	root      string
}

func newIntegrateLab(t *testing.T) *integrateLab {
	t.Helper()
	l := batchRepo(t)
	lane := filepath.Join(l.dir, "lane-integrate")
	if err := os.MkdirAll(lane, 0o755); err != nil {
		t.Fatalf("lane: %v", err)
	}
	basis := filepath.Join(l.dir, "basis.md")
	if err := os.WriteFile(basis, []byte("- #1 on alice's APPROVE at its exact head\n- #4 on alice's APPROVE at its exact head\n"), 0o644); err != nil {
		t.Fatalf("basis: %v", err)
	}
	// A FOURTH MEMBER THAT IS CLEAN. batchRepo's three are a clean one, a conflicting
	// one and a poison one, which is the shape the gate's own tests want; a landing that
	// goes all the way through wants two members that merge AND pass, so that the order
	// members are closed in is a thing a test can see.
	dev := strings.TrimSpace(l.git(l.work, "rev-parse", "origin/dev"))
	l.git(l.work, "checkout", "-q", "-B", "pr4", dev)
	l.write("pkg/d/d.go", "package d\n\nfunc D() int { return 4 }\n")
	four := l.commit("pr 4")
	l.git(l.work, "push", "-q", "origin", four+":refs/pull/4/head")
	l.git(l.work, "checkout", "-q", "main")
	l.heads[4] = four
	l.host.PRs[4] = merge.PR{Number: 4, HeadOID: four, Base: "dev"}
	l.host.SetCheckRuns(four, merge.CheckDetail{Name: "ci-ok", Conclusion: "success", SHA: four})
	return &integrateLab{
		lab:       l,
		lane:      lane,
		reviewers: testReviewerFile(t, t.TempDir(), defaultReviewersTSV),
		basis:     basis,
		root:      filepath.Join(l.dir, "integrate-root"),
	}
}

// args is one whole invocation, with the members the caller names at the heads the
// fixture actually gave them.
func (il *integrateLab) args(name string, members ...int) []string {
	parts := make([]string, 0, len(members))
	for _, n := range members {
		parts = append(parts, fmt.Sprintf("%d@%s", n, il.heads[n]))
	}
	return []string{"integrate",
		"--repo", "o/n", "--local", il.work, "--base", "dev",
		"--members", strings.Join(parts, ","),
		"--lane", il.lane, "--reviewers", il.reviewers,
		"--on", "hulk", "--name", name, "--root", il.root,
		"--basis", il.basis, "--timeout", "5m",
		"--checks", "go build ./...",
		"--ci-interval", "1s", "--ci-timeout", "10m",
	}
}

// greenOnEveryHead makes the fake forge answer a green ci-ok for any commit, which is
// what the batch's own head needs: its sha is the gate's and no test can know it in
// advance.
func (il *integrateLab) greenOnEveryHead() {
	il.host.OnChecks = func(oid string, call int) (merge.Checks, bool) {
		var c merge.Checks
		c.AddRun("ci-ok", "success", oid)
		return c, true
	}
}

// batchPRIsOnTheForge teaches the fake forge about the batch's own pull request: its
// number is the fake's next, and its head is whatever the push put on the remote branch,
// read live so that the read is evidence the push happened.
func (il *integrateLab) batchPRIsOnTheForge(t *testing.T, number int, branch string) {
	t.Helper()
	il.host.OnPR = func(n, call int) (merge.PR, bool) {
		if n != number {
			return merge.PR{}, false
		}
		sha := strings.TrimSpace(il.git(il.remote, "rev-parse", "refs/heads/"+branch))
		return merge.PR{Number: n, HeadOID: sha, HeadRef: branch, Base: "dev", Mergeable: "MERGEABLE"}, true
	}
}

// 1. STEP 1. A member whose head moved is refused BY NAME, and nothing is gated.
//
// The hand loop's one recurring near-miss: a read recorded at a head that has since
// moved (land6's HANDOFF, "Waiting"). The caller names the head they read; the forge is
// asked; a disagreement stops the landing before a single merge.
func TestIntegrateRefusesAMemberWhoseHeadMovedAndNamesIt(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	// The forge has moved #3 on under the caller.
	moved := il.host.PRs[3]
	moved.HeadOID = "0123456789abcdef0123456789abcdef01234567"
	il.host.PRs[3] = moved

	exit, stdout, stderr := il.run(il.args("integration-1", 1, 3)...)

	if exit != 1 {
		t.Fatalf("a moved head is the verb saying NO, which is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE HEADS member=#3")
	contains(t, stdout, "verdict=moved")
	contains(t, stderr, "INTEGRATE REFUSED step=heads member=#3")
	contains(t, stderr, "head moved")
	// NOTHING WAS GATED, PUSHED OR OPENED.
	absent(t, stdout, "INTEGRATE BATCH")
	if len(il.forge.Created) != 0 {
		t.Errorf("a refusal at step 1 opened %d pull requests; it must open none", len(il.forge.Created))
	}
}

// 2. STEP 2. A member carrying an unreleased HOLD at its exact head is refused by name,
// and the refusal carries who held it and where the hold came from.
//
// The fold is internal/merge/verdict.go's (#1572, #1748) and this test drives it through
// the same fake the batch's own hold tests use: there is no second parser in this verb.
func TestIntegrateRefusesAHeldMemberAtPassOne(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	il.host.SetVerdicts(3, merge.Verdict{
		ID: "comment:5743389888", Who: "alice", Word: "hold", Head: il.heads[3],
		At: "2026-09-19T16:24:00Z", Source: "comment-rule",
	})

	exit, stdout, stderr := il.run(il.args("integration-2", 1, 3)...)

	if exit != 1 {
		t.Fatalf("a held member is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE HOLD pass=1 member=#3 verdict=held")
	contains(t, stdout, "who=alice")
	contains(t, stdout, "source=comment-rule")
	contains(t, stderr, "INTEGRATE REFUSED step=hold member=#3")
	contains(t, stderr, "unreleased HOLD")
	// AND THE ONE THAT MATTERS: no flag in this verb lifts a hold, so the refusal says so.
	contains(t, stderr, "no flag here lifts one")
	absent(t, stdout, "INTEGRATE BATCH")
}

// 3. STEP 3. A member that conflicts with the entries ahead is named by `simulate`, and
// the landing stops before the gate is paid for.
//
// #2 of the fixture changes the same line of base/shared.go as #1, so with #1 ahead of
// it the squash conflicts -- which is exactly what land7 met at 17:35:38Z when #1764
// landed under a gated head and #1723's AGENTS.md hunk stopped merging.
func TestIntegrateNamesTheMemberThatConflictsWithTheEntriesAhead(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)

	exit, stdout, stderr := il.run(il.args("integration-3", 1, 2)...)

	if exit != 1 {
		t.Fatalf("a conflicting member is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "SIMULATE CONFLICT #2")
	contains(t, stdout, "INTEGRATE SIMULATE member=#2 verdict=conflict")
	contains(t, stderr, "INTEGRATE REFUSED step=simulate member=#2")
	contains(t, stderr, "conflicts with the entries ahead")
	absent(t, stdout, "INTEGRATE BATCH")
}

// 4. --dry-run RUNS STEPS 1-3 AND NOTHING ELSE: it gates nothing, pushes nothing, opens
// nothing and queues nothing, and its last line says how far it went.
func TestADryRunStopsAfterTheSimulationAndLandsNothing(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	before := remoteRefs(il.lab)

	args := append(il.args("integration-4", 1, 4), "--dry-run")
	exit, stdout, stderr := il.run(args...)

	if exit != 0 {
		t.Fatalf("a clean dry run is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE HEADS ok=2")
	contains(t, stdout, "INTEGRATE HOLD pass=1")
	contains(t, stdout, "INTEGRATE SIMULATE entries=2 conflicts=none")
	contains(t, stdout, "INTEGRATE DONE")
	contains(t, stdout, "dry_run=true")
	contains(t, stdout, "steps=3/11")
	contains(t, stdout, "landed=none")
	for _, step := range []string{"INTEGRATE BATCH", "INTEGRATE PUSH", "INTEGRATE PR ", "INTEGRATE LAND", "INTEGRATE CLOSE"} {
		absent(t, stdout, step)
	}
	if got := remoteRefs(il.lab); got != before {
		t.Errorf("a dry run changed the remote's refs:\nbefore: %s\nafter:  %s", before, got)
	}
	if len(il.forge.Created)+len(il.forge.Closed)+len(il.enqueue.enqueued) != 0 {
		t.Errorf("a dry run wrote to the forge: %d created, %d closed, %d enqueued",
			len(il.forge.Created), len(il.forge.Closed), len(il.enqueue.enqueued))
	}
}

// 5. THE WHOLE LOOP, GREEN: gate, push under the lease, pull request with the receipt and
// the basis, ci-ok, the second hold read, the queue, the pointer on every member, and the
// table of what is left.
func TestIntegrateGatesPushesOpensPollsLandsAndClosesEveryMember(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	il.greenOnEveryHead()
	il.batchPRIsOnTheForge(t, 9001, "rowan/integration-5")
	il.host.Open = []merge.RebasePR{
		{Number: 1, MergeState: "CLEAN"},
		{Number: 2, MergeState: "DIRTY"},
		{Number: 7, MergeState: "CLEAN"},
	}

	exit, stdout, stderr := il.run(il.args("integration-5", 1, 4)...)

	if exit != 0 {
		t.Fatalf("a clean landing is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	// EVERY STEP PRINTED ITS OWN TYPED LINE, in order.
	for _, want := range []string{
		"INTEGRATE START name=integration-5",
		"INTEGRATE HEADS ok=2 moved=none",
		"INTEGRATE HOLD pass=1 members=2 held=none",
		"INTEGRATE SIMULATE entries=2 conflicts=none",
		"INTEGRATE BATCH on=hulk",
		"INTEGRATE PUSH branch=rowan/integration-5",
		"lease=must-not-exist existed=0",
		"forced=no",
		"INTEGRATE PR number=9001",
		"INTEGRATE CI pr=9001",
		"state=green",
		"INTEGRATE HOLD pass=2 members=2 held=none",
		"INTEGRATE LAND pr=9001",
		"INTEGRATE CLOSE member=#1",
		"INTEGRATE CLOSE member=#4",
		"verdict=closed",
		"INTEGRATE REVERIFY pr=#7",
		"INTEGRATE REVERIFY open=2 dirty=1",
		"INTEGRATE DONE name=integration-5",
		"closed=2 steps=11/11",
	} {
		contains(t, stdout, want)
	}
	// THE BRANCH IS ON THE REMOTE, at the head the gate printed.
	refs := remoteRefs(il.lab)
	if !strings.Contains(refs, "refs/heads/rowan/integration-5") {
		t.Fatalf("the integration branch never reached the remote: %s", refs)
	}
	// THE PULL REQUEST CARRIES THE RECEIPT AND THE BASIS, word for word.
	if len(il.forge.Created) != 1 {
		t.Fatalf("one batch is one pull request, got %d", len(il.forge.Created))
	}
	pr := il.forge.Created[0]
	if !pr.Draft {
		t.Error("a batch pull request opens as a draft unless the caller says --no-draft")
	}
	if pr.Base != "dev" || pr.Head != "rowan/integration-5" {
		t.Errorf("the pull request is %s <- %s; wanted dev <- rowan/integration-5", pr.Base, pr.Head)
	}
	contains(t, pr.Body, "BATCH OK name=integration-5")
	contains(t, pr.Body, "- #1 on alice's APPROVE at its exact head")
	contains(t, pr.Body, "must-not-exist lease")
	contains(t, pr.Body, "LoadLaneVerdicts")
	// THE QUEUE TOOK IT, AT THE FRONT, EXACTLY ONCE.
	if len(il.enqueue.enqueued) != 1 {
		t.Fatalf("the batch entered the queue %d times; a landing enqueues once", len(il.enqueue.enqueued))
	}
	if !il.enqueue.jumps[0] {
		t.Error("a batch lands at the front of its base's queue")
	}
	// EVERY MEMBER IS CLOSED WITH A POINTER THAT NAMES THE BATCH AND ITS OWN HEAD.
	if len(il.forge.Closed) != 2 {
		t.Fatalf("two members, two closes, got %d", len(il.forge.Closed))
	}
	for _, c := range il.forge.Closed {
		contains(t, c.Body, "Landed in batch #9001")
		contains(t, c.Body, "BATCH OK name=integration-5")
		contains(t, c.Body, "it did not move between the read and the door")
	}
	if il.forge.Closed[0].PR != 1 || il.forge.Closed[1].PR != 4 {
		t.Errorf("the members are closed in landing order, got %d then %d", il.forge.Closed[0].PR, il.forge.Closed[1].PR)
	}
}

// 6. STEP 5's LEASE. A branch of that name already on the remote is a refusal, and the
// verb never forces: the existing branch is still there afterwards, at the sha it had.
func TestThePushRefusesABranchThatAlreadyExistsAndNeverForces(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	il.greenOnEveryHead()
	// Somebody else's branch of the same name, at the fixture's dev.
	dev := strings.TrimSpace(il.git(il.work, "rev-parse", "origin/dev"))
	il.git(il.work, "push", "-q", "origin", dev+":refs/heads/rowan/integration-6")

	exit, stdout, stderr := il.run(il.args("integration-6", 1, 4)...)

	if exit != 1 {
		t.Fatalf("an occupied branch is the verb saying NO, exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE PUSH branch=rowan/integration-6")
	contains(t, stdout, "lease=must-not-exist existed=1 verdict=refused")
	contains(t, stderr, "INTEGRATE REFUSED step=push")
	contains(t, stderr, "it never forces")
	// THE OTHER BRANCH IS UNTOUCHED.
	after := strings.TrimSpace(il.git(il.remote, "rev-parse", "refs/heads/rowan/integration-6"))
	if after != dev {
		t.Errorf("the existing branch moved from %s to %s; this verb never forces", dev, after)
	}
	// AND NO PULL REQUEST WAS OPENED OVER A BRANCH THAT IS NOT THIS BATCH'S.
	if len(il.forge.Created) != 0 {
		t.Errorf("a refused push opened %d pull requests", len(il.forge.Created))
	}
}

// 7. STEP 7. A red with a named failing test is TERMINAL and the test is named. Nothing
// is re-run: COMMON rule 4, "a named failing test is a finding, not a rerun".
func TestARedCIWithANamedFailingTestIsTerminalAndNamesIt(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	il.batchPRIsOnTheForge(t, 9001, "rowan/integration-7")
	// The members are green; the batch's own head is red.
	il.host.OnChecks = func(oid string, call int) (merge.Checks, bool) {
		var c merge.Checks
		if oid == il.heads[1] || oid == il.heads[4] {
			c.AddRun("ci-ok", "success", oid)
		} else {
			c.AddRun("ci-ok", "failure", oid)
		}
		return c, true
	}
	il.failForge = &fakeFailForge{
		run:  35453996452,
		jobs: []ci.FailedJob{{ID: 105934323438, Name: "test (2/4 studio)", Conclusion: "failure"}},
		log: "=== RUN   TestCardBudgetCacheReadEndsWithPromptDefect\n" +
			"    cardbudget_test.go:62: the fake runner writes the fake harness log: signal: killed\n" +
			"--- FAIL: TestCardBudgetCacheReadEndsWithPromptDefect (0.31s)\n" +
			"FAIL\tgithub.com/mas-bandwidth/nova-tools/internal/swarm\t0.4s\n",
	}

	exit, stdout, stderr := il.run(il.args("integration-7", 1, 4)...)

	if exit != 1 {
		t.Fatalf("a red batch head is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE CI pr=9001")
	contains(t, stdout, "state=failure")
	contains(t, stdout, "INTEGRATE FAIL pr=9001")
	contains(t, stdout, "test=TestCardBudgetCacheReadEndsWithPromptDefect")
	contains(t, stderr, "a named failing test is a finding and never a rerun")
	// TERMINAL: the door was never reached and no member was closed.
	absent(t, stdout, "INTEGRATE LAND")
	if len(il.enqueue.enqueued) != 0 || len(il.forge.Closed) != 0 {
		t.Errorf("a red landed something: %d enqueued, %d closed", len(il.enqueue.enqueued), len(il.forge.Closed))
	}
}

// 8. STEP 8. A HOLD recorded between the gate and the door refuses the landing -- the
// read at admission is not the read at the door, which is reading 3's whole point and
// #1572's own timeline.
func TestAHoldRecordedBetweenTheGateAndTheDoorRefusesTheLanding(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	il.greenOnEveryHead()
	il.batchPRIsOnTheForge(t, 9001, "rowan/integration-8")
	// THE LOOP READS A MEMBER THREE TIMES, not two: this verb at admission, the gate
	// again inside batch (its own reading-3 fold), and this verb at the door. The hold
	// here appears only on the THIRD read, so what refuses the landing is step 8 and not
	// the gate -- which is the transition #1572's own timeline is about.
	reads := 0
	il.host.OnVerdicts = func(n, call int) ([]merge.Verdict, bool) {
		if n != 4 {
			return nil, true
		}
		reads++
		if reads < 3 {
			return nil, true
		}
		return []merge.Verdict{{
			ID: "comment:5743805198", Who: "alice", Word: "hold", Head: il.heads[4],
			At: "2026-09-19T17:12:00Z", Source: "comment-rule",
		}}, true
	}

	exit, stdout, stderr := il.run(il.args("integration-8", 1, 4)...)

	if exit != 1 {
		t.Fatalf("a hold at the door is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE HOLD pass=1 members=2 held=none")
	contains(t, stdout, "INTEGRATE HOLD pass=2 member=#4 verdict=held")
	contains(t, stderr, "INTEGRATE REFUSED step=hold member=#4")
	// THE GATE RAN AND THE DOOR DID NOT.
	contains(t, stdout, "INTEGRATE BATCH on=hulk")
	absent(t, stdout, "INTEGRATE LAND")
	if len(il.enqueue.enqueued) != 0 {
		t.Errorf("a held member reached the queue %d times", len(il.enqueue.enqueued))
	}
	if len(il.forge.Closed) != 0 {
		t.Errorf("a refused landing closed %d members", len(il.forge.Closed))
	}
}

// 9. THE SENSITIVE-PREFIX RULE (SPEC-TOOLWORK eligibility rule 13). A member whose diff
// touches a named prefix has ONE reader, and without that mind's APPROVE at THIS head it
// does not land -- and with no --sensitive file the rule says `unchecked` rather than
// passing a diff it never bounded.
func TestASensitivePrefixWantsTheDesignatedMindsApproveAtThisHead(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	prefixes := filepath.Join(il.dir, "sensitive.txt")
	if err := os.WriteFile(prefixes, []byte("# the prefixes whose read is the designated mind's\nbase/\n"), 0o644); err != nil {
		t.Fatalf("prefixes: %v", err)
	}

	// Without the approve: refused, and the refusal names the prefix and the mind.
	args := append(il.args("integration-9", 1), "--sensitive", prefixes, "--designated", "johnny")
	exit, stdout, stderr := il.run(args...)
	if exit != 1 {
		t.Fatalf("a sensitive prefix with no designated approve is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE HOLD pass=1 member=#1 verdict=sensitive")
	contains(t, stderr, "INTEGRATE REFUSED step=hold member=#1")
	contains(t, stderr, "whose read is johnny's and nobody else's")
	contains(t, stderr, "where that mind is asleep the work waits")

	// With it, at the exact head: admitted, and the line says whose read admitted it.
	il.host.SetVerdicts(1, merge.Verdict{
		ID: "review:222", Who: "johnny", Word: "approve", Head: il.heads[1],
		At: "2026-09-19T17:50:00Z", Source: "review",
	})
	exit, stdout, stderr = il.run(append(args, "--dry-run")...)
	if exit != 0 {
		t.Fatalf("the designated mind's approve at this head admits it, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "sensitive=johnny")

	// And with no --sensitive file at all the rule is UNCHECKED and says so.
	exit, stdout, _ = il.run(append(il.args("integration-9b", 1), "--dry-run")...)
	if exit != 0 {
		t.Fatalf("an unchecked rule is not a refusal, got %d\n%s", exit, stdout)
	}
	contains(t, stdout, "sensitive=unchecked")
}

// 10. THE GRAMMAR. Every line this verb prints on stdout begins `INTEGRATE <STEP>` with a
// step out of the eleven, and every refusal on stderr names its step. A line nobody can
// parse is a line the next landing's reader cannot act on.
func TestEveryLineTheVerbPrintsIsOneTypedLine(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	il.greenOnEveryHead()
	il.batchPRIsOnTheForge(t, 9001, "rowan/integration-10")

	_, stdout, _ := il.run(il.args("integration-10", 1, 4)...)

	known := map[string]bool{"START": true, "DONE": true, "FAIL": true, "REFUSED": true}
	for _, s := range integrateSteps {
		known[s] = true
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "INTEGRATE ") {
			// The sub-verbs' own lines -- BATCH, SIMULATE, LAND -- pass through
			// unchanged, which is the point of composing them.
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Errorf("a typed line with no step: %q", line)
			continue
		}
		if !known[fields[1]] {
			t.Errorf("line %q names step %q, which is not one of the eleven", line, fields[1])
		}
		seen[fields[1]] = true
		for _, f := range fields[2:] {
			if !strings.Contains(f, "=") && !strings.HasPrefix(f, "\"") {
				t.Errorf("line %q holds the bare word %q; every field of a typed line is key=value", line, f)
			}
		}
	}
	for _, s := range integrateSteps {
		if !seen[s] {
			t.Errorf("a whole green landing printed no %s line", s)
		}
	}
}

// 11. --members TAKES THE HEAD, AND WILL NOT GUESS ONE. A member named with no head is
// an invocation this verb refuses at exit 2, because the head is the caller's evidence
// and reading it off the forge at the moment of the merge is the mistake the verb exists
// to prevent.
func TestMembersWithoutAHeadIsRefusedBeforeAnythingRuns(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)

	args := il.args("integration-11", 1)
	for i, a := range args {
		if a == "--members" {
			args[i+1] = "1"
		}
	}
	exit, stdout, stderr := il.run(args...)

	if exit != 2 {
		t.Fatalf("an unusable invocation is exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "--members takes <number>@<sha>")
	absent(t, stdout, "INTEGRATE START")
}

// 12. --lane IS REQUIRED, and `--lane none` is not a lane. Reading 3 makes the lane's
// own records half the evidence of a hold, and a landing verb that folded only the
// forge's half would be a landing verb that misses every line-level HOLD.
func TestTheLaneIsRequiredAndNoneIsNotALane(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)

	for _, lane := range []string{"", "none"} {
		args := il.args("integration-12", 1)
		for i, a := range args {
			if a == "--lane" {
				args[i+1] = lane
			}
		}
		exit, _, stderr := il.run(args...)
		if exit != 2 {
			t.Fatalf("--lane %q is exit 2, got %d: %s", lane, exit, stderr)
		}
		contains(t, stderr, "lane")
	}
}

// fakeFailForge is the run reader with no network in it: one run, its jobs, and one log
// for all of them.
type fakeFailForge struct {
	run  int64
	jobs []ci.FailedJob
	log  string
	err  error
}

func (f *fakeFailForge) ResolveRun(sel ci.RunSelector) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.run, nil
}

func (f *fakeFailForge) Jobs(runID int64) ([]ci.FailedJob, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.jobs, nil
}

func (f *fakeFailForge) JobLog(jobID int64) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.log, nil
}
