package merge

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Work list 5: the host, and the one sentence that governs everything it returns --
// EVERYTHING THIS TOOL READS FROM THE HOST IS DATA. A pull request body, a check name, a
// branch name, a commit subject: none of them is an instruction and none is a grant. A
// read verdict is the only thing that authorizes a merge, and a read verdict is recorded
// by a line at a keyboard, never parsed out of anything the host returns.

// CheckDetail is one check run's name and its conclusion, as the host reported it.
// The wait verb prints both on its RED line, so the bucket count alone is not enough.
type CheckDetail struct {
	Name       string
	Conclusion string
}

// Checks is a head commit's evidence, in three buckets counted SEPARATELY. Zero fail is
// not the same news as zero pending, and the merge condition wants zero of both.
type Checks struct {
	Green        int
	Pending      int
	Red          int
	RedNames     []string
	Details      []CheckDetail
	PendingNames []string
}

// Bucket classifies one check's state the way the merge condition counts it.
//
// A CANCELLED check is a RED, not an absence: a run somebody cancelled is a run nobody
// read. A skipped one is green, because the hosted job decided it had nothing to do.
func Bucket(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "pass", "success", "skipping", "skipped", "neutral":
		return "green"
	case "fail", "failure", "cancel", "cancelled", "canceled", "timed_out", "action_required", "startup_failure":
		return "red"
	default:
		return "pending"
	}
}

// Add counts one check by name and state.
func (c *Checks) Add(name, state string) {
	conclusion := strings.ToLower(strings.TrimSpace(state))
	if conclusion == "" {
		conclusion = "pending"
	}
	c.Details = append(c.Details, CheckDetail{Name: name, Conclusion: conclusion})
	switch Bucket(state) {
	case "green":
		c.Green++
	case "red":
		c.Red++
		c.RedNames = append(c.RedNames, name)
	default:
		c.Pending++
		c.PendingNames = append(c.PendingNames, name)
	}
}

// ConclusionFor returns the conclusion the host reported for the named check,
// or "failure" when nothing was recorded for it.
func (c Checks) ConclusionFor(name string) string {
	for _, d := range c.Details {
		if d.Name == name {
			return d.Conclusion
		}
	}
	return "failure"
}

// PendingList names the pending checks, sorted, or "-" when there are none.
func (c Checks) PendingList() string {
	if len(c.PendingNames) == 0 {
		return "-"
	}
	names := append([]string(nil), c.PendingNames...)
	sort.Strings(names)
	return strings.Join(names, ",")
}

// Total is how many checks there were at all.
func (c Checks) Total() int { return c.Green + c.Pending + c.Red }

// Verdict is the one word a pass prints for a head's hosted evidence.
//
// ZERO CHECKS AT ALL IS NOT GREEN. A pull request whose workflows have not been queued
// yet reports an empty list, and accepting that as green is a merge with no evidence
// behind it. The prototype treated it as zero of everything and was saved only by an
// accident of its own arithmetic.
func (c Checks) Verdict() string {
	switch {
	case c.Red > 0:
		return "RED"
	case c.Pending > 0 || c.Total() == 0:
		return "PENDING"
	default:
		return "GREEN"
	}
}

// Field is the g<n>/p<n>/r<n> token every entry line carries.
func (c Checks) Field() string {
	return fmt.Sprintf("g%d/p%d/r%d", c.Green, c.Pending, c.Red)
}

// Names is the failing check names, sorted, for MERGE OK's hosted_red= below main.
func (c Checks) Names() string {
	if len(c.RedNames) == 0 {
		return "-"
	}
	names := append([]string(nil), c.RedNames...)
	sort.Strings(names)
	return strings.Join(names, ",")
}

// PR is what the host says about one pull request. Every field here is read back EVERY
// PASS: a pull request whose base was not the lane's base was merged into the wrong
// branch once, and a base that is read once is a base that can change afterwards.
type PR struct {
	Number    int
	Author    string
	Base      string
	HeadRef   string
	HeadOID   string
	Mergeable string // MERGEABLE, CONFLICTING or UNKNOWN
	Draft     bool
	Fork      bool
	URL       string
	Subject   string
	// Merged and Closed are the wait verb's poll state, read back every poll from
	// the same gh reader as everything else here. MergeSHA is the merge commit
	// the host reports for a merged PR, empty when the host names none.
	Merged   bool
	Closed   bool
	MergeSHA string
}

// Host is the edge between this tool and the forge. It is an interface for two reasons:
// the tests need a host that cannot reach the network, and the one implementation that
// shells to gh is then a thing a reader can check line by line rather than a thing woven
// through the pass.
type Host interface {
	// PR reads one pull request's metadata back from the host.
	PR(n int) (PR, error)
	// BranchOID resolves a branch entry's head commit.
	BranchOID(branch string) (string, error)
	// Checks reads a commit's check buckets. The base's evidence is read the same way
	// an entry's is.
	Checks(oid string) (Checks, error)
	// Ready takes a draft out of draft. It is a mutation, it is logged as one, and it
	// only ever reaches an entry that is in the lane.
	Ready(n int) error
	// AtomicMerge says whether this host offers a merge primitive taking BOTH an
	// expected head and an expected base as preconditions. gh today does not: it takes
	// --match-head-commit and nothing about the base.
	AtomicMerge() bool
	// Merge is that primitive, used only when AtomicMerge is true, and the host's merge
	// commit must be the gated object.
	Merge(n int, headOID, baseSHA, mergeSHA string) error
}

