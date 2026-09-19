package main

// `nova-review mutate` is the mechanical half of a read, taken off the reader.
//
// Class H of pit stop 3 (nova-tools#828): 504 read cards in one day, each cloning a repo
// to check "is the new test red without the change". That is a mutation test's job, and a
// model is a slow and unreliable way to run it. The verb reverts every non-test hunk in a
// throwaway worktree at the head and runs the tests the change touched: they must fail.
// The harvest runs it before any reader is spawned, and the reader then judges spec fit
// and nothing else, from the packet.
//
// It has two forms. The RANGE form is the one above, and it now says how much it put
// back (`reverted=<n>`), so "every non-test hunk" is a number a caller can gate on
// instead of a sentence in a spec. The SEED form is the other half and the one the
// accept gate's negative controls are built from (SPEC-TOOLWORK.md §1 rules 7 and 9,
// PR #1637): one deliberate one-line defect goes INTO the head, and the named packages'
// suites must kill it. Its edit count is asserted, not reported -- a seed that changed
// nothing proves a suite red on nothing, and a seed that changed two things does not
// say which one was caught.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
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
	seed := fs.String("seed", "", "")
	tests := fs.String("tests", "", "")
	timeout := fs.Int("timeout", 120, "")
	maxFlag := fs.Int("max", bounded.Default, "")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return refuseMutate(errOut, "bad mutate flags")
	}
	if *repo == "" || *head == "" {
		return refuseMutate(errOut, "--repo and --head are required")
	}
	if *timeout <= 0 {
		return refuseMutate(errOut, "--timeout must be positive")
	}
	if *maxFlag < 0 {
		return refuseMutate(errOut, "--max must be non-negative")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()

	// The two forms are exclusive and each names what it needs. A run that took
	// --base and --seed together would have to pick one, and a verb that picks for
	// the caller is a verb whose answer nobody can tie to a question.
	if *seed != "" {
		if *base != "" {
			return refuseMutate(errOut, "--seed and --base are the two forms; give one")
		}
		if *tests == "" {
			return refuseMutate(errOut, "--seed needs --tests <package>[,<package>...]: the suites that must kill the seed")
		}
		return mutateSeed(ctx, *repo, *head, *seed, *tests, out, errOut)
	}
	if *tests != "" {
		return refuseMutate(errOut, "--tests belongs to the --seed form; the range form runs the tests the change touched")
	}
	if *base == "" {
		return refuseMutate(errOut, "--repo, --base and --head are required")
	}

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

	// The verdict line prints on both outcomes and carries every count, because the
	// count a caller needs is the one it did not ask for: a FAIL with red=0 and a FAIL
	// with red=9 are different failures, and a PASS with reverted=0 would be a PASS
	// over a control that never ran.
	line := fmt.Sprintf("MUTATE %s reverted=%d red=%d green=%d %s\n", review.Short(res.Head), res.Reverted, res.Red, res.Green, res.Verdict())
	if res.Pass {
		fmt.Fprint(out, line)
		return 0
	}
	fmt.Fprint(errOut, line)
	return 1
}

// mutateSeed is the seed form. The refusal comes BEFORE the seeded run: a control whose
// size is wrong is not a control, and running it anyway would produce a verdict line
// somebody could quote. The named suites are listed and run once at the UNSEEDED head
// before that, because a package that does not exist and a suite that is already red
// both kill every seed, and a PASS under either is a control that never ran.
func mutateSeed(ctx context.Context, repo, head, seed, tests string, out, errOut io.Writer) int {
	var pkgs []string
	for _, p := range strings.Split(tests, ",") {
		if p = strings.TrimSpace(p); p != "" {
			pkgs = append(pkgs, p)
		}
	}
	if len(pkgs) == 0 {
		return refuseMutate(errOut, "--tests names no package")
	}
	res, err := review.MutateSeed(ctx, review.SeedOptions{Repo: repo, Head: head, Seed: seed, Tests: pkgs})
	var count *review.SeedCountError
	switch {
	case errors.As(err, &count):
		return refuseMutate(errOut, count.Error())
	case err != nil:
		return refuseMutate(errOut, err.Error())
	}
	line := fmt.Sprintf("MUTATE %s seed=%s edits=%d red=%d green=%d %s\n", review.Short(res.Head), res.Seed, res.Edits, res.Red, res.Green, res.Verdict())
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
