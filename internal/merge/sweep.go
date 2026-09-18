package merge

import "strings"

// Sweep is sweep-loop.sh as one function: ONE PASS over a repository's merge queue.
//
// It exists because the shell loop around `gh` was three behaviours welded to a sleep
// (bin/sweep-loop.sh): enqueue a green, unqueued rowan/* pull request, rerun a red or
// cancelled head run while the queue is shallow, and -- the card this came from --
// dequeue an entry the forge calls UNMERGEABLE. Everything it reads and writes goes
// through an injected SweepHost, so the pass is a function of what the forge said and
// the tests reach no network at all.
//
// The rules, each one the shell loop's own:
//
//   - only open pull requests whose head ref begins with prefix are swept;
//   - a DIRTY pull request is skipped, because the loop skipped it;
//   - a pull request already in the merge queue is skipped entirely: it is neither
//     enqueued again nor rerun;
//   - a completed success off the queue is OFFERED to the one door, which admits it only
//     if its head is a batch's (Glenn, 2026-09-18: nothing reaches the dev merge queue but
//     a batch); a refusal is counted as refused and the pass carries on;
//   - a completed failure or cancellation is rerun only while the queue is at most
//     RerunQueueDepth deep (the loop held `[ "$qn" -le 5 ]`);
//   - an UNMERGEABLE queue entry is dequeued, and is otherwise untouched.
//
// A mutation the host refuses is not counted and does not stop the pass: the loop's
// `&&` did the same, and one line of counts is what a caller reads.
func Sweep(host SweepHost, branch, prefix string) (SweepResult, error) {
	entries, err := host.Queue()
	if err != nil {
		return SweepResult{}, err
	}
	res := SweepResult{InQueue: len(entries)}
	queued := make(map[int]bool, len(entries))
	for _, e := range entries {
		queued[e.PR] = true
	}

	prs, err := host.OpenPRs()
	if err != nil {
		return SweepResult{}, err
	}
	for _, pr := range prs {
		if !strings.HasPrefix(pr.HeadRef, prefix) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(pr.MergeState), MergeStateDirty) {
			continue
		}
		if queued[pr.Number] {
			continue
		}
		run, err := host.HeadRun(pr.HeadRef)
		if err != nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(run.Status), RunCompleted) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(run.Conclusion)) {
		case RunSuccess:
			// THE WHOLE PULL REQUEST, not its number: the admission rule is about the
			// HEAD BRANCH -- a batch's own, or nothing -- and a pass that handed the host
			// a bare number would be a pass whose host had to go and read back the one
			// fact the decision turns on. Every refusal is counted and none stops the
			// pass, exactly as the shell loop's `&&` did.
			if err := host.Enqueue(pr); err == nil {
				res.Queued++
				queued[pr.Number] = true
			} else {
				res.Refused++
			}
		case RunFailure, RunCancelled:
			// THE THRESHOLD IS THE DEPTH READ AT THE TOP OF THE PASS, exactly as the
			// loop read it once per tick: a pass that re-read the depth after every
			// rerun would fire a different number of reruns than the loop did.
			if res.InQueue <= RerunQueueDepth {
				if err := host.Rerun(run.ID); err == nil {
					res.Reruns++
				}
			}
		}
	}

	// SEPARATELY, and after the enqueue pass: an entry the forge reports UNMERGEABLE
	// is removed. It is already in the queue, so the loop above skipped it; the two
	// never race over the same pull request.
	for _, e := range entries {
		if !strings.EqualFold(strings.TrimSpace(e.State), QueueUnmergeable) {
			continue
		}
		if err := host.Dequeue(e.PR); err == nil {
			res.Dequeued++
		}
	}
	return res, nil
}

// The forge's states, named once so the pass and its tests cannot drift.

const (
	MergeStateDirty  = "DIRTY"
	QueueUnmergeable = "UNMERGEABLE"

	RunCompleted = "completed"
	RunSuccess   = "success"
	RunFailure   = "failure"
	RunCancelled = "cancelled"

	// RerunQueueDepth is the queue depth at or below which a failed head run is
	// rerun. sweep-loop.sh held the same bound: [ "$qn" -le 5 ].
	RerunQueueDepth = 5

	// DefaultSweepPrefix is the head-ref prefix this sweep is about: the open pull
	// requests a session's cards push. The flag exists because a bench may sweep a
	// different prefix, not because this one is a law.
	DefaultSweepPrefix = "rowan/"
)

// QueueEntry is one entry of a repository's merge queue, as the host reports it.
type QueueEntry struct {
	PR    int
	State string
}

// SweepPR is one open pull request the sweep may act on.
type SweepPR struct {
	Number     int
	HeadRef    string
	MergeState string // mergeStateStatus: CLEAN, DIRTY, BLOCKED, UNKNOWN...
}

// SweepRun is the newest CI run on a branch.
type SweepRun struct {
	ID         int64
	Status     string // completed, in_progress, queued...
	Conclusion string // success, failure, cancelled, "" while unfinished
}

// SweepHost is the edge between the sweep and the forge. It is an interface for the
// same two reasons Host is: the tests need a forge that cannot reach the network, and
// the one implementation that shells to gh is then a thing a reader can check line by
// line rather than a thing woven through the pass.
type SweepHost interface {
	// Queue reads the merge queue's entries and their states.
	Queue() ([]QueueEntry, error)
	// OpenPRs reads the repository's open pull requests.
	OpenPRs() ([]SweepPR, error)
	// HeadRun reads the newest run of the branch's CI workflow.
	HeadRun(branch string) (SweepRun, error)
	// Enqueue admits a pull request to the merge queue, through the ONE DOOR
	// (Enqueuer.Enqueue): a batch's own head, or a head with that head's BATCH OK
	// receipt, and nothing else. It takes the whole pull request because the rule is
	// about the head branch.
	Enqueue(pr SweepPR) error
	// Rerun reruns one completed run.
	Rerun(run int64) error
	// Dequeue removes a pull request from the merge queue.
	Dequeue(pr int) error
}

// SweepResult is what one pass did, in the four counts the verb prints.
type SweepResult struct {
	Queued   int
	Reruns   int
	Dequeued int
	InQueue  int
	// Refused is the green pull requests the one door would not admit -- an ordinary
	// card's branch with no batch behind it. It is a COUNT AND NOT A FAILURE: a session
	// whose cards are all green and none batched refuses every one of them, which is the
	// lock working.
	Refused int
}
