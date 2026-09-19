package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cliflags"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// CLIP (slice 4 of SPEC-JOBS, section 4). A kept worktree is reused across cards; after
// each card clip commits the card's branch, harvests the card's result, and resets the
// worktree to base, so the clone is reset rather than re-cloned and the next card never
// sees this card's uncommitted state or its history. Every path comes from a flag.
func cmdClip(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("clip", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	worktree := fs.String("worktree", "", "")
	branch := fs.String("branch", "", "")
	base := fs.String("base", "", "")
	message := fs.String("message", "", "")
	result := fs.String("result", "", "")
	harvest := fs.String("harvest", "", "")
	if err := fs.Parse(args); err != nil {
		if cliflags.Help(stdout, err, cliflags.Usage(usage, "clip")) {
			return 0
		}
		return refuse(stderr, " clip", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " clip", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*worktree) == "" {
		return refuse(stderr, " clip", "--worktree is required; it wants the kept worktree to clip")
	}
	if strings.TrimSpace(*branch) == "" {
		return refuse(stderr, " clip", "--branch is required; it wants the card's branch to commit")
	}
	if strings.TrimSpace(*base) == "" {
		return refuse(stderr, " clip", "--base is required; it wants the base to reset the worktree to")
	}

	got, err := swarm.Clip(swarm.ClipRequest{
		Worktree: *worktree,
		Branch:   *branch,
		Base:     *base,
		Message:  *message,
		Result:   *result,
		Harvest:  *harvest,
	})
	if err != nil {
		return refuse(stderr, " clip", oneline.Err(err))
	}
	harvested := got.Harvested
	if harvested == "" {
		harvested = "-"
	}
	fmt.Fprintf(stdout, "CLIP OK branch=%s base=%s commit=%s harvested=%s\n",
		oneline.Field(*branch), oneline.Field(got.Base), oneline.Field(got.Commit), oneline.Field(harvested))
	return 0
}
