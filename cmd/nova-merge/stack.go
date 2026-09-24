package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// cmdStack is the stacked rebase verb: re-land a set of same-base PRs in ledger order
// against the landing branch, resolving both-sides appends mechanically and stopping on
// any other conflict.
func cmdStack(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("stack")
	repo := f.fs.String("repo", "", "")
	base := f.fs.String("base", "dev", "")
	prRaw := f.fs.String("prs", "", "")
	root := f.fs.String("root", "", "")
	timeoutRaw := f.fs.String("timeout", "10m", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("repo", *repo, "the repository whose PRs are stacked, as <owner>/<name>")
	f.require("prs", *prRaw, "the pull request numbers to stack in ledger order, like 1,2,3")
	f.require("root", *root, "the directory this stack clones and builds under")
	if *repo != "" {
		if err := validRepoSlug(*repo); err != nil {
			f.problem(fmt.Sprintf("--repo is <owner>/<name>, got %q: %s", *repo, oneline.Escape(err.Error())))
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
		f.problem(fmt.Sprintf("--timeout is a duration like 10m, got %q", *timeoutRaw))
	}
	if !f.done(stderr) {
		return 2
	}
	return runStack(stackRun{
		repo:    *repo,
		base:    *base,
		prs:     prs,
		root:    *root,
		timeout: timeout,
	}, stdout, stderr, deps)
}

// stackRun is one stack's whole invocation, checked.
type stackRun struct {
	repo    string
	base    string
	prs     []int
	root    string
	timeout time.Duration
}

// runStack clones the repository, fetches the base, and merges each PR head in ledger
// order. A conflict that is a pure both-sides append (empty base section in diff3) is
// resolved mechanically; any other conflict drops the PR by name.
func runStack(in stackRun, stdout, stderr io.Writer, deps Deps) int {
	start := time.Now()
	rootAbs, err := filepath.Abs(in.root)
	if err != nil {
		return stackRefused(stderr, err)
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return stackRefused(stderr, err)
	}
	work := filepath.Join(rootAbs, "stack-work")
	if err := safepath.RemoveUnder(rootAbs, work); err != nil {
		return stackRefused(stderr, err)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return stackRefused(stderr, err)
	}
	clone := filepath.Join(work, "repo")
	cloneArgs := []string{"clone", "--quiet", "--", deps.RepoURL(in.repo), clone}
	gg := merge.NewGit(work, in.timeout, deps.Runner)
	if _, err := gg.Run(cloneArgs...); err != nil {
		return stackRefused(stderr, err)
	}
	cg := merge.NewGit(clone, in.timeout, deps.Runner)
	// diff3 conflict style is how the mechanical resolver sees the base section.
	cg.Run("config", "merge.conflictStyle", "diff3")
	if _, err := cg.Run("fetch", "--quiet", "origin", in.base); err != nil {
		return stackRefused(stderr, fmt.Errorf("could not fetch origin/%s: %w", in.base, err))
	}
	if _, err := cg.Run("checkout", "--quiet", "-B", "stack", "FETCH_HEAD"); err != nil {
		return stackRefused(stderr, err)
	}
	baseSHA, err := cg.Out("rev-parse", "HEAD")
	if err != nil {
		return stackRefused(stderr, err)
	}
	fmt.Fprintf(stderr, "STACK START base=%s prs=%d t=%.1fs\n",
		oneline.Field(baseSHA), len(in.prs), since(start))

	var members, dropped []int
	for _, n := range in.prs {
		if _, err := cg.Run("fetch", "--quiet", "origin", "pull/"+strconv.Itoa(n)+"/head"); err != nil {
			return stackRefused(stderr, fmt.Errorf("could not fetch pull/%d/head: %w", n, err))
		}
		message := fmt.Sprintf("stack pull request #%d", n)
		_, mergeErr := cg.Run(merge.Identity("merge", "--no-ff", "--no-edit", "-m", message, "FETCH_HEAD")...)
		if mergeErr == nil {
			members = append(members, n)
			fmt.Fprintf(stderr, "STACK MERGED #%d t=%.1fs\n", n, since(start))
			continue
		}
		unmerged, cerr := hasConflicts(cg)
		if cerr != nil {
			return stackRefused(stderr, cerr)
		}
		if !unmerged {
			return stackRefused(stderr, fmt.Errorf("the merge of pull/%d failed and left no conflicting file: %w", n, mergeErr))
		}
		resolved, rerr := stackResolveBothSidesAppends(cg)
		if rerr != nil || !resolved {
			cg.Run("merge", "--abort")
			reason := "conflict is not a both-sides append"
			if rerr != nil {
				reason = rerr.Error()
			}
			dropped = append(dropped, n)
			fmt.Fprintf(stderr, "STACK DROP #%d reason=%q t=%.1fs\n", n, reason, since(start))
			continue
		}
		if _, err := cg.Run(merge.Identity("commit", "--no-edit", "-m", message)...); err != nil {
			cg.Run("merge", "--abort")
			dropped = append(dropped, n)
			fmt.Fprintf(stderr, "STACK DROP #%d reason=%q t=%.1fs\n", n, "could not commit resolved merge", since(start))
			continue
		}
		members = append(members, n)
		fmt.Fprintf(stderr, "STACK RESOLVED #%d t=%.1fs\n", n, since(start))
	}
	headSHA, err := cg.Out("rev-parse", "HEAD")
	if err != nil {
		return stackRefused(stderr, err)
	}
	fmt.Fprintf(stdout, "STACK OK base=%s head=%s members=%s dropped=%s\n",
		oneline.Field(baseSHA), oneline.Field(headSHA),
		oneline.Field(numberList(members)), oneline.Field(numberList(dropped)))
	return 0
}

// stackResolveBothSidesAppends reads every conflicted file and resolves it when each
// conflict block is a pure both-sides append -- the diff3 base section is empty, meaning
// both sides are inserting content at the same anchor without modifying existing content.
func stackResolveBothSidesAppends(g *merge.Git) (bool, error) {
	out, err := g.Run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return false, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return false, nil
	}
	for _, file := range strings.Split(out, "\n") {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(g.Dir, file))
		if err != nil {
			return false, fmt.Errorf("could not read %s: %w", file, err)
		}
		resolved, ok := stackResolveConflictContent(string(content))
		if !ok {
			return false, nil
		}
		if err := os.WriteFile(filepath.Join(g.Dir, file), []byte(resolved), 0o644); err != nil {
			return false, fmt.Errorf("could not write %s: %w", file, err)
		}
		if _, err := g.Run("add", "--", file); err != nil {
			return false, fmt.Errorf("could not stage %s: %w", file, err)
		}
	}
	return true, nil
}

