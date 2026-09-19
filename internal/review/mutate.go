package review

// The mutation test, and why a reader is the wrong place for it.
//
// On 2026-09-17 this house ran 504 read cards in a day, and a large part of what each
// one did was mechanical: clone the repo, revert the change, run the tests, and see
// whether the new test is red without it. Glenn, 2026-09-16: a read that checks "red
// without the change" is doing a mutation test's job. A model is an expensive and
// unreliable way to run a command whose answer is an exit code, and a reader who spends
// their context on it has less left for the judgment only a reader can make: does this
// change fit the spec.
//
// So the check moves here. Mutate takes a repo, a base and a head, builds a throwaway
// worktree at the head, reverts every hunk that is NOT in a test file back to the base,
// and runs the tests the change touched. The tests MUST fail: a test that is green
// without the change it claims to cover proves nothing, and the verdict names it.
//
// What it does not do: it never judges the code, it never writes to the repo it is
// pointed at (the worktree is temporary and removed on every path, including a panic
// in the caller's process is the one case it cannot cover), and it never reads a
// diff for meaning. It runs one command and reports one line.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// ErrNoTestsChanged is the refusal for a range that changes no test file: a fix without
// its red test is not admitted, so there is nothing to mutate and nothing to prove.
var ErrNoTestsChanged = errors.New("no test file changed between the base and the head")

// ErrNoChangeToRevert is the refusal for a range that changes ONLY test files. Reverting
// nothing runs the tests exactly as the head runs them, and "the tests fail" would then
// be a statement about the head's own suite, not about the change. A green run and a red
// run would both be meaningless, so the verb refuses rather than answer either way.
var ErrNoChangeToRevert = errors.New("no change to revert: every changed file is a test file")

// MutateOptions is one invocation. The caller bounds the whole run with the context's
// deadline; the listing's cap is the caller's too (internal/bounded), so nothing here
// decides how much a reader is shown.
type MutateOptions struct {
	Repo string
	Base string
	Head string
	// TempRoot is the directory the throwaway worktree is made in, and the root its
	// removal is bounded by. Empty means os.TempDir(), which is what every caller in
	// production wants. A test names its own t.TempDir() instead, so that "nothing was
	// left behind" is a statement about a directory only that test writes: the shared
	// temp directory is shared with every other job on the same self-hosted runner, and
	// a sibling's `nova-review-mutate-*` appearing between a snapshot and its check made
	// the assertion red four times (#1341 twice, #1345, #1360).
	TempRoot string
	// Exec builds the command for every test run mutate makes, or is nil for
	// exec.CommandContext. The accept gate (SPEC-TOOLWORK §1 rule 3) hands one that
	// wraps the argv in nova-sandbox, so the suites it judges run inside the wall; the
	// dir is the worktree the tests run in, and the wrapper names it as the cwd. Nothing
	// else about the run changes: mutate still sets Dir and Env on what comes back.
	Exec func(ctx context.Context, dir string, name string, args ...string) *exec.Cmd
}

// GreenTest is a test that stayed green with the change reverted: the one thing that
// makes a MUTATE verdict FAIL, named so the author can see which test proves nothing.
type GreenTest struct {
	Name string
	File string
}

// Skip is a changed test file whose suite could not be run, with the reason named. A skip
// never makes a verdict PASS: a file that was not run has no failing test, so it counts
// against the verdict exactly as a green file does.
type Skip struct {
	File   string
	Reason string
}

// MutateResult is the whole answer. Red and Green count TEST UNITS (a Go test function,
// or one Lisp suite), not files; Pass is the per-file rule: every changed test file has at
// least one failing unit with the change reverted.
type MutateResult struct {
	Head  string
	Red   int
	Green int
	// Reverted is the number of non-test HUNKS put back: SPEC-REVIEW says the
	// revert covers "every non-test hunk", and a caller that gates on that sentence
	// needs it as a number. A verdict over an empty revert is a verdict about the
	// head's own suite, so the count is what tells a gate the control ran at all.
	Reverted int
	Pass     bool
	Greens   []GreenTest
	// Reds are the units that went red with the change reverted, by name and file: the
	// accept gate asks whether the card's named TEST: is among them (named-test-not-red),
	// which a count alone cannot answer.
	Reds  []GreenTest
	Skips []Skip
}

// Verdict is the word that ends the MUTATE line.
func (r *MutateResult) Verdict() string {
	if r.Pass {
		return "PASS"
	}
	return "FAIL"
}

// Short is the head's eight-character prefix, the MUTATE line's first field.
func Short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// unit is one runnable test: a Go test function in a package, or a Lisp suite script.
type unit struct {
	name string // Go test function name, or the run-tests.sh path for a Lisp suite
	file string // the changed test file that declares it
	pkg  string // the Go package dir relative to the repo root, or the Lisp project dir
	lisp bool
}

