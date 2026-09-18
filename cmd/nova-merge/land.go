package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE ONE CALLER OF THE ONE DOOR.
//
// Glenn, 2026-09-18: "nothing reaches the dev merge queue but a batch -- the batch verb is
// the only enqueuer; swarms produce branches, never queue entries." That morning four pull
// requests landed on dev that nobody enqueued: each carried GitHub's auto-merge, left on by
// a `gh pr merge` call made while the pull request was red, and the forge queued them
// itself hours later. The lock has three parts and this is the third:
//
//	internal/merge.Enqueuer.Enqueue  the ONE function that admits anything to a queue
//	internal/ci's class rule         no `gh pr merge`, no --auto, anywhere in the tools
//	nova-merge land                  the one caller: a batch, green, at the front
//
// `batch` builds the integration branch, tests it the way CI tests, prints BATCH OK and
// PUSHES NOTHING. The caller pushes that branch and opens the pull request -- the step that
// needs a person who knows this is the batch they wanted. `land` is then everything after:
// it reads the pull request back from the forge, refuses it unless its head is a batch's
// (or its BATCH OK receipt says this very commit is one), refuses it unless the pull
// request's OWN checks are green, and enqueues it at the front.
//
// The two green-ness questions are different questions and both are asked. The gate's green
// is a bench's: this tree builds, vets, tests and runs the lisp suite. CI's green is the
// forge's, on the commit the queue will take. integration-4 went green on hulk under a
// plain `go test ./...` and three CI legs then failed; a lander that trusted the receipt
// alone would have queued it.

// cmdLand enqueues one batch's pull request, at the front of its base's merge queue.
func cmdLand(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("land")
	repo := f.fs.String("repo", "", "")
	pr := f.fs.Int("pr", 0, "")
	receipt := f.fs.String("receipt", "", "")
	receiptFile := f.fs.String("receipt-file", "", "")
	noJump := f.fs.Bool("no-jump", false, "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("repo", *repo, "the repository whose merge queue this batch enters, as <owner>/<name>")
	if *repo != "" {
		if err := validRepoSlug(*repo); err != nil {
			f.problem(fmt.Sprintf("--repo is <owner>/<name>, got %q: %s", *repo, oneline.Escape(err.Error())))
		}
	}
	if *pr < 1 {
		f.problem(fmt.Sprintf("--pr is the number of the batch's own pull request, the one opened from the branch `nova-merge batch` built, got %d", *pr))
	}
	// TWO SPELLINGS OF ONE RECEIPT IS TWO RECEIPTS, and a run that took the first would be
	// a run whose evidence depends on which flag the caller believed.
	if strings.TrimSpace(*receipt) != "" && strings.TrimSpace(*receiptFile) != "" {
		f.problem("--receipt and --receipt-file are two spellings of one receipt; give one")
	}
	if *timeout < 1 || *timeout > maxTimeout {
		f.problem(fmt.Sprintf("--timeout is a number of seconds this tool waits for gh before saying so, from 1 to %d, got %d", maxTimeout, *timeout))
	}
	if !f.done(stderr) {
		return 2
	}
	line := strings.TrimSpace(*receipt)
	if path := strings.TrimSpace(*receiptFile); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return refuse(stderr, " land", fmt.Sprintf("--receipt-file %s could not be read: %s", oneline.Field(path), oneline.Err(err)))
		}
		line = lastBatchLine(string(raw))
	}
	return runLandVerb(landRun{
		repo:    *repo,
		pr:      *pr,
		receipt: line,
		jump:    !*noJump,
		timeout: time.Duration(*timeout) * time.Second,
	}, stdout, stderr, deps)
}

// landRun is one landing's whole invocation, checked.
type landRun struct {
	repo    string
	pr      int
	receipt string
	jump    bool
	timeout time.Duration
}

