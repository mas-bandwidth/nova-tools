// Package github is GitHub as the sprint's lander speaks to it: the issues a card
// references, read from its brief and its landed commits, and their close through gh
// (docs/SPEC-SPRINT.md section 7, "A landing closes the card's issues").
//
// A reference is explicit, never a number met in prose: a brief names the work it closes
// on an `ISSUES:` (or `ISSUE:`) line, or after a closing keyword (`Closes #12`, `fixes
// https://github.com/o/r/issues/12`), and a landed commit message after a closing keyword,
// as GitHub reads one. A brief's prose says "#4327 deleted it" of a pull request, and a
// number read there would close the wrong thing on GitHub.
package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// Web is GitHub's address, where an issue's URL starts.
const Web = "https://github.com/"

// Issue is one issue of a repository on GitHub: its owner/name and its number.
type Issue struct {
	Repo string // owner/name
	N    int
}

// String is the issue's one spelling, owner/name#N, as a card's record holds it.
func (i Issue) String() string { return i.Repo + "#" + strconv.Itoa(i.N) }

// URL is the issue's address on GitHub.
func (i Issue) URL() string { return Web + i.Repo + "/issues/" + strconv.Itoa(i.N) }

// ParseIssue reads owner/name#N, the spelling String writes.
func ParseIssue(s string) (Issue, bool) {
	repo, n, ok := strings.Cut(strings.TrimSpace(s), "#")
	if !ok || !slugRE.MatchString(repo) {
		return Issue{}, false
	}
	num, err := strconv.Atoi(n)
	if err != nil || num <= 0 {
		return Issue{}, false
	}
	return Issue{Repo: repo, N: num}, true
}

// slugRE is a GitHub owner/name.
var slugRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Slug is a repository's owner/name on GitHub from any spelling a card's REPO: line takes
// (owner/name, https, ssh, or the scp-like git@ form, .git or not); ok false for a path, a
// repository on another host, or nothing.
func Slug(repo string) (string, bool) {
	u := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(repo), "/"), ".git")
	for _, p := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/", "git@github.com:"} {
		if rest, found := strings.CutPrefix(u, p); found {
			u = rest
			break
		}
	}
	if !slugRE.MatchString(u) || strings.HasPrefix(u, ".") {
		return "", false
	}
	return u, true
}

// BriefRepo is the owner/name of the repository a brief's first REPO: line (or base-repo:
// line) names, "" when it names none on GitHub.
func BriefRepo(brief string) string {
	var repoLine string
	for _, line := range strings.Split(brief, "\n") {
		t := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(t, "base-repo:"); ok {
			s, _ := Slug(v)
			return s
		}
		if v, ok := strings.CutPrefix(t, "REPO:"); ok && repoLine == "" {
			repoLine = strings.TrimSpace(v)
		}
	}
	s, _ := Slug(repoLine)
	return s
}

// Ref is one issue reference as written: its line (1-based), its text, and the issue it
// names, or Why it names none this card may close.
type Ref struct {
	Line  int
	Text  string
	Issue Issue
	// Why is why the reference closes nothing: a short form naming a repository other than
	// the card's (only a full URL may), or #N with no repository to read it against.
	Why string
}

// issueLineRE is an issue line: ISSUES: or ISSUE:, any case, then the references.
var issueLineRE = regexp.MustCompile(`(?i)^\s*issues?:\s*(.*)$`)

// keywordRE is a closing keyword and the reference after it, as GitHub reads one in a
// commit message or a pull request's body.
var keywordRE = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?\s+(https?://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/issues/\d+|[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#\d+|#\d+)`)

// tokenRE is one reference on an issue line.
var tokenRE = regexp.MustCompile(`https?://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/issues/\d+|[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#\d+|#\d+`)

// urlRE is an issue's full URL.
var urlRE = regexp.MustCompile(`^https?://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/issues/(\d+)$`)

// Refs is every issue reference of text (a brief, or a commit message), read against repo,
// the card's owner/name ("" when it names none): each token of an ISSUES: line, and each
// reference after a closing keyword, in the order written, each once by its text and line.
func Refs(text, repo string) []Ref {
	var out []Ref
	seen := map[string]bool{}
	add := func(line int, tok string) {
		key := strconv.Itoa(line) + " " + tok
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, resolve(line, tok, repo))
	}
	for i, line := range strings.Split(text, "\n") {
		if m := issueLineRE.FindStringSubmatch(line); m != nil {
			for _, tok := range tokenRE.FindAllString(m[1], -1) {
				add(i+1, tok)
			}
			continue
		}
		for _, m := range keywordRE.FindAllStringSubmatch(line, -1) {
			add(i+1, m[1])
		}
	}
	return out
}