// Mutate reverts the non-test hunks of base..head in a throwaway worktree at head and runs
// the tests of every changed test file there. The worktree is removed before it returns,
// on every path.
func Mutate(ctx context.Context, opts MutateOptions) (*MutateResult, error) {
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
	base, err := revParse(ctx, repo, opts.Base)
	if err != nil {
		return nil, fmt.Errorf("--base %s names no commit in this repo", opts.Base)
	}
	// The change is what the head added since the two diverged, never what the base
	// gained meanwhile: a merge-base left side is the same range a reviewer reads, and
	// it keeps an unrelated commit on the base out of the revert.
	if mb, err := gitLine(ctx, repo, "merge-base", base, head); err == nil && mb != "" {
		base = mb
	}

	changed, err := changedFiles(ctx, repo, base, head)
	if err != nil {
		return nil, err
	}
	var tests, others []change
	for _, c := range changed {
		if isTestFile(c.path) {
			tests = append(tests, c)
		} else {
			others = append(others, c)
		}
	}
	// Both refusals still carry the resolved head, because the line that reports them
	// names the head the caller asked about: a refusal nobody can tie to a commit is a
	// refusal nobody can act on.
	if len(tests) == 0 {
		return &MutateResult{Head: head}, ErrNoTestsChanged
	}
	if len(others) == 0 {
		return &MutateResult{Head: head}, ErrNoChangeToRevert
	}

	tempRoot := opts.TempRoot
	if tempRoot == "" {
		tempRoot = os.TempDir()
	}
	wt, err := os.MkdirTemp(tempRoot, "nova-review-mutate-")
	if err != nil {
		return nil, fmt.Errorf("could not make the worktree directory under %s: %v", tempRoot, err)
	}
	// One removal, on every path out of this function: a worktree left behind is a
	// checkout of somebody's head sitting in the temp directory, and a `git worktree
	// list` that grows by one per run is the repo's own record going wrong.
	defer func() {
		_, _ = gitLine(context.WithoutCancel(ctx), repo, "worktree", "remove", "--force", wt)
		_ = safepath.RemoveUnder(tempRoot, wt)
		_, _ = gitLine(context.WithoutCancel(ctx), repo, "worktree", "prune")
	}()
	if _, err := gitLine(ctx, repo, "worktree", "add", "--detach", wt, head); err != nil {
		return nil, fmt.Errorf("could not add a worktree at %s: %v", Short(head), err)
	}
	if err := revert(ctx, repo, wt, base, others); err != nil {
		return nil, err
	}
	reverted, err := countHunks(ctx, repo, base, head, others)
	if err != nil {
		return nil, err
	}

	units, skips := plan(wt, tests)
	res := &MutateResult{Head: head, Skips: skips, Reverted: reverted}
	redFiles := map[string]bool{}
	skipped := map[string]bool{}
	for _, s := range skips {
		skipped[s.File] = true
	}
	for _, u := range groupByPkg(units) {
		failed, reason := runUnits(ctx, opts.Exec, wt, u)
		if reason != "" {
			for _, one := range u {
				if !skipped[one.file] {
					skipped[one.file] = true
					res.Skips = append(res.Skips, Skip{File: one.file, Reason: reason})
				}
			}
			continue
		}
		for _, one := range u {
			if failed[one.name] {
				res.Red++
				redFiles[one.file] = true
				res.Reds = append(res.Reds, GreenTest{Name: one.name, File: one.file})
				continue
			}
			// A unit with no FAIL line is green, and so is a unit with no result
			// line at all: a unit that did not run is not evidence that it fails,
			// and counting it red would let a filter typo read as a proof.
			res.Green++
			res.Greens = append(res.Greens, GreenTest{Name: one.name, File: one.file})
		}
	}
	sort.Slice(res.Greens, func(i, j int) bool {
		if res.Greens[i].File != res.Greens[j].File {
			return res.Greens[i].File < res.Greens[j].File
		}
		return res.Greens[i].Name < res.Greens[j].Name
	})
	// The verdict is per FILE: every changed test file that could be run has at least
	// one test that fails with the change reverted. A file that could not be run at all
	// is named on its own SKIP line and judged by nobody here -- the verdict says what
	// was run, and a skip with no reason beside it is the one thing this must not be.
	res.Pass = res.Red > 0
	for _, t := range tests {
		if !redFiles[t.path] && !skipped[t.path] {
			res.Pass = false
		}
	}
	return res, nil
}

type change struct {
	status string // A, D, M (renames are off, so a rename is a D and an A)
	path   string
}

