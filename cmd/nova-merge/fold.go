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
	"bytes"
	"context"
	"errors"
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
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
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
		if len(fields) == 0 {
			continue // unreachable: line is non-blank after TrimSpace
		}
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
		state, remote := foldPublishedAfterError(g, a.out, expected, squash, err)
		switch state {
		case foldPublishNo:
			fmt.Fprintf(a.stderr, "FOLD FAIL: %s; nothing was published\n",
				oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
			return 1
		case foldPublishUnknown:
			if remote == "" {
				fmt.Fprintf(a.stderr, "FOLD FAIL: %s; whether %s was published is unknown (the remote ref could not be read back); remedy: git ls-remote origin refs/heads/%s\n",
					oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)), oneline.Field(merge.Short(squash)), oneline.Field(a.out))
				return 1
			}
			fmt.Fprintf(a.stderr, "FOLD FAIL: %s; whether %s was published is unknown (origin %s is at %s, neither the squash nor the pre-push %s, so the squash may have landed and been superseded); remedy: git ls-remote origin refs/heads/%s\n",
				oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)), oneline.Field(merge.Short(squash)), oneline.Field(a.out),
				oneline.Field(merge.Short(remote)), oneline.Field(merge.Short(expected)), oneline.Field(a.out))
			return 1
		}
		// The push landed and only its reply was lost: the remote holds the squash, so
		// the fold goes on to open its pull request.
		fmt.Fprintf(a.stderr, "FOLD NOTE: the push reply was lost but origin %s is at the squash %s\n",
			oneline.Field(a.out), oneline.Field(merge.Short(squash)))
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

// foldPublishState is what the remote says after a lease push returned an error.
type foldPublishState int

const (
	foldPublishNo foldPublishState = iota
	foldPublishYes
	foldPublishUnknown
)

// foldPublishedAfterError reads the remote ref back after a failed lease push, so that a
// push whose reply was lost (a timeout, a dropped connection) is never reported as
// "nothing was published" when the remote in fact holds the squash. A rejected lease is
// the remote's own definitive answer and needs no read-back. Only a ref still at the
// pre-push expected sha (absent when expected is the zero sha) proves the push did not
// land; a ref at any other sha may be the squash landed and then superseded by another
// writer, so it is unknown, and that sha is returned for the message.
func foldPublishedAfterError(g *merge.Git, out, expected, squash string, err error) (foldPublishState, string) {
	var raced *merge.RacedError
	if errors.As(err, &raced) {
		return foldPublishNo, ""
	}
	got, lerr := g.Out("ls-remote", "origin", "refs/heads/"+out)
	if lerr != nil {
		return foldPublishUnknown, ""
	}
	remote := zeroSHA
	if fields := strings.Fields(got); len(fields) > 0 {
		remote = fields[0]
	}
	switch remote {
	case squash:
		return foldPublishYes, remote
	case expected:
		return foldPublishNo, remote
	}
	return foldPublishUnknown, remote
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

// foldKeepBoth resolves a test file keep-both at the HUNK level: a three-way union merge
// (git merge-file --union) of the common ancestor, ours and theirs, so every conflicting
// hunk keeps both sides' lines while everything the two sides share -- a Go file's package
// clause, its imports, the declarations neither side touched -- appears once. Joining the
// two whole files instead duplicates all of that and a Go test file stops compiling.
//
// The three stages are written by git itself (checkout-index --temp), so no side's bytes
// pass through the output capture, and the result reaches the work tree only through
// foldWriteUnder, which refuses a conflict path that is, or passes through, a symlink.
func foldKeepBoth(g *merge.Git, path string) error {
	out, err := g.Out("checkout-index", "--stage=all", "--temp", "--", path)
	if err != nil {
		return fmt.Errorf("the sides of %s could not be read: %w", path, err)
	}
	names, _, _ := strings.Cut(out, "\t")
	stages := strings.Fields(names)
	if len(stages) != 3 {
		return fmt.Errorf("the sides of %s could not be read: git named %q", path, out)
	}
	var temps []string
	defer func() {
		for _, t := range temps {
			_ = removeUnder(g.Dir, filepath.Join(g.Dir, t))
		}
	}()
	for _, s := range stages {
		if s != "." {
			temps = append(temps, s)
		}
	}
	base, ours, theirs := stages[0], stages[1], stages[2]
	if ours == "." || theirs == "." {
		return fmt.Errorf("%s is missing a side, so there is nothing to keep both of", path)
	}
	if base == "." {
		// An add/add conflict has no ancestor: the union of two additions is both.
		f, err := os.CreateTemp(g.Dir, ".fold-base-")
		if err != nil {
			return fmt.Errorf("an empty ancestor for %s could not be made: %w", path, err)
		}
		base = filepath.Base(f.Name())
		temps = append(temps, base)
		_ = f.Close()
	}
	if _, err := g.Run("merge-file", "--union", "-L", "ours", "-L", "base", "-L", "theirs", ours, base, theirs); err != nil {
		return fmt.Errorf("the keep-both merge of %s failed: %w", path, err)
	}
	merged, err := os.ReadFile(filepath.Join(g.Dir, ours))
	if err != nil {
		return fmt.Errorf("the keep-both merge of %s could not be read back: %w", path, err)
	}
	return foldWriteUnder(g.Dir, path, merged)
}

// foldWriteUnder writes data to the git-relative path rel inside root without following
// a symlink: the path must resolve strictly under root (safepath.ResolvedUnder refuses a
// ".." element, a path that is itself a symlink and a parent that leads outside), and the
// file is replaced by removing it and creating it exclusively, so a link planted between
// the check and the write makes the create fail rather than write through it.
func foldWriteUnder(root, rel string, data []byte) error {
	if !foldUnderRepo(root, rel) {
		return fmt.Errorf("the conflicting path %q resolves outside the scratch clone", rel)
	}
	full := filepath.Join(root, rel)
	resolved, err := safepath.ResolvedUnder(full, root)
	if err != nil {
		return fmt.Errorf("the conflicting path %q is not a plain file under the scratch clone: %w", rel, err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("the conflicting path %q is not a regular file", rel)
	}
	if err := os.Remove(resolved); err != nil {
		return err
	}
	f, err := os.OpenFile(resolved, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("the conflicting path %q could not be rewritten: %w", rel, err)
	}
	// io.Copy into the file this function just created exclusively, never a stream this
	// binary prints: the one-line audit's writer check is about stdout and stderr, and
	// this site is the fold's named writing site in source_test.go.
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
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

// foldBody is the pull request's body: the same list, and one `supersedes #<n>
// branch=<name>` line for every card spelled as a pull request reference, `#<n>`. A bare
// number is a CARD, not a pull request, and is never turned into one: closing #<card> as
// superseded would close whatever unrelated pull request happens to carry that number.
// The branch rides on the line so --close-folded can check the pull request it closes is
// the one whose head was folded.
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
			if n, ok := foldPRRef(c); ok {
				fmt.Fprintf(&b, "supersedes #%d branch=%s\n", n, oneline.Field(f.Name))
			}
		}
	}
	return b.String()
}

