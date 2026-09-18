package merge

import (
	"context"
	"strings"
	"testing"
)

// THE ONE DOOR INTO THE MERGE QUEUE, under test.
//
// Glenn, 2026-09-18: nothing reaches the dev merge queue but a batch. Four pull requests
// were landed that morning by GitHub's own auto-merge, silently enabled by `gh pr merge`
// calls made on pull requests that were not green -- the enqueue happened hours later,
// from the forge, with nobody in the room. So: one function admits a pull request to the
// queue, it is a queue mutation and never a merge call, and it REFUSES a head that is not
// a batch's unless the caller presents that head's own BATCH OK receipt.
//
// Everything here drives a fake host. No test in this package reaches the network.

// fakeEnqueueHost records the two questions the one door asks the forge, and nothing in it
// can merge anything: a test that could not say what the forge was asked could not tell an
// enqueue from a merge.
type fakeEnqueueHost struct {
	ids   map[int]string
	idErr error
	err   error

	asked    []int
	enqueued []enqueueCall
}

type enqueueCall struct {
	ID   string
	Jump bool
}

func (f *fakeEnqueueHost) PullRequestID(ctx context.Context, pr int) (string, error) {
	f.asked = append(f.asked, pr)
	if f.idErr != nil {
		return "", f.idErr
	}
	id, ok := f.ids[pr]
	if !ok {
		id = "PR_unnamed"
	}
	return id, nil
}

func (f *fakeEnqueueHost) EnqueuePullRequest(ctx context.Context, id string, jump bool) error {
	if f.err != nil {
		return f.err
	}
	f.enqueued = append(f.enqueued, enqueueCall{ID: id, Jump: jump})
	return nil
}

func newFakeEnqueueHost() *fakeEnqueueHost {
	return &fakeEnqueueHost{ids: map[int]string{}}
}

// A batch's own branch needs no receipt: the shape IS the receipt, and it is the shape
// `nova-merge batch` builds and nothing else does.
func TestEnqueueTakesABatchBranch(t *testing.T) {
	h := newFakeEnqueueHost()
	h.ids[1341] = "PR_integration6"
	if err := NewEnqueuer(h).Enqueue(context.Background(),
		EnqueuePR{Number: 1341, HeadRef: "rowan/integration-6", HeadSHA: strings.Repeat("a", 40)}, true); err != nil {
		t.Fatalf("a batch branch is the one shape this door takes: %v", err)
	}
	if len(h.enqueued) != 1 || h.enqueued[0].ID != "PR_integration6" || !h.enqueued[0].Jump {
		t.Fatalf("want one enqueue of PR_integration6 with jump, got %v", h.enqueued)
	}
}

// THE REFUSAL THIS CARD EXISTS FOR: an ordinary card's branch, green or not, is not a
// batch. Nothing is asked of the forge at all -- the refusal is decided before the door
// is opened.
func TestEnqueueRefusesAHeadThatIsNotABatch(t *testing.T) {
	h := newFakeEnqueueHost()
	err := NewEnqueuer(h).Enqueue(context.Background(),
		EnqueuePR{Number: 1207, HeadRef: "rowan/impl-merge-queue", HeadSHA: strings.Repeat("b", 40)}, false)
	if err == nil {
		t.Fatal("an ordinary head enqueued itself; the queue takes batches only")
	}
	if _, ok := AsEnqueueRefusal(err); !ok {
		t.Fatalf("the refusal is this package's own, so a caller can tell it from a forge error: %v", err)
	}
	if !strings.Contains(err.Error(), BatchBranchPrefix) {
		t.Errorf("the refusal never names the shape it wanted (%s): %v", BatchBranchPrefix, err)
	}
	if len(h.asked) != 0 || len(h.enqueued) != 0 {
		t.Fatalf("the forge was reached on a refused enqueue: asked=%v enqueued=%v", h.asked, h.enqueued)
	}
}