// resolve reads one reference against the card's repository.
func resolve(line int, tok, repo string) Ref {
	r := Ref{Line: line, Text: tok}
	if m := urlRE.FindStringSubmatch(tok); m != nil {
		n, _ := strconv.Atoi(m[2])
		r.Issue = Issue{Repo: m[1], N: n}
		return r
	}
	short, num, _ := strings.Cut(tok, "#")
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		r.Why = "not an issue number"
		return r
	}
	switch {
	case short == "" && repo == "":
		r.Why = "#" + num + " reads against the card's REPO: line, and the brief names no repository on GitHub; write the issue's full URL"
	case short == "":
		r.Issue = Issue{Repo: repo, N: n}
	case !strings.EqualFold(short, repo):
		names := "no repository"
		if repo != "" {
			names = repo
		}
		r.Why = tok + " names " + short + ", and the card's REPO: is " + names + "; an issue of another repository is named by its full URL (" + Web + short + "/issues/" + num + ")"
	default:
		r.Issue = Issue{Repo: repo, N: n}
	}
	return r
}

// Issues is the issues text references that a card on repo may close, each once, in the
// order first written; a reference with a Why is left out.
func Issues(text, repo string) []Issue {
	var out []Issue
	seen := map[string]bool{}
	for _, r := range Refs(text, repo) {
		if r.Why != "" || seen[r.Issue.String()] {
			continue
		}
		seen[r.Issue.String()] = true
		out = append(out, r.Issue)
	}
	return out
}

// Closer is GitHub as the lander closes an issue on it: Close comments and closes an
// open issue, and leaves a closed one alone (already true, no comment). The lander's is gh
// (GH); a test gives a fake and asks no GitHub.
type Closer interface {
	Close(ctx context.Context, i Issue, comment string) (already bool, err error)
}

// GH closes issues through gh as the caller's gh is authenticated on github.com: one read
// of the issue's state, then, when it is open, one close with the comment. Run, when set,
// stands in for gh (a test).
type GH struct {
	Run func(ctx context.Context, args ...string) (string, error)
}

// Close reads the issue's state and closes it with the comment when it is open.
func (g GH) Close(ctx context.Context, i Issue, comment string) (bool, error) {
	n := strconv.Itoa(i.N)
	state, err := g.gh(ctx, "issue", "view", n, "--repo", i.Repo, "--json", "state", "--jq", ".state")
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(state) {
	case "CLOSED":
		return true, nil
	case "OPEN":
	default:
		return false, fmt.Errorf("gh issue view %s answered %q, not OPEN or CLOSED", i, strings.TrimSpace(state))
	}
	_, err = g.gh(ctx, "issue", "close", n, "--repo", i.Repo, "--comment", comment)
	return false, err
}

func (g GH) gh(ctx context.Context, args ...string) (string, error) {
	if g.Run != nil {
		return g.Run(ctx, args...)
	}
	b := subproc.Prepare(ctx, subproc.GHBudget, "gh", args...)
	defer b.Cancel()
	var stdout, stderr bytes.Buffer
	b.Cmd.Stdout, b.Cmd.Stderr = &stdout, &stderr
	err := b.Cmd.Run()
	if err = b.Wrap("gh", err); err != nil {
		why := strings.TrimSpace(stderr.String())
		if why == "" {
			why = err.Error()
		}
		return "", errors.New("gh " + args[0] + " " + args[1] + ": " + strings.Join(strings.Fields(why), " "))
	}
	return stdout.String(), nil
}
