package review

// The seed form of the mutation test, and why the count is asserted rather than
// reported.
//
// SPEC-TOOLWORK.md §1 rules 7 and 9 (PR #1637), issue #1646. The range form answers
// "is this change's test red without the change". The seed form answers the other
// half, and it is the half every negative control in the accept gate is built from:
// "here is one deliberate defect -- does the suite catch it". A control is only worth
// what its defect is worth. A seed that changed nothing proves the suite red on
// nothing, and a green run under it would read as a suite that works. A seed that
// changed two things does not say which one the suite caught, so a gate that passed
// under it has proved one of two rules and nobody knows which. So the verb counts the
// patch it actually applied and refuses anything but exactly one edit: the count is a
// precondition of the run, not a field on its report.
//
// The count is taken from the worktree AFTER `git apply`, never from the patch file's
// own `@@` header. A header is arithmetic the seed's author wrote, and a control that
// believes it can be told any number at all.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// ErrSeedDoesNotApply is the refusal for a patch git could not apply at the head: the
// control was never installed, so nothing was proved either way and no verdict is
// printed.
var ErrSeedDoesNotApply = errors.New("the seed does not apply at the head")

// SeedCountError is the one-edit refusal, carrying the count so the caller can print
// the spec's line without recomputing anything.
type SeedCountError struct{ Edits int }

func (e *SeedCountError) Error() string {
	return fmt.Sprintf("seed makes %d edits, want exactly 1", e.Edits)
}

// SeedOptions is one seeded run. Tests names the packages whose suites must kill the
// mutant, repo-relative, as the card's own TEST: line names them.
type SeedOptions struct {
	Repo     string
	Head     string
	Seed     string // path to a unified diff
	Tests    []string
	TempRoot string
}

// SeedResult is the whole answer: the head the mutant was put into, the seed's own
// digest so a report names WHICH control ran, the asserted edit count, and the units
// that went red and green under it.
type SeedResult struct {
	Head  string
	Seed  string // first 8 hex of the seed patch's SHA-256
	Edits int
	Red   int
	Green int
	Pass  bool
}

// Verdict is the word that ends the MUTATE line. PASS is the suite killing the mutant.
func (r *SeedResult) Verdict() string {
	if r.Pass {
		return "PASS"
	}
	return "FAIL"
}

