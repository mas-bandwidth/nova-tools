//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

func testReviewerFile(t *testing.T, dir string, content string) string {
	t.Helper()
	path := filepath.Join(dir, "reviewers.tsv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write reviewers: %v", err)
	}
	checkCmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	if out, err := checkCmd.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "true" {
		exec.Command("git", "-C", dir, "init", "-q").Run()
		exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
		exec.Command("git", "-C", dir, "config", "user.email", "test@example.com").Run()
	}
	exec.Command("git", "-C", dir, "add", path).Run()
	exec.Command("git", "-C", dir, "commit", "-q", "-m", "reviewers").Run()
	return path
}

func testReviewerCommit(t *testing.T, l *lab, content string) (string, string) {
	t.Helper()
	path := filepath.Join(l.work, "reviewers.tsv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write reviewers: %v", err)
	}
	l.git(l.work, "add", "reviewers.tsv")
	sha := l.commit("update reviewers")
	return path, sha[:12]
}

// 2. TestAHoldInAnySourceStops: lane record, review CHANGES_REQUESTED, comment.
func TestAHoldInAnySourceStops(t *testing.T) {
	t.Parallel()
	revFile := testReviewerFile(t, t.TempDir(), defaultReviewersTSV)

	// Source 1: review
	l1 := batchRepo(t)
	l1.host.SetVerdicts(1, merge.Verdict{
		ID: "review:301", Who: "alice", Word: "hold", Head: l1.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "review",
	})
	exit, _, stderr := l1.run("batch", "--name", "b1", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l1.dir, "b1"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l1.heads[1])+" carries an unreleased HOLD\" who=alice hold=review:301 source=review")

	// Source 2: comment
	l2 := batchRepo(t)
	l2.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:302", Who: "alice", Word: "hold", Head: l2.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "comment-rule",
	})
	exit, _, stderr = l2.run("batch", "--name", "b2", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l2.dir, "b2"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l2.heads[1])+" carries an unreleased HOLD\" who=alice hold=comment:302 source=comment-rule")

	// Source 3: record
	l3 := batchRepo(t)
	l3.host.SetVerdicts(1, merge.Verdict{
		ID: "record:2026-09-19T01:00:00Z", Who: "alice", Word: "hold", Head: l3.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "record",
	})
	exit, _, stderr = l3.run("batch", "--name", "b3", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l3.dir, "b3"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l3.heads[1])+" carries an unreleased HOLD\" who=alice hold=record:2026-09-19T01:00:00Z source=record")
}

// 3. TestAnAbstainRecordIsNotAnInput: a recorded HOLD at H1, then reader's ABSTAIN at H1; held.
// Then push to H2 and reader's ABSTAIN at H2; still held, carried=yes.
func TestAnAbstainRecordIsNotAnInput(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)
	h1 := "1111111111111111111111111111111111111111"
	h2 := l.heads[1]

	l.host.SetVerdicts(1,
		merge.Verdict{ID: "record:at1", Who: "alice", Word: "hold", Head: h1, At: "2026-09-19T01:00:00Z", Source: "record"},
		merge.Verdict{ID: "record:at2", Who: "alice", Word: "abstain", Head: h1, At: "2026-09-19T01:05:00Z", Source: "record"},
		merge.Verdict{ID: "record:at3", Who: "alice", Word: "abstain", Head: h2, At: "2026-09-19T01:10:00Z", Source: "record"},
	)

	exit, _, stderr := l.run("batch", "--name", "b-abstain", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-abstain"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(h2)+" carries an unreleased HOLD\" who=alice hold=record:at1 source=record held_at="+merge.Short(h1)+" carried=yes")
}

// 14. TestAnUntypedCommentFromAMayHoldLoginIsPending
func TestAnUntypedCommentFromAMayHoldLoginIsPending(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revFile := testReviewerFile(t, l.dir, "alice\talice\tyes\nstranger\tstranger\tno\n")

	// May-hold login comment is pending
	l.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:901", Who: "unknown", Word: "pending", Head: l.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "comment-pending",
	})
	exit, _, stderr := l.run("batch", "--name", "b-pending", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-pending"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l.heads[1])+" has a pending comment\" who=unknown hold=comment:901 source=comment-pending")

	// Comment from a login with no may-hold is skipped (not pending)
	l2 := batchRepo(t)
	l2.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:902", Who: "unknown", Word: "unknown", Head: l2.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "comment-skipped",
	})
	exit2, stdout2, _ := l2.run("batch", "--name", "b-notpending", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l2.dir, "b-notpending"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit2 != 0 {
		t.Fatalf("batch exit %d", exit2)
	}
	contains(t, stdout2, "members=1")
}

