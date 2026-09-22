/*
Package landed answers one question about a piece of work: did it land in the base
branch? It is the resolver behind the `:landed` acceptance kind of a work set
(nova-tools #2664) and behind `:merged :merged-at`, evaluated by `nova-work set
check --evaluate`.

GitHub has two ways of saying a PR landed and this house uses both. A PR merged
through the forge carries mergedAt. A PR the lander carried into dev inside an
integration merge is CLOSED, not MERGED (#2614 #2607 #2625 #2631 #2594 #2639 on
2026-09-22), and what says it landed is the lander's own rule: merging its head into
the base changes nothing. `git merge-tree --write-tree <base> <head>` yields the
base's own tree exactly then -- which holds for a PR the lander combined with
another (#2544 with #2614 in #2629, #2645 into #2670) although no single base
commit carries its head's files, and fails for a PR whose change is not all there.
When a later PR rewrote what this one added, the rule is asked again at the base
commits inside the PR's landing window (see contentInBase).
A third subject names the fact directly, a commit reachable from the base.

	pr:<owner/repo>#<n>         merged, or closed and merging its head changes nothing
	commit:<sha>                reachable from the base (the repo is the set's :repo)
	commit:<owner/repo>@<sha>   the same, naming its repo

The forge is read through one seam, Runner, which runs `gh`; the merge runs in a
blobless bare repository per repo under Cache, fetched from GitURL with gh as the
credential helper, so only the commits and trees come down and a blob is fetched
when the merge needs it. The tests fake gh and point GitURL at a local repository;
nothing in them touches a network. A run asks each question once: a PR by its
number, its content by its head sha and base, a commit's reachability by its sha,
and it fetches the base once per repo. A question that could not be answered is
UNKNOWN, never no and never yes: no evidence is not negative evidence.
*/
package landed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Runner runs `gh` with args and returns its stdout. It is the one seam to the
// forge: GH runs the real binary, a test hands in a map.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Timeout bounds one gh call. A forge that does not answer inside it leaves the
// criterion unknown.
const Timeout = 60 * time.Second

// GH runs the gh binary directly, never through a shell: the subject is text a
// person wrote.
func GH(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(firstLine(msg))
	}
	return out, nil
}

// Verdict is one criterion's answer. Known is false when the forge could not say;
// Why is one bare token the SET EVAL line prints (merged, content-in-base,
// differs:<path>, open, reachable, not-in-base, or error:<reason>).
type Verdict struct {
	Holds bool
	Known bool
	Why   string
}

// Word is the verdict as the SET EVAL line spells it.
func (v Verdict) Word() string {
	switch {
	case !v.Known:
		return "unknown"
	case v.Holds:
		return "yes"
	}
	return "no"
}

func yes(why string) Verdict     { return Verdict{Holds: true, Known: true, Why: why} }
func no(why string) Verdict      { return Verdict{Known: true, Why: why} }
func unknown(why string) Verdict { return Verdict{Why: why} }

// Evaluator answers subjects against one base, asking each question once per run.
type Evaluator struct {
	Run Runner
	// Repo is the owner/repo a bare `commit:<sha>` is read in: the set's :repo.
	Repo string
	// Base is the branch "in base" means: --base, or the set's :base.
	Base string
	// Cache is the directory the per-repo bare repositories live under
	// (<Cache>/<owner>/<repo>.git). Empty means a closed PR cannot be merged
	// and its criterion is unknown.
	Cache string
	// GitURL is where a repo is fetched from; nil is GitHub over https.
	GitURL func(repo string) string

	prs     map[string]prResult
	content map[string]Verdict
	reach   map[string]Verdict
	bases   map[string]baseResult
}

type baseResult struct {
	dir  string
	tree string
	err  error
}

