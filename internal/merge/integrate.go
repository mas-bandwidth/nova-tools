package merge

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// integrate.go is the WRITE SIDE the landing verb needs and the read side does not have.
//
// Host (host.go) is deliberately a reader: it answers what the forge says about a pull
// request, its checks, its verdicts and its runs, and the one mutation on it -- Ready --
// takes a draft out of draft. The hand loop the landing shifts ran on 2026-09-19 needed
// three more, and typed all three by hand, sixteen times in land6's shift alone:
//
//	gh pr create   the batch's own pull request, with the BATCH OK receipt in its body
//	gh pr comment  the cause named on a member's pull request
//	gh pr close    the member, closed with the pointer line to the batch that carried it
//
// They live behind their own interface rather than on Host for the reason the enqueue
// edge does (land.go): the smaller the surface that can write to a forge, the fewer the
// ways something reaches it that nobody meant to. A test drives FakeIntegrateForge and
// reaches no network.
//
// Issue #1845 (L6 of #1725). Law: docs/SPEC-MERGE.md:808-837 and docs/SPEC-DECIDE.md
// reading 3 -- this file writes, it never decides; the hold fold is verdict.go's and
// nothing here re-reads a DISPOSITION line.

// NewPR is one pull request to open: base, head, title, body, and whether it is a draft.
type NewPR struct {
	Base  string
	Head  string
	Title string
	Body  string
	Draft bool
}

// PRRef is a pull request that now exists: its number and its URL.
type PRRef struct {
	Number int
	URL    string
}

// IntegrateForge is the forge's write side: open a pull request, say something on one,
// close one. Every method takes text the caller composed and returns what the forge said.
type IntegrateForge interface {
	// CreatePR opens one pull request and returns its number and URL.
	CreatePR(p NewPR) (PRRef, error)
	// Comment adds one comment to a pull request. It never closes it.
	Comment(n int, body string) error
	// ClosePR closes a pull request WITH a comment: the pointer line is the reason the
	// close is readable at all, so the two are one call and cannot come apart.
	ClosePR(n int, comment string) error
}

// GHIntegrate is the production write side: one gh invocation per write, under the
// caller's timeout, through the same guard every other gh call in this package passes.
type GHIntegrate struct {
	Repo    string
	Timeout time.Duration
	Runner  Runner
}

// NewGHIntegrate returns a write side that shells to gh against one repository.
func NewGHIntegrate(repo string, timeout time.Duration, runner Runner) *GHIntegrate {
	if runner == nil {
		runner = Exec{}
	}
	return &GHIntegrate{Repo: repo, Timeout: timeout, Runner: runner}
}

func (g *GHIntegrate) gh(args ...string) (string, error) {
	if err := guard(args, ""); err != nil {
		return "", err
	}
	git := NewGit("", g.Timeout, g.Runner)
	ctx, cancel := contextWithTimeout(g.Timeout)
	defer cancel()
	out, err := git.Runner.Run(ctx, "", "gh", args...)
	if err != nil {
		return out, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, oneLineOf(out))
	}
	return out, nil
}

// bodyFile writes the body to a temporary file and hands gh --body-file.
//
// THE BODY IS NEVER AN ARGUMENT. A batch body is thousands of characters of receipt and
// basis, it holds newlines and backticks, and a shell-quoted argument of that size is
// both an ARG_MAX question and lesson 48's: a body beginning with a dash would be an
// option. --body-file takes bytes and asks nothing of them.
func (g *GHIntegrate) bodyFile(body string) (string, func(), error) {
	f, err := os.CreateTemp("", "nova-merge-body-*.md")
	if err != nil {
		return "", func() {}, err
	}
	clean := func() { _ = os.Remove(f.Name()) }
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		clean()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		clean()
		return "", func() {}, err
	}
	return f.Name(), clean, nil
}

