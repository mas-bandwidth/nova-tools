package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// THE LANDING GATE, AS A VERB.
//
// Glenn, 2026-09-18: landing is by integration batches only, and "we need to stop using
// shell scripts for things". The gate ran that morning on hulk as a shell script --
// clone, merge each pull request head onto the current base IN ORDER, drop the ones that
// will not merge, then build, vet, test and run the lisp suite over the result -- and it
// found five poison pull requests in three minutes each time it ran. This is that script
// with the shell taken out, and with the three things a shell loop cannot do: a member
// that conflicts is dropped BY NAME rather than silently skipped, every step says what it
// is doing with the elapsed time while it runs, and the verdict is one line a caller can
// parse.
//
// IT PUSHES NOTHING AND IT OPENS NOTHING. The batch ends as a branch in a clone under
// --root and a line naming that branch's head; pushing it and opening the pull request is
// the caller's, who is the one who knows whether the batch is the one they wanted. There
// is no path to a push in this file, and the tests assert the remote saw none.
//
// It reuses `simulate`'s machinery rather than repeating it: hasConflicts, firstLine and
// runCheck are simulate's, and the difference between the two verbs is what they are for
// -- simulate PREDICTS which queued entry will turn the base red, and batch BUILDS the
// integration branch that lands.

// batchBase is the branch integration batches land on in this repository (Glenn,
// 2026-09-16: all merges into dev, main is fast-forwarded only when dev is green). It is
// the one default here, and --base overrides it.
const batchBase = "dev"

// batchTimeout is the per-step deadline. It is a duration rather than a count of seconds
// because the steps it bounds are a whole test suite rather than one git call, and 30
// minutes is longer than the gate has ever taken on any bench in the fleet.
const batchTimeout = "30m"

// batchStep is one step of the gate: the word its progress line carries, the command it
// runs in the merged tree, the program that must be on PATH for it to mean anything, and
// the file of the checkout it runs.
type batchStep struct {
	name    string
	command string
	needs   string
	file    string
}

// batchGate is the suite, in order, exactly as the shell script ran it. A step whose
// program or file is missing is SKIPPED OUT LOUD: a gate that quietly ran three of its
// four steps and printed OK is a gate that says green about a thing it did not check.
var batchGate = []batchStep{
	{name: "build", command: "go build ./...", needs: "go"},
	{name: "vet", command: "go vet ./...", needs: "go"},
	{name: "test", command: "go test ./...", needs: "go"},
	{name: "lisp", command: "sh tools/ci/lisp-test.sh", needs: "sbcl", file: "tools/ci/lisp-test.sh"},
}

// batchTempVars are the variables a child reads to find its temp directory, and this ONE
// LINE is the only place in this binary that names any of them. It is a SET and never a
// read: the gate hands every check a temp directory inside the batch's own working
// directory, which is rule 13 carried into the subprocesses. A `go test` or an sbcl suite
// that keys off the ambient one writes into /tmp instead, and two gates on one host
// wrecked each other's state that way -- "held by another process", "destination exists"
// -- on 2026-09-18 (tools/ci/lisp-test.sh).
var batchTempVars = []string{"TMPDIR", "GOTMPDIR", "LISP_TEST_TMPROOT", "TMP", "TEMP"}

// privateTempEnv is env with every temp-directory variable pointed at tmp, each named
// exactly once, so a child cannot inherit two answers and pick the other one.
func privateTempEnv(env []string, tmp string) []string {
	drop := map[string]bool{}
	for _, name := range batchTempVars {
		drop[name] = true
	}
	out := make([]string, 0, len(env)+len(batchTempVars))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if drop[key] {
			continue
		}
		out = append(out, kv)
	}
	for _, name := range batchTempVars {
		out = append(out, name+"="+tmp)
	}
	return out
}

