package card

// wrapper_commit.go is the commit step (#2932 rev 4, replaces the bash
// harvest's commit_step): wrapper step 6, before copyOut, commits
// <job>/out/repo onto the attempt's branch nova/<S>/<label>-a<attempt>, and
// end.record carries that commit as pushed_sha. Harvest never commits; it
// pushes this commit from the results dir.
//
//   - The message is the harness's RESULT line 1; the author and committer
//     are `nova-card <bench>` (no person).
//   - Card scratch (ScratchFiles) is left out of the commit, wherever it sits.
//   - A changed file over MaxCommitFile means no commit: pushed_sha "-" and
//     the wrapper line says OVERSIZE <file>.
//   - A native run (nova-swarm native) clones in its own job directory, not
//     under out; with NOVA_CARD_OUT in its environment it hands that repo and
//     the card's RESULT.md to out (swarm.HandOffCardOut), so this step has one
//     place to look whichever runner the bench used.
//   - No out/repo, or nothing to commit, is pushed_sha "-" (NO-COMMIT): the
//     card's friend read is the report rule (#3036), never harvest. A code
//     card whose model said DONE and committed nothing ends FAILED reason
//     no-commit (NoCommitEnd), in done/fail, never a done/ok nobody harvests.
//   - There is no rebase and no mirror refresh: a moved base is the lander's
//     update-branch (#3139).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// MaxCommitFile is the largest file the commit step commits (1 MB).
const MaxCommitFile = 1 << 20

// ScratchFiles are the card's own files, never committed.
var ScratchFiles = []string{"RESULT.md", "notes.txt", "REPORT.md", "usage.tsv", "harness-output.log", "repo.bundle"}

// NoCommit is pushed_sha for a card that committed nothing.
const NoCommit = "-"

// CommitResult is what the commit step did: SHA is the commit (or "-"), Note
// is COMMITTED, NO-COMMIT or OVERSIZE <file>.
type CommitResult struct {
	SHA  string
	Note string
}

// CommitOutput commits the uncommitted work in repo onto branch with message,
// authored `nova-card <bench>`. A repo with no .git is NO-COMMIT.
func CommitOutput(repo, branch, message, bench string) (CommitResult, error) {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return CommitResult{SHA: NoCommit, Note: "NO-COMMIT"}, nil
		}
		// A .git that is there but unreadable is not "no repo".
		return CommitResult{}, err
	}
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=nova-card", "GIT_AUTHOR_EMAIL="+bench,
		"GIT_COMMITTER_NAME=nova-card", "GIT_COMMITTER_EMAIL="+bench,
		"GIT_TERMINAL_PROMPT=0")
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		cmd.Env = env
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
		}
		return strings.TrimSpace(out.String()), nil
	}
	pathspec := []string{"--", "."}
	for _, f := range ScratchFiles {
		pathspec = append(pathspec, ":(exclude,glob)**/"+f)
	}
	if _, err := git(append([]string{"add", "-A"}, pathspec...)...); err != nil {
		return CommitResult{}, err
	}
	staged, err := git("diff", "--cached", "--name-only", "-z", "--no-renames", "--diff-filter=ACMRT")
	if err != nil {
		return CommitResult{}, err
	}
	for _, f := range strings.Split(staged, "\x00") {
		if f == "" {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(f))); err == nil && fi.Mode().IsRegular() && fi.Size() > MaxCommitFile {
			_, _ = git("reset", "-q")
			return CommitResult{SHA: NoCommit, Note: "OVERSIZE " + f}, nil
		}
	}
	if _, err := git("checkout", "-q", "-B", branch); err != nil {
		return CommitResult{}, err
	}
	if _, err := git("diff", "--cached", "--quiet"); err != nil {
		if strings.TrimSpace(message) == "" {
			message = "nova-card " + branch
		}
		if _, err := git("commit", "-q", "--no-verify", "-m", message); err != nil {
			return CommitResult{}, err
		}
	} else if ahead, err := git("rev-list", "--count", "HEAD", "--not", "--remotes"); err != nil {
		// A git that could not count is an error, never NO-COMMIT: the
		// copy's push (or its no-commit fail) hangs on this answer.
		return CommitResult{}, err
	} else if ahead == "0" {
		// Nothing staged and nothing the harness committed that no remote has.
		return CommitResult{SHA: NoCommit, Note: "NO-COMMIT"}, nil
	}
	sha, err := git("rev-parse", "HEAD")
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{SHA: sha, Note: "COMMITTED"}, nil
}

// NoCommitWhy is the end's why for a code card that said DONE and committed
// nothing.
const NoCommitWhy = "model said DONE but committed nothing (NO-COMMIT)"