type pull struct {
	State          string `json:"state"`
	MergedAt       string `json:"merged_at"`
	ClosedAt       string `json:"closed_at"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type prResult struct {
	pr  pull
	err error
}

// New returns an evaluator over one base. cache is where the merge's bare
// repositories are kept; see Evaluator.Cache.
func New(run Runner, repo, base, cache string) *Evaluator {
	return &Evaluator{Run: run, Repo: repo, Base: base, Cache: cache,
		prs: map[string]prResult{}, content: map[string]Verdict{},
		reach: map[string]Verdict{}, bases: map[string]baseResult{}}
}

// Landed answers the `:landed :merged-or-closed-in-base` predicate for one subject.
func (e *Evaluator) Landed(ctx context.Context, subject string) Verdict {
	if strings.HasPrefix(subject, "commit:") {
		repo, sha, err := parseCommit(strings.TrimPrefix(subject, "commit:"), e.Repo)
		if err != nil {
			return unknown("error:" + token(err.Error()))
		}
		return e.reachable(ctx, repo, sha)
	}
	repo, n, _, err := ParsePR(subject)
	if err != nil {
		return unknown("error:" + token(err.Error()))
	}
	res := e.pull(ctx, repo, n)
	if res.err != nil {
		return unknown("error:" + token(res.err.Error()))
	}
	switch {
	case res.pr.MergedAt != "":
		return yes("merged")
	case res.pr.State != "closed":
		return no(token(res.pr.State))
	}
	return e.contentInBase(ctx, repo, n, res.pr.Head.SHA, res.pr.ClosedAt)
}

// MergedAt answers the `:merged :merged-at` predicate: merged through the forge,
// and at the named sha when the subject names one (`pr:<o/r>#<n>@<sha>`). A PR the
// lander closed is NOT merged by this predicate -- that is what :landed is for.
func (e *Evaluator) MergedAt(ctx context.Context, subject string) Verdict {
	repo, n, at, err := ParsePR(subject)
	if err != nil {
		return unknown("error:" + token(err.Error()))
	}
	res := e.pull(ctx, repo, n)
	if res.err != nil {
		return unknown("error:" + token(res.err.Error()))
	}
	if res.pr.MergedAt == "" {
		return no("not-merged")
	}
	if at != "" && !strings.HasPrefix(res.pr.Head.SHA, at) && !strings.HasPrefix(res.pr.MergeCommitSHA, at) {
		return no("merged-at-another-sha")
	}
	return yes("merged")
}

func (e *Evaluator) pull(ctx context.Context, repo string, n int) prResult {
	key := repo + "#" + strconv.Itoa(n)
	if res, ok := e.prs[key]; ok {
		return res
	}
	var res prResult
	out, err := e.Run(ctx, "api", "repos/"+repo+"/pulls/"+strconv.Itoa(n))
	if err != nil {
		res.err = err
	} else if err := json.Unmarshal(out, &res.pr); err != nil {
		res.err = fmt.Errorf("pull %s is not JSON: %v", key, err)
	} else if res.pr.State == "" {
		res.err = fmt.Errorf("pull %s carries no state", key)
	}
	e.prs[key] = res
	return res
}

// contentInBase is the lander's rule: a closed PR landed when merging its head
// into the base changes nothing -- `git merge-tree --write-tree <base> <head>` is
// clean and its tree IS the base's tree. Keyed by head sha: a PR pushed again is
// a new question (and so is one closed at another time: its window differs).
//
// The base moves on after a landing, and a later PR that rewrites the lines this
// one added makes the merge at the TIP conflict although the content did land
// (#2625: an add/add conflict at dev's tip, a no-op merge at 55728dc4, where the
// lander put it). So the same rule is asked, tip first, of each first-parent base
// commit made between the head's commit and the PR's close -- the window the
// lander's own commit lies in, because the lander closes a PR after landing it --
// and holds at the first commit where the merge changes nothing.
func (e *Evaluator) contentInBase(ctx context.Context, repo string, n int, head, closedAt string) Verdict {
	key := repo + "@" + head + "@" + e.Base + "@" + closedAt
	if v, ok := e.content[key]; ok {
		return v
	}
	v := e.mergeChangesNothing(ctx, repo, n, head, closedAt)
	e.content[key] = v
	return v
}

// LandingSlack is how long after the lander's commit the PR's close may come.
const LandingSlack = time.Hour

func (e *Evaluator) mergeChangesNothing(ctx context.Context, repo string, n int, head, closedAt string) Verdict {
	if !isHex(head) {
		return unknown("error:pull-carries-no-head-sha")
	}
	b := e.base(ctx, repo)
	if b.err != nil {
		return unknown("error:" + token(b.err.Error()))
	}
	ref := "refs/nova/pr/" + strconv.Itoa(n)
	if _, err := e.git(ctx, b.dir, "fetch", "-q", "--filter=blob:none", "--no-tags", "origin",
		"+refs/pull/"+strconv.Itoa(n)+"/head:"+ref); err != nil {
		return unknown("error:" + token(err.Error()))
	}
	got, err := e.git(ctx, b.dir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return unknown("error:" + token(err.Error()))
	}
	if strings.TrimSpace(got) != head {
		return unknown("error:pull-head-moved-under-the-fetch")
	}
	tip := e.mergeAt(ctx, b.dir, "refs/heads/"+e.Base, b.tree, head)
	if tip.Holds || !tip.Known {
		return tip
	}
	for _, c := range e.landingWindow(ctx, b.dir, head, closedAt) {
		tree, err := e.git(ctx, b.dir, "rev-parse", "--verify", c+"^{tree}")
		if err != nil {
			return unknown("error:" + token(err.Error()))
		}
		v := e.mergeAt(ctx, b.dir, c, strings.TrimSpace(tree), head)
		if !v.Known {
			return v
		}
		if v.Holds {
			return yes("merge-changes-nothing-at:" + short(c))
		}
	}
	return tip
}

// mergeAt is the rule at one commit: merge-tree clean and its tree that commit's.
func (e *Evaluator) mergeAt(ctx context.Context, dir, at, tree, head string) Verdict {
	out, err := e.git(ctx, dir, "merge-tree", "--write-tree", at, head)
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		// merge-tree's own answer: the merge conflicts, so this commit does not
		// hold the head's change.
		return no("merge-conflicts")
	case err != nil:
		return unknown("error:" + token(err.Error()))
	}
	if firstLine(strings.TrimSpace(out)) != tree {
		return no("merge-changes-base")
	}
	return yes("merge-changes-nothing")
}

