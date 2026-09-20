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
		var bad *buildError
		if errors.As(err, &bad) {
			// Before the seed, so it is the HEAD's or the bench's and never the
			// seed's. Named as such, because the two are fixed by different people.
			return res, fmt.Errorf("%s does not build at %s without the seed, so no seed could be judged against it: %s", cleanPkg(pkg), Short(head), bad.Detail)
		}
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
	// `-U0` alongside the numstat: the arithmetic stays git's own, and the second
	// read answers the question the arithmetic cannot -- WHERE the changed lines are.
	// One line added here and one removed there is one of each on the numstat and two
	// edits in the tree (#1803).
	placed, err := gitOut(ctx, wt, "diff", "--cached", "--no-ext-diff", "--no-renames", "-U0")
	if err != nil {
		return res, fmt.Errorf("could not read the applied seed: %v", err)
	}
	res.Edits = countEdits(applied, placed)
	if res.Edits != 1 {
		return res, &SeedCountError{Edits: res.Edits}
	}

	for _, pkg := range pkgs {
		red, green, err := runPackage(ctx, opts.Exec, wt, pkg)
		var bad *buildError
		if errors.As(err, &bad) {
			// After the seed, and the head was proved to build and be green above, so
			// this is the SEED's. A control that does not compile is not a weaker
			// control -- it is no control at all, and it kills every suite it is
			// pointed at, which is indistinguishable from the kill it claims (#1847).
			return res, &SeedBuildError{Pkg: cleanPkg(pkg), Detail: bad.Detail}
		}
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
// count of every patch seed by THIS definition and no second one. The arguments are the
// two reads of what git applied, exactly as MutateSeed takes them: the `--numstat` is the
// arithmetic and the `-U0` diff is where the changed lines are (an edit is a place and
// not a sum, #1803). A unified diff in place of the numstat is not a numstat and counts
// wrong.
func CountEdits(numstat, placed string) int { return countEdits(numstat, placed) }

// countEdits is the whole definition of "one edit", and it is the patch git applied
// that is counted, never the patch file's header.
//
// One edit is: one line changed (one removed, one added), one line added, one line
// removed, or one line MOVED -- the same text removed in one place and added in
// another, which git reports exactly as a changed line does. All four are one removed
// line or fewer and one added line or fewer, so a file's own count is the larger of
// its two sides. Two changed lines are two, and a seed that changed nothing is zero.
//
// AN EDIT IS A PLACE AND NOT A SUM, and for three months it was a sum. The count was
// `max(total added, total removed)` over the whole patch, so two edits in two places
// CANCELLED: a line added in `a.go` and a line removed in `b.go` is added=1,
// removed=1, larger side 1, and a seed that changed two things printed `edits=1` and
// ran (Emma's bench dogfood, #1803). The same arithmetic passed a line added in one
// function and a line removed in another in the SAME file. That is the exact failure
// rule 7 exists to prevent -- a gate that went red under such a seed has proved one
// of two rules and nobody can say which.
//
// So the count is now per PLACE:
//
//	the number of files touched, if that is more than one
//	otherwise the number of `@@` hunks, if that is more than the line count
//	otherwise the file's own larger side, as before
//
// with ONE exception, which is rule 7's own: a moved line is two hunks and one edit,
// and it is verified rather than assumed -- exactly one added line, exactly one
// removed line, and the SAME TEXT. Two hunks holding different text are two edits.
//
// The line arithmetic still comes from `--numstat`, which is git's own per file,
// rather than from reading the unified diff: a removed Markdown rule is `----` in
// that text and a removed `-- x` SQL comment is `--- x`, and both read as the `---`
// file header they are not. A seed whose one edit was on such a line was refused as a
// control that changed nothing. The `-U0` diff is read only for the two things
// numstat cannot say -- how many hunks, and what the one added and one removed line
// SAY -- and it is read only after the first `@@`, where every line carries a `+` or
// a `-` prefix and the header ambiguity cannot arise.
func countEdits(numstat, placed string) int {
	lines, files := countLines(numstat)
	if files != 1 {
		// Two files are two places whatever their lines add up to, and a file whose
		// only change is its mode counts 0 lines and is still a place.
		if files > lines {
			return files
		}
		return lines
	}
	hunks, added, removed := countPlaces(placed)
	if hunks <= 1 {
		return lines
	}
	// Rule 7's fourth shape: one line moved. The same text, out of one place and into
	// another, which git reports as one removed line and one added line.
	if hunks == 2 && len(added) == 1 && len(removed) == 1 && added[0] == removed[0] {
		return 1
	}
	if hunks > lines {
		return hunks
	}
	return lines
}

// countLines is git's own arithmetic over `--numstat`: the larger side per file,
// summed, and the number of files that side was taken from.
func countLines(numstat string) (lines, files int) {
	for _, line := range strings.Split(numstat, "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\n"), "\t", 3)
		if len(f) < 3 {
			continue
		}
		// A binary file is `-\t-\t<path>`: git will not count its lines, so it
		// counts as one edit and a seed that touches two of them is two.
		if f[0] == "-" || f[1] == "-" {
			files++
			lines++
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
		files++
		if a > d {
			lines += a
		} else {
			lines += d
		}
	}
	return lines, files
}

