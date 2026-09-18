package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// `land` IS THE ONE CALLER OF THE ONE DOOR (Glenn, 2026-09-18: nothing reaches the dev
// merge queue but a batch).
//
// The gate -- `nova-merge batch` -- builds the integration branch and prints BATCH OK; the
// caller pushes it and opens the pull request; and `land` is what puts that pull request in
// the queue, at the front, after checking two things the gate cannot: that the pull request
// is still open, and that ITS OWN checks are green on the head the receipt names. Nothing
// else in the tools may enqueue anything.

// fakeLandEnqueue is the forge's queue edge, with no network in it.
type fakeLandEnqueue struct {
	err      error
	enqueued []int
	jumps    []bool
}

func (f *fakeLandEnqueue) PullRequestID(ctx context.Context, pr int) (string, error) {
	return "PR_node", nil
}

func (f *fakeLandEnqueue) EnqueuePullRequest(ctx context.Context, id string, jump bool) error {
	if f.err != nil {
		return f.err
	}
	f.enqueued = append(f.enqueued, len(f.enqueued)+1)
	f.jumps = append(f.jumps, jump)
	return nil
}

// landDeps hands the verb a fake lane host and a fake queue edge, and records neither
// timeout nor repo beyond what the test asserts.
func landDeps(h *merge.FakeHost, q *fakeLandEnqueue) Deps {
	return Deps{
		NewHost:        func(repo string, timeout time.Duration) merge.Host { return h },
		NewEnqueueHost: func(repo string, timeout time.Duration) merge.EnqueueHost { return q },
	}
}

func runLand(t *testing.T, h *merge.FakeHost, q *fakeLandEnqueue, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit := run(args, &out, &errb, landDeps(h, q))
	return exit, out.String(), errb.String()
}

// greenBatchPR is an open pull request off a batch's branch whose own checks are green.
func greenBatchPR(t *testing.T, number int, head string) *merge.FakeHost {
	t.Helper()
	h := merge.NewFakeHost()
	h.PRs[number] = merge.PR{Number: number, HeadRef: "rowan/integration-6", HeadOID: head, Mergeable: "MERGEABLE"}
	h.ChecksBy[head] = merge.Checks{Green: 9}
	return h
}

// A batch's pull request, green, goes to the FRONT of the queue.
func TestLandEnqueuesAGreenBatchAtTheFront(t *testing.T) {
	head := strings.Repeat("a", 40)
	h, q := greenBatchPR(t, 1341, head), &fakeLandEnqueue{}
	exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "mas-bandwidth/nova-tools", "--pr", "1341")
	if exit != 0 {
		t.Fatalf("land: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(q.enqueued) != 1 || !q.jumps[0] {
		t.Fatalf("want one enqueue at the front, got enqueued=%v jumps=%v", q.enqueued, q.jumps)
	}
	contains(t, stdout, "LAND OK pr=1341")
	contains(t, stdout, "jump=true")
	if lines := strings.Count(stdout, "\n"); lines != 1 {
		t.Fatalf("land prints one line; got %d:\n%s", lines, stdout)
	}
}

