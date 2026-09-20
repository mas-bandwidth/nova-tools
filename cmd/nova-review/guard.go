package main

// `nova-review guard` is the mechanical half of a guard card. The model only
// picks which packages to run. The verdict is computed from exit codes and
// test names: revert the commit's non-test files, keep the tests, they must
// go red. Issue #2042.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/review"
)

func guard(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("guard", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "")
	head := fs.String("head", "", "")
	tests := fs.String("tests", "", "")
	timeout := fs.Int("timeout", 120, "")
	maxFlag := fs.Int("max", bounded.Default, "")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return refuseGuard(errOut, "bad guard flags")
	}
	if *repo == "" || *head == "" {
		return refuseGuard(errOut, "--repo and --head are required")
	}
	if *timeout <= 0 {
		return refuseGuard(errOut, "--timeout must be positive")
	}
	if *maxFlag < 0 {
		return refuseGuard(errOut, "--max must be non-negative")
	}

	var pkgs []string
	for _, p := range strings.Split(*tests, ",") {
		if p = strings.TrimSpace(p); p != "" {
			pkgs = append(pkgs, p)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()

	res, err := review.Guard(ctx, review.GuardOptions{Repo: *repo, Head: *head, Tests: pkgs})
	switch {
	case errors.Is(err, review.ErrNoChangeToRevert):
		fmt.Fprintf(out, "GUARD %s platform=%s status=ABSTAIN reason=no-change-to-revert: every changed file is a test file, so there is no production hunk to revert and this control cannot be proved either way\n", review.Short(res.Head), platformOf(res))
		return 2
	case errors.Is(err, review.ErrHeadRed):
		fmt.Fprintf(out, "GUARD %s platform=%s status=ABSTAIN reason=head-red: the named packages are already red at the head, so this control cannot be proved either way\n", review.Short(res.Head), platformOf(res))
		return 2
	case err != nil:
		return refuseGuard(errOut, err.Error())
	}

	skips := bounded.Capped(out, *maxFlag, "GUARD", "tail", guardRemedy(*repo, *head, *tests))
	for _, t := range res.Tails {
		skips.Line(fmt.Sprintf("GUARD TAIL which=%s pkg=%s exit=%d last=%s", oneline.Field(t.Which), oneline.Field(t.Pkg), t.Exit, oneline.Field(t.Last)))
	}
	skips.More()
	if len(res.Reds) > 0 {
		reds := bounded.Capped(out, *maxFlag, "GUARD", "red", guardRemedy(*repo, *head, *tests))
		for _, name := range res.Reds {
			reds.Line(fmt.Sprintf("GUARD RED test=%s", oneline.Field(name)))
		}
		reds.More()
	}

	line := fmt.Sprintf("GUARD %s platform=%s reverted=%d red=%d green=%d status=%s\n", review.Short(res.Head), oneline.Field(res.Platform), res.Reverted, res.Red, res.Green, oneline.Field(res.Verdict))
	if res.Verdict == review.VerdictNotApplicable {
		line = fmt.Sprintf("GUARD %s platform=%s status=NOT-APPLICABLE reason=%s\n", review.Short(res.Head), oneline.Field(res.Platform), oneline.Field(res.Reason))
	}
	switch res.Verdict {
	case review.VerdictGuarded:
		fmt.Fprint(out, line)
		return 0
	case review.VerdictNotApplicable:
		fmt.Fprint(out, line)
		return 2
	default:
		fmt.Fprint(errOut, line)
		return 1
	}
}

func platformOf(res *review.GuardResult) string {
	if res != nil && res.Platform != "" {
		return oneline.Field(res.Platform)
	}
	return oneline.Field(runtime.GOOS + "/" + runtime.GOARCH)
}

func guardRemedy(repo, head, tests string) string {
	line := fmt.Sprintf("nova-review guard --repo %s --head %s", shellQuote(repo), shellQuote(head))
	if tests != "" {
		line += " --tests " + shellQuote(tests)
	}
	return line + " --max 0"
}

func refuseGuard(w io.Writer, reason string) int {
	fmt.Fprintf(w, "GUARD REFUSED: %s\n", oneline.Escape(reason))
	return 2
}
