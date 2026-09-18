package merge

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ONE ENTRY TO THE MERGE QUEUE, AND IT IS A BATCH.
//
// Glenn, 2026-09-18: "nothing reaches the dev merge queue but a batch". That morning four
// pull requests landed on dev that nobody enqueued in the room. They had not been merged
// by hand and they were not batch members: each carried GitHub's AUTO-MERGE, switched on
// hours earlier by a `gh pr merge` call made while the pull request was still red, and the
// forge enqueued them itself the moment their checks went green. Twenty-seven open pull
// requests were carrying the same standing instruction when the sweep found them.
//
// Two things follow, and they are both in this file:
//
//   - THERE IS ONE FUNCTION THAT ADMITS A PULL REQUEST TO A MERGE QUEUE. It is
//     Enqueuer.Enqueue, it speaks the enqueuePullRequest mutation, and it is never `gh pr
//     merge` in any spelling. `gh pr merge --auto` does not enqueue: it leaves an
//     instruction on the forge that fires later, with no caller, which is the one thing a
//     lane cannot reason about. (The tool's own guard has refused --auto since #922;
//     internal/ci's class test now refuses the whole `pr merge` spelling in the tools.)
//
//   - IT REFUSES ANYTHING THAT IS NOT A BATCH. A pull request may be admitted when its
//     head branch is a batch's own -- rowan/integration-* , the shape `nova-merge batch`
//     builds and nothing else does -- or when the caller presents THAT HEAD'S BATCH OK
//     receipt, the line the landing gate printed after it built, vetted, tested and ran
//     the lisp suite over the merged tree. No receipt, no entry. A green card's branch is
//     not an entry: it is a member of a batch somebody has yet to build.
//
// The forge is an interface for the usual two reasons: the tests must be able to say what
// the forge answered, and the one implementation that shells to gh is then a thing a
// reader can check line by line.

// BatchBranchPrefix is the shape of a batch's own branch in this repository: `nova-merge
// batch --name integration-6` builds rowan/integration-6 and pushes nothing, and the
// caller opens the pull request from it. A head under this prefix needs no receipt because
// the branch is one.
const BatchBranchPrefix = "rowan/integration-"

// batchOKPrefix is the gate's verdict line, from cmd/nova-merge/batch.go:
//
//	BATCH OK name=<name> base=<sha> head=<sha> members=<list> dropped=<list>
//
// A BATCH FAIL line carries the same fields and is not a receipt; neither is a line with
// no head, because the receipt's whole job is to say WHICH TREE was tested.
const batchOKPrefix = "BATCH OK "

// batchNoMembers is what the gate writes for an empty list. A batch whose every member was
// dropped is a receipt for the base itself and lands nothing.
const batchNoMembers = "none"

// EnqueuePR is the one pull request an admission names: its number, the head branch the
// forge reports for it, the commit at that head, and the receipt the caller presents when
// the branch is not a batch's own.
type EnqueuePR struct {
	Number  int
	HeadRef string
	HeadSHA string
	Receipt string
}

// BatchReceipt is a BATCH OK line, read.
type BatchReceipt struct {
	Name    string
	Base    string
	Head    string
	Members string
	Dropped string
}

// EnqueueRefusal is this door saying NO: the invocation was readable and what it asked for
// is not allowed. It is a type so a caller can tell a refusal from a forge that timed out
// -- one is a decision and the other is a retry.
type EnqueueRefusal struct {
	PR  int
	Why string
}

func (e *EnqueueRefusal) Error() string {
	return fmt.Sprintf("refusing to enqueue #%d: %s", e.PR, e.Why)
}

// AsEnqueueRefusal reports whether err is this door's refusal.
func AsEnqueueRefusal(err error) (*EnqueueRefusal, bool) {
	var r *EnqueueRefusal
	ok := errors.As(err, &r)
	return r, ok
}

// EnqueueHost is the edge between the one door and the forge: resolve the pull request's
// node id, and run the queue mutation. It holds no merge primitive of any kind, which is
// the point -- a host that cannot merge cannot be talked into merging.
type EnqueueHost interface {
	// PullRequestID resolves the pull request's GraphQL node id, which is what the queue
	// mutation takes in place of a number.
	PullRequestID(ctx context.Context, pr int) (string, error)
	// EnqueuePullRequest adds the pull request to its base branch's merge queue, at the
	// front when jump is true.
	EnqueuePullRequest(ctx context.Context, id string, jump bool) error
}

// Enqueuer is the one door. It is a struct rather than a free function so that the host is
// injected once and every caller in the tools reaches the queue the same way.
type Enqueuer struct{ Host EnqueueHost }

// NewEnqueuer returns the door onto one forge.
func NewEnqueuer(host EnqueueHost) *Enqueuer { return &Enqueuer{Host: host} }

