package main

// The fold (docs/SPEC-MERGE.md "#1142"): an ordered list of branches folded onto one base
// and landed as ONE squashed pull request. It merges the branches in the file's order in
// a scratch clone of the lane's repository, resolves the two conflict classes it is told
// to (keep-both in a test file, the incoming side in a source file) and drops a branch
// whose conflict is anywhere else; after every merge it runs the repository's package
// test and the layout test of #560; a branch still red after three tries is dropped,
// named and left out of the squash. The exhausted list squashes to one commit whose
// message lists every folded branch and its cards, and that one object is pushed with the
// one lease rule 4 allows and opened as one pull request.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// foldForge is the fold's narrow view of the forge: opening the one pull request and
// closing a folded pull request as superseded. It is separate from merge.Host so the
// merge path has no way to open or close a pull request; a Host that does not offer it is
// refused rather than silently skipping the pull request.
type foldForge interface {
	CreatePR(head, base, title, body string) (int, error)
	ClosePR(n int) error
}

// foldBranch is one line of the --branches file: a branch and the cards it folds.
type foldBranch struct {
	Name  string
	Cards []string
}

// foldWorkDir is the scratch clone, under the lane, so no path is guessed (rule 13).
const foldWorkDir = "fold-scratch"

// zeroSHA is the all-zero commit a force-with-lease names when the out branch does not
// exist yet: "the ref must not exist", in the one spelling rule 4 allows.
const zeroSHA = "0000000000000000000000000000000000000000"

func cmdFold(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("fold")
	branches := f.fs.String("branches", "", "")
	onto := f.fs.String("onto", "", "")
	out := f.fs.String("out", "", "")
	closeFold := f.fs.Bool("close-folded", false, "")
	pr := f.fs.Int("pr", 0, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	if *closeFold {
		if *pr < 1 {
			return foldRefuse(stderr, "--close-folded needs --pr <n>, the merged fold's pull request",
				"nova-merge fold --close-folded --pr <n>")
		}
	} else {
		if strings.TrimSpace(*branches) == "" {
			return foldRefuse(stderr, "--branches is required and names the ordered branch list; refusing to guess",
				"nova-merge fold --branches <file> --onto <base> --out <branch>")
		}
		if strings.TrimSpace(*onto) == "" {
			return foldRefuse(stderr, "--onto is required and names the base the fold lands on; refusing to guess", "--onto <base>")
		}
		if strings.TrimSpace(*out) == "" {
			return foldRefuse(stderr, "--out is required and names the branch the squash lands on; refusing to guess", "--out <branch>")
		}
	}
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("fold", *f.lane, stderr)
	if st == nil {
		return code
	}
	host := deps.NewHost(st.Repo, f.dur())
	forge, ok := host.(foldForge)
	if !ok {
		fmt.Fprintf(stderr, "FOLD REFUSED: this host offers no way to open or close a pull request; remedy: run fold against a host that does\n")
		return 2
	}
	if *closeFold {
		return foldCloseFolded(forge, host, *pr, stdout, stderr)
	}
	// A lane whose state.json was lost is not a lane; openLane has already refused that.
	// --lane is required by laneFlags.check(), and rule 20's init remedy is openLane's.
	if err := merge.ValidRefName(*onto); err != nil {
		return foldRefuse(stderr, "--onto: "+err.Error(), "--onto <base>")
	}
	if err := merge.ValidRefName(*out); err != nil {
		return foldRefuse(stderr, "--out: "+err.Error(), "--out <branch>")
	}
	plan, err := readFoldBranches(*branches)
	if err != nil {
		return foldRefuse(stderr, err.Error(), "nova-merge fold --branches <file> --onto <base> --out <branch>")
	}
	if len(plan) == 0 {
		return foldRefuse(stderr, "--branches "+oneline.Field(*branches)+" names no branch; a fold of nothing is not a fold",
			"nova-merge fold --branches <file> --onto <base> --out <branch>")
	}
	return foldRun(foldRunArgs{
		lane: *f.lane, onto: *onto, out: *out, plan: plan,
		st: st, stdout: stdout, stderr: stderr, deps: deps, forge: forge, timeout: f.dur(),
	})
}

// foldRefuse is one line and one remedy, exit 2.
func foldRefuse(stderr io.Writer, why, remedy string) int {
	fmt.Fprintf(stderr, "FOLD REFUSED: %s; remedy: %s\n",
		oneline.Escape(oneline.Cap(why, oneline.TailBytes)), oneline.Escape(remedy))
	return 2
}

// readFoldBranches reads one branch per line, each line carrying the cards that branch
// folds: ` <branch> <card>...`. A blank line, or one whose first token begins with '#',
// is a comment. An unreadable or empty file is refused by the caller.
func readFoldBranches(path string) ([]foldBranch, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--branches %s could not be read: %w", oneline.Field(path), err)
	}
	var out []foldBranch
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		b := foldBranch{Name: fields[0]}
		b.Cards = append(b.Cards, fields[1:]...)
		out = append(out, b)
	}
	return out, nil
}