// A head that is not a batch's branch may still be a batch's TREE -- a batch pushed under
// another name -- and the receipt is what says so: the BATCH OK line the gate printed for
// exactly this commit.
func TestEnqueueTakesANonBatchHeadWithThatHeadsReceipt(t *testing.T) {
	head := strings.Repeat("c", 40)
	h := newFakeEnqueueHost()
	h.ids[99] = "PR_99"
	receipt := "BATCH OK name=nightly base=" + strings.Repeat("d", 40) + " head=" + head + " members=1301,1302 dropped=none"
	if err := NewEnqueuer(h).Enqueue(context.Background(),
		EnqueuePR{Number: 99, HeadRef: "rowan/nightly", HeadSHA: head, Receipt: receipt}, false); err != nil {
		t.Fatalf("a BATCH OK receipt for this very head is the second key: %v", err)
	}
	if len(h.enqueued) != 1 || h.enqueued[0].Jump {
		t.Fatalf("want one enqueue without jump, got %v", h.enqueued)
	}
}

// A receipt for ANOTHER commit is the whole hazard: the gate ran, somebody pushed once
// more, and the line still says OK. It is refused for a batch branch too -- a receipt
// that names a different head is evidence about a tree nobody is landing.
func TestEnqueueRefusesAReceiptForAnotherHead(t *testing.T) {
	h := newFakeEnqueueHost()
	receipt := "BATCH OK name=nightly base=" + strings.Repeat("d", 40) + " head=" + strings.Repeat("e", 40) + " members=1301 dropped=none"
	for _, ref := range []string{"rowan/nightly", "rowan/integration-6"} {
		err := NewEnqueuer(h).Enqueue(context.Background(),
			EnqueuePR{Number: 99, HeadRef: ref, HeadSHA: strings.Repeat("c", 40), Receipt: receipt}, false)
		if err == nil {
			t.Fatalf("%s: a receipt for another head opened the door", ref)
		}
		if !strings.Contains(err.Error(), "head") {
			t.Errorf("%s: the refusal never says which field disagreed: %v", ref, err)
		}
	}
	if len(h.enqueued) != 0 {
		t.Fatalf("the forge was reached on a refused enqueue: %v", h.enqueued)
	}
}

// A batch whose every member was dropped lands nothing, and a receipt for it is a receipt
// for the base itself.
func TestEnqueueRefusesAReceiptThatLandsNothing(t *testing.T) {
	head := strings.Repeat("c", 40)
	h := newFakeEnqueueHost()
	receipt := "BATCH OK name=nightly base=" + strings.Repeat("d", 40) + " head=" + head + " members=none dropped=1301,1302"
	err := NewEnqueuer(h).Enqueue(context.Background(),
		EnqueuePR{Number: 99, HeadRef: "rowan/nightly", HeadSHA: head, Receipt: receipt}, false)
	if err == nil {
		t.Fatal("a batch with no members enqueued itself")
	}
	if !strings.Contains(err.Error(), "members") {
		t.Errorf("the refusal never names the empty members list: %v", err)
	}
}

// Anything that is not the gate's own green line is not a receipt: a FAIL line, a
// truncated line, a line with no head.
func TestEnqueueRefusesEveryLineThatIsNotABatchOK(t *testing.T) {
	head := strings.Repeat("c", 40)
	for _, receipt := range []string{
		"",
		"BATCH FAIL name=nightly base=" + strings.Repeat("d", 40) + " head=" + head + " members=1301 dropped=none step=test",
		"BATCH OK name=nightly base=" + strings.Repeat("d", 40) + " members=1301 dropped=none",
		"BATCH OK name=nightly base=x head=" + head[:7] + " members=1301 dropped=none",
		"batch ok head=" + head,
	} {
		h := newFakeEnqueueHost()
		err := NewEnqueuer(h).Enqueue(context.Background(),
			EnqueuePR{Number: 99, HeadRef: "rowan/nightly", HeadSHA: head, Receipt: receipt}, false)
		if err == nil {
			t.Errorf("%q was taken for a receipt", receipt)
		}
		if len(h.enqueued) != 0 {
			t.Errorf("%q reached the forge", receipt)
		}
	}
}

// The parse is a function of the line, so the gate's own format is pinned here rather than
// read out of a refusal message.
func TestParseBatchReceiptReadsTheGatesOwnLine(t *testing.T) {
	head, base := strings.Repeat("c", 40), strings.Repeat("d", 40)
	got, err := ParseBatchReceipt("BATCH OK name=integration-6 base=" + base + " head=" + head + " members=1301,1302 dropped=1307")
	if err != nil {
		t.Fatalf("ParseBatchReceipt: %v", err)
	}
	if got.Name != "integration-6" || got.Base != base || got.Head != head || got.Members != "1301,1302" || got.Dropped != "1307" {
		t.Fatalf("ParseBatchReceipt read %+v", got)
	}
}

