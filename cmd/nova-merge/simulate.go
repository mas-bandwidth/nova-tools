package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// defaultChecks is the hand loop this verb replaces, written out exactly as the
// coordinator ran it on 2026-09-17: build everything, then the CI class tests.
const defaultChecks = "go build ./...,go test ./internal/ci/"

// QueueReader reads the live merge queue for a base branch: the pull request numbers
// waiting on it, in the order the host will land them. It is an interface so the tests
// drive a fake queue and reach no network, and so the one implementation that shells to
// gh is a thing a reader can check line by line.
type QueueReader interface {
	Entries(base string) ([]int, error)
}

// ghQueue is the production QueueReader: one `gh api graphql` call naming the
// mergeQueue for the base branch. gh runs inside the repository so it resolves the
// owner and name from that clone's own origin.
type ghQueue struct {
	repo    string
	timeout time.Duration
	runner  merge.Runner
}

func newGHQueue(repo string, timeout time.Duration, runner merge.Runner) QueueReader {
	if runner == nil {
		runner = merge.Exec{}
	}
	return &ghQueue{repo: repo, timeout: timeout, runner: runner}
}

// mergeQueueQuery is the one read of the host's merge queue. Nothing here is an
// instruction and nothing here is a grant; the answer is a list of numbers.
const mergeQueueQuery = `query($owner:String!,$name:String!,$branch:String!){repository(owner:$owner,name:$name){mergeQueue(branch:$branch){entries(first:100){nodes{position pullRequest{number}}}}}}`

func (q *ghQueue) Entries(base string) ([]int, error) {
	slug, err := repoSlug(q.repo, q.timeout, q.runner)
	if err != nil {
		return nil, err
	}
	owner, name, _ := strings.Cut(slug, "/")
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	defer cancel()
	out, err := q.runner.Run(ctx, q.repo, "gh", "api", "graphql",
		"-f", "query="+mergeQueueQuery,
		"-f", "owner="+owner,
		"-f", "name="+name,
		"-f", "branch="+base)
	if err != nil {
		return nil, fmt.Errorf("gh api graphql could not read the merge queue for %q: %w: %s", base, err, firstLine(out, nil))
	}
	return decodeMergeQueue(out)
}

// repoSlug derives <owner>/<name> from the repository's own origin, so gh can name the
// repository in a GraphQL query. It reaches no network: it is `git remote get-url`.
func repoSlug(repo string, timeout time.Duration, runner merge.Runner) (string, error) {
	g := merge.NewGit(repo, timeout, runner)
	raw, err := g.Out("remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("the repository at %s has no readable origin: %w", repo, err)
	}
	return parseRepoSlug(raw)
}

// parseRepoSlug reads the last two path segments of an origin, whether it is
// https://host/owner/name.git or git@host:owner/name.git; the host is not
// inspected, so the parser and its test name no real host.
func parseRepoSlug(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+len("://"):]
	} else if i := strings.Index(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ".git")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("the origin %q does not name a <owner>/<name> GitHub repository", raw)
	}
	owner, name := parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || name == "" {
		return "", fmt.Errorf("the origin %q does not name a <owner>/<name> GitHub repository", raw)
	}
	return owner + "/" + name, nil
}

