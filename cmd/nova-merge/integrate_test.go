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
	// ONE DEFAULT NON-AUTHOR APPROVE PER MEMBER, matching what basis.md already
	// narrates ("on alice's APPROVE at its exact head"). The positive-read fold of the
	// landing verb (SPEC-MERGE:808-837) requires an approve from a line that is not the
	// member's author at the member's current head, so a fixture with none would refuse
	// every clean member for the wrong reason. A test that wants a member WITH no read
	// deletes this default for that one member.
	for n := 1; n <= 4; n++ {
		l.host.SetVerdicts(n, merge.Verdict{Who: "alice", Word: "approve", Head: l.heads[n],
			At: "2026-09-19T12:00:00Z", Source: "review"})
	}
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
			return nil, false
		}
		reads++
		if reads < 3 {
			return nil, false
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

// 9. THE SENSITIVE-PREFIX RULE (SPEC-MERGE "The sensitive-prefix policy"). A member whose
// diff touches a tracked prefix has ONE reader -- the row's reviewer -- and without that
// mind's APPROVE at THIS head it does not land. The policy is the tracked
// .nova/merge-sensitive.tsv at the base commit, never a caller path.
func TestASensitivePrefixWantsTheDesignatedMindsApproveAtThisHead(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	// The policy is committed to the base branch, not passed as a caller flag.
	il.git(il.work, "checkout", "-q", "dev")
	il.write(".nova/merge-sensitive.tsv", "# prefix\twho\nbase/\tjohnny\n")
	il.commit("the tracked sensitive policy")
	il.git(il.work, "push", "-q", "origin", "HEAD:refs/heads/dev")
	il.git(il.work, "checkout", "-q", "main")

	// Without the approve: refused, and the refusal names the prefix and the mind.
	exit, stdout, stderr := il.run(il.args("integration-9", 1)...)
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
	exit, stdout, stderr = il.run(append(il.args("integration-9b", 1), "--dry-run")...)
	if exit != 0 {
		t.Fatalf("the designated mind's approve at this head admits it, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "sensitive=johnny")

	// And a base with no tracked file at all declares no sensitive prefixes: the rule is
	// UNCHECKED and says so.
	il2 := newIntegrateLab(t)
	exit, stdout, _ = il2.run(append(il2.args("integration-9c", 1), "--dry-run")...)
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

// ROW 1. The positive read condition (SPEC-MERGE:808-837): a member with no recorded
// non-author approve at its current head is refused -- the absence of a HOLD is not a
// read, and a self-approve (the author's own) is not a read either.
func TestIntegrateRefusesAMemberWithNoRecordedApprove(t *testing.T) {
	t.Parallel()

	il := newIntegrateLab(t)
	// Member #2 with no approve at all: the fixture's default approve is removed for
	// this one member, so the positive-read fold has nothing to satisfy it with.
	delete(il.host.Reads, 2)

	exit, stdout, stderr := il.run(il.args("integration-no-read", 2)...)
	if exit != 1 {
		t.Fatalf("a member with no non-author approve is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE HOLD pass=1 member=#2")
	contains(t, stdout, "verdict=no-read")
	absent(t, stdout, "verdict=clear")
	contains(t, stderr, "INTEGRATE REFUSED step=hold member=#2")
	contains(t, stderr, "non-author APPROVE")

	// Member #1 whose approve is the AUTHOR's own: a self-approve is not a read.
	il2 := newIntegrateLab(t)
	pr := il2.host.PRs[1]
	pr.Author = "alice"
	il2.host.PRs[1] = pr

	exit, stdout, stderr = il2.run(il2.args("integration-self-read", 1)...)
	if exit != 1 {
		t.Fatalf("a self-approve is not a read, exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE HOLD pass=1 member=#1")
	contains(t, stdout, "verdict=no-read")
	contains(t, stderr, "INTEGRATE REFUSED step=hold member=#1")
}

// ROW 2. The sensitive policy is read from the tracked, versioned
// .nova/merge-sensitive.tsv at the base commit, never from a caller path, and the
// caller's --sensitive/--designated flags are refused outright as no longer meaningful.
func TestIntegrateReadsTheSensitivePolicyFromTheTrackedFileNotACallerPath(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)

	// Commit the tracked policy at the base commit: one row, prefix `base/` -> `johnny`.
	il.git(il.work, "checkout", "-q", "dev")
	il.write(".nova/merge-sensitive.tsv", "# prefix\twho\nbase/\tjohnny\n")
	il.commit("the tracked sensitive policy")
	il.git(il.work, "push", "-q", "origin", "HEAD:refs/heads/dev")
	il.git(il.work, "checkout", "-q", "main")

	// No --sensitive/--designated flags at all: member #1 (which touches base/shared.go)
	// needs johnny's approve at this head, and the refusal names johnny.
	exit, stdout, stderr := il.run(il.args("integration-sensitive-1", 1)...)
	if exit != 1 {
		t.Fatalf("a member touching a tracked sensitive prefix without its reader is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE HOLD pass=1 member=#1 verdict=sensitive")
	contains(t, stderr, "INTEGRATE REFUSED step=hold member=#1")
	contains(t, stderr, "johnny")

	// A caller-supplied --sensitive/--designated no longer invents a reviewer: the flags
	// are refused outright as no longer meaningful.
	prefixes := filepath.Join(il.dir, "caller-sensitive.txt")
	if err := os.WriteFile(prefixes, []byte("base/\n"), 0o644); err != nil {
		t.Fatalf("prefixes: %v", err)
	}
	exit, _, stderr = il.run(append(il.args("integration-sensitive-2", 1), "--sensitive", prefixes, "--designated", "somebody-else")...)
	if exit != 2 {
		t.Fatalf("caller-invented --sensitive/--designated is refused outright (exit 2), got %d\n%s", exit, stderr)
	}
	contains(t, stderr, "no longer caller flags")
}

// ROW 3. An intervening branch creation inside the window between the existence read and
// the push is never overwritten: the create-only lease catches it in the same command as
// the push.
func TestAnIntervalRaceOnBranchCreationIsNotOverwritten(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	il.greenOnEveryHead()

	dev := strings.TrimSpace(il.git(il.work, "rev-parse", "origin/dev"))
	// The hand at another keyboard: the instant this verb is about to push its branch, a
	// DIFFERENT process creates refs/heads/rowan/<name> first, at an ancestor of the
	// gate's head -- exactly the window the must-not-exist lease exists to close.
	fired := false
	il.runner = &hookRunner{inner: merge.Exec{}, before: func(_ string, args []string) {
		if fired || len(args) == 0 || args[0] != "push" {
			return
		}
		fired = true
		il.git(il.work, "push", "-q", "origin", dev+":refs/heads/rowan/integration-race")
	}}

	exit, stdout, stderr := il.run(il.args("integration-race", 1, 4)...)
	if exit != 1 {
		t.Fatalf("an intervening branch creation must be refused, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	// The intervening branch is UNCHANGED: this verb never overwrites it.
	after := strings.TrimSpace(il.git(il.remote, "rev-parse", "refs/heads/rowan/integration-race"))
	if after != dev {
		t.Errorf("the intervening branch moved from %s to %s; the create-only lease must never overwrite it", dev, after)
	}
}

// ROW 4. A 7-char abbreviation resolves to its one full OID before anything else runs,
// and a prefix that names no commit refuses before anything is gated.
func TestIntegrateResolvesAnAbbreviatedMemberShaOrRefusesAmbiguity(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)

	abbrev := il.heads[1][:7]
	args := il.args("integration-abbrev", 1)
	for i, a := range args {
		if a == "--members" {
			args[i+1] = "1@" + abbrev
		}
	}
	exit, stdout, stderr := il.run(append(args, "--dry-run")...)
	if exit != 0 {
		t.Fatalf("a unique abbreviation resolves and is admitted, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	// The full 40-character OID is what is printed, never the abbreviation alone.
	contains(t, stdout, "#1@"+il.heads[1])

	il2 := newIntegrateLab(t)
	args2 := il2.args("integration-absent", 1)
	for i, a := range args2 {
		if a == "--members" {
			args2[i+1] = "1@deadbee"
		}
	}
	exit, stdout2, stderr := il2.run(args2...)
	if exit != 2 {
		t.Fatalf("a prefix that names no commit is exit 2, got %d\n%s", exit, stderr)
	}
	contains(t, stderr, "does not resolve to exactly one commit")
	absent(t, stdout2, "INTEGRATE START")
}

// ROW 5. --on is the caller's own label, checked against the host the process runs on and
// printed, never obeyed; the line carries local=<hostname> verified=<true|false>.
func TestIntegrateBatchLineNamesTheLocalHostAndWhetherItMatchesOn(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	il.greenOnEveryHead()
	il.batchPRIsOnTheForge(t, 9001, "rowan/integration-host")

	// --on hulk does not match the fixture hostname "testbench": the line says so, and
	// the landing is unaffected either way (verified is printed, never obeyed).
	exit, stdout, _ := il.run(il.args("integration-host", 1, 4)...)
	if exit != 0 {
		t.Fatalf("a mismatched --on label is non-blocking, got %d\n%s", exit, stdout)
	}
	contains(t, stdout, "INTEGRATE BATCH")
	contains(t, stdout, "local=testbench")
	contains(t, stdout, "verified=false")

	// --on testbench matches the fixture hostname: verified=true.
	il.batchPRIsOnTheForge(t, 9002, "rowan/integration-host2")
	args := il.args("integration-host2", 1, 4)
	for i, a := range args {
		if a == "--on" {
			args[i+1] = "testbench"
		}
	}
	exit, stdout, _ = il.run(args...)
	if exit != 0 {
		t.Fatalf("a matching --on label is non-blocking, got %d\n%s", exit, stdout)
	}
	contains(t, stdout, "INTEGRATE BATCH")
	contains(t, stdout, "local=testbench")
	contains(t, stdout, "verified=true")
}

// ROW 6a. The basis is read and validated BEFORE the push, so an empty basis never
// leaves an orphaned branch on the remote.
func TestIntegrateValidatesTheBasisBeforeThePushNotAfter(t *testing.T) {
	t.Parallel()
	il := newIntegrateLab(t)
	il.greenOnEveryHead()
	before := remoteRefs(il.lab)

	empty := filepath.Join(il.dir, "empty-basis.md")
	if err := os.WriteFile(empty, []byte(""), 0o644); err != nil {
		t.Fatalf("empty basis: %v", err)
	}
	args := il.args("integration-basis", 1, 4)
	for i, a := range args {
		if a == "--basis" {
			args[i+1] = empty
		}
	}
	exit, _, stderr := il.run(args...)
	if exit != 1 {
		t.Fatalf("an empty basis is exit 1, got %d\n%s", exit, stderr)
	}
	if after := remoteRefs(il.lab); after != before {
		t.Errorf("an empty basis changed the remote's refs:\nbefore: %s\nafter:  %s", before, after)
	}
	if strings.Contains(remoteRefs(il.lab), "rowan/integration-basis") {
		t.Errorf("an empty basis still pushed rowan/integration-basis")
	}
}

// ROW 6b. A prior partial run of THIS verb (the branch already there at this run's head,
// and an open pull request naming it) is reconciled and resumed, not refused.
func TestIntegrateReconcilesItsOwnPriorRunInsteadOfRefusingTheBranch(t *testing.T) {
	// Fixed commit dates make the gate's merge head reproducible across the learn run and
	// the verb's own run.
	t.Setenv("GIT_AUTHOR_DATE", "2026-09-19T12:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2026-09-19T12:00:00Z")
	il := newIntegrateLab(t)
	il.greenOnEveryHead()

	name := "integration-reconcile"
	head := integrateGateHead(t, il, name, 1, 4)

	// A prior partial run: the branch already exists at this run's head, and an open
	// pull request names it.
	il.git(filepath.Join(il.root, name, "repo"), "push", "-q", "origin", head+":refs/heads/rowan/"+name)
	il.host.Open = []merge.RebasePR{{Number: 9001, HeadRef: "rowan/" + name, MergeState: "CLEAN"}}
	il.batchPRIsOnTheForge(t, 9001, "rowan/"+name)

	exit, stdout, stderr := il.run(il.args(name, 1, 4)...)
	if exit != 0 {
		t.Fatalf("a prior run at the same head is resumed, not refused, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "INTEGRATE PUSH branch=rowan/"+name)
	contains(t, stdout, "verdict=resumed")
	contains(t, stdout, "INTEGRATE PR number=9001 resumed=true")
	if len(il.forge.Created) != 0 {
		t.Errorf("a resumed run opened %d new pull requests; it must open none", len(il.forge.Created))
	}
}

// integrateGateHead runs the batch gate for a name/member set and returns the head the
// gate produced, so a test can simulate a prior partial run at exactly that head.
func integrateGateHead(t *testing.T, il *integrateLab, name string, members ...int) string {
	t.Helper()
	parts := make([]string, 0, len(members))
	for _, n := range members {
		parts = append(parts, fmt.Sprintf("%d", n))
	}
	exit, stdout, stderr := il.run("batch", "--name", name, "--pr", strings.Join(parts, ","),
		"--repo", "o/n", "--root", il.root, "--base", "dev", "--timeout", "5m")
	if exit != 0 {
		t.Fatalf("learning the gate head: batch exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "BATCH OK ") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if strings.HasPrefix(f, "head=") {
				return strings.TrimPrefix(f, "head=")
			}
		}
	}
	t.Fatalf("no BATCH OK head in:\n%s", stdout)
	return ""
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