// NoCommitKinds are the card kinds that legitimately commit nothing: a
// script card, a read and a report. Every other kind (fix, recut, port,
// docs-guard, and a model card with no KIND) is a code card.
var NoCommitKinds = []string{KindScript, typedrec.KindRead, typedrec.KindReport}

// CodeKind reports whether a card of kind must commit to be done.
func CodeKind(kind string) bool {
	for _, k := range NoCommitKinds {
		if k == kind {
			return false
		}
	}
	return true
}

// NoCommitEnd is the end step's no-commit rule (quack-0925d, 2026-09-25): a
// code card the harness ended DONE, whose commit step found nothing to commit
// (NO-COMMIT) and whose model's line 2 in <out>/RESULT.md is DONE, ends
// FAILED reason no-commit with NoCommitWhy, so ns_card_end moves it to
// done/fail in the same call. The model's RESULT.md (its DONE line) is left
// as it is, for the result record. A card whose model said ABSTAIN or
// BLOCKED, wrote no line 2, or is not a code card, or a commit step that
// committed (or refused OVERSIZE), is returned unchanged with ok false.
func NoCommitEnd(kind, out string, end WrapperEnd) (WrapperEnd, bool) {
	if end.Outcome != "DONE" || end.Commit != "NO-COMMIT" || !CodeKind(kind) {
		return end, false
	}
	raw, err := os.ReadFile(filepath.Join(out, "RESULT.md"))
	if err != nil || typedrec.SplitModel(raw, kind).Status != typedrec.StatusDone {
		return end, false
	}
	end.Outcome, end.Reason, end.Why = "FAILED", "no-commit", NoCommitWhy
	return end, true
}

// PrepushCap is the maximum duration for pre-push tests under nova-card copy (#4314).
const PrepushCap = 2 * time.Minute

// PrepushTestResult is the outcome of running prepush tests.
type PrepushTestResult struct {
	Passed       bool
	Receipt      string
	FailingTests []string
	Output       string
}

// PrepushTestRunner is the function that runs prepush tests on packages in repoDir.
type PrepushTestRunner func(ctx context.Context, repoDir string, pkgs []string) (PrepushTestResult, error)

// hasMakefile reports whether repoDir contains a Makefile.
func hasMakefile(repoDir string) bool {
	if repoDir == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(repoDir, "Makefile"))
	return err == nil && !fi.IsDir()
}