// countPlaces reads a `-U0` diff for the two things the numstat cannot say: how many
// hunks it has, and what its added and removed lines actually hold.
//
// Every line of a `-U0` hunk carries a `+` or a `-`, because there is no context; a
// hunk header starts `@@` at column 0 and a content line never can, since it always
// carries its prefix first. `diff --git ` at column 0 is the next file's header for
// the same reason, and ends the file this loop is inside. Nothing before the first
// `@@` of a file is read -- which is where `--- a/x` and `+++ b/x` live, and the
// whole reason the line count is left to git.
func countPlaces(placed string) (hunks int, added, removed []string) {
	inHunk := false
	for _, line := range strings.Split(placed, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inHunk = false
		case strings.HasPrefix(line, "@@"):
			hunks++
			inHunk = true
		case !inHunk:
			// A file header, a mode line, an index line: not a place.
		case strings.HasPrefix(line, "+"):
			added = append(added, line[1:])
		case strings.HasPrefix(line, "-"):
			removed = append(removed, line[1:])
		}
	}
	return hunks, added, removed
}

// listPackage is the first precondition: the package the caller named must exist at
// this head. `go test ./nosuch/` exits non-zero having run nothing, which every
// later count reads as a suite that died -- so a typo in --tests was a control that
// held, every time, for any seed.
func listPackage(ctx context.Context, execFn seedExec, wt, pkg string) error {
	pkg = cleanPkg(pkg)
	cmd := seedCommand(ctx, execFn, wt, "go", "list", pkgArg(pkg))
	cmd.Dir = wt
	if cmd.Env == nil {
		// The seam's Env is the seam's (the accept gate's HOME and GOCACHE inside its
		// wall); only a bare command gets the cleaned environment. Cold read 2 of
		// #1721: replacing it ran the card's tests with the operator's HOME.
		cmd.Env = goenv.Clean(os.Environ())
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return deadlineErr(pkg)
	}
	// The refusal names the PACKAGE and nothing else. `go list`'s own first line
	// carries the throwaway worktree's absolute path, and this line is quoted by a
	// gate, into a PR body and into a harvest log: a temp directory that existed for
	// two seconds on one bench is noise everywhere it lands, and the caller's typo is
	// the whole answer.
	if err != nil || strings.TrimSpace(string(out)) == "" {
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
	if cmd.Env == nil {
		// The seam's Env is the seam's (the accept gate's HOME and GOCACHE inside its
		// wall); only a bare command gets the cleaned environment. Cold read 2 of
		// #1721: replacing it ran the card's tests with the operator's HOME.
		cmd.Env = goenv.Clean(os.Environ())
	}
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
	// A PACKAGE THAT DOES NOT BUILD IS NOT A RED, HERE. For a real mutant it is the
	// strongest red there is -- the range form says so and means it. For a SEED it is
	// the opposite: a control that is red because nothing compiled has proved nothing
	// whatever about the suite, and it is §1 rule 6's `broken` seed, whose want is the
	// token `build` and not a kill. It was scored as a kill, so a seed with a syntax
	// error in it printed `edits=1 red=1 green=0 PASS` over a tree that did not
	// compile (the Opus readers' dogfood, #1847; the same class as #1807).
	//
	// It is raised as an ERROR and named by the caller, because what it means depends
	// on WHEN it happened: at the unseeded head it is the head's or the bench's, and
	// after the seed is applied it is the seed's.
	if strings.Contains(text, "[build failed]") || strings.Contains(text, "[setup failed]") {
		return 0, 0, &buildError{Pkg: pkg, Detail: firstCompilerLine(text)}
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

// buildError is "this package did not compile", raised by runPackage and never printed
// by it: the same fact is the bench's before the seed is applied and the seed's after,
// and only the caller knows which side of that line it is on.
type buildError struct {
	Pkg    string
	Detail string
}

func (e *buildError) Error() string {
	return fmt.Sprintf("%s does not build: %s", e.Pkg, e.Detail)
}

// SeedBuildError is the does-not-build refusal for the seeded tree: a control that
// never ran, and a could-not-run rather than a verdict (#1847).
type SeedBuildError struct {
	Pkg    string
	Detail string
}

func (e *SeedBuildError) Error() string {
	return fmt.Sprintf("seed does not build: %s", e.Detail)
}

// firstCompilerLine picks the compiler's own line out of a `go test` build failure --
// `<file>:<line>:<col>: <what>` -- which is the whole answer for the seed's author and
// the only part of that output worth carrying into a refusal. `# <package>` above it
// and `FAIL <pkg> [build failed]` below it say nothing the refusal does not already.
func firstCompilerLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "FAIL") {
			continue
		}
		if i := strings.Index(s, ".go:"); i > 0 && strings.Count(s[i:], ":") >= 2 {
			return s
		}
	}
	return firstLine(text)
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
