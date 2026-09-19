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
	lane := f.fs.String("lane", "", "")
	pr := f.fs.Int("pr", 0, "")
	receipt := f.fs.String("receipt", "", "")
	receiptFile := f.fs.String("receipt-file", "", "")
	noJump := f.fs.Bool("no-jump", false, "")
	timeout := f.fs.Int("timeout", 120, "")
	reviewersFile := f.fs.String("reviewers", "", "")
	noRequireHolds := f.fs.Bool("no-require-holds", false, "")
	reason := f.fs.String("reason", "", "")
	untypedComments := f.fs.String("untyped-comments", "", "")
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
	// --reviewers XOR --no-require-holds: exactly one required (SPEC-DECIDE reading 3, demanded test 28)
	if (*reviewersFile == "") == (*noRequireHolds == false) {
		f.problem("exactly one of --reviewers <file> or --no-require-holds --reason <text> is required; exit 2 with neither or both")
	}
	if *reviewersFile != "" && (strings.TrimSpace(*lane) == "" || strings.TrimSpace(*lane) == "none") {
		f.problem("--lane is required when --reviewers is specified")
	}
	if *noRequireHolds && strings.TrimSpace(*reason) == "" {
		f.problem("--no-require-holds requires --reason <text>")
	}
	if *untypedComments == "ignore" && strings.TrimSpace(*reason) == "" {
		f.problem("--untyped-comments=ignore requires --reason <text>")
	}
	if *untypedComments != "" && *untypedComments != "ignore" {
		f.problem(fmt.Sprintf("--untyped-comments must be ignore, got %q", *untypedComments))
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
		repo:            *repo,
		lane:            strings.TrimSpace(*lane),
		pr:              *pr,
		receipt:         line,
		jump:            !*noJump,
		timeout:         time.Duration(*timeout) * time.Second,
		reviewersFile:   strings.TrimSpace(*reviewersFile),
		noRequireHolds:  *noRequireHolds,
		reason:          strings.TrimSpace(*reason),
		untypedComments: strings.TrimSpace(*untypedComments),
	}, stdout, stderr, deps)
}

// landRun is one landing's whole invocation, checked.
type landRun struct {
	repo            string
	lane            string
	pr              int
	receipt         string
	jump            bool
	timeout         time.Duration
	reviewersFile   string
	noRequireHolds  bool
	reason          string
	untypedComments string
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

	// SPEC-DECIDE reading 3 (lines 1134-1136): land folds every member of the receipt again,
	// from the wire, immediately before Enqueuer.Enqueue, and one held member refuses the whole landing.
	var rs *merge.ReviewerSet
	if !in.noRequireHolds {
		sha, err := getReviewersSHA(in.reviewersFile)
		if err != nil {
			return landCouldNotRun(stderr, fmt.Sprintf("reviewer file %s could not be read: %s", oneline.Field(in.reviewersFile), oneline.Err(err)))
		}
		_ = sha
		var err2 error
		rs, err2 = merge.LoadReviewers(in.reviewersFile)
		if err2 != nil {
			return landCouldNotRun(stderr, fmt.Sprintf("reviewer file %s could not be read: %s", oneline.Field(in.reviewersFile), oneline.Err(err2)))
		}
	}

	checkPRs := []int{in.pr}
	if recMembers := receiptMembers(in.receipt); recMembers != "" && recMembers != "-" && recMembers != "none" {
		for _, part := range strings.Split(recMembers, ",") {
			part = strings.TrimSpace(part)
			if num, err := strconv.Atoi(part); err == nil && num > 0 {
				checkPRs = append(checkPRs, num)
			}
		}
	}

	for _, m := range checkPRs {
		var mPR merge.PR
		if m == in.pr {
			mPR = data
		} else {
			var err error
			mPR, err = host.PR(m)
			if err != nil {
				return landCouldNotRun(stderr, fmt.Sprintf("member pull request %d could not be read: %s", m, oneline.Err(err)))
			}
		}

		var vs []merge.Verdict
		if in.lane != "" {
			laneVs, err := merge.LoadLaneVerdicts(in.lane, m)
			if err != nil {
				return landCouldNotRun(stderr, fmt.Sprintf("lane read records for member pull request %d could not be read: %s", m, oneline.Err(err)))
			}
			vs = append(vs, laneVs...)
		}

		opts := merge.VerdictOpts{
			Author:          mPR.Author,
			CurrentHead:     mPR.HeadOID,
			Reviewers:       rs,
			UntypedComments: in.untypedComments,
		}
		forgeVs, err := host.Verdicts(m, opts)
		if err != nil {
			if !in.noRequireHolds {
				return landCouldNotRun(stderr, fmt.Sprintf("member pull request %d's verdicts could not be read: %s", m, oneline.Err(err)))
			}
		} else {
			if in.noRequireHolds {
				for _, v := range forgeVs {
					if v.Source == "record" {
						vs = append(vs, v)
					}
				}
			} else {
				vs = append(vs, forgeVs...)
			}
		}

		holds := merge.UnliftedHolds(vs, mPR.HeadOID, mPR.Author, rs)
		if len(holds) > 0 {
			h := holds[0]
			if h.Source == "comment-pending" {
				line := fmt.Sprintf("LAND REFUSED reason=held member=#%d who=unknown hold=%s source=comment-pending at=%s",
					m, oneline.Field(h.ID), oneline.Field(h.At))
				if in.noRequireHolds {
					line += fmt.Sprintf(" holds=waived reason=%q", in.reason)
				}
				if in.untypedComments == "ignore" {
					line += fmt.Sprintf(" untyped=ignored reason=%q", in.reason)
				}
				fmt.Fprintf(stderr, "%s\n", oneline.Escape(line))
			} else {
				carried := "no"
				if h.Carried {
					carried = "yes"
				}
				conf := h.Conf
				if conf == "" {
					conf = "-"
				}
				line := fmt.Sprintf("LAND REFUSED reason=held member=#%d who=%s hold=%s source=%s held_at=%s carried=%s at=%s conf=%s",
					m, oneline.Field(h.Who), oneline.Field(h.ID), oneline.Field(h.Source), oneline.Field(merge.Short(h.Head)), oneline.Field(carried), oneline.Field(h.At), oneline.Field(conf))
				if in.noRequireHolds {
					line += fmt.Sprintf(" holds=waived reason=%q", in.reason)
				}
				if in.untypedComments == "ignore" {
					line += fmt.Sprintf(" untyped=ignored reason=%q", in.reason)
				}
				fmt.Fprintf(stderr, "%s\n", oneline.Escape(line))
			}
			return 1
		}
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
	okLine := fmt.Sprintf("LAND OK pr=%d head=%s branch=%s checks=%s members=%s jump=%t",
		in.pr, oneline.Field(data.HeadOID), oneline.Field(data.HeadRef), oneline.Field(head.Field()),
		oneline.Field(receiptMembers(in.receipt)), in.jump)
	if in.noRequireHolds {
		okLine += fmt.Sprintf(" holds=waived reason=%q", in.reason)
	}
	if in.untypedComments == "ignore" {
		okLine += fmt.Sprintf(" untyped=ignored reason=%q", in.reason)
	}
	fmt.Fprintf(stdout, "%s\n", oneline.Escape(okLine))
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
