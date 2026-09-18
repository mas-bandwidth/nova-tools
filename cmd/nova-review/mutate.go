package main

// `nova-review mutate` is the mechanical half of a read, taken off the reader.
//
// Class H of pit stop 3 (nova-tools#828): 504 read cards in one day, each cloning a repo
// to check "is the new test red without the change". That is a mutation test's job, and a
// model is a slow and unreliable way to run it. The verb reverts every non-test hunk in a
// throwaway worktree at the head and runs the tests the change touched: they must fail.
// The harvest runs it before any reader is spawned, and the reader then judges spec fit
// and nothing else, from the packet.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/review"
)

func mutate(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("mutate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "")
	base := fs.String("base", "", "")
	head := fs.String("head", "", "")
	timeout := fs.Int("timeout", 120, "")
	maxFlag := fs.Int("max", bounded.Default, "")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return refuseMutate(errOut, "bad mutate flags")
	}
	if *repo == "" || *base == "" || *head == "" {
		return refuseMutate(errOut, "--repo, --base and --head are required")
	}
	if *timeout <= 0 {
		return refuseMutate(errOut, "--timeout must be positive")
	}
	if *maxFlag < 0 {
		return refuseMutate(errOut, "--max must be non-negative")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()

	res, err := review.Mutate(ctx, review.MutateOptions{Repo: *repo, Base: *base, Head: *head})
	switch {
	case errors.Is(err, review.ErrNoTestsChanged):
		// The read rule, mechanised: a fix without its red test is not admitted, so
		// there is nothing here for a reader to be spawned for.
		fmt.Fprintf(errOut, "MUTATE %s no-tests-changed\n", review.Short(res.Head))
		return 2
	case errors.Is(err, review.ErrNoChangeToRevert):
		fmt.Fprintf(errOut, "MUTATE %s no-change-to-revert: every changed file is a test file; a run with nothing reverted proves nothing either way\n", review.Short(res.Head))
		return 2
	case err != nil:
		return refuseMutate(errOut, err.Error())
	}

	skips := bounded.Capped(out, *maxFlag, "MUTATE", "skip", mutateRemedy(*repo, *base, *head))
	for _, s := range res.Skips {
		skips.Line(fmt.Sprintf("MUTATE SKIP file=%s: %s", oneline.Field(s.File), oneline.Escape(s.Reason)))
	}
	skips.More()
	// The green tests are listed only when the verdict is FAIL. On a PASS they are the
	// file's other tests, which passed before the change and pass without it, and a
	// listing of them is exactly the output the cap-and-count rule exists to prevent:
	// the count on the verdict line is what a PASS needs to carry.
	if !res.Pass {
		greens := bounded.Capped(out, *maxFlag, "MUTATE", "green", mutateRemedy(*repo, *base, *head))
		for _, g := range res.Greens {
			greens.Line(fmt.Sprintf("MUTATE GREEN test=%s file=%s: green with the change reverted; it proves nothing", oneline.Field(g.Name), oneline.Field(g.File)))
		}
		greens.More()
	}

	// The verdict line prints on both outcomes and carries both counts, because the
	// count a caller needs is the one it did not ask for: a FAIL with red=0 and a FAIL
	// with red=9 are different failures.
	line := fmt.Sprintf("MUTATE %s red=%d green=%d %s\n", review.Short(res.Head), res.Red, res.Green, res.Verdict())
	if res.Pass {
		fmt.Fprint(out, line)
		return 0
	}
	fmt.Fprint(errOut, line)
	return 1
}

func mutateRemedy(repo, base, head string) string {
	return fmt.Sprintf("nova-review mutate --repo %s --base %s --head %s --max 0", shellQuote(repo), shellQuote(base), shellQuote(head))
}

func refuseMutate(w io.Writer, reason string) int {
	fmt.Fprintf(w, "MUTATE REFUSED: %s\n", oneline.Escape(reason))
	return 2
}
