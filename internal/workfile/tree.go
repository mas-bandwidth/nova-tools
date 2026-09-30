// Package workfile is nova-work's tree file: every issue of every repository
// of a source, with its full contents, as one restricted s-expression read by
// internal/worklang (docs/SPEC-WORK-V1.md section 1).
//
// The package holds the model (Tree, Repo, Issue and what an issue carries),
// the canonical writer (Encode), the strict reader (Decode) and the
// field-for-field comparison (Diff). Encode then Decode is the identity on
// every Tree Encode accepts; Encode refuses a value it could not write back
// unchanged rather than writing it lossily. Nothing here reaches a network.
package workfile

import (
	"fmt"
	"strconv"
	"strings"
)

// Format is the tree file's format word, the second element of the top form.
const Format = "v1"

// Tree is the root: the source it was imported from, the organization, the
// time the fetch began, and every repository imported, sorted by name.
type Tree struct {
	Source  string // "github"
	Org     string
	Fetched string // RFC 3339, UTC
	Repos   []Repo
}

// Repo is one repository: owner/name, its URL, whether it is archived, and
// every issue it holds, sorted by number.
type Repo struct {
	Name     string // owner/name
	URL      string
	Archived bool
	Issues   []Issue
}

// Issue is one issue with its full contents. Enumerations (State,
// StateReason, AuthorAssociation, a comment's association, a linked pull
// request's state) keep GitHub's spelling (OPEN, NOT_PLANNED); the file
// writes them as keywords (:open, :not-planned). An empty string is GitHub's
// null.
type Issue struct {
	Number            int
	URL               string
	NodeID            string
	Title             string
	State             string
	StateReason       string
	Origin            string // internal or external (section 1.4)
	Author            string
	AuthorAssociation string
	Created           string
	Updated           string
	Closed            string
	Locked            bool
	LockReason        string
	Labels            []string // sorted
	Assignees         []string // sorted
	Milestone         *Milestone
	Body              string
	Comments          []Comment   // in creation order
	References        []Reference // cross-references to this issue, in timeline order
	LinkedPRs         []LinkedPR  // pull requests that close it, in GitHub's order
}

// Milestone is an issue's milestone.
type Milestone struct {
	Number int
	Title  string
}

// Comment is one comment on an issue.
type Comment struct {
	ID                string
	URL               string
	Author            string
	AuthorAssociation string
	Created           string
	Updated           string
	Body              string
}

// Reference is a cross-reference to the issue from an issue or a pull
// request: the source's kind (Issue or PullRequest), repository, number and
// URL, who made it and when, and whether the source will close the issue.
type Reference struct {
	Kind      string
	Repo      string
	Number    int
	URL       string
	Actor     string
	At        string
	WillClose bool
}

// LinkedPR is a pull request that closes the issue when it merges.
type LinkedPR struct {
	Repo   string
	Number int
	URL    string
	State  string
}

// Web is the source's web root every repository and issue URL starts with.
const Web = "https://github.com/"

// IssueURL is the URL of issue n of repo (owner/name).
func IssueURL(repo string, n int) string { return Web + repo + "/issues/" + strconv.Itoa(n) }

// Path is an issue's path in the tree: repos/<owner>/<repo>/issues/<n>.
func Path(repo string, number int) string {
	return "repos/" + repo + "/issues/" + strconv.Itoa(number)
}

// PathOfURL turns an issue URL into its tree path; the two are one lookup
// in each direction and nothing is written to the source to make them so.
func PathOfURL(url string) (string, error) {
	rest, ok := strings.CutPrefix(url, Web)
	if !ok {
		return "", fmt.Errorf("not a GitHub issue URL: %q", url)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] != "issues" {
		return "", fmt.Errorf("not a GitHub issue URL: %q", url)
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 || strconv.Itoa(n) != parts[3] {
		return "", fmt.Errorf("not a GitHub issue URL: %q", url)
	}
	return Path(parts[0]+"/"+parts[1], n), nil
}

// URLOfPath turns a tree path into the issue's URL on GitHub.
func URLOfPath(path string) (string, error) {
	parts := strings.Split(path, "/")
	if len(parts) != 5 || parts[0] != "repos" || parts[1] == "" || parts[2] == "" || parts[3] != "issues" {
		return "", fmt.Errorf("not a tree issue path: %q", path)
	}
	n, err := strconv.Atoi(parts[4])
	if err != nil || n <= 0 || strconv.Itoa(n) != parts[4] {
		return "", fmt.Errorf("not a tree issue path: %q", path)
	}
	return IssueURL(parts[1]+"/"+parts[2], n), nil
}

// OriginOf is the origin an author association gives: an issue filed by the
// owner, a member or a collaborator is internal, any other is external
// (SPEC-WORK-V1 section 1.4).
func OriginOf(association string) string {
	switch association {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return "internal"
	}
	return "external"
}

// Counts is the size of a tree.
type Counts struct {
	Repos, Issues, Comments, References, LinkedPRs int
}

// Count counts a tree.
func (t *Tree) Count() Counts {
	c := Counts{Repos: len(t.Repos)}
	for _, r := range t.Repos {
		c.Issues += len(r.Issues)
		for _, is := range r.Issues {
			c.Comments += len(is.Comments)
			c.References += len(is.References)
			c.LinkedPRs += len(is.LinkedPRs)
		}
	}
	return c
}

// Repo returns the repository named owner/name.
func (t *Tree) Repo(name string) (*Repo, bool) {
	for i := range t.Repos {
		if t.Repos[i].Name == name {
			return &t.Repos[i], true
		}
	}
	return nil, false
}