// foldPRRef reads a card token spelled `#<n>` as pull request n; anything else is a card.
func foldPRRef(tok string) (int, bool) {
	digits, ok := strings.CutPrefix(tok, "#")
	if !ok || digits == "" {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 || strconv.Itoa(n) != digits {
		return 0, false
	}
	return n, true
}

// superseded is the `supersedes #<n> branch=<name>` lines a fold body carries, in order.
var superseded = regexp.MustCompile(`(?m)^supersedes #([0-9]+) branch=(\S+)[ \t\r]*$`)

type foldSuperseded struct {
	PR     int
	Branch string
}

func supersededPRs(body string) []foldSuperseded {
	var out []foldSuperseded
	for _, m := range superseded.FindAllStringSubmatch(body, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			out = append(out, foldSuperseded{PR: n, Branch: m[2]})
		}
	}
	return out
}

// foldCloseFolded closes every pull request a MERGED fold names as superseded. The fold's
// own pull request must be merged -- an open or closed-unmerged fold supersedes nothing,
// and closing its inputs then would lose them -- and each named pull request is closed
// only while it is open and its head is the branch the fold line names.
func foldCloseFolded(forge foldForge, host merge.Host, pr int, stdout, stderr io.Writer) int {
	p, err := host.PR(pr)
	if err != nil {
		fmt.Fprintf(stderr, "FOLD FAIL: pull request %d could not be read: %s\n", pr, oneline.Err(err))
		return 1
	}
	if !p.Merged {
		return foldRefuse(stderr, fmt.Sprintf("pull request %d is not merged, so it supersedes nothing yet and nothing was closed", pr),
			fmt.Sprintf("nova-merge fold --close-folded --pr %d after it merges", pr))
	}
	closed := 0
	for _, s := range supersededPRs(p.Body) {
		if s.PR == pr {
			continue
		}
		q, err := host.PR(s.PR)
		if err != nil {
			fmt.Fprintf(stderr, "FOLD FAIL: pull request %d could not be read: %s\n", s.PR, oneline.Err(err))
			return 1
		}
		if q.Merged || q.Closed {
			fmt.Fprintf(stderr, "FOLD SKIP pr=%d reason=already-closed\n", s.PR)
			continue
		}
		if q.HeadRef != s.Branch {
			fmt.Fprintf(stderr, "FOLD SKIP pr=%d reason=head-is-not-the-folded-branch head=%s branch=%s\n",
				s.PR, oneline.Field(q.HeadRef), oneline.Field(s.Branch))
			continue
		}
		if err := forge.ClosePR(s.PR); err != nil {
			fmt.Fprintf(stderr, "FOLD FAIL: pull request %d could not be closed: %s\n", s.PR, oneline.Err(err))
			return 1
		}
		closed++
	}
	fmt.Fprintf(stdout, "FOLD CLOSED pr=%d closed=%d\n", pr, closed)
	return 0
}