func runLandVerb(in landRun, stdout, stderr io.Writer, deps Deps) int {
	// THE RULE FIRST, AND THE PART OF IT THE INVOCATION SETTLES BEFORE ANY FORGE READ
	// (dogfood round 5, edge 3). A receipt that is a BATCH FAIL line, or one whose batch
	// dropped every member, is refused with the host untouched: the forge cannot change
	// what the caller typed, and a refusal that costs two round trips is two round trips
	// spent on an answer that was already in hand.
	if err := merge.RuleBeforeTheForge(in.pr, in.receipt); err != nil {
		return landRefused(stderr, oneline.Cap(err.Error(), oneline.TailBytes))
	}
	host := deps.NewHost(in.repo, in.timeout)
	data, err := host.PR(in.pr)
	if err != nil {
		return landCouldNotRun(stderr, fmt.Sprintf("pull request %d could not be read: %s", in.pr, oneline.Err(err)))
	}
	if data.Merged || data.Closed {
		return landRefused(stderr, fmt.Sprintf("pull request %d is not open; a queue entry is an open pull request", in.pr))
	}
	if strings.EqualFold(strings.TrimSpace(data.Mergeable), "CONFLICTING") {
		return landRefused(stderr, fmt.Sprintf("pull request %d conflicts with its base; build the batch again on the base as it stands: nova-merge batch --name <name> --pr <list>", in.pr))
	}
	// AND THE REST OF THE RULE AS SOON AS THE HEAD IS NAMED, which is the one read it
	// needs. The rollup below is a second, slower read that answers a different question,
	// and "this head is not a batch's" never needed to wait for it. The door asks this
	// same function again on the way through: the rule lives in one place.
	if err := merge.Admissible(merge.EnqueuePR{
		Number: in.pr, HeadRef: data.HeadRef, HeadSHA: data.HeadOID, Receipt: in.receipt,
	}); err != nil {
		return landRefused(stderr, oneline.Cap(err.Error(), oneline.TailBytes))
	}
	// THE PULL REQUEST'S OWN CHECKS, read on its head. A batch that went green on a bench
	// and red on the forge is a batch that does not land -- and a pull request with no
	// checks at all is not evidence of anything, which is the reading rule every other
	// verb of this tool holds to.
	checks, err := host.Checks(data.HeadOID)
	if err != nil {
		return landCouldNotRun(stderr, fmt.Sprintf("pull request %d's checks could not be read: %s", in.pr, oneline.Err(err)))
	}
	head := checks.ForSHA(data.HeadOID)
	if head.Total() == 0 {
		head = checks
	}
	if head.Red > 0 || head.Pending > 0 || head.Total() == 0 {
		return landRefused(stderr, fmt.Sprintf("pull request %d is not green (%s); the queue takes a batch CI has judged, and this one it has not",
			in.pr, oneline.Field(head.Field())))
	}
	if err := merge.NewEnqueuer(deps.NewEnqueueHost(in.repo, in.timeout)).Enqueue(
		context.Background(),
		merge.EnqueuePR{Number: in.pr, HeadRef: data.HeadRef, HeadSHA: data.HeadOID, Receipt: in.receipt},
		in.jump); err != nil {
		if _, ok := merge.AsEnqueueRefusal(err); ok {
			return landRefused(stderr, oneline.Cap(err.Error(), oneline.TailBytes))
		}
		return landCouldNotRun(stderr, oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprintf(stdout, "LAND OK pr=%d head=%s branch=%s checks=%s members=%s jump=%t\n",
		in.pr, oneline.Field(data.HeadOID), oneline.Field(data.HeadRef), oneline.Field(head.Field()),
		oneline.Field(receiptMembers(in.receipt)), in.jump)
	return 0
}

// landRefused is the verb running and saying NO: exit 1. The invocation was readable and
// the answer is that this pull request does not enter the queue.
func landRefused(stderr io.Writer, why string) int {
	fmt.Fprintf(stderr, "LAND REFUSED: %s\n", oneline.Escape(why))
	return 1
}

// landCouldNotRun is exit 2: gh could not be reached, a flag was unusable, a read failed.
// It is a different exit code from a refusal because a caller retries one and not the
// other -- and, since dogfood round 5, a DIFFERENT WORD. Both exits printed `LAND
// REFUSED`, so "this does not enter the queue" and "the forge could not be read" were one
// line to anything reading the stream, and the exit code was the only thing that told them
// apart. A reader who has the line has the answer.
func landCouldNotRun(stderr io.Writer, why string) int {
	fmt.Fprintf(stderr, "LAND ERROR: %s\n", oneline.Escape(why))
	return 2
}

// lastBatchLine is the receipt inside a gate's output: a caller pipes the whole run into a
// file, and the verdict is its last non-empty line. Reading the LAST line rather than
// grepping for one keeps a BATCH FAIL a refusal here rather than a line nobody matched.
func lastBatchLine(raw string) string {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// receiptMembers is what the landing line says landed: the members of the receipt, or "-"
// when the entry came in on a batch branch with no receipt beside it.
func receiptMembers(receipt string) string {
	if strings.TrimSpace(receipt) == "" {
		return "-"
	}
	rec, err := merge.ParseBatchReceipt(receipt)
	if err != nil {
		return "-"
	}
	return rec.Members
}
