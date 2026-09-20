package review

// Guard is the post-landing negative control: revert the commit's non-test files,
// keep the tests, run the named packages. The verdict is computed from exit codes
// and test names, never judged. Issue #2042: a model's UNGUARDED was wrong about
// a third of the time — ddce356e went red on two named tests, 7afd48c0 ran a
// darwin-only file on linux, 51724da5 contradicted its own body.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

const (
	// VerdictGuarded: the control compiled and named tests went red.
	VerdictGuarded = "GUARDED"
	// VerdictUnguarded: the control compiled and stayed green.
	VerdictUnguarded = "UNGUARDED"
	// VerdictCompilerHeld: the revert does not compile; coupling is not an assertion.
	VerdictCompilerHeld = "COMPILER-HELD"
	// VerdictNotApplicable: this OS will not compile the reverted file.
	VerdictNotApplicable = "NOT-APPLICABLE"
)

// ErrHeadRed is the abstain when the named packages are already red at the head,
// so a red control would not be about the revert.
var ErrHeadRed = errors.New("the named packages are already red at the head, so this control cannot be proved either way")

// GuardOptions is one invocation. Tests is the model's only input: which packages
// to run. Empty means the packages of the commit's changed .go files.
type GuardOptions struct {
	Repo     string
	Head     string
	Tests    []string
	TempRoot string
}

// GuardTail is one recorded test tail: baseline (at the head) or control (reverted).
type GuardTail struct {
	Which string // baseline | control
	Pkg   string
	Exit  int
	Last  string
}

// GuardResult is the computed answer. Verdict is one of the four tokens; it is
// never taken from a file a model wrote.
type GuardResult struct {
	Head     string
	Platform string
	Reverted int
	Red      int
	Green    int
	Verdict  string
	Reason   string
	Reds     []string
	Tails    []GuardTail
	Packages []string
}

// Guard reverts the non-test files of head~1..head in a throwaway worktree and
// runs the named packages. It writes nothing into the repo it is pointed at.
func Guard(ctx context.Context, opts GuardOptions) (*GuardResult, error) {
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
	base, err := revParse(ctx, repo, head+"~1")
	if err != nil {
		return nil, fmt.Errorf("--head %s has no parent to revert against", Short(head))
	}

	res := &GuardResult{Head: head, Platform: runtime.GOOS + "/" + runtime.GOARCH}
	changed, err := changedFiles(ctx, repo, base, head)
	if err != nil {
		return res, err
	}
	var others []change
	for _, c := range changed {
		if isTestFile(c.path) {
			continue
		}
		others = append(others, c)
	}
	if len(others) == 0 {
		return res, ErrNoChangeToRevert
	}

	tempRoot := opts.TempRoot
	if tempRoot == "" {
		tempRoot = os.TempDir()
	}
	wt, err := os.MkdirTemp(tempRoot, "nova-review-guard-")
	if err != nil {
		return res, fmt.Errorf("could not make the worktree directory under %s: %v", tempRoot, err)
	}
	defer func() {
		_, _ = gitLine(context.WithoutCancel(ctx), repo, "worktree", "remove", "--force", wt)
		_ = safepath.RemoveUnder(tempRoot, wt)
		_, _ = gitLine(context.WithoutCancel(ctx), repo, "worktree", "prune")
	}()
	if _, err := gitLine(ctx, repo, "worktree", "add", "--detach", wt, head); err != nil {
		return res, fmt.Errorf("could not add a worktree at %s: %v", Short(head), err)
	}

	n, err := countHunks(ctx, repo, base, head, others)
	if err != nil {
		return res, err
	}
	res.Reverted = n

	if !productionAppliesHere(wt, others) {
		res.Verdict = VerdictNotApplicable
		res.Reason = "build-tags"
		return res, nil
	}

	pkgs := opts.Tests
	if len(pkgs) == 0 {
		pkgs = packagesOf(changed)
	}
	if len(pkgs) == 0 {
		return res, errors.New("no package to run: pass --tests <package>[,<package>...]")
	}
	for i := range pkgs {
		pkgs[i] = cleanPkg(pkgs[i])
	}
	sort.Strings(pkgs)
	res.Packages = pkgs
	for _, pkg := range pkgs {
		if err := listPackage(ctx, wt, pkg); err != nil {
			return res, err
		}
	}

	for _, pkg := range pkgs {
		run := runGuardPackage(ctx, wt, pkg)
		if run.err != nil {
			return res, run.err
		}
		res.Tails = append(res.Tails, GuardTail{Which: "baseline", Pkg: pkg, Exit: run.exit, Last: run.last})
		if run.build {
			return res, fmt.Errorf("the named package %s does not build at the head, so this control cannot be proved either way", pkg)
		}
		if run.red > 0 {
			return res, ErrHeadRed
		}
	}

	if err := revert(ctx, repo, wt, base, others); err != nil {
		return res, err
	}

	var compileHeld, tagHeld bool
	for _, pkg := range pkgs {
		run := runGuardPackage(ctx, wt, pkg)
		if run.err != nil {
			return res, run.err
		}
		res.Tails = append(res.Tails, GuardTail{Which: "control", Pkg: pkg, Exit: run.exit, Last: run.last})
		if run.tags {
			tagHeld = true
			continue
		}
		if run.build {
			compileHeld = true
			continue
		}
		res.Red += run.red
		res.Green += run.green
		res.Reds = append(res.Reds, run.failed...)
	}
	sort.Strings(res.Reds)

	switch {
	case res.Red > 0:
		res.Verdict = VerdictGuarded
	case compileHeld:
		res.Verdict = VerdictCompilerHeld
	case tagHeld && res.Green == 0:
		res.Verdict = VerdictNotApplicable
		res.Reason = "build-tags"
	default:
		res.Verdict = VerdictUnguarded
	}
	return res, nil
}

