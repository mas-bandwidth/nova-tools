package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
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
	// #1572's second half. The hold that put a held head on dev arrived AFTER the gate
	// said BATCH OK and BEFORE this verb ran: a check only in `batch` would still have
	// admitted it. So the same read happens here, on this verb's own fresh look, over
	// the batch pull request AND every member its receipt names.
	readers := f.fs.String("readers", "", "")
	noRequireHolds := f.fs.Bool("no-require-holds", false, "")
	ignoreHold := newIntList()
	f.fs.Var(ignoreHold, "ignore-hold", "")
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
	if !*noRequireHolds && len(splitLogins(*readers)) == 0 {
		f.problem("--readers <login,...> names the readers whose HOLD stops this landing, like gafferongames; it is required because the one landing that did not read them put a held head on dev on 2026-09-19 (#1572). Pass --no-require-holds to land without the read and own that.")
	}
	if *noRequireHolds && len(splitLogins(*readers)) > 0 {
		f.problem("--readers and --no-require-holds say opposite things about the same read; give one")
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
		repo:         *repo,
		pr:           *pr,
		receipt:      line,
		jump:         !*noJump,
		timeout:      time.Duration(*timeout) * time.Second,
		readers:      splitLogins(*readers),
		requireHolds: !*noRequireHolds,
		ignoreHold:   ignoreHold.values,
	}, stdout, stderr, deps)
}

// landRun is one landing's whole invocation, checked.
type landRun struct {
	repo         string
	pr           int
	receipt      string
	jump         bool
	timeout      time.Duration
	readers      []string
	requireHolds bool
	ignoreHold   []int
}

func runLandVerb(in landRun, stdout, stderr io.Writer, deps Deps) int {
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
	// THE HOLD READ, ON THIS VERB'S OWN FRESH LOOK (#1572). The batch pull request
	// itself and every member its receipt names, because the hold that reached dev on
	// 2026-09-19 was on a MEMBER and arrived in the nine minutes between BATCH OK and
	// here. A landing is refused, not dropped: this verb lands one thing and there is
	// nothing to leave behind.
	if code := landHoldRead(in, host, stderr); code != 0 {
		return code
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
// It is a different exit code from a refusal because a caller retries one and not the other.
func landCouldNotRun(stderr io.Writer, why string) int {
	fmt.Fprintf(stderr, "LAND REFUSED: %s\n", oneline.Escape(why))
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

// landHoldRead refuses the landing when the batch or any member its receipt names carries
// an unlifted HOLD from one of the named readers. Exit 1: the verb ran, read the forge and
// says no.
func landHoldRead(in landRun, host merge.Host, stderr io.Writer) int {
	if !in.requireHolds {
		fmt.Fprintf(stderr, "LAND NOTE holds=unread reason=%s\n", oneline.Field(
			"--no-require-holds was given: this batch lands whatever a reader has said about it or its members"))
		return 0
	}
	ignored := map[int]bool{}
	for _, n := range in.ignoreHold {
		ignored[n] = true
	}
	for _, n := range landSubjects(in) {
		pr, err := host.PR(n)
		if err != nil {
			return landCouldNotRun(stderr, fmt.Sprintf("pull request %d could not be read, and the hold read is on: %s", n, oneline.Err(err)))
		}
		vs, err := host.Verdicts(n)
		if err != nil {
			return landCouldNotRun(stderr, fmt.Sprintf("pull request %d's comments and reviews could not be read, and the hold read is on: %s", n, oneline.Err(err)))
		}
		holds := merge.UnliftedHolds(vs, pr.HeadOID, in.readers)
		if len(holds) == 0 {
			continue
		}
		if ignored[n] {
			fmt.Fprintf(stderr, "LAND NOTE #%d holds=ignored reason=%s\n", n,
				oneline.Field(fmt.Sprintf("--ignore-hold %d was given: %s", n, oneline.Escape(holds[0].Line()))))
			continue
		}
		return landRefused(stderr, fmt.Sprintf(
			"pull request %d carries an unlifted %s; a hold is lifted by an APPROVE from that same reader naming this very head, and --ignore-hold %d steps over it on the record",
			n, oneline.Escape(holds[0].Line()), n))
	}
	return 0
}

// landSubjects is the batch pull request and every member its receipt names, in that
// order. A landing with no receipt reads the batch alone -- there is nothing else to name.
func landSubjects(in landRun) []int {
	out := []int{in.pr}
	seen := map[int]bool{in.pr: true}
	rec, err := merge.ParseBatchReceipt(strings.TrimSpace(in.receipt))
	if err != nil {
		return out
	}
	for _, raw := range strings.Split(rec.Members, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < 1 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}