// stackResolveConflictContent parses diff3 conflict markers and resolves blocks whose
// base section (between ||||| and =======) is empty: both sides are purely inserting at
// the same anchor. The resolution keeps ours then theirs. A block with a non-empty base
// is not a both-sides append and the whole file is rejected.
func stackResolveConflictContent(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	var result []string
	i := 0
	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "<<<<<<< ") {
			result = append(result, lines[i])
			i++
			continue
		}
		// Conflict block.
		i++ // skip <<<<<<< line
		var oursLines []string
		for i < len(lines) && !strings.HasPrefix(lines[i], "||||||| ") && lines[i] != "=======" {
			oursLines = append(oursLines, lines[i])
			i++
		}
		var baseLines []string
		if i < len(lines) && strings.HasPrefix(lines[i], "||||||| ") {
			i++ // skip ||||||| line
			for i < len(lines) && lines[i] != "=======" {
				baseLines = append(baseLines, lines[i])
				i++
			}
		}
		if i < len(lines) && lines[i] == "=======" {
			i++ // skip ======= line
		}
		var theirsLines []string
		for i < len(lines) && !strings.HasPrefix(lines[i], ">>>>>>> ") {
			theirsLines = append(theirsLines, lines[i])
			i++
		}
		if i < len(lines) {
			i++ // skip >>>>>>> line
		}
		// A non-empty base section means existing content was modified, not just appended.
		if strings.TrimSpace(strings.Join(baseLines, "\n")) != "" {
			return "", false
		}
		result = append(result, oursLines...)
		result = append(result, theirsLines...)
	}
	return strings.Join(result, "\n"), true
}

// stackRefused prints the one refusal line and returns exit 2.
func stackRefused(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "STACK REFUSED: %s\n", oneline.Err(err))
	return 2
}