func changedFiles(ctx context.Context, repo, base, head string) ([]change, error) {
	out, err := gitOut(ctx, repo, "diff", "--no-ext-diff", "--no-renames", "--name-status", base, head)
	if err != nil {
		return nil, fmt.Errorf("could not read the change between %s and %s: %v", Short(base), Short(head), err)
	}
	var cs []change
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		cs = append(cs, change{status: strings.TrimSpace(fields[0]), path: fields[1]})
	}
	return cs, nil
}

// isTestFile is the whole definition of "a test hunk", and it is deliberately narrow: a Go
// file ending _test.go, or a Lisp file under a tests/ directory (the Lisp legs keep their
// cases there). Everything else is the change, and the change is what gets reverted --
// a fixture under testdata/ included, because a fixture a fix had to change is part of
// the fix and a test that only passes with the new fixture has proved nothing about the
// code.
func isTestFile(p string) bool {
	return strings.HasSuffix(p, "_test.go") || isLispTest(p)
}

// revert puts every non-test path back the way the base had it, inside the worktree only:
// a path the head added is removed, a path the head changed or deleted is checked out from
// the base. `git checkout <base> -- <paths>` is run from the worktree, so the index and the
// tree it writes are the worktree's own and the caller's repo is never touched.
func revert(ctx context.Context, repo, wt, base string, others []change) error {
	var restore []string
	for _, c := range others {
		switch c.status {
		case "A":
			if err := os.Remove(filepath.Join(wt, filepath.FromSlash(c.path))); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("could not remove %s from the worktree: %v", c.path, err)
			}
		default:
			restore = append(restore, c.path)
		}
	}
	for len(restore) > 0 {
		n := len(restore)
		if n > 100 {
			n = 100
		}
		args := append([]string{"checkout", base, "--"}, restore[:n]...)
		if _, err := gitOut(ctx, wt, args...); err != nil {
			return fmt.Errorf("could not revert the non-test change to %s: %v", Short(base), err)
		}
		restore = restore[n:]
	}
	return nil
}

var goTestFunc = regexp.MustCompile(`(?m)^func (Test[A-Z_0-9][A-Za-z_0-9]*)\(`)

// plan turns changed test files into runnable units, reading them in the WORKTREE and never
// in the caller's working copy: the worktree is the head, and the caller may sit on any
// commit at all -- on the base, a test file the head adds is not there to read. A Go test
// file contributes every test function it declares there (the revert leaves the test hunks
// alone, so that is the head's text); a Lisp test file contributes its project's
// run-tests.sh. A file that is neither is skipped with its reason named.
func plan(wt string, tests []change) ([]unit, []Skip) {
	var units []unit
	var skips []Skip
	for _, t := range tests {
		if t.status == "D" {
			skips = append(skips, Skip{File: t.path, Reason: "the head deletes this test file; there is nothing to run"})
			continue
		}
		switch {
		case strings.HasSuffix(t.path, "_test.go"):
			src, err := os.ReadFile(filepath.Join(wt, filepath.FromSlash(t.path)))
			if err != nil {
				skips = append(skips, Skip{File: t.path, Reason: "could not read the test file at the head"})
				continue
			}
			names := goTestFunc.FindAllStringSubmatch(string(src), -1)
			if len(names) == 0 {
				skips = append(skips, Skip{File: t.path, Reason: "the file declares no Test function"})
				continue
			}
			pkg := path.Dir(t.path)
			for _, m := range names {
				units = append(units, unit{name: m[1], file: t.path, pkg: pkg})
			}
		case isLispTest(t.path):
			proj := lispProject(t.path)
			if proj == "" {
				skips = append(skips, Skip{File: t.path, Reason: "no lisp project directory above this tests/ directory"})
				continue
			}
			script := path.Join(proj, "run-tests.sh")
			if _, err := os.Stat(filepath.Join(wt, filepath.FromSlash(script))); err != nil {
				skips = append(skips, Skip{File: t.path, Reason: "the lisp project has no run-tests.sh"})
				continue
			}
			units = append(units, unit{name: script, file: t.path, pkg: proj, lisp: true})
		default:
			skips = append(skips, Skip{File: t.path, Reason: "no runner for this kind of test file"})
		}
	}
	return units, skips
}

func isLispTest(p string) bool {
	if !strings.HasPrefix(p, "lisp/") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "tests" {
			return true
		}
	}
	return false
}

// lispProject is the directory holding the tests/ directory this file is under.
func lispProject(p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		if seg == "tests" && i > 0 {
			return strings.Join(segs[:i], "/")
		}
	}
	return ""
}

