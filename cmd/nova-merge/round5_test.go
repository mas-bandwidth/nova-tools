package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// DOGFOOD ROUND 5, THE MERGE LANE. Three edges a non-author found on vision, each red
// here before it was fixed. They are one file because they are one reading: a verb whose
// name promises a look must not act, a refusal must cost nothing, and a gate must not
// spend two minutes on a tree nobody changed.

// EDGE 1: `queue audit` ACTS BY DEFAULT, AND ITS NAME SAYS IT READS.
//
// "audit" is a reading word. The verb took --dry-run defaulting to false, so the caller
// who typed the obvious thing -- `nova-merge queue audit --repo o/n`, to SEE what was
// armed -- took twenty-seven standing instructions off the forge instead, and found out
// from the counts afterwards. The safe reading is the default and the act is asked for:
// --apply. The line says which one ran, in a word rather than a boolean nobody reads the
// polarity of.
func TestQueueAuditReadsByDefaultAndActsOnlyWithApply(t *testing.T) {
	f := &fakeAudit{open: []merge.AutoMergePR{
		{Number: 1301, HeadRef: "rowan/impl-a", Title: "a card"},
		{Number: 1307, HeadRef: "rowan/impl-b", Title: "another card"},
	}}
	exit, stdout, stderr := f.run(t, "queue", "audit", "--repo", "mas-bandwidth/nova-tools")
	if exit != 0 {
		t.Fatalf("queue audit: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	// THE WHOLE EDGE: the forge was not written to.
	if len(f.disabled) != 0 {
		t.Fatalf("`queue audit` with no --apply DISABLED %v; the default reads and writes nothing", f.disabled)
	}
	// Both are still named, because the list is what the caller came for.
	contains(t, stdout, "QUEUE AUDIT entry=1301 branch=rowan/impl-a")
	contains(t, stdout, "QUEUE AUDIT entry=1307 branch=rowan/impl-b")
	contains(t, stdout, "QUEUE AUDIT repo=mas-bandwidth/nova-tools found=2 disabled=0 failed=0 mode=dry-run\n")
	absent(t, stdout, "dry_run=")
}

// And --apply is the act, in the same shape, with the mode named.
func TestQueueAuditWithApplyDisablesEveryAutoMergeAndSaysSo(t *testing.T) {
	f := &fakeAudit{open: []merge.AutoMergePR{
		{Number: 1301, HeadRef: "rowan/impl-a", Title: "a card"},
		{Number: 1307, HeadRef: "rowan/impl-b", Title: "another card"},
	}}
	exit, stdout, stderr := f.run(t, "queue", "audit", "--repo", "mas-bandwidth/nova-tools", "--apply")
	if exit != 0 {
		t.Fatalf("queue audit --apply: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(f.disabled) != 2 {
		t.Fatalf("want both cleared, got %v", f.disabled)
	}
	contains(t, stdout, "QUEUE AUDIT repo=mas-bandwidth/nova-tools found=2 disabled=2 failed=0 mode=apply\n")
}

// --dry-run stays, and stays the default's spelling: a caller with the old flag in a
// script gets what the flag has always meant, not a refusal.
func TestQueueAuditStillTakesTheOldDryRunFlag(t *testing.T) {
	f := &fakeAudit{open: []merge.AutoMergePR{{Number: 1301, HeadRef: "rowan/impl-a", Title: "a card"}}}
	exit, stdout, stderr := f.run(t, "queue", "audit", "--repo", "o/n", "--dry-run")
	if exit != 0 {
		t.Fatalf("queue audit --dry-run: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(f.disabled) != 0 {
		t.Fatalf("--dry-run wrote to the forge: %v", f.disabled)
	}
	contains(t, stdout, "found=1 disabled=0 failed=0 mode=dry-run\n")
}

// --apply and --dry-run together are two answers to one question: refused at exit 2
// rather than one of them silently winning.
func TestQueueAuditRefusesApplyAndDryRunTogether(t *testing.T) {
	f := &fakeAudit{open: []merge.AutoMergePR{{Number: 1301, HeadRef: "rowan/impl-a"}}}
	exit, stdout, stderr := f.run(t, "queue", "audit", "--repo", "o/n", "--apply", "--dry-run")
	if exit != 2 {
		t.Fatalf("two answers to one question is exit 2, got %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(f.disabled) != 0 {
		t.Fatalf("a refused invocation wrote to the forge: %v", f.disabled)
	}
}

// EDGE 3a: THE RULE IS DECIDED BEFORE THE FORGE IS READ.
//
// `land` read the pull request, then read its whole check rollup, and only then asked
// whether the head was a batch at all -- so the answer "this is not a batch" cost two
// round trips to the forge and arrived after the slow one. Every part of the rule that
// is a fact of the INVOCATION -- a receipt that does not parse, a receipt whose batch
// dropped everything -- is decided with the host untouched.
func TestLandChecksTheRuleBeforeAnyForgeRead(t *testing.T) {
	for _, c := range []struct {
		name    string
		receipt string
		want    string
	}{
		{
			name:    "a receipt that is not a BATCH OK line",
			receipt: "BATCH FAIL name=integration-9 base=" + strings.Repeat("c", 40) + " head=" + strings.Repeat("d", 40) + " members=1,2 dropped=none skipped=none checks=required step=test packages=p tests=T reason=\"red\"",
		},
		{
			name:    "a receipt whose batch dropped every member",
			receipt: "BATCH OK name=integration-9 base=" + strings.Repeat("c", 40) + " head=" + strings.Repeat("d", 40) + " members=none dropped=1,2 skipped=none checks=required",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			head := strings.Repeat("a", 40)
			h, q := greenBatchPR(t, 1341, head), &fakeLandEnqueue{}
			exit, stdout, stderr := runLand(t, h, q,
				"land", "--repo", "o/n", "--pr", "1341", "--receipt", c.receipt)
			if exit != 1 {
				t.Fatalf("a rule refusal is exit 1, got %d\n%s\n%s", exit, stdout, stderr)
			}
			contains(t, stderr, "LAND REFUSED")
			// THE EDGE: the forge was never asked. Not the pull request, not its checks.
			if h.PRCalls != 0 || h.ChecksCalls != 0 {
				t.Fatalf("the rule was decided AFTER the forge was read: PRCalls=%d ChecksCalls=%d", h.PRCalls, h.ChecksCalls)
			}
			if len(q.enqueued) != 0 {
				t.Fatalf("the queue was touched: %v", q.enqueued)
			}
		})
	}
}

// A head that is not a batch's needs the forge to name the branch at all, so it costs ONE
// read -- and never the second, slow one. The check rollup is a rollup of every run on a
// commit; reading it to answer a question about a branch name is the waste this pins.
func TestLandRefusesANonBatchHeadWithoutReadingItsChecks(t *testing.T) {
	head := strings.Repeat("b", 40)
	h, q := merge.NewFakeHost(), &fakeLandEnqueue{}
	h.PRs[1207] = merge.PR{Number: 1207, HeadRef: "rowan/impl-something", HeadOID: head, Mergeable: "MERGEABLE"}
	h.ChecksBy[head] = merge.Checks{Green: 9}
	exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1207")
	if exit != 1 {
		t.Fatalf("a non-batch head is REFUSED at exit 1, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "LAND REFUSED")
	if h.ChecksCalls != 0 {
		t.Fatalf("the branch rule read the check rollup %d times; the branch name answers it", h.ChecksCalls)
	}
}

// EDGE 3b: A REFUSAL AND A BREAKDOWN ARE TWO WORDS, NOT ONE.
//
// Both exits printed `LAND REFUSED`, so exit 1 ("this does not enter the queue") and exit
// 2 ("the forge could not be read") were one line to anything reading the stream -- and a
// caller retries one and not the other. The prefixes differ.
func TestLandRefusalAndErrorHaveDifferentPrefixes(t *testing.T) {
	// The forge cannot be read: exit 2, LAND ERROR.
	h, q := merge.NewFakeHost(), &fakeLandEnqueue{}
	h.Err = errors.New("gh: could not resolve host")
	exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1341")
	if exit != 2 {
		t.Fatalf("a forge that could not be read is exit 2, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "LAND ERROR")
	if strings.Contains(stderr, "LAND REFUSED") {
		t.Fatalf("exit 2 still says REFUSED; the two answers are one line to a reader:\n%s", stderr)
	}

	// The rule says no: exit 1, LAND REFUSED, and never the error word.
	head := strings.Repeat("b", 40)
	h2, q2 := merge.NewFakeHost(), &fakeLandEnqueue{}
	h2.PRs[1207] = merge.PR{Number: 1207, HeadRef: "rowan/impl-something", HeadOID: head, Mergeable: "MERGEABLE"}
	h2.ChecksBy[head] = merge.Checks{Green: 9}
	exit, stdout, stderr = runLand(t, h2, q2, "land", "--repo", "o/n", "--pr", "1207")
	if exit != 1 {
		t.Fatalf("a rule refusal is exit 1, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "LAND REFUSED")
	if strings.Contains(stderr, "LAND ERROR") {
		t.Fatalf("exit 1 says ERROR:\n%s", stderr)
	}
}

// EDGE 5: A BATCH THAT DROPPED EVERY MEMBER IS ANSWERED BEFORE THE SUITE RUNS.
//
// On vision a batch whose one member conflicted with the base ran build, vet, vet-windows
// and test over the BASE -- two minutes proving that dev is green, which dev's own CI had
// already proved -- and printed BATCH OK with members=none. The receipt was then refused
// by `land`, correctly, at the end of a run that had nothing to say. An empty batch is a
// FAIL, at exit 1, before the first step.
//
// And the drop reason told the truth about the wrong thing: the first member has no
// members ahead of it, so "the merge conflicts with the members ahead" named a set that
// was empty. What it conflicts with is the base.
func TestBatchWithEveryMemberDroppedFailsBeforeTheSuiteRuns(t *testing.T) {
	l := batchRepo(t)
	// Move the base under pr2: dev takes its own change to the file pr2 changes, so pr2
	// conflicts with the BASE rather than with a member ahead of it.
	l.git(l.work, "checkout", "-q", "dev")
	l.write("base/shared.go", "package base\n\nvar Shared = \"dev moved\"\n")
	l.commit("dev moves under the batch")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")
	l.git(l.work, "checkout", "-q", "main")

	root := filepath.Join(l.dir, "batch")
	exit, stdout, stderr := l.run("batch", "--name", "integration-empty", "--pr", "2",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 1 {
		t.Fatalf("a batch with no members is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH FAIL name=integration-empty")
	contains(t, stdout, "members=none")
	contains(t, stdout, "dropped=2")
	contains(t, stdout, `reason="every member dropped"`)
	absent(t, stdout, "BATCH OK")

	// THE TWO MINUTES: not one step of the suite ran.
	absent(t, stderr, "BATCH STEP")

	// THE DROP REASON NAMES WHAT IT REALLY CONFLICTS WITH. There was nothing ahead of #2.
	contains(t, stderr, "BATCH DROP #2 reason=\"the merge conflicts with the base\" t=")
	if strings.Contains(stderr, "members ahead") {
		t.Fatalf("the first member's drop named members ahead of it; there were none:\n%s", stderr)
	}
}

// A member that conflicts with one AHEAD of it still says so: the two reasons are two
// different facts and the fix must not flatten them into one.
func TestBatchStillNamesTheMembersAheadWhenThereAreSome(t *testing.T) {
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	exit, stdout, stderr := l.run("batch", "--name", "integration-ahead", "--pr", "1,2",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")
	if exit != 0 {
		t.Fatalf("a green batch is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "members=1 dropped=2")
	contains(t, stderr, "BATCH DROP #2 reason=\"the merge conflicts with the members ahead\" t=")
}
