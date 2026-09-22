package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// nova-tools #2550, the gate's own control.
//
// The live break, from tmp/session-0919b/land-schema-5/gate-cells-1338.log:
//
//	BATCH DROP #1551 reason="head 5adf9cb212a8 carries an unreleased HOLD" who=emma
//	hold=comment:5767845547 source=comment-rule held_at=67712d4eb59a carried=yes
//	at=2026-09-21T21:35:02Z conf=-
//
// held_at is a head the branch moved past; the current head carries four typed APPROVE
// comments from the same friend. 57 consecutive ticks took the same 16 cell pull requests
// and dropped every one of them, members=none, for 11.5 h.
//
// This drives the whole verb -- forge comment JSON, ParseComment, the hold fold, the DROP
// line -- rather than the fold alone, because what cost the 11.5 h was the DROP line the
// gate printed, not a function's return value.

const carriedHoldReviewers2550 = "who\tlogins\tmay-hold\nemma\temma-claude\tyes\n"

// supersededHead2550 is the held_at prefix from the log, padded: the log prints
// merge.Short and never the whole object name.
const supersededHead2550 = "67712d4eb59a" + "0f1e2d3c4b5a69788796a5b4c3d2"

func commentJSON2550(t *testing.T, bodies []struct{ Body, At string }) string {
	t.Helper()
	type user struct {
		Login string `json:"login"`
	}
	type wire struct {
		ID        int64  `json:"id"`
		User      user   `json:"user"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]wire, 0, len(bodies))
	for i, b := range bodies {
		out = append(out, wire{ID: int64(5767845547 + i), User: user{Login: "emma-claude"}, Body: b.Body, CreatedAt: b.At})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal comments: %v", err)
	}
	return string(raw)
}

func TestTheGateTakesAMemberWhoseCarriedHoldTheSameFriendApprovedAtHead(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revFile := testReviewerFile(t, l.dir, carriedHoldReviewers2550)
	head := l.heads[1]

	// The hold is pinned at the current head. A pin off head is not a hold
	// (#2710) and would make this pass without the later APPROVE; the release
	// is what has to take the member.
	comments := commentJSON2550(t, []struct{ Body, At string }{
		{Body: "DISPOSITION who=emma head=" + head + " verdict=HOLD\nnotes.txt is untracked in the cell.",
			At: "2026-09-21T21:35:02Z"},
		{Body: "DISPOSITION who=emma head=" + head + " verdict=APPROVE score=10/10", At: "2026-09-22T01:48:00Z"},
		{Body: "DISPOSITION who=emma head=" + head + " verdict=APPROVE score=10/10", At: "2026-09-22T02:45:00Z"},
	})
	l.host.SetRawVerdicts(1, comments, "[]")

	exit, stdout, stderr := l.run("batch", "--name", "integration-2550", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b2550"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)

	if exit != 0 {
		t.Fatalf("the gate went red over a member whose hold its author released at head: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	absent(t, stderr, "BATCH DROP #1 ")
	absent(t, stderr, "carried=yes")
	contains(t, stdout, "members=1")
	contains(t, stdout, "dropped=none")
}

// nova-tools #2615 follow-up: the same-friend match is case-insensitive end to end
// through `nova-merge batch`, not only in the fold. Measured 2026-09-22 20:14Z, lane
// land-1615: BATCH DROP #2587 held who=johnny though the pull request carried a typed
// `who=johnny` APPROVE at head, because the HOLD comment had been typed `who=Johnny`.
func TestTheGateTakesAMemberWhoseCarriedHoldWasReleasedByADifferentlyCasedApprove(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revFile := testReviewerFile(t, l.dir, carriedHoldReviewers2550)
	head := l.heads[1]

	// Both lines name the current head, so the fold has to match who=Emma to
	// who=emma. A HOLD pinned at another head would be set aside (#2710)
	// before case was ever compared.
	comments := commentJSON2550(t, []struct{ Body, At string }{
		{Body: "DISPOSITION who=Emma head=" + head + " verdict=HOLD score=4/10\nsome finding.",
			At: "2026-09-21T21:35:02Z"},
		{Body: "DISPOSITION who=emma head=" + head + " verdict=APPROVE score=10/10", At: "2026-09-22T01:48:00Z"},
	})
	l.host.SetRawVerdicts(1, comments, "[]")

	exit, stdout, stderr := l.run("batch", "--name", "integration-2615case", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b2615case"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)

	if exit != 0 {
		t.Fatalf("the gate went red over a case-mismatched HOLD/APPROVE from the same friend: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	absent(t, stderr, "BATCH DROP #1 ")
	absent(t, stderr, "carried=yes")
	contains(t, stdout, "members=1")
	contains(t, stdout, "dropped=none")
}

// A HOLD whose head= is the pull request's current head still counts. Taking
// the APPROVE away must still drop the member. carried=no: the pin is this head.
func TestTheGateStillDropsAHoldPinnedAtHeadNobodyAnswered(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revFile := testReviewerFile(t, l.dir, carriedHoldReviewers2550)
	head := l.heads[1]

	comments := commentJSON2550(t, []struct{ Body, At string }{
		{Body: "DISPOSITION who=emma head=" + head + " verdict=HOLD\nnotes.txt is untracked in the cell.",
			At: "2026-09-21T21:35:02Z"},
	})
	l.host.SetRawVerdicts(1, comments, "[]")

	exit, stdout, stderr := l.run("batch", "--name", "integration-2550b", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b2550b"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)

	if exit != 0 {
		t.Fatalf("batch exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, fmt.Sprintf("BATCH DROP #1 reason=\"head %s carries an unreleased HOLD\" who=emma hold=comment:5767845547 source=comment-rule held_at=%s carried=no",
		merge.Short(head), merge.Short(head)))
	contains(t, stdout, "dropped=1")
}

// nova-tools #2710: a HOLD whose first line pins head= to some other commit is
// not a hold at the current head. The gate takes the member.
func TestTheGateDoesNotDropAHoldPinnedOffTheCurrentHead(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	revFile := testReviewerFile(t, l.dir, carriedHoldReviewers2550)

	comments := commentJSON2550(t, []struct{ Body, At string }{
		{Body: "DISPOSITION who=emma head=" + supersededHead2550 + " verdict=HOLD\nnotes.txt is untracked in the cell.",
			At: "2026-09-21T21:35:02Z"},
	})
	l.host.SetRawVerdicts(1, comments, "[]")

	exit, stdout, stderr := l.run("batch", "--name", "integration-2710", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "b2710"), "--base", "dev", "--timeout", "5m",
		"--reviewers", revFile)

	if exit != 0 {
		t.Fatalf("the gate went red over a HOLD pinned at another head: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	absent(t, stderr, "BATCH DROP #1 ")
	contains(t, stdout, "members=1")
	contains(t, stdout, "dropped=none")
}