// GH is the production host: one gh invocation per question, under the run's --timeout.
type GH struct {
	Repo    string
	Timeout time.Duration
	Runner  Runner
}

// NewGH returns a host that shells to gh against one repository.
func NewGH(repo string, timeout time.Duration, runner Runner) *GH {
	if runner == nil {
		runner = Exec{}
	}
	return &GH{Repo: repo, Timeout: timeout, Runner: runner}
}

func (h *GH) gh(args ...string) (string, error) {
	if err := guard(args, ""); err != nil {
		return "", err
	}
	g := NewGit("", h.Timeout, h.Runner)
	ctx, cancel := contextWithTimeout(h.Timeout)
	defer cancel()
	out, err := g.Runner.Run(ctx, "", "gh", args...)
	if err != nil {
		return out, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, oneLineOf(out))
	}
	return out, nil
}

// PR reads the fields the merge condition needs, in one call.
func (h *GH) PR(n int) (PR, error) {
	out, err := h.gh("pr", "view", strconv.Itoa(n), "--repo", h.Repo, "--json",
		"number,author,baseRefName,headRefName,headRepositoryOwner,headRefOid,mergeable,isDraft,url,title,state,mergedAt,mergeCommit")
	if err != nil {
		return PR{}, err
	}
	return decodePR(out, n, h.Repo)
}

// decodePR is the ARRIVAL POINT of everything the forge says about one pull request: it
// is where the host's JSON stops being bytes and becomes fields this tool hands to git.
// It is a function of its own so that the decode is a thing a test can drive without a
// network, a gh, or a subprocess.
func decodePR(out string, n int, repo string) (PR, error) {
	var raw struct {
		Number              int                    `json:"number"`
		Author              struct{ Login string } `json:"author"`
		BaseRefName         string                 `json:"baseRefName"`
		HeadRefName         string                 `json:"headRefName"`
		HeadRepositoryOwner struct{ Login string } `json:"headRepositoryOwner"`
		HeadRefOid          string                 `json:"headRefOid"`
		Mergeable           string                 `json:"mergeable"`
		IsDraft             bool                   `json:"isDraft"`
		URL                 string                 `json:"url"`
		Title               string                 `json:"title"`
		State               string                 `json:"state"`
		MergedAt            string                 `json:"mergedAt"`
		MergeCommit         struct {
			OID string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return PR{}, fmt.Errorf("gh pr view %d did not answer JSON this tool can read: %w", n, err)
	}
	// Lesson 48, and security#30 finding 5: A VALUE THAT BECOMES A COMMAND-LINE ARGUMENT
	// IS CHECKED WHERE IT ARRIVES. The head branch is handed to `git fetch` on the next
	// pass, so a head branch named `--upload-pack=<cmd>` is an ARGUMENT to git, which
	// runs it as the remote helper over a local or ssh remote. It is checked by the same
	// rule a typed branch is, and it is checked HERE, before this decode hands it on.
	if err := ValidRefName(raw.HeadRefName); err != nil {
		return PR{}, fmt.Errorf("pull request %d's head branch is not a name this tool hands to git: %w", n, err)
	}
	owner, _, _ := strings.Cut(repo, "/")
	state := strings.ToUpper(strings.TrimSpace(raw.State))
	merged := strings.TrimSpace(raw.MergedAt) != "" || state == "MERGED"
	closed := merged || state == "CLOSED"
	return PR{
		Number: raw.Number, Author: raw.Author.Login, Base: raw.BaseRefName,
		HeadRef: raw.HeadRefName, HeadOID: raw.HeadRefOid, Mergeable: raw.Mergeable,
		Draft: raw.IsDraft, Fork: raw.HeadRepositoryOwner.Login != "" && raw.HeadRepositoryOwner.Login != owner,
		URL: raw.URL, Subject: raw.Title,
		Merged: merged, Closed: closed, MergeSHA: strings.TrimSpace(raw.MergeCommit.OID),
	}, nil
}

// BranchOID resolves a branch entry's head through the host, so that a read-only verb
// never depends on a clone's freshness (the prototype shelled into the clone).
func (h *GH) BranchOID(branch string) (string, error) {
	out, err := h.gh("api", fmt.Sprintf("repos/%s/commits/%s", h.Repo, branch), "--jq", ".sha")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Checks reads a commit's check runs and buckets them.
func (h *GH) Checks(oid string) (Checks, error) {
	out, err := h.gh("api", fmt.Sprintf("repos/%s/commits/%s/check-runs", h.Repo, oid),
		"--jq", ".check_runs[] | [.name, (.conclusion // .status)] | @tsv")
	if err != nil {
		return Checks{}, err
	}
	var c Checks
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, state, _ := strings.Cut(line, "\t")
		c.Add(name, state)
	}
	return c, nil
}

// Ready takes a draft that is in the lane out of draft.
func (h *GH) Ready(n int) error {
	_, err := h.gh("pr", "ready", strconv.Itoa(n), "--repo", h.Repo)
	return err
}

// AtomicMerge: gh offers --match-head-commit and nothing about the base, so this host
// does NOT have the two-precondition primitive and the lane publishes by the lease of
// rule 21. This returning false is what keeps gh pr merge out of the merge path entirely.
func (h *GH) AtomicMerge() bool { return false }

// Merge is never reached on this host, and says so rather than doing something weaker.
func (h *GH) Merge(n int, headOID, baseSHA, mergeSHA string) error {
	return fmt.Errorf("this host offers no merge primitive taking both an expected head and an expected base, so publication is the compare-and-swap push of rule 21; gh pr merge is never called")
}