// foldRunArgs is the whole input of one fold, so the run is a function of named values.
type foldRunArgs struct {
	lane    string
	onto    string
	out     string
	plan    []foldBranch
	st      *merge.State
	stdout  io.Writer
	stderr  io.Writer
	deps    Deps
	forge   foldForge
	timeout time.Duration
}

func foldRun(a foldRunArgs) int {
	scratch := filepath.Join(a.lane, foldWorkDir)
	if err := removeUnder(a.lane, scratch); err != nil {
		fmt.Fprintf(a.stderr, "FOLD REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	laneGit := merge.NewGit(filepath.Join(a.lane, merge.RepoDir), a.timeout, a.deps.Runner)
	remote, err := laneGit.Out("remote", "get-url", "origin")
	if err != nil {
		fmt.Fprintf(a.stderr, "FOLD REFUSED: the lane's repository names no origin to fold from: %s\n", oneline.Err(err))
		return 2
	}
	if _, err := merge.NewGit(a.lane, a.timeout, a.deps.Runner).Run("clone", "-q", "--", remote, scratch); err != nil {
		fmt.Fprintf(a.stderr, "FOLD REFUSED: the scratch clone could not be made: %s\n", oneline.Err(err))
		return 2
	}
	g := merge.NewGit(scratch, a.timeout, a.deps.Runner)
	if _, err := g.Run("fetch", "-q", "origin", "--prune"); err != nil {
		fmt.Fprintf(a.stderr, "FOLD REFUSED: the scratch clone could not fetch the branches to fold: %s\n", oneline.Err(err))
		return 2
	}
	baseSHA, err := g.Out("rev-parse", "origin/"+a.onto)
	if err != nil {
		fmt.Fprintf(a.stderr, "FOLD REFUSED: --onto %s is not a branch this repository holds: %s\n", oneline.Field(a.onto), oneline.Err(err))
		return 2
	}
	expected := zeroSHA
	if sha, err := g.Out("rev-parse", "--verify", "--quiet", "origin/"+a.out); err == nil && merge.IsSHA(sha) {
		expected = sha
	}
	if _, err := g.Run("checkout", "--detach", baseSHA); err != nil {
		fmt.Fprintf(a.stderr, "FOLD REFUSED: the scratch clone could not check out %s: %s\n", oneline.Field(merge.Short(baseSHA)), oneline.Err(err))
		return 2
	}

	var folded, dropped []foldBranch
	work := baseSHA
	for _, b := range a.plan {
		sha, err := g.Out("rev-parse", "origin/"+b.Name)
		if err != nil {
			fmt.Fprintf(a.stderr, "FOLD DROP branch=%s reason=not-at-origin\n", oneline.Field(b.Name))
			dropped = append(dropped, b)
			continue
		}
		detail := ""
		accepted := false
		for attempt := 1; attempt <= 3; attempt++ {
			if _, err := g.Run("reset", "--hard", work); err != nil {
				fmt.Fprintf(a.stderr, "FOLD REFUSED: the scratch clone could not reset to %s: %s\n", oneline.Field(merge.Short(work)), oneline.Err(err))
				return 2
			}
			if _, err := g.Run(merge.Identity("merge", "--no-ff", "-m", "fold: "+b.Name, sha)...); err != nil {
				files, listErr := merge.ConflictFiles(g)
				if listErr != nil || len(files) == 0 {
					_ = merge.AbortMerge(g)
					detail = "reason=merge-failed"
					break
				}
				if dropFile, ok := foldUnresolvable(files); !ok {
					_ = merge.AbortMerge(g)
					detail = "file=" + oneline.Field(dropFile) + " reason=conflict-outside-test-and-source"
					break
				}
				if err := foldResolveConflicts(g, files); err != nil {
					_ = merge.AbortMerge(g)
					detail = "reason=" + oneline.Err(err)
					break
				}
				if _, err := g.Run(merge.Identity("commit", "--no-edit")...); err != nil {
					detail = "reason=resolved-merge-would-not-commit"
					break
				}
			}
			if err := runFoldTests(scratch, a.deps); err != nil {
				if attempt < 3 {
					a.deps.Sleep(time.Duration(attempt) * time.Second)
					continue
				}
				if _, rerr := g.Run("reset", "--hard", work); rerr != nil {
					fmt.Fprintf(a.stderr, "FOLD REFUSED: the scratch clone could not reset to %s: %s\n", oneline.Field(merge.Short(work)), oneline.Err(rerr))
					return 2
				}
				detail = "reason=red-after-three-tries: " + oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes))
				break
			}
			head, herr := g.Out("rev-parse", "HEAD")
			if herr != nil {
				fmt.Fprintf(a.stderr, "FOLD REFUSED: the scratch clone names no HEAD: %s\n", oneline.Err(herr))
				return 2
			}
			work = head
			accepted = true
			break
		}
		if accepted {
			folded = append(folded, b)
			continue
		}
		fmt.Fprintf(a.stderr, "FOLD DROP branch=%s %s\n", oneline.Field(b.Name), oneline.Escape(detail))
		dropped = append(dropped, b)
	}

	total := len(folded)
	if total == 0 {
		fmt.Fprintf(a.stdout, "FOLD OK folded=0 dropped=%d pr=0\n", len(dropped))
		return 0
	}
	if _, err := g.Run("reset", "--soft", baseSHA); err != nil {
		fmt.Fprintf(a.stderr, "FOLD REFUSED: the squash could not be staged: %s\n", oneline.Err(err))
		return 2
	}
	if _, err := g.Run(merge.Identity("commit", "-m", foldMessage(folded))...); err != nil {
		fmt.Fprintf(a.stderr, "FOLD REFUSED: the squash could not be committed: %s\n", oneline.Err(err))
		return 2
	}
	squash, err := g.Out("rev-parse", "HEAD")
	if err != nil || !merge.IsSHA(squash) {
		fmt.Fprintf(a.stderr, "FOLD REFUSED: the squash names no sha: %s\n", oneline.Err(err))
		return 2
	}
	if _, err := g.Publish("origin", a.out, expected, squash); err != nil {
		fmt.Fprintf(a.stderr, "FOLD FAIL: %s; nothing was published\n",
			oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 1
	}
	pr, err := a.forge.CreatePR(a.out, a.onto, "nova-merge fold: "+a.out+" onto "+a.onto, foldBody(folded, a.onto, a.out))
	if err != nil {
		fmt.Fprintf(a.stderr, "FOLD FAIL: the squash is at %s and the pull request could not be opened: %s\n",
			oneline.Field(merge.Short(squash)), oneline.Err(err))
		return 1
	}
	fmt.Fprintf(a.stdout, "FOLD OK folded=%d dropped=%d pr=%d\n", total, len(dropped), pr)
	return 0
}

// foldUnresolvable returns the first file whose conflict is neither in a test file nor in
// a source file: the classes this tool is allowed to resolve by naming the file. The
// second result is false when that file exists, and then the branch is dropped.
func foldUnresolvable(files []string) (string, bool) {
	for _, p := range files {
		if foldClass(p) == "other" {
			return p, false
		}
	}
	return "", true
}

// foldClass is the conflict class of one path, from its name alone, so that a resolution
// is a rule and not a judgment call: a test file is one whose name carries test, a source
// file is one of the extensions below, and anything else is dropped rather than guessed.
func foldClass(path string) string {
	lower := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	if strings.Contains(base, "test") || strings.Contains(lower, "/tests/") {
		return "test"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".lisp", ".c", ".h", ".cc", ".cpp", ".java", ".py", ".rs", ".js", ".ts", ".rb", ".sh":
		return "source"
	}
	return "other"
}

// foldResolveConflicts resolves every unmerged path: keep-both for a test file, the
// incoming side for a source file. Nothing here reaches a class foldUnresolvable refused.
func foldResolveConflicts(g *merge.Git, files []string) error {
	for _, p := range files {
		if !foldUnderRepo(g.Dir, p) {
			return fmt.Errorf("a conflicting path %q resolves outside the scratch clone", p)
		}
		if foldClass(p) == "test" {
			if err := foldKeepBoth(g, p); err != nil {
				return err
			}
		} else if _, err := g.Run("checkout", "--theirs", "--", p); err != nil {
			return err
		}
		if _, err := g.Run("add", "--", p); err != nil {
			return err
		}
	}
	return nil
}

// foldKeepBoth writes ours then theirs, so a test file keeps every slice both sides
// carried and no line is lost.
func foldKeepBoth(g *merge.Git, path string) error {
	ours, err := g.Run("show", ":2:"+path)
	if err != nil {
		return fmt.Errorf("the base side of %s could not be read: %w", path, err)
	}
	theirs, err := g.Run("show", ":3:"+path)
	if err != nil {
		return fmt.Errorf("the incoming side of %s could not be read: %w", path, err)
	}
	return os.WriteFile(filepath.Join(g.Dir, path), []byte(ours+theirs), 0o644)
}

// foldUnderRepo reports whether a git-relative path stays inside the work tree.
func foldUnderRepo(root, rel string) bool {
	if filepath.IsAbs(rel) {
		return false
	}
	full := filepath.Join(root, rel)
	r, err := filepath.Rel(root, full)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(os.PathSeparator))
}

