package main

import (
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdSweep is sweep-loop.sh as a verb: ONE PASS over a repository's merge queue. It
// enqueues the open rowan/* pull requests whose checks completed green, reruns a
// failed or cancelled head run while the queue holds five entries or fewer, and
// dequeues the entries the forge reports UNMERGEABLE. The loop lived in
// bin/sweep-loop.sh with `/tmp/enqueue-skip` and `/tmp/enqueue-hold` beside it; the
// pass itself is internal/merge.Sweep, and this verb is its flags and its one line.
//
// It names its repository and its merge-queue branch outright, like `wait` names its
// repository, because neither is a lane's: a sweep is a property of a forge, not of a
// lane on one.
func cmdSweep(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("sweep")
	repo := f.fs.String("repo", "", "")
	branch := f.fs.String("branch", "", "")
	prefix := f.fs.String("prefix", merge.DefaultSweepPrefix, "")
	once := f.fs.Bool("once", false, "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("repo", *repo, "the repository whose merge queue this sweep reads, as <owner>/<name>")
	if *repo != "" {
		if err := validRepoSlug(*repo); err != nil {
			f.problem(fmt.Sprintf("--repo is <owner>/<name>, got %q: %s", *repo, oneline.Escape(err.Error())))
		}
	}
	f.require("branch", *branch, "the branch whose merge queue this sweep reads, as it is named at the host")
	if *branch != "" {
		if err := merge.ValidRefName(*branch); err != nil {
			f.problem(fmt.Sprintf("--branch: %s", oneline.Escape(err.Error())))
		}
	}
	// An empty prefix would sweep every open pull request of the repository, which is
	// a different and much larger verb than the one this card asked for.
	if *prefix == "" {
		f.problem("--prefix is the head-ref prefix this sweep lands, like rowan/; an empty one would sweep every open pull request")
	}
	// ONE PASS AND NOT A LOOP. The loop this replaces slept between ticks and honoured
	// a hold file; neither is here yet, and a sweep with no --once would be a tool that
	// hangs against a rate limit while every line of it says it is working.
	if !*once {
		f.problem("--once is required: this is one pass of the sweep loop, and a loop with no interval and no deadline is a tool that has stopped saying anything")
	}
	if *timeout < 1 || *timeout > maxTimeout {
		f.problem(fmt.Sprintf("--timeout is a number of seconds this tool waits for gh before saying so, from 1 to %d, got %d", maxTimeout, *timeout))
	}
	if !f.done(stderr) {
		return 2
	}
	host := deps.NewSweepHost(*repo, *branch, time.Duration(*timeout)*time.Second)
	res, err := merge.Sweep(host, *branch, *prefix)
	if err != nil {
		fmt.Fprintf(stderr, "SWEEP REFUSED: %s; run: nova-merge sweep --repo %s --branch %s --once\n",
			oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)), oneline.Field(*repo), oneline.Field(*branch))
		return 2
	}
	// refused=<n> is the lock, counted: the green pull requests the one door would not
	// admit because their heads are not a batch's (Glenn, 2026-09-18). A session whose
	// cards are all green and none batched sweeps to queued=0 refused=<many>, which is the
	// rule working rather than a sweep that failed.
	fmt.Fprintf(stdout, "SWEEP queued=%d reruns=%d dequeued=%d inqueue=%d refused=%d\n",
		res.Queued, res.Reruns, res.Dequeued, res.InQueue, res.Refused)
	return 0
}