// CreatePR opens the pull request and reads its number back off the forge rather than
// out of the URL gh prints: the number is what every later call takes, and parsing it
// out of a string is one forge change away from a wrong number.
func (g *GHIntegrate) CreatePR(p NewPR) (PRRef, error) {
	if err := ValidRefName(p.Head); err != nil {
		return PRRef{}, fmt.Errorf("the head branch is not a name this tool hands to a forge: %w", err)
	}
	if err := ValidRefName(p.Base); err != nil {
		return PRRef{}, fmt.Errorf("the base branch is not a name this tool hands to a forge: %w", err)
	}
	path, clean, err := g.bodyFile(p.Body)
	if err != nil {
		return PRRef{}, err
	}
	defer clean()
	args := []string{"pr", "create", "--repo", g.Repo, "--base", p.Base, "--head", p.Head,
		"--title", p.Title, "--body-file", path}
	if p.Draft {
		args = append(args, "--draft")
	}
	if _, err := g.gh(args...); err != nil {
		return PRRef{}, err
	}
	out, err := g.gh("pr", "view", p.Head, "--repo", g.Repo, "--json", "number,url")
	if err != nil {
		return PRRef{}, err
	}
	var raw struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return PRRef{}, fmt.Errorf("gh pr view %s did not answer JSON this tool can read: %w", p.Head, err)
	}
	if raw.Number < 1 {
		return PRRef{}, fmt.Errorf("gh pr view %s answered no pull request number", p.Head)
	}
	return PRRef{Number: raw.Number, URL: raw.URL}, nil
}

// Comment says one thing on one pull request.
func (g *GHIntegrate) Comment(n int, body string) error {
	path, clean, err := g.bodyFile(body)
	if err != nil {
		return err
	}
	defer clean()
	_, err = g.gh("pr", "comment", strconv.Itoa(n), "--repo", g.Repo, "--body-file", path)
	return err
}

// ClosePR closes a pull request with its comment. gh takes both in one call, so the
// pointer line and the close cannot come apart -- a member closed with no pointer is a
// member nobody can trace to the batch that carried it.
func (g *GHIntegrate) ClosePR(n int, comment string) error {
	path, clean, err := g.bodyFile(comment)
	if err != nil {
		return err
	}
	defer clean()
	_, err = g.gh("pr", "close", strconv.Itoa(n), "--repo", g.Repo, "--comment-file", path)
	return err
}

// FakeIntegrateForge is the write side with no network in it: it records every write and
// hands back the numbers a test set. It is in the package proper, beside FakeHost, so
// that cmd/nova-merge's tests can drive it too.
type FakeIntegrateForge struct {
	// Created is every pull request opened, in order.
	Created []NewPR
	// Comments is every comment, in order: the pull request and the body.
	Comments []FakeComment
	// Closed is every pull request closed, in order, with the comment it was closed on.
	Closed []FakeComment

	// NextNumber is the number the next CreatePR answers; it increments.
	NextNumber int
	// CreateErr, CommentErr and CloseErr are what those calls fail with when set.
	CreateErr  error
	CommentErr error
	CloseErr   error
}

// FakeComment is one write of text at one pull request.
type FakeComment struct {
	PR   int
	Body string
}

// NewFakeIntegrateForge returns a fake whose first pull request is #9001 -- a number no
// fixture's member carries, so a test that confuses the batch with a member says so.
func NewFakeIntegrateForge() *FakeIntegrateForge {
	return &FakeIntegrateForge{NextNumber: 9001}
}

func (f *FakeIntegrateForge) CreatePR(p NewPR) (PRRef, error) {
	if f.CreateErr != nil {
		return PRRef{}, f.CreateErr
	}
	f.Created = append(f.Created, p)
	if f.NextNumber < 1 {
		f.NextNumber = 9001
	}
	n := f.NextNumber
	f.NextNumber++
	return PRRef{Number: n, URL: fmt.Sprintf("https://example.invalid/pull/%d", n)}, nil
}

func (f *FakeIntegrateForge) Comment(n int, body string) error {
	if f.CommentErr != nil {
		return f.CommentErr
	}
	f.Comments = append(f.Comments, FakeComment{PR: n, Body: body})
	return nil
}

func (f *FakeIntegrateForge) ClosePR(n int, comment string) error {
	if f.CloseErr != nil {
		return f.CloseErr
	}
	f.Closed = append(f.Closed, FakeComment{PR: n, Body: comment})
	return nil
}
