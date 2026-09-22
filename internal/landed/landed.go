/*
Package landed answers one question about a piece of work: did it land in the base
branch? It is the resolver behind the `:landed` acceptance kind of a work set
(nova-tools #2664) and behind `:merged :merged-at`, evaluated by `nova-work set
check --evaluate`.

GitHub has two ways of saying a PR landed and this house uses both. A PR merged
through the forge carries mergedAt. A PR the lander carried into dev inside an
integration merge is CLOSED, not MERGED (#2614 #2607 #2625 #2631 #2594 #2639 on
2026-09-22), and what says it landed is the lander's own content-in-dev rule: every
file the PR's head touched is byte-identical on the base. The base moves on after a
landing -- the next PR may edit the same file -- so when the tip no longer carries
the head's bytes, the rule is asked again at each recent base commit whose subject
names the PR (`land-1600: 2 approved PRs (#2614 #2607)`): the lander's commit is
where its content-in-dev held. A third subject names the fact directly, a commit
reachable from the base.

	pr:<owner/repo>#<n>         merged, or closed with every head file identical on the base
	commit:<sha>                reachable from the base (the repo is the set's :repo)
	commit:<owner/repo>@<sha>   the same, naming its repo

Every fact is read through one seam, Runner, which runs `gh` with the arguments
given; the tests fake it and never touch a network. A run asks each question once:
a PR by its number, its content by its head sha and base, the base tree by its ref,
a commit's reachability by its sha. A question the forge could not answer is
UNKNOWN, never no and never yes: no evidence is not negative evidence.
*/
package landed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
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

// Evaluator answers subjects against one base, asking the forge each question once
// per run.
type Evaluator struct {
	Run Runner
	// Repo is the owner/repo a bare `commit:<sha>` is read in: the set's :repo.
	Repo string
	// Base is the branch "in base" means: --base, or the set's :base.
	Base string

	prs     map[string]prResult
	content map[string]Verdict
	trees   map[string]treeResult
	reach   map[string]Verdict
	history map[string]historyResult
}

// HistoryDepth is how many base commits, newest first, are searched for one that
// names a PR: one page of the forge's commit list.
const HistoryDepth = 100

type baseCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

type historyResult struct {
	commits []baseCommit
	err     error
}