// cmdBatch runs one integration batch and says whether it may land.
func cmdBatch(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("batch")
	name := f.fs.String("name", "", "")
	prRaw := f.fs.String("pr", "", "")
	base := f.fs.String("base", batchBase, "")
	root := f.fs.String("root", "", "")
	repo := f.fs.String("repo", "", "")
	reference := f.fs.String("reference", "", "")
	timeoutRaw := f.fs.String("timeout", batchTimeout, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("name", *name, "the batch's own name: its directory under --root, and the branch rowan/<name> it builds")
	f.require("pr", *prRaw, "the pull request numbers to land, in the order they land, like 1301,1302,1307")
	f.require("root", *root, "the directory this batch clones and builds under, which it rebuilds on every run")
	f.require("repo", *repo, "the <owner>/<name> whose pull request heads are merged, like mas-bandwidth/nova-tools")
	// --name is one path element and half a ref name, so it is held to both: a name that
	// could climb out of --root is a name that could remove a directory nobody named, and
	// a name git could read as an option is lesson 48.
	if s := strings.TrimSpace(*name); s != "" && !safepath.NameOK(s) {
		f.problem(fmt.Sprintf("--name is one path element of letters, digits, dot, dash and underscore, and never begins with a dash, got %q", *name))
	} else if s != "" {
		if err := merge.ValidRefName("rowan/" + s); err != nil {
			f.problem(fmt.Sprintf("--name: %s", oneline.Escape(err.Error())))
		}
	}
	if s := strings.TrimSpace(*base); s != "" {
		if err := merge.ValidRefName(s); err != nil {
			f.problem(fmt.Sprintf("--base: %s", oneline.Escape(err.Error())))
		}
	}
	prs, perr := parsePRList(*prRaw)
	if perr != nil {
		f.problem(oneline.Escape(perr.Error()))
	}
	timeout, terr := time.ParseDuration(*timeoutRaw)
	if terr != nil || timeout <= 0 {
		f.problem(fmt.Sprintf("--timeout is a duration per step like 30m, got %q", *timeoutRaw))
	}
	if !f.done(stderr) {
		return 2
	}
	return runBatch(batchRun{
		name:      *name,
		base:      *base,
		root:      *root,
		repo:      *repo,
		reference: *reference,
		prs:       prs,
		timeout:   timeout,
	}, stdout, stderr, deps)
}

// batchRun is one batch's whole invocation, checked, so the run below reads as the steps
// it performs rather than as a second pass over the flags.
type batchRun struct {
	name      string
	base      string
	root      string
	repo      string
	reference string
	prs       []int
	timeout   time.Duration
}

func runBatch(in batchRun, stdout, stderr io.Writer, deps Deps) int {
	start := time.Now()
	rootAbs, err := filepath.Abs(in.root)
	if err != nil {
		return batchRefused(stderr, err)
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return batchRefused(stderr, err)
	}
	// THE WORKING DIRECTORY IS REBUILT EVERY RUN, so a batch never merges on top of a
	// tree an earlier one left half-merged. It is a path this tool COMPUTED, so its
	// removal is safepath's and nobody else's: Glenn, 2026-09-17, "it is just one mistake
	// away from deleting the whole disk".
	work := filepath.Join(rootAbs, in.name)
	if err := safepath.RemoveUnder(rootAbs, work); err != nil {
		return batchRefused(stderr, err)
	}
	tmp := filepath.Join(work, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return batchRefused(stderr, err)
	}
	clone := filepath.Join(work, "repo")
	cloneArgs := []string{"clone", "--quiet"}
	if strings.TrimSpace(in.reference) != "" {
		// A mirror on this bench makes the clone local rather than a download. It is an
		// optimisation and never a requirement: without it the clone is an ordinary one.
		cloneArgs = append(cloneArgs, "--reference", in.reference)
	}
	// `--` before the URL, so a URL beginning with a dash is a URL and not an option.
	cloneArgs = append(cloneArgs, "--", deps.RepoURL(in.repo), clone)
	if _, err := merge.NewGit(work, in.timeout, deps.Runner).Run(cloneArgs...); err != nil {
		return batchRefused(stderr, err)
	}
	g := merge.NewGit(clone, in.timeout, deps.Runner)
	if _, err := g.Run("fetch", "--quiet", "origin", in.base); err != nil {
		return batchRefused(stderr, fmt.Errorf("could not fetch origin/%s: %w", in.base, err))
	}
	branch := "rowan/" + in.name
	if _, err := g.Run("checkout", "--quiet", "-B", branch, "FETCH_HEAD"); err != nil {
		return batchRefused(stderr, fmt.Errorf("could not start %s at origin/%s: %w", branch, in.base, err))
	}
	baseSHA, err := g.Out("rev-parse", "HEAD")
	if err != nil {
		return batchRefused(stderr, err)
	}
	fmt.Fprintf(stderr, "BATCH START name=%s base=%s prs=%d t=%.1fs\n",
		oneline.Field(in.name), oneline.Field(baseSHA), len(in.prs), since(start))

	members, dropped, code := mergeMembers(g, in, stderr, start)
	if code != 0 {
		return code
	}
	headSHA, err := g.Out("rev-parse", "HEAD")
	if err != nil {
		return batchRefused(stderr, err)
	}
	line := fmt.Sprintf("name=%s base=%s head=%s members=%s dropped=%s",
		oneline.Field(in.name), oneline.Field(baseSHA), oneline.Field(headSHA),
		oneline.Field(numberList(members)), oneline.Field(numberList(dropped)))

	env := privateTempEnv(os.Environ(), tmp)
	for _, step := range batchGate {
		if why := stepUnavailable(step, clone); why != "" {
			fmt.Fprintf(stderr, "BATCH SKIP %s reason=%q t=%.1fs\n", oneline.Field(step.name), why, since(start))
			continue
		}
		fmt.Fprintf(stderr, "BATCH STEP %s command=%q t=%.1fs\n", oneline.Field(step.name), step.command, since(start))
		out, err := runCheck(clone, step.command, in.timeout, env)
		if err == nil {
			continue
		}
		pkgs, tests := failuresIn(out)
		fmt.Fprintf(stdout, "BATCH FAIL %s step=%s packages=%s tests=%s reason=%q\n",
			line, oneline.Field(step.name), oneline.Field(numberOrNone(pkgs)), oneline.Field(numberOrNone(tests)),
			oneline.Cap(firstLine(out, err), oneline.TailBytes))
		return 1
	}
	fmt.Fprintf(stdout, "BATCH OK %s\n", line)
	return 0
}

// mergeMembers merges every pull request head onto the branch IN THE ORDER GIVEN, which
// is the order they will land. A head that will not merge is dropped and said out loud,
// and the members after it are still judged -- on the tree without it, which is the tree
// that would land.
func mergeMembers(g *merge.Git, in batchRun, stderr io.Writer, start time.Time) (members, dropped []int, code int) {
	for _, n := range in.prs {
		if _, err := g.Run("fetch", "--quiet", "origin", "pull/"+strconv.Itoa(n)+"/head"); err != nil {
			return nil, nil, batchRefused(stderr, fmt.Errorf("could not fetch pull/%d/head: %w", n, err))
		}
		// The merge writes a commit object, so it carries nova-merge's own identity: a CI
		// runner has no git identity anywhere and `git merge --no-ff` there dies with
		// "Committer identity unknown" (the Ubuntu leg of #57).
		message := fmt.Sprintf("merge pull request #%d into %s", n, in.name)
		_, err := g.Run(merge.Identity("merge", "--no-ff", "--no-edit", "-m", message, "FETCH_HEAD")...)
		if err == nil {
			members = append(members, n)
			fmt.Fprintf(stderr, "BATCH MERGED #%d t=%.1fs\n", n, since(start))
			continue
		}
		unmerged, cerr := hasConflicts(g)
		if cerr != nil {
			return nil, nil, batchRefused(stderr, cerr)
		}
		if !unmerged {
			return nil, nil, batchRefused(stderr, fmt.Errorf("the merge of pull/%d failed and left no conflicting file: %w", n, err))
		}
		if _, aerr := g.Run("merge", "--abort"); aerr != nil {
			return nil, nil, batchRefused(stderr, aerr)
		}
		dropped = append(dropped, n)
		fmt.Fprintf(stderr, "BATCH DROP #%d reason=%q t=%.1fs\n", n, "the merge conflicts with the members ahead", since(start))
	}
	return members, dropped, 0
}

// batchRefused is what a tool error costs: ONE line on stderr and exit 2, which is this
// tool's "could not run". A red batch is exit 1 and a green one is 0, and a clone that
// could not be made is neither -- it is nothing anybody may read as a verdict.
func batchRefused(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "BATCH REFUSED: %s\n", oneline.Err(err))
	return 2
}

