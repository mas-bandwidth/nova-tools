package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// #1572. A HELD HEAD REACHED dev THROUGH A GREEN GATE.
//
// On 2026-09-19 `nova-merge batch` read #1551 at 02:31:19Z -- OPEN, base dev,
// MERGEABLE/CLEAN, ci-ok success at 6adbbd1d -- and admitted it. At 02:34:25Z a scoped
// HOLD was posted on that very head as a pull request comment. At 02:43:03Z the batch
// merged and the held commit was on dev. Everything the gate is specified to check was
// checked and was true; the gate had no way to know.
//
// These tests are that morning, in the lab: the same order, the same shapes, and the
// same question asked of the tool rather than of the person driving it.

// The red run of #1572: the gate reads the hold and DROPS the member by name.
func TestBatchDropsAMemberCarryingAnUnliftedHold(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	// The hold arrives on #1's own head, from a named reader, as a comment -- which is
	// how a scoped objection is expressed on this repository.
	l.host.SetVerdicts(1, merge.Verdict{
		Who: "gafferongames", Word: "hold", Head: l.heads[1],
		At: "2026-09-19T02:34:25Z", Source: "comment",
	})

	exit, stdout, stderr := l.run("batch", "--name", "integration-hold", "--pr", "1,3",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--readers", "gafferongames")

	// #3 poisons the batch, so the run is red -- but the thing under test is the line
	// above it: #1 is dropped BEFORE the merge, with the login and the instant on it.
	if exit == 0 {
		t.Fatalf("the batch went green over a held member\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	contains(t, stderr, "BATCH DROP #1 reason=\"an unlifted HOLD from gafferongames at 2026-09-19T02:34:25Z")
	contains(t, stdout, "dropped=1")
	absent(t, stdout, "members=1")
	// The member is dropped, not merged: the held head never reaches the gate's tree.
	absent(t, stderr, "BATCH MERGED #1 ")
}

// AN APPROVE AT THIS VERY HEAD LIFTS THE HOLD, and nothing else does. This is the same
// reader, after the hold, naming the head the gate is about to merge.
func TestAnApproveAtThisHeadFromTheSameReaderLiftsTheHold(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	l.host.SetVerdicts(1,
		merge.Verdict{Who: "gafferongames", Word: "hold", Head: l.heads[1], At: "2026-09-19T02:34:25Z", Source: "comment"},
		merge.Verdict{Who: "gafferongames", Word: "approve", Head: l.heads[1], At: "2026-09-19T03:11:13Z", Source: "comment"},
	)

	exit, stdout, stderr := l.run("batch", "--name", "integration-lifted", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--readers", "gafferongames")

	if exit != 0 {
		t.Fatalf("a lifted hold still stopped the member: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "members=1")
	contains(t, stdout, "dropped=none")
	absent(t, stderr, "BATCH DROP #1")
}

// AN APPROVE AT AN OLDER HEAD LIFTS NOTHING. The reader approved code this batch does not
// carry, which is the rule already applied to a stale ci-ok and to a stale read record.
func TestAnApproveAtAnOlderHeadDoesNotLiftTheHold(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	l.host.SetVerdicts(1,
		merge.Verdict{Who: "gafferongames", Word: "hold", Head: l.heads[1], At: "2026-09-19T02:34:25Z", Source: "comment"},
		merge.Verdict{Who: "gafferongames", Word: "approve", Head: "6adbbd1d89869455e920a43ccf7378daf4375add", At: "2026-09-19T03:11:13Z", Source: "comment"},
	)

	exit, stdout, stderr := l.run("batch", "--name", "integration-stale", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--readers", "gafferongames")

	// A batch whose every member was dropped still runs its gate and is exit 0, so what
	// is asserted is the DROP: the member is not in the tree that was built.
	if exit != 0 {
		t.Fatalf("batch: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "members=none")
	contains(t, stdout, "dropped=1")
	contains(t, stderr, "BATCH DROP #1 reason=\"an unlifted HOLD from gafferongames")
}

// ONLY A NAMED READER'S WORD COUNTS. This lane's own status comments quote HOLD while
// reporting on one -- "**HOLD answered. New exact head ...**" was posted by this very
// lane on 2026-09-19 -- so a gate that counted anyone who types the word would have
// dropped several innocent members that night.
func TestAHoldFromSomeoneOutsideTheNamedReaderSetDoesNotDrop(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	l.host.SetVerdicts(1, merge.Verdict{
		Who: "some-lane-bot", Word: "hold", Head: l.heads[1],
		At: "2026-09-19T02:34:25Z", Source: "comment",
	})

	exit, stdout, stderr := l.run("batch", "--name", "integration-unnamed", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--readers", "gafferongames")

	if exit != 0 {
		t.Fatalf("a hold from a login nobody named dropped a member: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "members=1")
	absent(t, stderr, "BATCH DROP #1")
}

// THE ESCAPE IS A REFUSAL AND NOT A WALL -- and it is on the record, with the hold it
// stepped over named.
func TestIgnoreHoldAdmitsTheMemberAndSaysWhichHoldItSteppedOver(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	l.host.SetVerdicts(1, merge.Verdict{
		Who: "gafferongames", Word: "hold", Head: l.heads[1],
		At: "2026-09-19T02:34:25Z", Source: "comment",
	})

	exit, stdout, stderr := l.run("batch", "--name", "integration-ignored", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--readers", "gafferongames", "--ignore-hold", "1")

	if exit != 0 {
		t.Fatalf("--ignore-hold did not admit the member: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "members=1")
	contains(t, stderr, "BATCH NOTE #1 holds=ignored reason=\"--ignore-hold 1 was given: HOLD from gafferongames at 2026-09-19T02:34:25Z")
}

// A GATE THAT READS NOBODY'S HOLD IS THE GATE THAT ADMITTED A HELD HEAD. Neither the
// named set nor the loud waiver is a refusal at the flags, exit 2, before anything runs.
func TestBatchRefusesWithNeitherReadersNorTheLoudWaiver(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")

	exit, stdout, stderr := l.run("batch", "--name", "integration-noreaders", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 2 {
		t.Fatalf("a batch with no hold read and no waiver is exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "--readers <login,...>")
	contains(t, stderr, "#1572")
	if strings.Contains(stdout, "BATCH OK") {
		t.Error("the gate ran")
	}
}

// AND THE WAIVER SAYS SO OUT LOUD, the way --no-require-checks does.
func TestNoRequireHoldsRunsAndSaysTheHoldsWereNotRead(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	l.host.SetVerdicts(1, merge.Verdict{
		Who: "gafferongames", Word: "hold", Head: l.heads[1],
		At: "2026-09-19T02:34:25Z", Source: "comment",
	})

	exit, stdout, stderr := l.run("batch", "--name", "integration-unread", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--no-require-holds")

	if exit != 0 {
		t.Fatalf("--no-require-holds did not run: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "members=1")
	contains(t, stderr, "BATCH NOTE holds=unread reason=")
}

// THE SECOND-ORDER NOTE ON #1572, which is the half that would still have lost that
// morning: the hold arrived AFTER the gate said BATCH OK and BEFORE `land` ran, so a read
// only in `batch` admits it anyway. `land` asks again, on its own fresh look, over the
// batch AND every member its receipt names.
func TestLandRefusesWhenAMemberOfTheReceiptCarriesAnUnliftedHold(t *testing.T) {
	head := strings.Repeat("a", 40)
	const memberHead = "6adbbd1d89869455e920a43ccf7378daf4375add"
	h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}
	h.PRs[1551] = merge.PR{Number: 1551, HeadOID: memberHead, Mergeable: "MERGEABLE"}
	h.SetVerdicts(1551, merge.Verdict{
		Who: "gafferongames", Word: "hold", Head: memberHead,
		At: "2026-09-19T02:34:25Z", Source: "comment",
	})
	receipt := "BATCH OK name=integration-12t2 base=" + strings.Repeat("d", 40) +
		" head=" + head + " members=1551,1200 dropped=none skipped=none checks=required"

	exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1560",
		"--receipt", receipt, "--readers", "gafferongames")

	if exit != 1 {
		t.Fatalf("a landing over a held member is refused at exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	// THE QUEUE IS NEVER TOUCHED. That is the whole of it: on 2026-09-19 it was.
	if len(q.enqueued) != 0 {
		t.Fatalf("the queue was touched over a held member: %v", q.enqueued)
	}
	contains(t, stderr, "LAND REFUSED")
	contains(t, stderr, "pull request 1551 carries an unlifted HOLD from gafferongames at 2026-09-19T02:34:25Z")
	absent(t, stdout, "LAND OK")
}

// A HOLD ON THE BATCH'S OWN PULL REQUEST stops it too -- a batch a reader has held is not
// a batch that lands because its branch is the right shape.
func TestLandRefusesWhenTheBatchItselfCarriesAnUnliftedHold(t *testing.T) {
	head := strings.Repeat("a", 40)
	h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}
	h.SetVerdicts(1560, merge.Verdict{
		Who: "gafferongames", Word: "hold", Head: head,
		At: "2026-09-19T02:34:25Z", Source: "review",
	})

	exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1560",
		"--readers", "gafferongames")

	if exit != 1 {
		t.Fatalf("a held batch is refused at exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if len(q.enqueued) != 0 {
		t.Fatalf("the queue was touched over a held batch: %v", q.enqueued)
	}
	contains(t, stderr, "pull request 1560 carries an unlifted HOLD from gafferongames")
}

// AND THE LANDING GOES THROUGH once that same reader approves this very head.
func TestLandGoesThroughWhenTheHoldIsLiftedAtThisHead(t *testing.T) {
	head := strings.Repeat("a", 40)
	h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}
	h.SetVerdicts(1560,
		merge.Verdict{Who: "gafferongames", Word: "hold", Head: head, At: "2026-09-19T02:34:25Z", Source: "comment"},
		merge.Verdict{Who: "gafferongames", Word: "approve", Head: head, At: "2026-09-19T03:11:13Z", Source: "comment"},
	)

	exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1560",
		"--readers", "gafferongames")

	if exit != 0 {
		t.Fatalf("a lifted hold stopped the landing: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "LAND OK pr=1560")
}

// land REFUSES WITH NEITHER the named set nor the loud waiver, exit 2, before it reads
// anything at all.
func TestLandRefusesWithNeitherReadersNorTheLoudWaiver(t *testing.T) {
	head := strings.Repeat("a", 40)
	h, q := greenBatchPR(t, 1560, head), &fakeLandEnqueue{}

	exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1560")

	if exit != 2 {
		t.Fatalf("a landing with no hold read and no waiver is exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if len(q.enqueued) != 0 {
		t.Fatalf("the queue was touched: %v", q.enqueued)
	}
	contains(t, stderr, "--readers <login,...>")
	contains(t, stderr, "#1572")
}
