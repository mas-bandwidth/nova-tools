package main

import (
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdWait blocks until one pull request is merged, is red, or the timeout runs
// out. It polls the SAME host reader the lane uses for PR state and checks --
// deps.NewHost, the gh reader behind internal/merge -- and prints exactly one
// line on stdout: MERGED (exit 0), RED (exit 2), or TIMEOUT (exit 3).
func cmdWait(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("wait")
	repo := f.fs.String("repo", "", "")
	pr := f.fs.Int("pr", 0, "")
	timeoutRaw := f.fs.String("timeout", "", "")
	intervalRaw := f.fs.String("interval", "30s", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("repo", *repo, "the repository to watch, as <owner>/<name>")
	if *repo != "" {
		if err := validRepoSlug(*repo); err != nil {
			f.problem(fmt.Sprintf("--repo is <owner>/<name>, got %q: %s", *repo, oneline.Escape(err.Error())))
		}
	}
	if !given(f.fs, "pr") {
		f.problem("--pr is required and is the pull request's number; refusing to guess")
	} else if *pr < 1 {
		f.problem(fmt.Sprintf("--pr is a pull request's number, which is positive, got %d", *pr))
	}
	timeout, err := time.ParseDuration(*timeoutRaw)
	if *timeoutRaw == "" {
		f.problem("--timeout is required; it is how long to wait before saying so, as a duration like 10m; a wait with no deadline is a lane that is stuck rather than working")
	} else if err != nil || timeout <= 0 {
		f.problem(fmt.Sprintf("--timeout is a duration like 10m, got %q", *timeoutRaw))
	}
	interval, err := time.ParseDuration(*intervalRaw)
	if err != nil || interval <= 0 {
		f.problem(fmt.Sprintf("--interval is how long to wait between polls, as a duration like 30s, got %q", *intervalRaw))
	}
	if !f.done(stderr) {
		return 2
	}
	// The gh calls this verb makes are bounded by the poll interval, capped at
	// the two minutes a hosted answer should take: a hung read is a tool that
	// says so rather than a tool that has stopped saying anything.
	hostTimeout := interval
	if hostTimeout > 120*time.Second {
		hostTimeout = 120 * time.Second
	}
	if hostTimeout < time.Second {
		hostTimeout = time.Second
	}
	return merge.Wait(deps.NewHost(*repo, hostTimeout), *pr, timeout, interval, deps.Now, deps.Sleep, stdout)
}