// Enqueue admits one pull request to its base's merge queue, or refuses and says why.
//
// jump puts the entry at the FRONT of the queue. A batch is the whole of a session's
// landing and the entries behind it are members of the next one, so the lander asks for
// the front; nothing else does.
func (e *Enqueuer) Enqueue(ctx context.Context, pr EnqueuePR, jump bool) error {
	if e == nil || e.Host == nil {
		return fmt.Errorf("no forge to enqueue against; this tool never reaches a merge queue except through an EnqueueHost")
	}
	if err := admissible(pr); err != nil {
		return err
	}
	id, err := e.Host.PullRequestID(ctx, pr.Number)
	if err != nil {
		return fmt.Errorf("read pull request %d's node id: %w", pr.Number, err)
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("the forge named no node id for pull request %d, and the queue mutation takes one", pr.Number)
	}
	if err := e.Host.EnqueuePullRequest(ctx, strings.TrimSpace(id), jump); err != nil {
		return fmt.Errorf("enqueue pull request %d: %w", pr.Number, err)
	}
	return nil
}

// admissible is the whole of the rule, decided BEFORE the forge is reached: a refused
// enqueue asks the host nothing at all, so a refusal costs no call and leaves no trace on
// the forge to explain later.
func admissible(pr EnqueuePR) error {
	if pr.Number < 1 {
		return &EnqueueRefusal{PR: pr.Number, Why: fmt.Sprintf("%d is not a pull request's number", pr.Number)}
	}
	batch := IsBatchBranch(pr.HeadRef)
	receipt := strings.TrimSpace(pr.Receipt)
	if receipt == "" {
		if batch {
			return nil
		}
		return &EnqueueRefusal{PR: pr.Number, Why: fmt.Sprintf(
			"its head branch is %q, and the queue takes a batch: a head under %s*, or a --batch receipt (the BATCH OK line) for this very head. Build one: nova-merge batch --name integration-<n> --pr <list> --repo <owner>/<name> --root <dir>",
			oneline.Field(pr.HeadRef), BatchBranchPrefix)}
	}
	rec, err := ParseBatchReceipt(receipt)
	if err != nil {
		return &EnqueueRefusal{PR: pr.Number, Why: err.Error()}
	}
	if rec.Members == batchNoMembers || strings.TrimSpace(rec.Members) == "" {
		return &EnqueueRefusal{PR: pr.Number, Why: fmt.Sprintf(
			"the receipt's members=%s: that batch dropped every member, so its head is the base and it lands nothing", batchNoMembers)}
	}
	// THE RECEIPT IS ABOUT A TREE, and the tree it is about is the one it names. A receipt
	// that does not name this head is evidence about a commit nobody is landing -- the gate
	// ran, somebody pushed once more, and the line still reads OK. It is refused on a batch
	// branch too: a caller who presents a receipt is asking to be checked against it.
	if !IsSHA(pr.HeadSHA) {
		return &EnqueueRefusal{PR: pr.Number, Why: fmt.Sprintf(
			"a receipt was presented and this pull request's head is %q, which is not a 40-character sha to compare it with", oneline.Field(pr.HeadSHA))}
	}
	if !strings.EqualFold(rec.Head, pr.HeadSHA) {
		return &EnqueueRefusal{PR: pr.Number, Why: fmt.Sprintf(
			"the receipt's head=%s is not this pull request's head %s; the gate tested another tree, so run the gate again on this one",
			oneline.Field(rec.Head), oneline.Field(pr.HeadSHA))}
	}
	return nil
}

// IsBatchBranch reports whether a head branch is a batch's own: the batch prefix, a name
// after it, and a ref name git and gh will take (lesson 48 -- a head ref becomes an
// argument).
func IsBatchBranch(ref string) bool {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, BatchBranchPrefix) || len(ref) == len(BatchBranchPrefix) {
		return false
	}
	return ValidRefName(ref) == nil
}

// ParseBatchReceipt reads the landing gate's green line. Every field the gate writes is
// key=value with no spaces inside a value, so the line is read as fields and never as a
// position.
func ParseBatchReceipt(line string) (BatchReceipt, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, batchOKPrefix) {
		return BatchReceipt{}, fmt.Errorf(
			"a receipt is the landing gate's own green line, which begins %q; got %q. A red gate is not a receipt, and neither is a line somebody retyped",
			strings.TrimSpace(batchOKPrefix), oneline.Field(firstWords(line, 3)))
	}
	rec := BatchReceipt{}
	for _, field := range strings.Fields(strings.TrimPrefix(line, batchOKPrefix)) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "name":
			rec.Name = value
		case "base":
			rec.Base = value
		case "head":
			rec.Head = value
		case "members":
			rec.Members = value
		case "dropped":
			rec.Dropped = value
		}
	}
	if rec.Head == "" {
		return BatchReceipt{}, errors.New("the receipt names no head=<sha>, so it is evidence about no particular tree")
	}
	if !IsSHA(rec.Head) {
		return BatchReceipt{}, fmt.Errorf(
			"the receipt's head=%s is not a 40-character sha; a truncated one might match another commit", oneline.Field(rec.Head))
	}
	return rec, nil
}