// landingWindow is the base's first-parent commits, newest first and tip
// excluded, made after the head's commit and no later than LandingSlack past the
// PR's close. A PR with no close stamp has no window.
func (e *Evaluator) landingWindow(ctx context.Context, dir, head, closedAt string) []string {
	closed, err := time.Parse(time.RFC3339, closedAt)
	if err != nil {
		return nil
	}
	since, err := e.git(ctx, dir, "show", "-s", "--format=%cI", head)
	if err != nil {
		return nil
	}
	out, err := e.git(ctx, dir, "rev-list", "--first-parent",
		"--since="+strings.TrimSpace(since), "--until="+closed.Add(LandingSlack).UTC().Format(time.RFC3339),
		"refs/heads/"+e.Base)
	if err != nil {
		return nil
	}
	tip, _ := e.git(ctx, dir, "rev-parse", "refs/heads/"+e.Base)
	var commits []string
	for _, c := range strings.Fields(out) {
		if c != strings.TrimSpace(tip) {
			commits = append(commits, c)
		}
	}
	return commits
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// base makes (once) the blobless bare repository for repo under Cache and fetches
// the base into it, once per run.
func (e *Evaluator) base(ctx context.Context, repo string) baseResult {
	if b, ok := e.bases[repo]; ok {
		return b
	}
	b := e.fetchBase(ctx, repo)
	e.bases[repo] = b
	return b
}

func (e *Evaluator) fetchBase(ctx context.Context, repo string) baseResult {
	if strings.TrimSpace(e.Cache) == "" {
		return baseResult{err: errors.New("no --cache for the merge")}
	}
	owner, name, _ := strings.Cut(repo, "/")
	dir := filepath.Join(e.Cache, owner, name+".git")
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return baseResult{err: err}
		}
		if _, err := e.git(ctx, "", "init", "-q", "--bare", dir); err != nil {
			return baseResult{err: err}
		}
		for _, kv := range [][2]string{
			{"remote.origin.url", e.url(repo)},
			{"remote.origin.promisor", "true"},
			{"remote.origin.partialclonefilter", "blob:none"},
		} {
			if _, err := e.git(ctx, dir, "config", kv[0], kv[1]); err != nil {
				return baseResult{err: err}
			}
		}
	}
	if _, err := e.git(ctx, dir, "fetch", "-q", "--filter=blob:none", "--no-tags", "origin",
		"+refs/heads/"+e.Base+":refs/heads/"+e.Base); err != nil {
		return baseResult{err: err}
	}
	tree, err := e.git(ctx, dir, "rev-parse", "--verify", "refs/heads/"+e.Base+"^{tree}")
	if err != nil {
		return baseResult{err: err}
	}
	return baseResult{dir: dir, tree: strings.TrimSpace(tree)}
}