// decodeMergeQueue reads the numbers out of a GraphQL answer. The nodes arrive in the
// host's order; a node that names no pull request is skipped rather than reported as 0.
func decodeMergeQueue(out string) ([]int, error) {
	var raw struct {
		Data struct {
			Repository struct {
				MergeQueue struct {
					Entries struct {
						Nodes []struct {
							PullRequest struct {
								Number int `json:"number"`
							} `json:"pullRequest"`
						} `json:"nodes"`
					} `json:"entries"`
				} `json:"mergeQueue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("gh api graphql did not answer JSON this tool can read: %w", err)
	}
	var entries []int
	for _, node := range raw.Data.Repository.MergeQueue.Entries.Nodes {
		if node.PullRequest.Number > 0 {
			entries = append(entries, node.PullRequest.Number)
		}
	}
	return entries, nil
}

// cmdSimulate merges a queue's entries onto the base in order, in a scratch worktree,
// runs every check after each, and names the first entry that turns the base red. It
// answers the question the coordinator answered by hand three times on 2026-09-17:
// which entry was green alone and red on top of the entries ahead of it.
func cmdSimulate(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("simulate")
	repo := f.fs.String("repo", "", "")
	base := f.fs.String("base", "", "")
	entriesPath := f.fs.String("entries", "", "")
	checksRaw := f.fs.String("checks", defaultChecks, "")
	timeoutRaw := f.fs.String("timeout", "5m", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("repo", *repo, "the path of a local clone whose origin holds the queue's pull request heads")
	f.require("base", *base, "the branch the queue is merging onto, for example dev")
	// The base is handed to git as an argument, so it is a ref name and nothing that
	// git could read as an option (lesson 48).
	if strings.TrimSpace(*base) != "" {
		if err := merge.ValidRefName(*base); err != nil {
			f.problem(fmt.Sprintf("--base: %s", oneline.Escape(err.Error())))
		}
	}
	timeout, err := time.ParseDuration(*timeoutRaw)
	if err != nil || timeout <= 0 {
		f.problem(fmt.Sprintf("--timeout is a duration per check like 5m, got %q", *timeoutRaw))
	}
	if !f.done(stderr) {
		return 2
	}
	checks := splitChecks(*checksRaw)
	if len(checks) == 0 {
		fmt.Fprintf(stderr, "SIMULATE REFUSED: --checks names no command; give at least one, like %q\n", defaultChecks)
		return 2
	}
	repoAbs, err := filepath.Abs(*repo)
	if err != nil {
		return simulateRefused(stderr, err)
	}
	gitDir, err := gitDirOf(repoAbs, timeout, deps)
	if err != nil {
		return simulateRefused(stderr, err)
	}
	entries, err := simulateEntries(*entriesPath, *base, repoAbs, timeout, deps)
	if err != nil {
		return simulateRefused(stderr, err)
	}
	// THE SCRATCH WORKTREE LIVES UNDER THE REPOSITORY'S OWN .git, never under the
	// system temp directory: rule 13, and the path is a computed one, so its removal is
	// safepath's.
	scratch, err := os.MkdirTemp(gitDir, "nova-merge-simulate-")
	if err != nil {
		return simulateRefused(stderr, err)
	}
	defer func() {
		g := merge.NewGit(repoAbs, timeout, deps.Runner)
		_, _ = g.Run("worktree", "remove", "--force", scratch)
		if err := safepath.RemoveUnder(gitDir, scratch); err != nil {
			fmt.Fprintf(stderr, "SIMULATE NOTE the scratch worktree at %s could not be removed: %s\n",
				oneline.Field(scratch), oneline.Err(err))
		}
	}()
	fetch := merge.NewGit(repoAbs, timeout, deps.Runner)
	if _, err := fetch.Run("fetch", "--quiet", "origin", *base); err != nil {
		return simulateRefused(stderr, fmt.Errorf("could not fetch origin/%s: %w", *base, err))
	}
	if _, err := fetch.Run("worktree", "add", "--detach", scratch, "origin/"+*base); err != nil {
		return simulateRefused(stderr, fmt.Errorf("could not make the scratch worktree: %w", err))
	}
	scratchGit := merge.NewGit(scratch, timeout, deps.Runner)
	ok, conflicts, poison := 0, 0, 0
	for _, n := range entries {
		if _, err := scratchGit.Run("fetch", "--quiet", "origin", "pull/"+strconv.Itoa(n)+"/head"); err != nil {
			return simulateRefused(stderr, fmt.Errorf("could not fetch pull/%d/head: %w", n, err))
		}
		// The squash-merge is wrapped in merge.Identity because the command can write a
		// commit object into the work tree; the tripwire holds every such site to it.
		if _, err := scratchGit.Run(merge.Identity("merge", "--squash", "FETCH_HEAD")...); err != nil {
			unmerged, cerr := hasConflicts(scratchGit)
			if cerr != nil {
				return simulateRefused(stderr, cerr)
			}
			if !unmerged {
				return simulateRefused(stderr, fmt.Errorf("the squash-merge of pull/%d failed and left no conflicting file: %w", n, err))
			}
			fmt.Fprintf(stdout, "SIMULATE CONFLICT #%d with the entries ahead\n", n)
			conflicts++
			if rerr := resetHard(scratchGit); rerr != nil {
				return simulateRefused(stderr, rerr)
			}
			continue
		}
		if _, err := scratchGit.Run(merge.Identity("commit", "--allow-empty", "-q", "-m", fmt.Sprintf("simulate entry #%d", n))...); err != nil {
			return simulateRefused(stderr, fmt.Errorf("the squash-merge of pull/%d could not be committed: %w", n, err))
		}
		poisonCheck := ""
		for _, check := range checks {
			out, err := runCheck(scratch, check, timeout, nil)
			if err != nil {
				poisonCheck = check
				fmt.Fprintf(stdout, "SIMULATE POISON #%d check=%q %s\n",
					n, check, oneline.Escape(oneline.Cap(firstLine(out, err), oneline.TailBytes)))
				break
			}
		}
		if poisonCheck != "" {
			poison = n
			break
		}
		fmt.Fprintf(stdout, "SIMULATE OK #%d\n", n)
		ok++
	}
	poisonField := "none"
	if poison != 0 {
		poisonField = "#" + strconv.Itoa(poison)
	}
	fmt.Fprintf(stdout, "SIMULATE DONE entries=%d ok=%d conflicts=%d poison=%s\n",
		len(entries), ok, conflicts, oneline.Escape(poisonField))
	if poison != 0 {
		return 2
	}
	return 0
}

// simulateRefused is the one shape a tool error takes here: the verb could not run. It
// is exit 1 on purpose -- exit 2 belongs to a poison the verb found.
func simulateRefused(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "SIMULATE REFUSED: %s\n", oneline.Err(err))
	return 1
}

// gitDirOf resolves the repository's own .git, which is the root the scratch worktree
// is made under. `--absolute-git-dir` answers an absolute path even from a worktree.
func gitDirOf(repo string, timeout time.Duration, deps Deps) (string, error) {
	g := merge.NewGit(repo, timeout, deps.Runner)
	out, err := g.Out("rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("%s is not a git repository this tool can read: %w", repo, err)
	}
	return out, nil
}

// simulateEntries reads the queue: a file of numbers where --entries names one, and the
// live merge queue through gh otherwise.
func simulateEntries(entriesPath, base, repo string, timeout time.Duration, deps Deps) ([]int, error) {
	if strings.TrimSpace(entriesPath) != "" {
		return readEntryFile(entriesPath)
	}
	var q QueueReader
	if deps.NewQueue != nil {
		q = deps.NewQueue(repo, timeout)
	}
	if q == nil {
		q = newGHQueue(repo, timeout, deps.Runner)
	}
	return q.Entries(base)
}

// readEntryFile reads one pull request number per line. A blank line is skipped and a
// line beginning with # is a comment, so a queue copied from a table still reads.
func readEntryFile(path string) ([]int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--entries %s could not be read: %w", path, err)
	}
	var entries []int
	for _, line := range strings.Split(string(raw), "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("--entries %s holds %q, which is not a pull request number", path, s)
		}
		entries = append(entries, n)
	}
	return entries, nil
}

// splitChecks splits the comma-separated check list, dropping empty entries so that a
// trailing comma is not a check that runs nothing and passes.
func splitChecks(raw string) []string {
	var out []string
	for _, c := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(c); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// hasConflicts reports whether the index holds unmerged paths, which is how git says
// the squash-merge stopped on a conflict rather than on some other failure.
func hasConflicts(g *merge.Git) (bool, error) {
	out, err := g.Run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// resetHard returns the work tree to the last entry that passed, so a conflict is
// skipped and the entries after it are still judged on top of the good state.
func resetHard(g *merge.Git) error {
	if _, err := g.Run("reset", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("the scratch worktree could not be reset after a conflict: %w", err)
	}
	if _, err := g.Run("clean", "-fd"); err != nil {
		return fmt.Errorf("the scratch worktree could not be cleaned after a conflict: %w", err)
	}
	return nil
}

// firstLine is the first line of a check's output that says something, or the error
// itself when the check printed nothing at all. It is what SIMULATE POISON names.
func firstLine(out string, err error) string {
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	if err != nil {
		return err.Error()
	}
	return "(no output)"
}

// runCheck runs one check in the scratch worktree under its own deadline. The child is
// its own process group so that a check which spawns children is killed whole when
// --timeout expires; a deadline that kills only the shell leaves the tree running.
//
// A nil env is this process's own CLEANED by goenv.Clean, which is what `simulate` hands
// it; `batch` hands it an environment whose temp directory is the batch's own, so that two
// gates running on one bench cannot write over each other's scratch -- built from
// goenv.Clean too, because its steps are go commands whose output this verb parses.
func runCheck(dir, check string, timeout time.Duration, env []string) (string, error) {
	name, args := shellCommand(check)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	// The checks are go commands -- defaultChecks is `go build ./...,go test
	// ./internal/ci/` -- and SIMULATE POISON quotes the first line of what they
	// print. A caller's GOFLAGS=-json, which CI's `make test` exports, would make
	// that first line a JSON object instead of the failure a reader needs. A nil
	// env is therefore this process's environment CLEANED, never the raw one, and
	// a caller that hands its own builds it from Clean the same way (ciTestEnv).
	if env == nil {
		env = goenv.Clean(os.Environ())
	}
	cmd.Env = env
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	configureCheckProcess(cmd)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return buf.String(), err
	case <-timer.C:
		killCheckProcess(cmd)
		<-done
		return buf.String(), fmt.Errorf("no answer within the %s --timeout", timeout)
	}
}
