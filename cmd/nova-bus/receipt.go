// receipt: a note marked heard, in the reader's own lane, without a reply.

package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdReceipt(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("receipt")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	remote := f.fs.String("remote", "", "the git remote to push to (required)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "commit but do not push; the receipt is NOT on the bus until it is pushed")
	dryRun := f.fs.Bool("dry-run", false, "resolve the notes and print what would be recorded, and write nothing")
	var notes stringList
	f.fs.Var(&notes, "note", "a note to mark heard, by id or by path (required; repeatable)")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as, "remote": remote, "branch": branch}) {
		return 2
	}
	if !f.attempts(*attempts, stderr) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	if !f.gitArgs(*remote, *branch, stderr) {
		return 2
	}
	if len(notes) == 0 {
		fmt.Fprint(stderr, "RECEIPT REFUSED: --note is required; refusing to guess; run: nova-bus receipt -h\n")
		return 2
	}
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus receipt -h"))
		return 2
	}
	if !*dryRun {
		release, lockErr := bus.LockCheckout(*busDir, checkoutLockWait)
		if lockErr != nil {
			fmt.Fprintf(stderr, "RECEIPT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(lockErr), "nova-bus receipt -h"))
			return 1
		}
		defer release()
	}
	t, ok := openBus("receipt", *busDir, stderr)
	if !ok {
		return 2
	}
	me, found := t.Config.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: --as %q names no one on this bus (known: %s); run: nova-bus receipt -h\n", *as, oneline.Escape(strings.Join(t.Config.KnownNames(), "; ")))
		return 2
	}
	plan, err := bus.PlanReceipts(t, me, notes, now)
	if err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(me.Name), oneline.Err(err))
		return 1
	}
	for _, already := range plan.Already {
		fmt.Fprintf(stdout, "RECEIPT ALREADY note=%s lane=%s\n", oneline.Field(already), oneline.Field(plan.Lane))
	}
	if len(plan.Record) == 0 {
		fmt.Fprintf(stdout, "RECEIPT OK recorded=0 already=%d commit=- pushed=false attempts=0%s\n", len(plan.Already), oneline.Escape(dryField(*dryRun)))
		return 0
	}
	// The reader's own BEAT, as in send: `wait` wrote it, so it is this run's own
	// machinery and not a change that is "not this receipt".
	beat := bus.BeatPath(me.Lane)
	if err := checkoutReady(*busDir, *branch, []string{plan.Path, beat}); err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		return 1
	}
	// Everything above is read-only and the dry run's; everything below writes.
	if *dryRun {
		for _, id := range plan.Record {
			fmt.Fprintf(stdout, "RECEIPT RECORD note=%s lane=%s\n", oneline.Field(id), oneline.Field(plan.Lane))
		}
		fmt.Fprintf(stdout, "RECEIPT OK recorded=%d already=%d commit=- pushed=false attempts=0 dry_run=true\n", len(plan.Record), len(plan.Already))
		return 0
	}
	if err := levelWithRemote(*busDir, *remote, *branch, *noPush); err != nil {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus receipt -h"))
		return 1
	}
	if err := plan.Append(*busDir); err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		return 1
	}
	paths, err := bus.StagePaths(*busDir, []string{plan.Path, beat})
	if err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		return 1
	}
	res, err := commit(*busDir, me, paths,
		bus.WithTrailer(plan.Message(me), bus.TrailerReceipt),
		*remote, *branch, *attempts, *noPush)
	if err != nil {
		fmt.Fprintf(stderr, "RECEIPT FAIL %s: %s\n", oneline.Escape(plan.Path), oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "RECEIPT OK recorded=%d already=%d commit=%s pushed=%t attempts=%d\n",
		len(plan.Record), len(plan.Already), oneline.Field(res.Commit), res.Pushed, res.Attempts)
	return 0
}