// runFoldTests is the package test the repository names for the tree, then the layout
// test of #560. Either red is red.
func runFoldTests(dir string, deps Deps) error {
	if deps.TestTree != nil {
		if err := deps.TestTree(dir); err != nil {
			return err
		}
	}
	if deps.TestLayout != nil {
		if err := deps.TestLayout(dir); err != nil {
			return err
		}
	}
	return nil
}

// foldMessage is the squash's subject and body: every folded branch and its cards, in the
// file's order, so a person reading the base's history reads what was folded.
func foldMessage(folded []foldBranch) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nova-merge fold\n\n")
	for _, f := range folded {
		fmt.Fprintf(&b, "%s", oneline.Field(f.Name))
		for _, c := range f.Cards {
			fmt.Fprintf(&b, " %s", oneline.Field(c))
		}
		fmt.Fprintf(&b, "\n")
	}
	return b.String()
}

// foldBody is the pull request's body: the same list, and one `supersedes #<n>` line for
// every numeric card, which is what --close-folded reads back to close the folded PRs.
func foldBody(folded []foldBranch, onto, out string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nova-merge fold of %s onto %s\n\n", oneline.Field(out), oneline.Field(onto))
	for _, f := range folded {
		fmt.Fprintf(&b, "%s", oneline.Field(f.Name))
		for _, c := range f.Cards {
			fmt.Fprintf(&b, " %s", oneline.Field(c))
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "\n")
	for _, f := range folded {
		for _, c := range f.Cards {
			if _, err := strconv.Atoi(c); err == nil {
				fmt.Fprintf(&b, "supersedes #%s\n", oneline.Field(c))
			}
		}
	}
	return b.String()
}

// superseded is the `supersedes #<n>` lines a fold body carries, in order.
var superseded = regexp.MustCompile(`(?m)supersedes #([0-9]+)`)

func supersededPRs(body string) []int {
	var out []int
	for _, m := range superseded.FindAllStringSubmatch(body, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// foldCloseFolded closes every pull request a merged fold names as superseded.
func foldCloseFolded(forge foldForge, host merge.Host, pr int, stdout, stderr io.Writer) int {
	p, err := host.PR(pr)
	if err != nil {
		fmt.Fprintf(stderr, "FOLD FAIL: pull request %d could not be read: %s\n", pr, oneline.Err(err))
		return 1
	}
	closed := 0
	for _, n := range supersededPRs(p.Body) {
		if n == pr {
			continue
		}
		if err := forge.ClosePR(n); err != nil {
			fmt.Fprintf(stderr, "FOLD FAIL: pull request %d could not be closed: %s\n", n, oneline.Err(err))
			return 1
		}
		closed++
	}
	fmt.Fprintf(stdout, "FOLD CLOSED pr=%d closed=%d\n", pr, closed)
	return 0
}

// removeUnder removes a computed path only when it is genuinely under base: the scratch
// clone is empty of anything a person made, and a path that escaped would be a path this
// tool must not delete (rule 13).
func removeUnder(base, target string) error {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to remove %s: it is not under %s", oneline.Field(target), oneline.Field(base))
	}
	return os.RemoveAll(target)
}

// realTestTree runs the package test the repository names for the tree: the nova-work
// script when the tree is a nova-work fold, and `go test` for Go.
func realTestTree(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "lisp", "nova-work", "run-tests.sh")); err == nil {
		return runFoldCmd(dir, "bash", "lisp/nova-work/run-tests.sh")
	}
	return runFoldCmd(dir, "go", "test", "./...")
}

// realTestLayout runs the layout test of #560 by name, after every merge.
func realTestLayout(dir string) error {
	return runFoldCmd(dir, "go", "test", "./internal/pulse/", "-run", "TestOneFilePerSliceAndSection560", "-count=1")
}

// runFoldCmd runs one test command in the scratch tree, bounded and reduced to one line.
func runFoldCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err,
			oneline.Cap(string(out), oneline.TailBytes))
	}
	return nil
}
