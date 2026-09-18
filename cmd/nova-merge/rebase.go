package main

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// cmdRebase is the rebase loop's tick as a verb: one pass over the repository's open pull
// requests, a card cut and launched for each DIRTY rowan/* pull request that has none yet.
// The card's text is the cut machinery's, not this verb's: `pulse.CutKind` owns the
// `rebase` kind and this only hands it the pull request's own fields.
func cmdRebase(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("rebase")
	repo := f.fs.String("repo", "", "")
	markers := f.fs.String("markers", "", "")
	out := f.fs.String("out", "", "")
	queue := f.fs.String("queue", "", "")
	base := f.fs.String("base", "dev", "")
	once := f.fs.Bool("once", false, "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	// Every path comes from a flag: the repository to read, the directory that remembers
	// which pull requests already have a card, the directory the cards go into, and the
	// queue whose state file is the one numberer.
	f.require("repo", *repo, "the repository whose open pull requests this pass reads, as <owner>/<name>")
	f.require("markers", *markers, "the directory holding one empty file per pull request already cut, which is this pass's whole memory")
	f.require("out", *out, "the directory the cut writes each rebase card into")
	f.require("queue", *queue, "the queue directory whose state file numbers the cards, so two cutters never share one")
	if !*once {
		f.problem("--once is required; this pass cuts and launches the DIRTY pull requests it finds now and never loops on its own")
	}
	if *timeout < 1 || *timeout > maxTimeout {
		f.problem(fmt.Sprintf("--timeout is a number of seconds this verb waits for gh or the launcher, from 1 to %d, got %d", maxTimeout, *timeout))
	}
	if *repo != "" {
		if err := validRepoSlug(*repo); err != nil {
			f.problem(fmt.Sprintf("--repo is <owner>/<name>, got %q: %s", *repo, oneline.Escape(err.Error())))
		}
	}
	if *base != "" {
		if err := merge.ValidRefName(*base); err != nil {
			f.problem(fmt.Sprintf("--base: %s", oneline.Escape(err.Error())))
		}
	}
	if !f.done(stderr) {
		return 2
	}
	if deps.NewRebaseList == nil {
		fmt.Fprintf(stderr, "REBASE REFUSED: this build wires no host reader, so the open pull request list cannot be read; wire NewRebaseList and run the same verb again\n")
		return 2
	}
	list := deps.NewRebaseList(*repo, time.Duration(*timeout)*time.Second)
	cut := func(c merge.RebaseCard) (string, error) {
		var line bytes.Buffer
		code := pulse.CutKind(pulse.CutKindInput{
			Kind: "rebase", Repo: *repo, PR: c.PR, Branch: c.HeadRef, Title: c.Title,
			Base: *base, Out: *out, Queue: *queue, Stdout: &line, Stderr: stderr,
		})
		if code != 0 {
			return "", fmt.Errorf("the cut verb refused a rebase card for PR #%d", c.PR)
		}
		name := cutCardName(line.String())
		if name == "" {
			return "", fmt.Errorf("the cut verb wrote a card for PR #%d but named none", c.PR)
		}
		return filepath.Join(*out, name), nil
	}
	return merge.Rebase(merge.RebaseInput{
		Markers: *markers, Out: *out, List: list, Cut: cut, Launch: deps.Launcher,
		Stdout: stdout, Stderr: stderr,
	})
}

// cutCardName reads the card=<name> field off the cut verb's one line, so this verb learns
// the path the cut chose rather than asking, or guessing, the number a second time.
func cutCardName(line string) string {
	for _, tok := range strings.Fields(line) {
		if name, ok := strings.CutPrefix(tok, "card="); ok {
			return name
		}
	}
	return ""
}