type pull struct {
	State          string `json:"state"`
	MergedAt       string `json:"merged_at"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type prResult struct {
	pr  pull
	err error
}

type treeResult struct {
	blobs map[string]string
	err   error
}

// New returns an evaluator over one base.
func New(run Runner, repo, base string) *Evaluator {
	return &Evaluator{Run: run, Repo: repo, Base: base,
		prs: map[string]prResult{}, content: map[string]Verdict{},
		trees: map[string]treeResult{}, reach: map[string]Verdict{},
		history: map[string]historyResult{}}
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
	return e.contentInBase(ctx, repo, n, res.pr.Head.SHA)
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

type prFile struct {
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	SHA              string `json:"sha"`
	PreviousFilename string `json:"previous_filename"`
}

// contentInBase is the lander's rule: a closed PR landed when every file its head
// touched is byte-identical on the base -- the same blob at the same path, a
// removed file absent, a renamed file's old path absent. Blob ids are git's own
// content hashes, so equal ids ARE equal bytes. Keyed by head sha: a PR pushed
// again is a new question.
func (e *Evaluator) contentInBase(ctx context.Context, repo string, n int, head string) Verdict {
	key := repo + "@" + head + "@" + e.Base
	if v, ok := e.content[key]; ok {
		return v
	}
	v := e.compareContent(ctx, repo, n)
	e.content[key] = v
	return v
}

func (e *Evaluator) compareContent(ctx context.Context, repo string, n int) Verdict {
	out, err := e.Run(ctx, "api", "--paginate", "repos/"+repo+"/pulls/"+strconv.Itoa(n)+"/files?per_page=100")
	if err != nil {
		return unknown("error:" + token(err.Error()))
	}
	files, err := decodeFiles(out)
	if err != nil {
		return unknown("error:" + token(err.Error()))
	}
	if len(files) == 0 {
		return no("no-files")
	}
	tip := e.tree(ctx, repo, e.Base)
	if tip.err != nil {
		return unknown("error:" + token(tip.err.Error()))
	}
	differs := firstDiff(files, tip.blobs)
	if differs == "" {
		return yes("content-in-base")
	}
	// The tip moved on. Ask again at each base commit that names the PR.
	hist := e.baseHistory(ctx, repo)
	if hist.err != nil {
		return unknown("error:" + token(hist.err.Error()))
	}
	for _, c := range hist.commits {
		if !namesPR(c.Commit.Message, n) {
			continue
		}
		at := e.tree(ctx, repo, c.SHA)
		if at.err != nil {
			return unknown("error:" + token(at.err.Error()))
		}
		if firstDiff(files, at.blobs) == "" {
			return yes("content-in:" + short(c.SHA))
		}
	}
	return no("differs:" + token(differs))
}

// firstDiff is the first path of the PR's head whose bytes the tree does not
// carry, or "" when it carries them all: the same blob at the same path, a
// removed file absent, a renamed file's old path absent.
func firstDiff(files []prFile, blobs map[string]string) string {
	for _, f := range files {
		blob, present := blobs[f.Filename]
		switch {
		case f.Status == "removed":
			if present {
				return f.Filename
			}
		case !present || blob != f.SHA:
			return f.Filename
		}
		if f.PreviousFilename != "" {
			if _, still := blobs[f.PreviousFilename]; still {
				return f.PreviousFilename
			}
		}
	}
	return ""
}

// namesPR reports whether a commit's subject line names #n as a whole number:
// #26 is not #2614.
func namesPR(message string, n int) bool {
	subject := firstLine(message)
	needle := "#" + strconv.Itoa(n)
	for i := strings.Index(subject, needle); i >= 0; {
		end := i + len(needle)
		if end == len(subject) || subject[end] < '0' || subject[end] > '9' {
			return true
		}
		next := strings.Index(subject[end:], needle)
		if next < 0 {
			return false
		}
		i = end + next
	}
	return false
}

// baseHistory is the newest HistoryDepth commits of the base, read once per run.
func (e *Evaluator) baseHistory(ctx context.Context, repo string) historyResult {
	key := repo + "@" + e.Base
	if h, ok := e.history[key]; ok {
		return h
	}
	var h historyResult
	out, err := e.Run(ctx, "api", "repos/"+repo+"/commits?sha="+e.Base+"&per_page="+strconv.Itoa(HistoryDepth))
	if err != nil {
		h.err = err
	} else if err := json.Unmarshal(out, &h.commits); err != nil {
		h.err = fmt.Errorf("commits of %s are not JSON: %v", e.Base, err)
	}
	e.history[key] = h
	return h
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// decodeFiles reads `gh api --paginate`, which prints one JSON array per page back
// to back.
func decodeFiles(out []byte) ([]prFile, error) {
	var all []prFile
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var page []prFile
		err := dec.Decode(&page)
		if errors.Is(err, io.EOF) {
			return all, nil
		}
		if err != nil {
			return nil, fmt.Errorf("pull files are not JSON: %v", err)
		}
		all = append(all, page...)
	}
}

// tree is one whole tree -- the base's, or one base commit's -- path to blob id,
// read once per repo and ref. A tree the forge truncated cannot say a file is
// absent, so it is an error rather than a partial answer.
func (e *Evaluator) tree(ctx context.Context, repo, ref string) treeResult {
	key := repo + "@" + ref
	if t, ok := e.trees[key]; ok {
		return t
	}
	var t treeResult
	out, err := e.Run(ctx, "api", "repos/"+repo+"/git/trees/"+ref+"?recursive=1")
	if err != nil {
		t.err = err
	} else {
		var body struct {
			Truncated bool `json:"truncated"`
			Tree      []struct {
				Path string `json:"path"`
				Type string `json:"type"`
				SHA  string `json:"sha"`
			} `json:"tree"`
		}
		switch err := json.Unmarshal(out, &body); {
		case err != nil:
			t.err = fmt.Errorf("tree of %s is not JSON: %v", ref, err)
		case body.Truncated:
			t.err = fmt.Errorf("tree of %s is truncated", ref)
		default:
			t.blobs = map[string]string{}
			for _, entry := range body.Tree {
				if entry.Type == "blob" {
					t.blobs[entry.Path] = entry.SHA
				}
			}
		}
	}
	e.trees[key] = t
	return t
}

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