// THE LOCK: an ordinary card's pull request, however green, is not a batch and does not
// enter the queue. The forge's queue is never touched.
func TestLandRefusesAPullRequestThatIsNotABatch(t *testing.T) {
	head := strings.Repeat("b", 40)
	h, q := merge.NewFakeHost(), &fakeLandEnqueue{}
	h.PRs[1207] = merge.PR{Number: 1207, HeadRef: "rowan/impl-something", HeadOID: head, Mergeable: "MERGEABLE"}
	h.ChecksBy[head] = merge.Checks{Green: 9}
	exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1207")
	if exit != 1 {
		t.Fatalf("a non-batch head must be REFUSED at exit 1, got %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(q.enqueued) != 0 {
		t.Fatalf("the queue was touched: %v", q.enqueued)
	}
	contains(t, stderr, "LAND REFUSED")
	if !strings.Contains(stderr, merge.BatchBranchPrefix) {
		t.Errorf("the refusal never names the shape it wanted: %s", stderr)
	}
}

// A pull request whose own checks are not green is not landed, whatever the gate said
// about the tree: the gate ran on a bench and CI runs on the forge, and the queue's
// entry is the one CI judged.
func TestLandRefusesAPullRequestWhoseChecksAreNotGreen(t *testing.T) {
	head := strings.Repeat("c", 40)
	for name, checks := range map[string]merge.Checks{
		"red":     {Green: 3, Red: 1, RedNames: []string{"test (ubuntu)"}},
		"pending": {Green: 3, Pending: 2},
		"none":    {},
	} {
		t.Run(name, func(t *testing.T) {
			h, q := greenBatchPR(t, 1341, head), &fakeLandEnqueue{}
			h.ChecksBy[head] = checks
			exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1341")
			if exit != 1 {
				t.Fatalf("exit %d, want 1\n%s\n%s", exit, stdout, stderr)
			}
			if len(q.enqueued) != 0 {
				t.Fatalf("the queue was touched: %v", q.enqueued)
			}
			contains(t, stderr, "LAND REFUSED")
		})
	}
}

// A pull request that is already merged or closed is not enqueued again.
func TestLandRefusesAPullRequestThatIsNotOpen(t *testing.T) {
	head := strings.Repeat("d", 40)
	h, q := greenBatchPR(t, 1341, head), &fakeLandEnqueue{}
	pr := h.PRs[1341]
	pr.Merged = true
	h.PRs[1341] = pr
	exit, _, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1341")
	if exit != 1 || len(q.enqueued) != 0 {
		t.Fatalf("a merged pull request was landed again: exit %d enqueued %v\n%s", exit, q.enqueued, stderr)
	}
}

// A receipt is how a batch pushed under another name gets in, and it is read from a FILE
// because the gate's line is long and a caller pipes it: `nova-merge batch ... | tail -1`.
func TestLandTakesABatchOKReceiptFromAFile(t *testing.T) {
	head := strings.Repeat("e", 40)
	h, q := merge.NewFakeHost(), &fakeLandEnqueue{}
	h.PRs[99] = merge.PR{Number: 99, HeadRef: "rowan/nightly", HeadOID: head, Mergeable: "MERGEABLE"}
	h.ChecksBy[head] = merge.Checks{Green: 9}
	path := filepath.Join(t.TempDir(), "receipt")
	line := "BATCH OK name=nightly base=" + strings.Repeat("f", 40) + " head=" + head + " members=1301,1302 dropped=none\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "99", "--receipt-file", path)
	if exit != 0 {
		t.Fatalf("land: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(q.enqueued) != 1 {
		t.Fatalf("the receipt did not open the door: %v", q.enqueued)
	}
	contains(t, stdout, "members=1301,1302")
}

// A receipt for another commit is refused: the gate tested a tree that is not this one.
func TestLandRefusesAReceiptForAnotherHead(t *testing.T) {
	head := strings.Repeat("e", 40)
	h, q := merge.NewFakeHost(), &fakeLandEnqueue{}
	h.PRs[99] = merge.PR{Number: 99, HeadRef: "rowan/nightly", HeadOID: head, Mergeable: "MERGEABLE"}
	h.ChecksBy[head] = merge.Checks{Green: 9}
	receipt := "BATCH OK name=nightly base=" + strings.Repeat("f", 40) + " head=" + strings.Repeat("9", 40) + " members=1301 dropped=none"
	exit, _, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "99", "--receipt", receipt)
	if exit != 1 || len(q.enqueued) != 0 {
		t.Fatalf("a receipt for another head landed: exit %d enqueued %v\n%s", exit, q.enqueued, stderr)
	}
}

// --no-jump lands behind whatever is already queued, for the caller who is adding a second
// batch rather than replacing the queue's order.
func TestLandWithoutJumpQueuesBehind(t *testing.T) {
	head := strings.Repeat("a", 40)
	h, q := greenBatchPR(t, 1341, head), &fakeLandEnqueue{}
	exit, stdout, stderr := runLand(t, h, q, "land", "--repo", "o/n", "--pr", "1341", "--no-jump")
	if exit != 0 {
		t.Fatalf("land: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(q.jumps) != 1 || q.jumps[0] {
		t.Fatalf("--no-jump still jumped: %v", q.jumps)
	}
	contains(t, stdout, "jump=false")
}

// The flags are the no-guessing law: no repo, no pull request, no run.
func TestLandRefusesAnInvocationThatGuesses(t *testing.T) {
	for _, args := range [][]string{
		{"land"},
		{"land", "--repo", "o/n"},
		{"land", "--pr", "1341"},
		{"land", "--repo", "not-a-slug", "--pr", "1341"},
		{"land", "--repo", "o/n", "--pr", "0"},
		{"land", "--repo", "o/n", "--pr", "1341", "--receipt", "x", "--receipt-file", "y"},
	} {
		h, q := greenBatchPR(t, 1341, strings.Repeat("a", 40)), &fakeLandEnqueue{}
		exit, stdout, stderr := runLand(t, h, q, args...)
		if exit != 2 {
			t.Errorf("%v: exit %d, want 2 (could not run)\n%s\n%s", args, exit, stdout, stderr)
		}
		if len(q.enqueued) != 0 {
			t.Errorf("%v: reached the forge", args)
		}
	}
}