// stepUnavailable is why a step cannot run here, or the empty string when it can.
func stepUnavailable(step batchStep, clone string) string {
	if step.file != "" {
		if _, err := os.Stat(filepath.Join(clone, filepath.FromSlash(step.file))); err != nil {
			return "this checkout holds no " + step.file
		}
	}
	if step.needs != "" {
		if _, err := exec.LookPath(step.needs); err != nil {
			return step.needs + " is not on this machine"
		}
	}
	return ""
}

// parsePRList reads the pull request numbers, separated by commas or spaces, in the
// order given -- which is the order they will be merged, so it is never sorted. A
// repeated number is a typo with two readings and is refused.
func parsePRList(raw string) ([]int, error) {
	seen := map[int]bool{}
	var prs []int
	for _, field := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		n, err := strconv.Atoi(field)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("--pr holds %q, which is not a pull request number", field)
		}
		if seen[n] {
			return nil, fmt.Errorf("--pr names #%d twice; a member lands once, and a repeat is a typo with two readings", n)
		}
		seen[n] = true
		prs = append(prs, n)
	}
	if len(prs) == 0 {
		return nil, fmt.Errorf("--pr names no pull request; a batch of nothing is not a batch")
	}
	return prs, nil
}

// failuresIn reads a go test run's own output for the packages that failed and the tests
// that failed in them, so the FAIL line names what to look at and a reader does not have
// to open the log to find out. A build failure prints `FAIL <pkg> [build failed]`, whose
// package is read the same way.
func failuresIn(out string) (pkgs, tests []string) {
	seenPkg, seenTest := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) >= 2 && fields[0] == "FAIL" && !strings.HasPrefix(fields[1], "["):
			if !seenPkg[fields[1]] {
				seenPkg[fields[1]] = true
				pkgs = append(pkgs, fields[1])
			}
		case len(fields) >= 3 && fields[0] == "---" && fields[1] == "FAIL:":
			name := strings.TrimSuffix(fields[2], ":")
			if !seenTest[name] {
				seenTest[name] = true
				tests = append(tests, name)
			}
		}
	}
	return pkgs, tests
}

// numberList renders the members and the dropped as one token a caller can split, and
// "none" where there are none -- never an empty value, which reads as a field the tool
// forgot to fill in.
func numberList(ns []int) string {
	if len(ns) == 0 {
		return "none"
	}
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, strconv.Itoa(n))
	}
	return strings.Join(out, ",")
}

// numberOrNone is numberList for a list of names, capped, because a run where everything
// failed must still print one line.
func numberOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return oneline.Cap(strings.Join(names, ","), oneline.TailBytes)
}

// since is how long the batch has been running, in seconds, for the progress lines. It is
// the wall clock on purpose: the question a reader has at minute three is how long this
// has really taken, which no injected clock can answer.
func since(start time.Time) float64 { return time.Since(start).Seconds() }