// 17. TestBatchOKCarriesHoldsDispositionsAndReviewers
func TestBatchOKCarriesHoldsDispositionsAndReviewers(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revPath, revSHA := testReviewerCommit(t, l, defaultReviewersTSV)

	l.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:1101", Who: "alice", Word: "hold", Head: l.heads[1],
		At: "2026-09-19T02:34:25Z", Source: "comment-rule",
	})

	exit, stdout, _ := l.run("batch", "--name", "b-ok-fields", "--pr", "1,2",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-ok-fields"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revPath)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stdout, "holds=1")
	contains(t, stdout, "reviewers="+revSHA)
	contains(t, stdout, "dispositions=")
}

// 21. TestUntypedCommentsIgnoreIsPerRunPrintedAndCarriesAReason
func TestUntypedCommentsIgnoreIsPerRunPrintedAndCarriesAReason(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	l.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:1201", Who: "unknown", Word: "pending", Head: l.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "comment-pending",
	})

	// Without --reason: exit 2
	exitFail, _, stderrFail := l.runBare("batch", "--name", "b-ign-fail", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-ign-fail"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile, "--untyped-comments", "ignore")
	if exitFail != 2 {
		t.Fatalf("missing --reason must exit 2, got %d", exitFail)
	}
	contains(t, stderrFail, "--untyped-comments=ignore requires --reason <text>")

	// With --reason: drops nothing, carries untyped=ignored reason="x"
	exit, stdout, _ := l.run("batch", "--name", "b-ign", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-ign"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile, "--untyped-comments", "ignore", "--reason", "tested")
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	contains(t, stdout, "members=1")
	contains(t, stdout, `untyped=ignored reason="tested"`)

	// Next run without the flag drops the member again
	exit2, _, stderr2 := l.run("batch", "--name", "b-ign2", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-ign2"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit2 != 0 {
		t.Fatalf("batch exit %d", exit2)
	}
	contains(t, stderr2, "BATCH DROP #1 reason=\"head "+merge.Short(l.heads[1])+" has a pending comment\"")
}

// 27. TestNoRequireHoldsWaivesTheForgeSourcesOnlyAndIsPrinted: recorded HOLD and forge HOLD;
// under --no-require-holds --reason x the recorded one still drops the member and every line
// carries holds=waived reason="x".
func TestNoRequireHoldsWaivesTheForgeSourcesOnlyAndIsPrinted(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "b-waived")
	laneDir := filepath.Join(l.dir, "lane-waived")

	// The recorded HOLD is a REAL lane read record (SPEC-DECIDE reading 3, *The inputs*
	// (a)), written through the read verb. It is not a Source:"record" verdict planted
	// through FakeHost.Verdicts, because production GH.Verdicts only ever returns forge
	// sources -- a fixture that returns a lane record makes this test a false green.
	if exit, _, errb := l.run("init", "--lane", laneDir, "--repo", "o/n", "--base", "dev", "--lane-branch", "nova-merge/dev"); exit != 0 {
		t.Fatalf("init lane failed: %s", errb)
	}
	if exit, _, errb := l.run("read", "--lane", laneDir, "--pr", "1", "--who", "alice", "--verdict", "hold", "--head", l.heads[1]); exit != 0 {
		t.Fatalf("read verb failed: %s", errb)
	}

	// A forge HOLD as well: the waiver drops the forge sources whole, so the recorded
	// hold is the one that drops the member.
	l.host.SetVerdicts(1,
		merge.Verdict{ID: "comment:1701", Who: "alice", Word: "hold", Head: l.heads[1], At: "2026-09-19T02:00:00Z", Source: "comment-rule"},
	)

	exit, stdout, stderr := l.run("batch", "--name", "b-waived", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--lane", laneDir, "--no-require-holds", "--reason", "emergency")
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	// Recorded hold still drops the member
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l.heads[1])+" carries an unreleased HOLD\" who=alice hold=record:")
	contains(t, stderr, "source=record")
	contains(t, stdout, `holds=waived reason="emergency"`)
}

