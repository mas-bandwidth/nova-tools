// Package report provides idempotent PR body and verdict comment posting
// for harvested cards. A finished card's six-line report lands in its PR
// body exactly once (a second run leaves the body digest unchanged), and
// a DONE read card's verdict is one typed comment at the PR's exact head,
// never posted twice.
package report

import (
	"fmt"
	"strings"
	"sync"
)

// Forge is the interface a PR backend must implement for idempotent posting.
type Forge interface {
	// FindPR returns the PR number for a repo and branch, or 0 if none exists.
	FindPR(repo, branch string) (int, error)
	// CreatePR opens a new PR and returns its number.
	CreatePR(repo, base, branch, title, body string) (int, error)
	// GetPRBody returns the current body of the given PR.
	GetPRBody(repo string, pr int) (string, error)
	// UpdatePRBody replaces the body of the given PR.
	UpdatePRBody(repo string, pr int, body string) error
	// ListComments returns the comment bodies on the given PR.
	ListComments(repo string, pr int) ([]string, error)
	// PostComment adds a comment to the given PR.
	PostComment(repo string, pr int, body string) error
}

// HeadChecker is an optional extension of Forge. When the backend implements
// it, PostVerdictOnce verifies that the head it was given is the PR's live
// head before posting, and refuses a stale head.
type HeadChecker interface {
	// PRHead returns the current head commit SHA of the given PR.
	PRHead(repo string, pr int) (string, error)
}

// verdictLocks serializes PostVerdictOnce per repo and PR, so two runs in
// the same process cannot both observe "no verdict yet" and both post.
var verdictLocks sync.Map // "repo#pr" -> *sync.Mutex

func verdictLock(repo string, pr int) *sync.Mutex {
	m, _ := verdictLocks.LoadOrStore(fmt.Sprintf("%s#%d", repo, pr), &sync.Mutex{})
	return m.(*sync.Mutex)
}

// EnsurePRBody posts the report body to the PR exactly once. If the PR does
// not exist it is created with the given body. If the PR exists but its body
// differs, the body is replaced. If the body is already what it should be,
// nothing is changed and the digest is unchanged.
//
// It returns the PR number and whether a change was made.
func EnsurePRBody(f Forge, repo, base, branch, title, body string) (int, bool, error) {
	pr, err := f.FindPR(repo, branch)
	if err != nil {
		return 0, false, err
	}
	if pr == 0 {
		n, err := f.CreatePR(repo, base, branch, title, body)
		return n, true, err
	}
	existing, err := f.GetPRBody(repo, pr)
	if err != nil {
		return pr, false, err
	}
	if existing == body {
		return pr, false, nil
	}
	return pr, true, f.UpdatePRBody(repo, pr, body)
}

// PostVerdictOnce posts a read card's verdict as a single typed comment on
// the PR. If a comment with the same content already exists on the PR, it is
// not posted again.
//
// Atomicity: the list-then-post check is serialized per repo and PR inside
// this process, so concurrent calls in one process post at most once. The
// Forge offers no atomic compare-and-post, so callers in DIFFERENT processes
// or hosts must serialize on the same PR themselves (for example a lease on
// the PR held for the duration of the call); this package cannot.
//
// Head: the caller must pass the PR's verified current head. If the Forge
// also implements HeadChecker, the live head is fetched and a mismatch is an
// error; nothing is posted for a stale head.
//
// The comment format is "PR<pr>: <verdict> head=<sha>" (e.g.
// "PR2509: APPROVE head=abc123").
func PostVerdictOnce(f Forge, repo string, pr int, verdict, head string) (bool, error) {
	if pr <= 0 {
		return false, fmt.Errorf("PostVerdictOnce: pr must be positive, got %d", pr)
	}
	if verdict != "APPROVE" && verdict != "HOLD" {
		return false, fmt.Errorf("PostVerdictOnce: verdict must be APPROVE or HOLD, got %q", verdict)
	}
	if head == "" {
		return false, fmt.Errorf("PostVerdictOnce: head must be non-empty")
	}
	comment := fmt.Sprintf("PR%d: %s head=%s", pr, verdict, head)

	mu := verdictLock(repo, pr)
	mu.Lock()
	defer mu.Unlock()

	if hc, ok := f.(HeadChecker); ok {
		live, err := hc.PRHead(repo, pr)
		if err != nil {
			return false, err
		}
		if live != head {
			return false, fmt.Errorf("PostVerdictOnce: head %s is not the live head %s of PR %d", head, live, pr)
		}
	}

	comments, err := f.ListComments(repo, pr)
	if err != nil {
		return false, err
	}
	for _, c := range comments {
		if strings.TrimSpace(c) == comment {
			return false, nil
		}
	}
	return true, f.PostComment(repo, pr, comment)
}
