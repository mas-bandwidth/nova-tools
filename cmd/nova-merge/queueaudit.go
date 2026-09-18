package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// `queue audit`: THE SWEEP THAT REMOVED 27, AS A VERB.
//
// Auto-merge is the other entrance to the merge queue, and it is the one nobody is standing
// at. `gh pr merge <n> --auto` does not enqueue anything: it leaves an instruction on the
// forge, and the forge acts on it later -- when the last check goes green, against whatever
// the head has become, with no caller and no line in any log of ours. On 2026-09-18 four
// pull requests reached dev that way and twenty-seven more were armed and waiting. They
// were taken off by hand, which is exactly the shape Glenn's rule names: a step done by
// hand twice becomes a verb.
//
// So this verb reads every open pull request carrying an auto-merge and takes it off, one
// line each, and one line of counts. --dry-run reads and writes nothing, for the caller who
// wants to see the list first.
//
// It is a subverb of `queue` because it is about the queue's own admissions, and it names
// its repository outright rather than reading a lane's: a forge's standing instructions are
// a property of the forge, like `sweep`'s queue is.
func cmdQueueAudit(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("queue audit")
	repo := f.fs.String("repo", "", "")
	dry := f.fs.Bool("dry-run", false, "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("repo", *repo, "the repository whose open pull requests are read for auto-merges, as <owner>/<name>")
	if *repo != "" {
		if err := validRepoSlug(*repo); err != nil {
			f.problem(fmt.Sprintf("--repo is <owner>/<name>, got %q: %s", *repo, oneline.Escape(err.Error())))
		}
	}
	if *timeout < 1 || *timeout > maxTimeout {
		f.problem(fmt.Sprintf("--timeout is a number of seconds this tool waits for gh before saying so, from 1 to %d, got %d", maxTimeout, *timeout))
	}
	if !f.done(stderr) {
		return 2
	}
	res, err := merge.Audit(context.Background(), deps.NewAuditHost(*repo, time.Duration(*timeout)*time.Second), *dry)
	if err != nil {
		return queueRefuse(stderr, fmt.Sprintf("the repository's auto-merges could not be read: %s", oneline.Err(err)))
	}
	// EVERY ONE IS NAMED. A count with no names is a number nobody can check, and the
	// question a reader has after this verb is which pull requests were carrying one.
	for _, pr := range res.PRs {
		fmt.Fprintf(stdout, "QUEUE AUDIT entry=%d branch=%s title=%s\n",
			pr.Number, oneline.Field(pr.HeadRef), oneline.Field(oneline.Cap(pr.Title, 80)))
	}
	fmt.Fprintf(stdout, "QUEUE AUDIT repo=%s found=%d disabled=%d failed=%d dry_run=%t\n",
		oneline.Field(*repo), res.Found, res.Disabled, res.Failed, *dry)
	// A refusal the forge made is exit 1 -- the verb ran and some of the work did not get
	// done -- and the pull requests it could not clear are named, because those are the ones
	// still armed.
	if res.Failed > 0 {
		fmt.Fprintf(stderr, "QUEUE AUDIT REFUSED: the forge would not clear %s; they still carry an auto-merge\n",
			oneline.Field(numberList(res.Refused)))
		return 1
	}
	return 0
}

// auditArgsFor is the audit's own flag line, taken off `queue`'s scanner the way `classify`
// is: the queue's scanner refuses a flag it does not know, and --repo, --dry-run and
// --timeout are this subverb's, not the queue's.
func auditArgsFor(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.TrimSpace(a) == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}