// groupByPkg gathers the units into one run per package, in a deterministic order, so a
// package's tests are one `go test` and not one per function.
func groupByPkg(units []unit) [][]unit {
	order := []string{}
	by := map[string][]unit{}
	for _, u := range units {
		key := u.pkg
		if u.lisp {
			key = "lisp:" + u.name
		}
		if _, seen := by[key]; !seen {
			order = append(order, key)
		}
		by[key] = append(by[key], u)
	}
	sort.Strings(order)
	out := make([][]unit, 0, len(order))
	for _, k := range order {
		out = append(out, by[k])
	}
	return out
}

var goResult = regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): ([A-Za-z_0-9]+)`)

// runUnits runs one package's units in the worktree and reports which of them FAILED. A
// package that does not build is the strongest red there is -- the tests cannot even
// compile without the change -- so every unit in it counts failed. A named skip reason
// comes back when the suite could not be run at all, and then nothing is judged.
func runUnits(ctx context.Context, execFn func(context.Context, string, string, ...string) *exec.Cmd, wt string, units []unit) (failed map[string]bool, skip string) {
	failed = map[string]bool{}
	if execFn == nil {
		execFn = func(ctx context.Context, _ string, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, name, args...)
		}
	}
	if units[0].lisp {
		script := units[0].name
		if _, err := os.Stat(filepath.Join(wt, filepath.FromSlash(script))); err != nil {
			return nil, "the lisp project has no run-tests.sh in the worktree"
		}
		cmd := execFn(ctx, wt, "sh", script)
		cmd.Dir = wt
		err := cmd.Run()
		var ee *exec.ExitError
		if err != nil && !errors.As(err, &ee) {
			return nil, fmt.Sprintf("the lisp suite could not be run: %v", err)
		}
		if err != nil {
			failed[script] = true
		}
		return failed, ""
	}

	names := make([]string, 0, len(units))
	for _, u := range units {
		names = append(names, regexp.QuoteMeta(u.name))
	}
	sort.Strings(names)
	args := []string{"test", "-count=1", "-v", "-run", "^(" + strings.Join(names, "|") + ")$", "./" + units[0].pkg + "/"}
	if units[0].pkg == "." {
		args[len(args)-1] = "./"
	}
	cmd := execFn(ctx, wt, "go", args...)
	cmd.Dir = wt
	// The verdict is a property of the range, never of the environment mutate was
	// started in. A caller's GOFLAGS=-json -- which is what CI's `make test`
	// exports -- would make this run answer in JSON, no `--- PASS:` line would
	// match below, and the unit that stayed green would be counted red.
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return nil, fmt.Sprintf("go test could not be run: %v", err)
		}
	}
	text := string(out)
	// A package that fails to compile prints no per-test result at all. Every unit in
	// it is red: the test file cannot even build with the change reverted.
	if strings.Contains(text, "[build failed]") || strings.Contains(text, "[setup failed]") {
		for _, u := range units {
			failed[u.name] = true
		}
		return failed, ""
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		m := goResult.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		if m[1] == "FAIL" {
			failed[m[2]] = true
		}
	}
	// A run that exited non-zero with no FAIL line at all (a panic before any result,
	// a timeout inside the binary) is evidence the package's tests do not pass, and
	// every unit it was asked for counts red rather than silently green.
	if err != nil && len(failed) == 0 {
		for _, u := range units {
			failed[u.name] = true
		}
	}
	return failed, ""
}

func revParse(ctx context.Context, repo, ref string) (string, error) {
	return gitLine(ctx, repo, "rev-parse", ref+"^{commit}")
}

func gitLine(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitOut(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// countHunks is how much was put back: the number of `@@` hunks in the base..head diff
// restricted to the non-test files the revert touched. A file the head ADDED and the
// revert removed whole counts as its own hunks, because that is what the diff says was
// undone.
//
// It is read from the range, not from the checkout: `git checkout <base> -- <paths>`
// says nothing about how many hunks it wrote, and a count that came from the same
// command whose work it is measuring would agree with itself whatever happened.
func countHunks(ctx context.Context, repo, base, head string, others []change) (int, error) {
	if len(others) == 0 {
		return 0, nil
	}
	n := 0
	paths := make([]string, 0, len(others))
	for _, c := range others {
		paths = append(paths, c.path)
	}
	for len(paths) > 0 {
		k := len(paths)
		if k > 100 {
			k = 100
		}
		args := append([]string{"diff", "--no-ext-diff", "--no-renames", "--unified=0", base, head, "--"}, paths[:k]...)
		out, err := gitOut(ctx, repo, args...)
		if err != nil {
			return 0, fmt.Errorf("could not count the hunks between %s and %s: %v", Short(base), Short(head), err)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "@@ ") {
				n++
			}
		}
		paths = paths[k:]
	}
	return n, nil
}