// MutateSeed applies one seed patch in a throwaway worktree at the head, asserts that
// it made exactly one edit, and runs the named packages' suites there. It writes
// nothing into the repo it is pointed at, on every path.
func MutateSeed(ctx context.Context, opts SeedOptions) (*SeedResult, error) {
	repo, err := filepath.Abs(opts.Repo)
	if err != nil {
		return nil, fmt.Errorf("--repo %s: %v", opts.Repo, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return nil, fmt.Errorf("--repo %s is not a git working copy", opts.Repo)
	}
	head, err := revParse(ctx, repo, opts.Head)
	if err != nil {
		return nil, fmt.Errorf("--head %s names no commit in this repo", opts.Head)
	}
	seedPath, err := filepath.Abs(opts.Seed)
	if err != nil {
		return nil, fmt.Errorf("--seed %s: %v", opts.Seed, err)
	}
	patch, err := os.ReadFile(seedPath)
	if err != nil {
		return nil, fmt.Errorf("could not read the seed %s: %v", opts.Seed, err)
	}
	sum := sha256.Sum256(patch)
	res := &SeedResult{Head: head, Seed: hex.EncodeToString(sum[:])[:8]}
	if len(opts.Tests) == 0 {
		return res, errors.New("--tests names no package")
	}

	tempRoot := opts.TempRoot
	if tempRoot == "" {
		tempRoot = os.TempDir()
	}
	wt, err := os.MkdirTemp(tempRoot, "nova-review-seed-")
	if err != nil {
		return nil, fmt.Errorf("could not make the worktree directory under %s: %v", tempRoot, err)
	}
	defer func() {
		_, _ = gitLine(context.WithoutCancel(ctx), repo, "worktree", "remove", "--force", wt)
		_ = safepath.RemoveUnder(tempRoot, wt)
		_, _ = gitLine(context.WithoutCancel(ctx), repo, "worktree", "prune")
	}()
	if _, err := gitLine(ctx, repo, "worktree", "add", "--detach", wt, head); err != nil {
		return nil, fmt.Errorf("could not add a worktree at %s: %v", Short(head), err)
	}
	if _, err := gitOut(ctx, wt, "apply", "--whitespace=nowarn", seedPath); err != nil {
		return res, fmt.Errorf("%w: %v", ErrSeedDoesNotApply, err)
	}

	// Stage first so an added or removed FILE is in the diff too: `git diff` alone
	// sees neither, and a seed that adds a file would otherwise count zero and be
	// refused for the wrong reason.
	if _, err := gitOut(ctx, wt, "add", "-A"); err != nil {
		return res, fmt.Errorf("could not stage the seeded worktree: %v", err)
	}
	applied, err := gitOut(ctx, wt, "diff", "--cached", "--no-ext-diff", "--no-renames", "-U0")
	if err != nil {
		return res, fmt.Errorf("could not read the applied seed: %v", err)
	}
	res.Edits = countEdits(applied)
	if res.Edits != 1 {
		return res, &SeedCountError{Edits: res.Edits}
	}

	pkgs := append([]string(nil), opts.Tests...)
	sort.Strings(pkgs)
	for _, pkg := range pkgs {
		red, green, reason := runPackage(ctx, wt, pkg)
		if reason != "" {
			return res, errors.New(reason)
		}
		res.Red += red
		res.Green += green
	}
	// PASS is the mutant dying. A suite that stays green under a one-line defect has
	// told the caller something about the suite, and it is not good news.
	res.Pass = res.Red > 0
	return res, nil
}

// countEdits is the whole definition of "one edit", and it is the patch git applied
// that is counted, never the patch file's header.
//
// One edit is: one line changed (one removed, one added), one line added, one line
// removed, or one line MOVED -- the same text removed in one place and added in
// another, which git reports exactly as a changed line does. All four are one removed
// line or fewer and one added line or fewer, so the count is the larger of the two
// sides. Two changed lines are two, and a seed that changed nothing is zero.
func countEdits(diff string) int {
	added, removed := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			// A file header, not a line of content.
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	if added > removed {
		return added
	}
	return removed
}

// runPackage runs one package's whole suite in the worktree and counts the units that
// failed and passed. A package that cannot build with the mutant in it is the
// strongest red there is, and it counts as one kill rather than as a run that could
// not happen: the defect was caught by the compiler.
func runPackage(ctx context.Context, wt, pkg string) (red, green int, skip string) {
	pkg = cleanPkg(pkg)
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "./"+pkg+"/")
	if pkg == "." {
		cmd = exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "./")
	}
	cmd.Dir = wt
	// The verdict is a property of the seed, never of the environment the verb was
	// started in: CI's `make test` exports GOFLAGS=-json, and under it no
	// `--- PASS:` line is printed at all.
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return 0, 0, fmt.Sprintf("go test could not be run in %s: %v", pkg, err)
		}
	}
	text := string(out)
	if strings.Contains(text, "[build failed]") || strings.Contains(text, "[setup failed]") {
		return 1, 0, ""
	}
	for _, line := range strings.Split(text, "\n") {
		m := goResult.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch m[1] {
		case "FAIL":
			red++
		case "PASS":
			green++
		}
	}
	// A run that exited non-zero with no FAIL line at all -- a panic before any
	// result, a timeout inside the binary -- is the mutant killing the package, not a
	// package that quietly passed.
	if err != nil && red == 0 {
		red = 1
	}
	return red, green, ""
}

func cleanPkg(pkg string) string {
	pkg = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(pkg), "./"), "/")
	if pkg == "" {
		return "."
	}
	return pkg
}
