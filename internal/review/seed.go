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
	"strconv"
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
	// Exec builds the command for every go run this form makes -- the package listing
	// and the suites -- or is nil for exec.CommandContext; the accept gate hands one that
	// wraps the argv in nova-sandbox, as MutateOptions.Exec does for the range form.
	Exec func(ctx context.Context, dir string, name string, args ...string) *exec.Cmd
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

	pkgs := append([]string(nil), opts.Tests...)
	sort.Strings(pkgs)

	// Two preconditions, both read at the UNSEEDED head, and both could-not-run
	// rather than verdicts.
	//
	// A package name `go list` does not resolve is a typo, and every seed "kills" it:
	// `go test ./nosuch/` exits non-zero having run nothing at all. A suite that is
	// already red is the same failure one step further in -- it kills every seed for
	// a reason that has nothing to do with the seed -- and the PASS printed under it
	// is the head's own failure wearing the control's name.
	for _, pkg := range pkgs {
		if err := listPackage(ctx, opts.Exec, wt, pkg); err != nil {
			return res, err
		}
	}
	for _, pkg := range pkgs {
		red, _, err := runPackage(ctx, opts.Exec, wt, pkg)
		if err != nil {
			return res, err
		}
		if red > 0 {
			return res, fmt.Errorf("the suite in %s is already red at %s without the seed: a seed proves nothing against a suite that was not green first", cleanPkg(pkg), Short(head))
		}
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
	applied, err := gitOut(ctx, wt, "diff", "--cached", "--no-ext-diff", "--no-renames", "--numstat")
	if err != nil {
		return res, fmt.Errorf("could not read the applied seed: %v", err)
	}
	res.Edits = countEdits(applied)
	if res.Edits != 1 {
		return res, &SeedCountError{Edits: res.Edits}
	}

	for _, pkg := range pkgs {
		red, green, err := runPackage(ctx, opts.Exec, wt, pkg)
		if err != nil {
			return res, err
		}
		res.Red += red
		res.Green += green
	}
	// PASS is the mutant dying. A suite that stays green under a one-line defect has
	// told the caller something about the suite, and it is not good news.
	res.Pass = res.Red > 0
	return res, nil
}

// CountEdits is countEdits for a caller outside this package: the accept gate's selftest
// (SPEC-TOOLWORK §1 rule 7) applies whole-object seeds -- a commit re-authored, a file
// added, a hunk dropped -- that the patch form cannot express, and still asserts the line
// count of every patch seed by THIS definition and no second one. The argument is the
// `git diff --numstat` of what was applied, as countEdits reads it; a unified diff is
// not a numstat and counts wrong.
func CountEdits(numstat string) int { return countEdits(numstat) }

// countEdits is the whole definition of "one edit", and it is the patch git applied
// that is counted, never the patch file's header.
//
// One edit is: one line changed (one removed, one added), one line added, one line
// removed, or one line MOVED -- the same text removed in one place and added in
// another, which git reports exactly as a changed line does. All four are one removed
// line or fewer and one added line or fewer, so the count is the larger of the two
// sides. Two changed lines are two, and a seed that changed nothing is zero.
//
// The count comes from `--numstat`, which is git's own arithmetic per file, rather
// than from reading the unified diff line by line: a removed Markdown rule is `----`
// in that text and a removed `-- x` SQL comment is `--- x`, and both read as the
// `---` file header they are not. A seed whose one edit was on such a line was
// refused as a control that changed nothing.
func countEdits(numstat string) int {
	added, removed := 0, 0
	for _, line := range strings.Split(numstat, "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\n"), "\t", 3)
		if len(f) < 3 {
			continue
		}
		// A binary file is `-\t-\t<path>`: git will not count its lines, so it
		// counts as one edit and a seed that touches two of them is two.
		if f[0] == "-" || f[1] == "-" {
			added++
			removed++
			continue
		}
		a, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		d, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		added += a
		removed += d
	}
	if added > removed {
		return added
	}
	return removed
}