// holdsGo reports whether repoDir/relDir holds at least one .go source file.
func holdsGo(repoDir, relDir string) bool {
	dir := filepath.Join(repoDir, filepath.FromSlash(relDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

// repoModule extracts the module path from go.mod in repoDir.
func repoModule(repoDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			mod := strings.TrimSpace(strings.TrimPrefix(line, "module"))
			if mod != "" {
				return mod, nil
			}
		}
	}
	return "", errors.New("no module statement found in go.mod")
}

// DerivePrepushPackages derives Go packages to test from the card's PATHS,
// packages that import them one level up, and the class tests (./internal/ci and ./internal/docs).
// Formatted as ./<relPath>, deduplicated and sorted.
func DerivePrepushPackages(ctx context.Context, repoDir, cardPaths string) ([]string, error) {
	packages := map[string]bool{
		"./internal/ci":   true,
		"./internal/docs": true,
	}

	touchedDirs := make(map[string]bool)
	for _, p := range ws.SplitPaths(cardPaths) {
		targetPath := filepath.Join(repoDir, filepath.FromSlash(p))
		var dir string
		if fi, err := os.Stat(targetPath); err == nil && fi.IsDir() {
			dir = p
		} else if strings.HasSuffix(p, ".go") {
			dir = filepath.Dir(p)
		} else if fi, err := os.Stat(targetPath); err == nil && !fi.IsDir() {
			dir = filepath.Dir(p)
		} else if filepath.Ext(p) != "" {
			dir = filepath.Dir(p)
		} else {
			dir = p
		}
		dir = filepath.ToSlash(filepath.Clean(dir))
		if holdsGo(repoDir, dir) {
			touchedDirs[dir] = true
		}
	}

	for d := range touchedDirs {
		if d == "." {
			packages["."] = true
		} else {
			packages["./"+d] = true
		}
	}

	if len(touchedDirs) > 0 {
		mod, err := repoModule(repoDir)
		if err != nil {
			return nil, fmt.Errorf("repo module: %w", err)
		}
		touchedImports := make(map[string]bool)
		for d := range touchedDirs {
			if d == "." {
				touchedImports[mod] = true
			} else {
				touchedImports[mod+"/"+d] = true
			}
		}

		var targets []string
		for _, top := range []string{"cmd", "internal", "tools"} {
			if fi, err := os.Stat(filepath.Join(repoDir, top)); err == nil && fi.IsDir() {
				targets = append(targets, "./"+top+"/...")
			}
		}

		if len(targets) > 0 {
			args := append([]string{"list", "-f",
				"{{.ImportPath}} {{range .Imports}}{{.}} {{end}}{{range .TestImports}}{{.}} {{end}}{{range .XTestImports}}{{.}} {{end}}"},
				targets...)
			cmd := exec.CommandContext(ctx, "go", args...)
			cmd.Dir = repoDir
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Run(); err != nil {
				if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return nil, ctx.Err()
				}
				return nil, fmt.Errorf("go list: %w", err)
			}
			sc := bufio.NewScanner(&out)
			for sc.Scan() {
				fields := strings.Fields(sc.Text())
				if len(fields) < 2 {
					continue
				}
				pkgImport := fields[0]
				for _, imp := range fields[1:] {
					if touchedImports[imp] {
						if pkgImport == mod {
							packages["."] = true
						} else if strings.HasPrefix(pkgImport, mod+"/") {
							rel := strings.TrimPrefix(pkgImport, mod+"/")
							packages["./"+rel] = true
						}
						break
					}
				}
			}
		}
	}

	pkgs := make([]string, 0, len(packages))
	for p := range packages {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	return pkgs, nil
}

type prepushJSONEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

func extractFailingTests(data []byte) []string {
	var failing []string
	seen := make(map[string]bool)
	var pkgFails []string
	seenPkg := make(map[string]bool)

	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev prepushJSONEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch {
		case ev.Action == "fail" && ev.Test != "":
			if !seen[ev.Test] {
				seen[ev.Test] = true
				failing = append(failing, ev.Test)
			}
		case (ev.Action == "fail" || ev.Action == "build-fail") && ev.Test == "":
			if ev.Package != "" && !seenPkg[ev.Package] {
				seenPkg[ev.Package] = true
				pkgFails = append(pkgFails, ev.Package)
			}
		}
	}
	if len(failing) == 0 && len(pkgFails) > 0 {
		return pkgFails
	}
	return failing
}

// RunPrepushTest runs `make test PKGS=... GOTEST_COUNT_FLAG=` under PrepushCap.
func RunPrepushTest(ctx context.Context, repoDir string, pkgs []string) (PrepushTestResult, error) {
	if len(pkgs) == 0 {
		return PrepushTestResult{
			Passed:  true,
			Receipt: "make test PKGS= GOTEST_COUNT_FLAG= pass",
		}, nil
	}
	tctx, cancel := context.WithTimeout(ctx, PrepushCap)
	defer cancel()

	runnerTemp, err := os.MkdirTemp("", "prepush-runner-")
	if err != nil {
		return PrepushTestResult{}, fmt.Errorf("create RUNNER_TEMP: %w", err)
	}
	defer os.RemoveAll(runnerTemp)

	pkgsArg := strings.Join(pkgs, " ")
	cmd := exec.CommandContext(tctx, "make", "test", "PKGS="+pkgsArg, "GOTEST_COUNT_FLAG=")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "RUNNER_TEMP="+runnerTemp)

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	runErr := cmd.Run()

	jsonPath := filepath.Join(runnerTemp, "test.json")
	var failingTests []string
	if data, err := os.ReadFile(jsonPath); err == nil {
		failingTests = extractFailingTests(data)
	} else {
		failingTests = extractFailingTests(combined.Bytes())
	}

	if runErr != nil {
		if errors.Is(tctx.Err(), context.DeadlineExceeded) {
			return PrepushTestResult{
				Passed:       false,
				FailingTests: []string{"timeout after " + PrepushCap.String()},
				Output:       combined.String(),
			}, tctx.Err()
		}
		if len(failingTests) == 0 {
			failingTests = []string{"make test failed: " + runErr.Error()}
		}
		return PrepushTestResult{
			Passed:       false,
			FailingTests: failingTests,
			Output:       combined.String(),
		}, nil
	}

	if len(failingTests) > 0 {
		return PrepushTestResult{
			Passed:       false,
			FailingTests: failingTests,
			Output:       combined.String(),
		}, nil
	}

	receipt := fmt.Sprintf("make test PKGS=%s GOTEST_COUNT_FLAG= pass", pkgsArg)
	return PrepushTestResult{
		Passed:  true,
		Receipt: receipt,
		Output:  combined.String(),
	}, nil
}
