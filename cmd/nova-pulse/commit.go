package main

// `nova-pulse commit` is the harvest's mechanical pre-step, in Go (nova-tools #2549).
//
// It runs ON a bench, over its own job directories, and commits every DONE card's work on
// `rowan/<label>` so that `nova-pulse harvest --bench` -- which pushes git and nothing else
// -- has something to push. It replaces `bin/harvest-priority`'s `$commit_step`, 5.8 KB of
// bash in a single-quoted string piped into `ssh <bench> bash -s`.
//
// IT IS ITS OWN VERB AND NOT A FIRST STEP OF `harvest --bench`, on purpose: `harvest
// --bench` runs on the COORDINATOR and reaches a bench only through the BenchShell seam and
// a fetched ref -- it never has the job's working tree, which is precisely what this step
// has to stage, commit and rebase. Folding it in would mean shipping the working-tree work
// back over the seam, which is the arrangement that broke.

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdCommit(args []string, stdout, stderr io.Writer) int {
	f := newFlags("commit")
	root := f.fs.String("root", "", "")
	bench := f.fs.String("bench", "", "")
	base := f.fs.String("base", pulse.DefaultBase, "")
	draftOnly := f.fs.String("draft-only", "", "")
	max := f.fs.Int("max", 0, "")
	dryRun := f.fs.Bool("dry-run", false, "")
	var cards benchFlag
	f.fs.Var(&cards, "cards", "")
	var mirrors benchFlag
	f.fs.Var(&mirrors, "mirror", "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*root, "root", "the swarm root on this machine whose job directories this folds")
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Commit(pulse.CommitInput{
		Root:      *root,
		Bench:     *bench,
		Cards:     []string(cards),
		Mirrors:   parseMirrors([]string(mirrors)),
		DraftOnly: *draftOnly,
		Base:      *base,
		Max:       *max,
		DryRun:    *dryRun,
		Stdout:    stdout,
		Stderr:    stderr,
	})
}

// parseMirrors reads `--mirror [<owner>/<name>=]<dir>` the way `--clone` is read: a bare
// directory is the mirror for any repository, and a bound one wins for its own.
func parseMirrors(vals []string) map[string]string {
	out := map[string]string{}
	for _, v := range vals {
		name, dir, ok := strings.Cut(v, "=")
		if !ok {
			if _, taken := out[""]; !taken {
				out[""] = strings.TrimSpace(v)
			}
			continue
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(dir)
	}
	return out
}