// firstWords is how much of an unreadable line a refusal quotes back: enough to recognise
// it, never the whole of something that arrived from outside.
func firstWords(line string, n int) string {
	words := strings.Fields(line)
	if len(words) > n {
		words = words[:n]
	}
	return strings.Join(words, " ")
}

// GHEnqueue is the production EnqueueHost, and the production AuditHost: one gh invocation
// per question, through the same mutation guard every other command in this package passes.
//
// It reaches the merge queue through gh's GraphQL edge, which is the only edge that answers
// for a merge queue at all.
type GHEnqueue struct {
	Repo    string
	Timeout time.Duration
	Runner  Runner
}

// NewGHEnqueue returns the one door's forge against one repository.
func NewGHEnqueue(repo string, timeout time.Duration, runner Runner) *GHEnqueue {
	if runner == nil {
		runner = Exec{}
	}
	return &GHEnqueue{Repo: repo, Timeout: timeout, Runner: runner}
}

// gh runs one gh command through the guard, before the command is built.
func (h *GHEnqueue) gh(ctx context.Context, args ...string) (string, error) {
	if err := guard(args, ""); err != nil {
		return "", err
	}
	g := NewGit("", h.Timeout, h.Runner)
	ctx, cancel := contextFor(ctx, h.Timeout)
	defer cancel()
	out, err := g.Runner.Run(ctx, "", "gh", args...)
	if err != nil {
		return out, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, oneLineOf(out))
	}
	return out, nil
}

// contextFor bounds a caller's context by this host's own timeout, so a caller that passed
// context.Background still cannot wait forever on gh.
func contextFor(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = enqueueTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// enqueueTimeout is how long this host waits for gh when a caller named no bound: the same
// default every verb of nova-merge carries, because a subprocess timeout is how long a tool
// waits before SAYING SO rather than a fact about the work.
const enqueueTimeout = 120 * time.Second

// enqueueMutation and enqueueJumpMutation are THE TWO SPELLINGS of the one admission, and
// they are the whole of this tool's write access to a merge queue. jump is absent rather
// than false in the first: the input a forge is sent says what was asked for.
const (
	enqueueMutation     = `mutation($id:ID!){enqueuePullRequest(input:{pullRequestId:$id}){clientMutationId}}`
	enqueueJumpMutation = `mutation($id:ID!){enqueuePullRequest(input:{pullRequestId:$id,jump:true}){clientMutationId}}`
)

// PullRequestID resolves the pull request's GraphQL node id.
func (h *GHEnqueue) PullRequestID(ctx context.Context, pr int) (string, error) {
	out, err := h.gh(ctx, "pr", "view", strconv.Itoa(pr), "-R", h.Repo, "--json", "id", "--jq", ".id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// EnqueuePullRequest runs the queue mutation, at the front of the queue when jump is true.
func (h *GHEnqueue) EnqueuePullRequest(ctx context.Context, id string, jump bool) error {
	query := enqueueMutation
	if jump {
		query = enqueueJumpMutation
	}
	_, err := h.gh(ctx, "api", "graphql", "--raw-field", "query="+query, "--field", "id="+id)
	return err
}

// Head reads the pull request's head branch and head commit, which is what the one door
// checks the batch shape and the receipt against. It is here rather than on the merge Host
// because a caller that only enqueues should not have to stand up a lane's host to do it.
func (h *GHEnqueue) Head(ctx context.Context, pr int) (ref, sha string, err error) {
	out, err := h.gh(ctx, "pr", "view", strconv.Itoa(pr), "-R", h.Repo,
		"--json", "headRefName,headRefOid", "--jq", `[.headRefName,.headRefOid] | @tsv`)
	if err != nil {
		return "", "", err
	}
	ref, sha, _ = strings.Cut(strings.TrimSpace(out), "\t")
	ref, sha = strings.TrimSpace(ref), strings.TrimSpace(sha)
	// Lesson 48: the head ref came from outside and this tool hands it on, so it is checked
	// where it arrives.
	if ref != "" {
		if err := ValidRefName(ref); err != nil {
			return "", "", fmt.Errorf("pull request %d's head branch is not a name this tool hands to gh: %w", pr, err)
		}
	}
	return ref, sha, nil
}