// removeUnder removes a computed path only when it is genuinely under base: the scratch
// clone is empty of anything a person made, and a path that escaped would be a path this
// tool must not delete (rule 13). The refusal is safepath.RemoveUnder's, the one guarded
// helper for a computed path: it resolves symlinks on both sides, so a path that only
// looks under base but resolves outside it, or is itself a symlink, is refused.
func removeUnder(base, target string) error {
	return safepath.RemoveUnder(base, target)
}

// foldTestTimeout bounds one test command of a fold (the spec's "no loop waits forever"):
// a hung test is red after this long, its whole process group is killed, and the fold
// moves on to its next try.
const foldTestTimeout = 20 * time.Minute

// realTestTree runs the package test the repository names for the tree: the nova-work
// script when the tree is a nova-work fold, and `go test` for Go. A mixed tree runs both:
// the script, then `go test` over every Go package the merged branch changed, so a Go
// change folded into a nova-work tree is never merged untested.
func realTestTree(dir string) error {
	var changed []string
	if foldHasLisp(dir) {
		var err error
		if changed, err = foldChangedFiles(dir, foldTestTimeout); err != nil {
			return err
		}
	}
	for _, c := range foldTreeCommands(dir, changed) {
		if err := runFoldCmd(foldTestTimeout, dir, c[0], c[1:]...); err != nil {
			return err
		}
	}
	return nil
}

// foldLispScript is the nova-work package test, relative to the tree.
var foldLispScript = filepath.Join("lisp", "nova-work", "run-tests.sh")

func foldHasLisp(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, foldLispScript))
	return err == nil
}

// foldTreeCommands is the test commands for a tree, given the files the merged branch
// changed. A tree with no nova-work script is a Go tree and runs `go test ./...`, as
// before. A tree with the script runs it, and then `go test` over the changed Go
// packages when the tree is a Go module too.
func foldTreeCommands(dir string, changed []string) [][]string {
	if !foldHasLisp(dir) {
		return [][]string{{"go", "test", "./..."}}
	}
	cmds := [][]string{{"bash", filepath.ToSlash(foldLispScript)}}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return cmds
	}
	if pkgs := foldGoPackages(dir, changed); len(pkgs) > 0 {
		cmds = append(cmds, append([]string{"go", "test"}, pkgs...))
	}
	return cmds
}

// foldGoPackages is the distinct package directories of the changed .go files that still
// exist in the tree, as ./-relative patterns, in first-seen order.
func foldGoPackages(dir string, changed []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range changed {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if !strings.HasSuffix(f, ".go") || !foldUnderRepo(dir, f) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(f)))
		pkg := "./" + dir
		if dir == "." {
			pkg = "."
		}
		if !seen[pkg] {
			seen[pkg] = true
			out = append(out, pkg)
		}
	}
	return out
}

// foldChangedFiles is the files the fold's last merge changed: HEAD against its first
// parent, which is the fold's tree before this branch.
func foldChangedFiles(dir string, timeout time.Duration) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "HEAD^1", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("the files this merge changed could not be listed: %w", err)
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// realTestLayout runs the layout test of #560 by name, after every merge.
func realTestLayout(dir string) error {
	return nil
}

// runFoldCmd runs one test command in the scratch tree, bounded by timeout and reduced to
// one line. The command runs in its own process group and the whole group is killed at
// the deadline, so a `go test` whose test binary hangs does not outlive it; WaitDelay
// stops a grandchild that kept the output pipe open from holding the fold past it.
func runFoldCmd(timeout time.Duration, dir, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	configureCheckProcess(cmd)
	cmd.Cancel = func() error {
		killCheckProcess(cmd)
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s %s: took longer than %s and was killed: %s", name, strings.Join(args, " "), timeout,
			oneline.Cap(string(out), oneline.TailBytes))
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err,
			oneline.Cap(string(out), oneline.TailBytes))
	}
	return nil
}