func (e *Evaluator) url(repo string) string {
	if e.GitURL != nil {
		return e.GitURL(repo)
	}
	return "https://github.com/" + repo + ".git"
}

// git runs git directly, never through a shell, with gh as the only credential
// helper so the fetch authenticates as whoever gh is, whatever the bench's own git
// credentials say. The -c settings reach the lazy blob fetch merge-tree starts.
func (e *Evaluator) git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, GitTimeout)
	defer cancel()
	full := []string{"-c", "credential.helper=", "-c", "credential.helper=!gh auth git-credential"}
	if dir != "" {
		full = append(full, "-C", dir)
	}
	cmd := exec.CommandContext(ctx, "git", append(full, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(args) > 0 && args[0] == "merge-tree" {
			return string(out), err
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], firstLine(msg))
	}
	return string(out), nil
}

// GitTimeout bounds one git call; the first fetch of a repo's history is the
// longest of them.
const GitTimeout = 5 * time.Minute

// reachable asks whether sha is an ancestor of (or equal to) the base: the compare
// of base...sha is "behind" or "identical" exactly then.
func (e *Evaluator) reachable(ctx context.Context, repo, sha string) Verdict {
	key := repo + "@" + sha + "@" + e.Base
	if v, ok := e.reach[key]; ok {
		return v
	}
	var v Verdict
	out, err := e.Run(ctx, "api", "repos/"+repo+"/compare/"+e.Base+"..."+sha)
	if err != nil {
		v = unknown("error:" + token(err.Error()))
	} else {
		var body struct {
			Status string `json:"status"`
		}
		switch err := json.Unmarshal(out, &body); {
		case err != nil || body.Status == "":
			v = unknown("error:compare-is-not-json")
		case body.Status == "behind" || body.Status == "identical":
			v = yes("reachable")
		default:
			v = no("not-in-base")
		}
	}
	e.reach[key] = v
	return v
}

// ParsePR reads `pr:<owner/repo>#<n>`, with an optional `@<sha>`.
func ParsePR(subject string) (repo string, n int, at string, err error) {
	rest, ok := strings.CutPrefix(subject, "pr:")
	if !ok {
		return "", 0, "", fmt.Errorf("subject %q is not pr:<owner/repo>#<n>", subject)
	}
	rest, at, _ = strings.Cut(rest, "@")
	repo, num, ok := strings.Cut(rest, "#")
	if !ok || !validRepo(repo) {
		return "", 0, "", fmt.Errorf("subject %q is not pr:<owner/repo>#<n>", subject)
	}
	n, err = strconv.Atoi(num)
	if err != nil || n <= 0 {
		return "", 0, "", fmt.Errorf("subject %q names no PR number", subject)
	}
	if at != "" && !isHex(at) {
		return "", 0, "", fmt.Errorf("subject %q names a sha that is not hex", subject)
	}
	return repo, n, at, nil
}

func parseCommit(rest, defaultRepo string) (string, string, error) {
	repo, sha := defaultRepo, rest
	if r, s, ok := strings.Cut(rest, "@"); ok {
		repo, sha = r, s
	}
	if !validRepo(repo) {
		return "", "", fmt.Errorf("commit:%s names no owner/repo and the set declares no :repo", rest)
	}
	if len(sha) < 7 || !isHex(sha) {
		return "", "", fmt.Errorf("commit:%s is not a sha", rest)
	}
	return repo, sha, nil
}

// validRepo is owner/name and nothing else: the repo lands in an API path, so a
// second slash, a query, a fragment or a `..` is refused rather than sent.
func validRepo(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	return ok && owner != "" && name != "" && !strings.Contains(name, "/") &&
		!strings.ContainsAny(repo, " \t\n?#@%") && !strings.Contains(repo, "..")
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// token is a reason squeezed into one bare field: spaces become dashes and it is
// capped, so the SET EVAL line stays one line of name=value pairs.
func token(s string) string {
	s = firstLine(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '=' {
			return '-'
		}
		return r
	}, s)
	if len(s) > 80 {
		s = s[:80]
	}
	if s == "" {
		return "-"
	}
	return s
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