func packagesOf(changed []change) []string {
	seen := map[string]bool{}
	var pkgs []string
	for _, c := range changed {
		if !strings.HasSuffix(c.path, ".go") {
			continue
		}
		pkg := cleanPkg(path.Dir(c.path))
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		pkgs = append(pkgs, pkg)
	}
	return pkgs
}

// productionAppliesHere is false only when every reverted production path is a
// Go file excluded on this GOOS (name suffix or //go:build). A non-Go path
// keeps the control applicable: reverting value.txt still makes the retained
// test red (Stella HOLD of #2128). A control that cannot compile the file here
// is NOT-APPLICABLE, not UNGUARDED (#2042, 7afd48c0).
func productionAppliesHere(wt string, others []change) bool {
	for _, c := range others {
		if !strings.HasSuffix(c.path, ".go") {
			return true
		}
		if c.status == "D" {
			continue
		}
		if fileAppliesHere(wt, c.path) {
			return true
		}
	}
	return false
}

func fileAppliesHere(wt, rel string) bool {
	dir := filepath.Join(wt, filepath.FromSlash(path.Dir(rel)))
	ok, err := build.Default.MatchFile(dir, path.Base(rel))
	if err != nil {
		return true
	}
	return ok
}

type guardPkgRun struct {
	red, green int
	failed     []string
	last       string
	exit       int
	build      bool
	tags       bool
	err        error
}

func runGuardPackage(ctx context.Context, wt, pkg string) guardPkgRun {
	pkg = cleanPkg(pkg)
	args := []string{"test", "-count=1", "-v", "./" + pkg + "/"}
	if pkg == "." {
		args[len(args)-1] = "./"
	}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = wt
	cmd.Env = goenv.WithoutSecrets(goenv.Clean(os.Environ()))
	out, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return guardPkgRun{err: deadlineErr(pkg)}
	}
	text := string(out)
	run := guardPkgRun{last: lastNonEmpty(text), exit: exitCode(runErr)}
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			return guardPkgRun{err: fmt.Errorf("go test could not be run in %s: %v", pkg, runErr)}
		}
	}
	if strings.Contains(text, "build constraints exclude") {
		run.tags = true
		return run
	}
	if strings.Contains(text, "[build failed]") || strings.Contains(text, "[setup failed]") {
		run.build = true
		return run
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		m := goResult.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		switch m[1] {
		case "FAIL":
			run.red++
			run.failed = append(run.failed, m[2])
		case "PASS":
			run.green++
		}
	}
	if runErr != nil && run.red == 0 && (packageFailed(text) || strings.Contains(text, "panic:")) {
		run.red = 1
		run.failed = append(run.failed, pkg)
	}
	return run
}

func lastNonEmpty(text string) string {
	last := ""
	for _, line := range strings.Split(text, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			last = s
		}
	}
	return last
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}