// listPackage is the first precondition: the package the caller named must exist at
// this head. `go test ./nosuch/` exits non-zero having run nothing, which every
// later count reads as a suite that died -- so a typo in --tests was a control that
// held, every time, for any seed.
func listPackage(ctx context.Context, execFn seedExec, wt, pkg string) error {
	pkg = cleanPkg(pkg)
	cmd := seedCommand(ctx, execFn, wt, "go", "list", pkgArg(pkg))
	cmd.Dir = wt
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return deadlineErr(pkg)
	}
	if err != nil {
		return fmt.Errorf("--tests names %s, which is not a package at this head: %s", pkg, firstLine(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return fmt.Errorf("--tests names %s, which is not a package at this head", pkg)
	}
	return nil
}

// runPackage runs one package's whole suite in the worktree and counts the units that
// failed and passed. A package that cannot build with the mutant in it is the
// strongest red there is, and it counts as one kill rather than as a run that could
// not happen: the defect was caught by the compiler.
//
// A non-zero exit is NOT by itself a kill. `go test` exits non-zero for every reason
// it has, including reasons that are the caller's and not the seed's, and a verb that
// read all of them as red would report PASS for a deadline, a missing toolchain or a
// module that would not load. The kill is the run saying so: a `--- FAIL:` unit, a
// package-level `FAIL\t<pkg>` line, or a `panic:`. Anything else non-zero is a
// could-not-run and leaves this verb with no verdict to print.
func runPackage(ctx context.Context, execFn seedExec, wt, pkg string) (red, green int, err error) {
	pkg = cleanPkg(pkg)
	cmd := seedCommand(ctx, execFn, wt, "go", "test", "-count=1", "-v", pkgArg(pkg))
	cmd.Dir = wt
	// The verdict is a property of the seed, never of the environment the verb was
	// started in: CI's `make test` exports GOFLAGS=-json, and under it no
	// `--- PASS:` line is printed at all.
	cmd.Env = goenv.Clean(os.Environ())
	out, runErr := cmd.CombinedOutput()
	// The deadline is the caller's, never the seed's: the run was killed mid-flight
	// and nothing at all was proved about the mutant.
	if ctx.Err() != nil {
		return 0, 0, deadlineErr(pkg)
	}
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			return 0, 0, fmt.Errorf("go test could not be run in %s: %v", pkg, runErr)
		}
	}
	text := string(out)
	if strings.Contains(text, "[build failed]") || strings.Contains(text, "[setup failed]") {
		return 1, 0, nil
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
	if runErr == nil || red > 0 {
		return red, green, nil
	}
	// No `--- FAIL:` line and a non-zero exit. A package-level FAIL (a guard in
	// TestMain that exits the binary itself) or a panic before any result is still
	// the mutant dying; anything else is a run that could not happen.
	if packageFailed(text) || strings.Contains(text, "panic:") {
		return 1, green, nil
	}
	return 0, 0, fmt.Errorf("go test in %s exited non-zero with no failure in its output, so nothing was proved either way: %s", pkg, firstLine(text))
}

// packageFailed looks for go test's own package-level verdict, `FAIL\t<pkg>`, which
// it prints for a test binary that exited non-zero without failing a unit.
func packageFailed(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "FAIL\t") && strings.TrimSpace(strings.TrimPrefix(line, "FAIL\t")) != "" {
			return true
		}
	}
	return false
}

func deadlineErr(pkg string) error {
	return fmt.Errorf("the --timeout deadline passed while %s was running, so the run was killed and nothing was proved either way", pkg)
}

// firstLine keeps a refusal to one line, as every line this package prints is.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return "no output"
}

func cleanPkg(pkg string) string {
	pkg = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(pkg), "./"), "/")
	if pkg == "" {
		return "."
	}
	return pkg
}

// seedExec is the command builder the seed form runs go through: nil is
// exec.CommandContext, and the accept gate's is the wall.
type seedExec = func(ctx context.Context, dir string, name string, args ...string) *exec.Cmd

func seedCommand(ctx context.Context, execFn seedExec, dir, name string, args ...string) *exec.Cmd {
	if execFn == nil {
		return exec.CommandContext(ctx, name, args...)
	}
	return execFn(ctx, dir, name, args...)
}

// pkgArg is the ./<pkg>/ pattern go takes, or ./ for the module root.
func pkgArg(pkg string) string {
	if pkg == "." {
		return "./"
	}
	return "./" + pkg + "/"
}