// A number below one is not a pull request, and the door says so before it asks anything.
func TestEnqueueRefusesANumberThatIsNotAPullRequest(t *testing.T) {
	h := newFakeEnqueueHost()
	if err := NewEnqueuer(h).Enqueue(context.Background(),
		EnqueuePR{Number: 0, HeadRef: "rowan/integration-6"}, false); err == nil {
		t.Fatal("#0 was admitted")
	}
	if len(h.asked) != 0 {
		t.Fatalf("the forge was asked about #0: %v", h.asked)
	}
}

// The GraphQL this door speaks: enqueuePullRequest, with jump where the caller asked for
// it, and NEVER a `pr merge`. The runner records every argument.
func TestGHEnqueueSpeaksTheQueueMutationAndNeverAMerge(t *testing.T) {
	r := &recordRunner{}
	h := NewGHEnqueue("mas-bandwidth/nova-tools", 0, r)
	r.out = "PR_kwDO\n"
	if err := NewEnqueuer(h).Enqueue(context.Background(),
		EnqueuePR{Number: 1341, HeadRef: "rowan/integration-6", HeadSHA: strings.Repeat("a", 40)}, true); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("want a node-id read and one mutation, got %d calls: %v", len(r.calls), r.calls)
	}
	mutation := strings.Join(r.calls[1], " ")
	if !strings.Contains(mutation, "enqueuePullRequest") {
		t.Errorf("the mutation is not an enqueue: %s", mutation)
	}
	if !strings.Contains(mutation, "jump:true") {
		t.Errorf("jump never reached the mutation: %s", mutation)
	}
	for _, call := range r.calls {
		for i := 0; i+1 < len(call); i++ {
			if call[i] == "pr" && call[i+1] == "merge" {
				t.Errorf("this door built a `gh pr merge` call: %v", call)
			}
		}
	}
}

// Without jump the mutation carries no jump at all, rather than jump:false -- the input
// this door sends is the one the forge documents.
func TestGHEnqueueWithoutJumpSendsNoJump(t *testing.T) {
	r := &recordRunner{out: "PR_kwDO\n"}
	h := NewGHEnqueue("mas-bandwidth/nova-tools", 0, r)
	if err := NewEnqueuer(h).Enqueue(context.Background(),
		EnqueuePR{Number: 1341, HeadRef: "rowan/integration-9"}, false); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if mutation := strings.Join(r.calls[1], " "); strings.Contains(mutation, "jump") {
		t.Errorf("an enqueue nobody asked to jump carries a jump: %s", mutation)
	}
}

// recordRunner is a Runner that runs nothing and remembers every argument list.
type recordRunner struct {
	out   string
	err   error
	calls [][]string
}

func (r *recordRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.out, r.err
}

// THE SWEEP'S PRODUCTION HOST IS BEHIND THE ONE DOOR. `nova-merge sweep` used to enqueue
// every green, unqueued rowan/* pull request it found -- the shell loop's behaviour, and
// the reason a session's cards could reach the queue without a batch at all. Its host now
// offers them to the door, which refuses everything that is not a batch, and this asserts
// the wiring where it matters: on a refusal the production host starts NO SUBPROCESS.
func TestGHSweepEnqueueGoesThroughTheOneDoor(t *testing.T) {
	r := &recordRunner{out: "PR_kwDO\n"}
	h := NewGHSweep("mas-bandwidth/nova-tools", "dev", 0, r)

	if err := h.Enqueue(SweepPR{Number: 1207, HeadRef: "rowan/impl-something", MergeState: "CLEAN"}); err == nil {
		t.Fatal("the sweep's host enqueued a card's branch; the queue takes batches only")
	} else if _, ok := AsEnqueueRefusal(err); !ok {
		t.Errorf("the sweep's refusal is not the door's: %v", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("a refused sweep enqueue still ran gh: %v", r.calls)
	}

	if err := h.Enqueue(SweepPR{Number: 1341, HeadRef: "rowan/integration-6", MergeState: "CLEAN"}); err != nil {
		t.Fatalf("the sweep's host refused a batch: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("want a node-id read and one mutation, got %v", r.calls)
	}
	if !strings.Contains(strings.Join(r.calls[1], " "), "enqueuePullRequest") {
		t.Errorf("the sweep's admission is not the queue mutation: %v", r.calls[1])
	}
}