// 29. TestRemovingMayHoldByCommitReleasesAndTheReceiptNamesTheCommit
func TestRemovingMayHoldByCommitReleasesAndTheReceiptNamesTheCommit(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)

	// Commit 1: alice may-hold
	testReviewerCommit(t, l, "alice\talice\tyes\n")

	// Commit 2: alice may-hold removed
	revPath2, revSHA2 := testReviewerCommit(t, l, "alice\talice\tno\n")

	// Member carries a comment hold from alice
	l.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:1801", Who: "alice", Word: "hold", Head: l.heads[1],
		At: "2026-09-19T01:00:00Z", Source: "comment-rule",
	})

	exit, stdout, _ := l.run("batch", "--name", "b-commit-rel", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-commit-rel"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revPath2)
	if exit != 0 {
		t.Fatalf("batch exit %d", exit)
	}
	// Hold is no longer active; PR is admitted
	contains(t, stdout, "members=1")
	contains(t, stdout, "reviewers="+revSHA2)
	absent(t, stdout, "alice")
}

// 34. TestGetReviewersSHARefusesUncommittedOrMissingRepo (SPEC-DECIDE reading 3, Johnny row 3):
// Reviewer file outside a repo or uncommitted must refuse exit 2.
func TestGetReviewersSHARefusesUncommittedOrMissingRepo(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "b-rev-err")

	// Case A: File outside git repo
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "reviewers.tsv")
	if err := os.WriteFile(outsideFile, []byte("rowan\trowan\tyes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr := l.run("batch", "--name", "b1", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", outsideFile)
	if exit != 2 {
		t.Fatalf("batch with reviewer file outside repo must refuse exit 2, got %d", exit)
	}
	contains(t, stderr, "reviewer file")

	// Case B: Uncommitted file in git repo
	uncommittedFile := filepath.Join(l.dir, "uncommitted-reviewers.tsv")
	if err := os.WriteFile(uncommittedFile, []byte("rowan\trowan\tyes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr = l.run("batch", "--name", "b2", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", uncommittedFile)
	if exit != 2 {
		t.Fatalf("batch with uncommitted reviewer file must refuse exit 2, got %d", exit)
	}
	contains(t, stderr, "reviewer file")
}

// 35. TestEmptyOrMalformedReviewersFileRefuses (SPEC-DECIDE reading 3, row 9):
// An empty reviewer file must refuse exit 2, never proceed unfenced.
func TestEmptyOrMalformedReviewersFileRefuses(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "b-empty-rev")

	emptyFile, _ := testReviewerCommit(t, l, "\n# only comments\n")
	exit, _, stderr := l.run("batch", "--name", "b-empty", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", emptyFile)
	if exit != 2 {
		t.Fatalf("batch with empty reviewer file must refuse exit 2, got %d", exit)
	}
	contains(t, stderr, "reviewer file")
}

// 36. TestReadReleasesRequiresScopeAndValidID (SPEC-DECIDE reading 3, row 12):
// --releases without --scope or with malformed ID must refuse exit 2.
func TestReadReleasesRequiresScopeAndValidID(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("dev")

	// Case A: --releases without --scope
	exit, _, stderr := l.run("read", "--lane", l.lane, "--pr", "1", "--who", "rowan",
		"--verdict", "approve", "--head", strings.Repeat("a", 40),
		"--releases", "comment:101")
	if exit != 2 {
		t.Fatalf("read --releases without --scope must refuse exit 2, got %d", exit)
	}
	contains(t, stderr, "--releases requires --scope")

	// Case B: --releases with malformed hold ID
	exit, _, stderr = l.run("read", "--lane", l.lane, "--pr", "1", "--who", "rowan",
		"--verdict", "approve", "--head", strings.Repeat("a", 40),
		"--scope", "parser", "--releases", "bad-hold-id")
	if exit != 2 {
		t.Fatalf("read --releases with malformed id must refuse exit 2, got %d", exit)
	}
	contains(t, stderr, "release id")
}

// 1. TestAHeldHeadIsDroppedFromABatchAndRefusedAtLand: #1572's timeline as a fixture:
// member #1 has a hold comment and is dropped from batch; a hold comment posted between
// BATCH OK and land refuses the landing on land's own fresh read.
func TestAHeldHeadIsDroppedFromABatchAndRefusedAtLand(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	l.host.SetVerdicts(1, merge.Verdict{
		ID: "comment:101", Who: "alice", Word: "hold", Head: l.heads[1],
		At: "2026-09-19T02:34:25Z", Source: "comment-rule",
	})

	exit, stdout, stderr := l.run("batch", "--name", "integration-hold", "--pr", "1,3",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)

	if exit == 0 {
		t.Fatalf("the batch went green over a held member\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"head "+merge.Short(l.heads[1])+" carries an unreleased HOLD\" who=alice hold=comment:101 source=comment-rule")
	contains(t, stdout, "dropped=1")
	absent(t, stderr, "BATCH MERGED #1 ")
}

// 20. TestThereIsNoOptInStrictFlag: class test on batch (land, queue sweep and react left with the per-PR lander)
func TestThereIsNoOptInStrictFlag(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	exit, _, stderr := l.runBare("batch", "--strict-comments")
	if exit != 2 {
		t.Fatalf("batch with --strict-comments must exit 2, got %d", exit)
	}
	contains(t, stderr, "-strict-comments")

}

// 26. TestThereIsNoFlagThatIgnoresOneHold: class test on batch (land, queue sweep and react left with the per-PR lander)
func TestThereIsNoFlagThatIgnoresOneHold(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	exit, _, stderr := l.runBare("batch", "--ignore-hold", "1")
	if exit != 2 {
		t.Fatalf("batch with --ignore-hold must exit 2, got %d", exit)
	}
	contains(t, stderr, "-ignore-hold")

}

// 28. TestReviewersXorNoRequireHolds: neither: exit 2; both: exit 2; without reason: exit 2.
// SPEC-DECIDE reading 3 demanded test 28 on batch (land left with the per-PR lander).
// A batch that omits the XOR is the leftover --ignore-hold under another spelling: lab.run
// injects the waiver, so only runBare can see it. Each side still batches.
func TestReviewersXorNoRequireHolds(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "b-xor")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	// Neither: exit 2
	exitNeither, _, stderrNeither := l.runBare("batch", "--name", "b1", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")
	if exitNeither != 2 {
		t.Fatalf("neither flag must exit 2, got %d", exitNeither)
	}
	contains(t, stderrNeither, "exactly one of --reviewers <file> or --no-require-holds --reason <text> is required")

	// Both: exit 2
	exitBoth, _, stderrBoth := l.runBare("batch", "--name", "b2", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile, "--no-require-holds", "--reason", "x")
	if exitBoth != 2 {
		t.Fatalf("both flags must exit 2, got %d", exitBoth)
	}
	contains(t, stderrBoth, "exactly one of --reviewers <file> or --no-require-holds --reason <text> is required")

	// No reason: exit 2
	exitNoReason, _, stderrNoReason := l.runBare("batch", "--name", "b3", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--no-require-holds")
	if exitNoReason != 2 {
		t.Fatalf("missing --reason must exit 2, got %d", exitNoReason)
	}
	contains(t, stderrNoReason, "--no-require-holds requires --reason <text>")

	// Negatives: each XOR side still batches.
	if err := os.MkdirAll(l.lane, 0755); err != nil {
		t.Fatal(err)
	}
	exitRev, stdoutRev, stderrRev := l.runBare("batch", "--name", "b-rev", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-rev"), "--base", "dev", "--timeout", "5m",
		"--lane", l.lane, "--reviewers", revFile)
	if exitRev != 0 {
		t.Fatalf("batch --reviewers --lane must still run, exit %d\n%s\n%s", exitRev, stdoutRev, stderrRev)
	}
	contains(t, stdoutRev, "BATCH OK")

	exitWaive, stdoutWaive, stderrWaive := l.runBare("batch", "--name", "b-waive", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b-waive"), "--base", "dev", "--timeout", "5m",
		"--lane", l.lane, "--no-require-holds", "--reason", "emergency")
	if exitWaive != 0 {
		t.Fatalf("batch --no-require-holds --reason --lane must still run, exit %d\n%s\n%s", exitWaive, stdoutWaive, stderrWaive)
	}
	contains(t, stdoutWaive, "BATCH OK")
	contains(t, stdoutWaive, `holds=waived reason="emergency"`)

}

// 33. TestBatchPreservesRealLaneReadHolds: real lane read records are loaded
// independently of the forge, and survive forge waivers and forge errors.
func TestBatchPreservesRealLaneReadHolds(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	laneDir := filepath.Join(l.dir, "lane")

	// Initialize real lane
	if exit, _, errb := l.run("init", "--lane", laneDir, "--repo", "o/n", "--base", "dev", "--lane-branch", "nova-merge/dev"); exit != 0 {
		t.Fatalf("init lane failed: %s", errb)
	}

	// Write real lane read record using standard "read" verb
	if exit, _, errb := l.run("read", "--lane", laneDir, "--pr", "1", "--who", "rowan", "--verdict", "hold", "--head", l.heads[1]); exit != 0 {
		t.Fatalf("read verb failed: %s", errb)
	}

	// Run batch with --lane and --no-require-holds --reason: lane hold must drop member #1
	exit, stdout, stderr := l.run("batch", "--name", "lane-hold-test", "--pr", "1,3",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--lane", laneDir, "--no-require-holds", "--reason", "waive forge sources")

	contains(t, stderr, `BATCH DROP #1 reason="head `+l.heads[1][:12]+` carries an unreleased HOLD" who=rowan hold=record:`)
	contains(t, stderr, "source=record")
	_ = exit
	_ = stdout

}

// 38. TestReviewersWithoutLaneRefuses (Rowan row B): --lane is required when --reviewers is specified
func TestReviewersWithoutLaneRefuses(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	// batch with --reviewers but no --lane
	exit, _, stderr := l.runBare("batch", "--name", "no-lane", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)
	if exit != 2 {
		t.Fatalf("batch without --lane must exit 2, got %d\nstderr: %s", exit, stderr)
	}
	contains(t, stderr, "--lane is required when --reviewers or --no-require-holds is specified")

}

// 38b. TestNoRequireHoldsWithoutLaneRefuses (red team #1896): --no-require-holds waives
// the FORGE sources only, never the lane's own read records (SPEC-DECIDE reading 3,
// *The inputs* (a)). The fold can only read those records when --lane names the lane, so
// a batch that waives the forge and names no lane is a run that cannot see a
// recorded HOLD -- the exact door --ignore-hold would have opened. Demanded test 27 was
// a false green over FakeHost Source:"record"; this one writes a real nova-merge read
// HOLD, then batch without --lane (XOR satisfied by --no-require-holds --reason,
// no --reviewers). It must refuse exit 2 and print no BATCH OK.
func TestNoRequireHoldsWithoutLaneRefuses(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "b-waive-no-lane")
	laneDir := filepath.Join(l.dir, "lane-held")

	if exit, _, errb := l.run("init", "--lane", laneDir, "--repo", "o/n", "--base", "dev", "--lane-branch", "nova-merge/dev"); exit != 0 {
		t.Fatalf("init lane failed: %s", errb)
	}
	if exit, _, errb := l.run("read", "--lane", laneDir, "--pr", "1", "--who", "alice", "--verdict", "hold", "--head", l.heads[1]); exit != 0 {
		t.Fatalf("read verb failed: %s", errb)
	}

	exit, stdout, stderr := l.runBare("batch", "--name", "waive-no-lane", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--no-require-holds", "--reason", "emergency")
	if exit != 2 {
		t.Fatalf("batch --no-require-holds without --lane must refuse exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "--lane is required")
	absent(t, stdout, "BATCH OK")
	absent(t, stderr, "BATCH MERGED")

}

// 39. TestBatchRefusesMalformedLaneRecord (Rowan row A / Stella blocker 1):
// A corrupt lane record file must cause batch to refuse exit 2, naming the bad file.
func TestBatchRefusesMalformedLaneRecord(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	readsDir := filepath.Join(l.lane, merge.ReadsDir, "1")
	if err := os.MkdirAll(readsDir, 0755); err != nil {
		t.Fatal(err)
	}
	badFile := filepath.Join(readsDir, "corrupt.json")
	if err := os.WriteFile(badFile, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	// batch with corrupt lane record must exit 2 and name badFile
	exit, _, stderr := l.run("batch", "--name", "bad-lane-record", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--lane", l.lane, "--reviewers", revFile)
	if exit != 2 {
		t.Fatalf("batch with malformed lane record must exit 2, got %d\nstderr: %s", exit, stderr)
	}
	contains(t, stderr, "BATCH REFUSED")
	contains(t, stderr, "corrupt.json")

}

// 40. TestBatchRefusesNonexistentLaneDirectory (Stella blocker 1):
// A supplied missing lane directory must refuse exit 2.
func TestBatchRefusesNonexistentLaneDirectory(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)
	missingLane := filepath.Join(l.dir, "nonexistent-lane")

	exit, _, stderr := l.run("batch", "--name", "missing-lane", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--lane", missingLane, "--reviewers", revFile)
	if exit != 2 {
		t.Fatalf("batch with nonexistent lane directory must exit 2, got %d\nstderr: %s", exit, stderr)
	}
	contains(t, stderr, "BATCH REFUSED")

}

// 41. TestReviewersWithLaneNoneRefuses (Stella blocker 3): literal --lane none with --reviewers must be refused
func TestReviewersWithLaneNoneRefuses(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	revFile := testReviewerFile(t, l.dir, defaultReviewersTSV)

	// batch with --reviewers and --lane none
	exit, _, stderr := l.runBare("batch", "--name", "lane-none", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--lane", "none", "--reviewers", revFile)
	if exit != 2 {
		t.Fatalf("batch with --lane none must exit 2, got %d\nstderr: %s", exit, stderr)
	}
	contains(t, stderr, "--lane is required when --reviewers or --no-require-holds is specified")

}
