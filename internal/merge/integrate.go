package merge

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
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

// THE BODY IS AN ARGUMENT AND NEVER A FILE THIS PACKAGE WROTE.
//
// `--body-file` would be the natural spelling and it is the one the hand loop typed, but
// rule 7 of docs/SPEC-MERGE.md holds this package to four writing sites, each named in
// internal/merge/source_test.go: the lane's state, its log, its lock and the rebase
// marker. A temp file for a pull request body is a fifth, and a rule whose exemption list
// grows whenever somebody needs one is not a rule. `--body <text>` costs nothing here: it
// is one argv element, so a body beginning with a dash is a body and not an option
// (lesson 48), and a batch body is kilobytes against an ARG_MAX of megabytes.

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
	args := []string{"pr", "create", "--repo", g.Repo, "--base", p.Base, "--head", p.Head,
		"--title", p.Title, "--body", p.Body}
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
	_, err := g.gh("pr", "comment", strconv.Itoa(n), "--repo", g.Repo, "--body", body)
	return err
}

// ClosePR closes a pull request with its comment. gh takes both in one call, so the
// pointer line and the close cannot come apart -- a member closed with no pointer is a
// member nobody can trace to the batch that carried it.
func (g *GHIntegrate) ClosePR(n int, comment string) error {
	_, err := g.gh("pr", "close", strconv.Itoa(n), "--repo", g.Repo, "--comment", comment)
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

// IntegrationBody is the batch pull request's body: the gate's receipt verbatim, the
// caller's basis file whole, the lease said out loud, and the hold read named.
//
// IT IS PROSE AND IT LIVES HERE RATHER THAN IN cmd/nova-merge, where every printed
// argument goes through internal/oneline and nothing may write bytes past that path
// (cmd/nova-merge/audit_test.go). That rule is right for a tool whose stdout a harness
// parses, and a markdown body escaped for a one-line grammar would be a body nobody
// could read. The caller's basis is copied WORD FOR WORD and never summarised: what a
// member landed on is a judgement, and a tool that composed that sentence would be a
// tool asserting a read it did not do.
type IntegrationBody struct {
	Bench     string
	Receipt   string
	Basis     string
	Branch    string
	Head      string
	Lane      string
	Reviewers string
}

// Render writes the body.
func (b IntegrationBody) Render() string {
	var out strings.Builder
	out.WriteString("Gated on `" + b.Bench + "` by `nova-merge integrate` (#1845).\n\n")
	out.WriteString("## The gate's receipt\n\n```\n" + b.Receipt + "\n```\n\n")
	out.WriteString("## The basis, per member\n\n" + b.Basis)
	if !strings.HasSuffix(b.Basis, "\n") {
		out.WriteString("\n")
	}
	out.WriteString("\n## The push\n\n`" + b.Branch + "` was pushed at `" + b.Head +
		"` under a **must-not-exist lease**: `origin` held no ref of that name before the push " +
		"(`ls-remote` count 0), the push was a plain one and never a force, and the ref read back `" +
		b.Head + "`.\n\n")
	out.WriteString("## The hold read\n\nThe members were read for holds twice — once at admission and " +
		"once at the door — through `internal/merge`'s verdict fold (`LoadLaneVerdicts`, `Host.Verdicts`, " +
		"`UnliftedHolds`), over the lane `" + b.Lane + "` and the reviewer file `" + b.Reviewers +
		"`. No flag in this verb lifts a hold.\n")
	return out.String()
}

// MemberPointer is the comment a member is closed with: which batch carried it, the
// gate's receipt, the head that did not move, and where its basis is. Prose, for the
// reason IntegrationBody is.
type MemberPointer struct {
	Batch     int
	Name      string
	Bench     string
	Head      string
	BatchHead string
	Base      string
	Members   string
	Receipt   string
}

// Render writes the pointer comment.
func (p MemberPointer) Render() string {
	n := strconv.Itoa(p.Batch)
	var out strings.Builder
	out.WriteString("Landed in batch #" + n + " (`" + p.Name + "`), gated on `" + p.Bench + "`.\n\n")
	out.WriteString("- head: `" + p.Head + "` — the head this landing folded, and it did not move between the read and the door\n")
	out.WriteString("- batch head: `" + p.BatchHead + "`, base `" + p.Base + "`\n")
	out.WriteString("- members: `" + p.Members + "`\n")
	out.WriteString("- basis: stated per member in #" + n + "'s body\n\n")
	out.WriteString("The gate's receipt:\n\n```\n" + p.Receipt + "\n```\n\n")
	out.WriteString("Closed by `nova-merge integrate` (#1845): the work is on the batch, not lost.\n")
	return out.String()
}

// SensitiveRule is one row of the tracked sensitive-prefix policy: a repository-relative
// path prefix and the one reviewer whose non-author APPROVE at the member's current head
// is the only thing that admits a member whose diff touches that prefix.
type SensitiveRule struct {
	Prefix string
	Who    string
}

// LoadSensitivePolicy reads the sensitive-prefix policy from the EXACT trusted base
// commit (`.nova/merge-sensitive.tsv`), never from a caller path or a member's own diff:
// a member that could edit the file that judges it would judge itself.
//
// Columns are `prefix<TAB>who`, one rule per line; blanks and lines beginning with `#`
// are ignored. A base commit that carries NO such file means the repository declares no
// sensitive prefixes, and the caller treats that as `unchecked` (the landing verb's
// typed line says so). A file that is present but declares no rule is a refusal, not a
// pass: "an empty rule would pass everything". Malformed rows, a prefix that escapes the
// repository (a `..` climb or an absolute path), and two rows whose prefixes overlap
// ambiguously are each refused by name.
func LoadSensitivePolicy(repoRoot, base string, timeout time.Duration) ([]SensitiveRule, error) {
	g := NewGit(repoRoot, timeout, nil)
	if _, err := g.Run("fetch", "--quiet", "origin", base); err != nil {
		return nil, fmt.Errorf("the base %q could not be fetched to read its sensitive policy: %w", base, err)
	}
	raw, err := g.Run("show", "origin/"+base+":.nova/merge-sensitive.tsv")
	if err != nil {
		if strings.Contains(strings.ToLower(raw), "does not exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("the tracked sensitive policy at %s:.nova/merge-sensitive.tsv could not be read: %w", base, err)
	}
	var rules []SensitiveRule
	seenPrefix := map[string]string{}
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		prefix, who, ok := strings.Cut(line, "\t")
		prefix = strings.TrimSpace(prefix)
		who = strings.TrimSpace(who)
		if !ok || prefix == "" || who == "" {
			return nil, fmt.Errorf("line %d of .nova/merge-sensitive.tsv is not `prefix<TAB>who`, got %q", i+1, line)
		}
		if safepath.HasDotDot(prefix) || strings.HasPrefix(prefix, "/") || strings.HasPrefix(prefix, `\`) {
			return nil, fmt.Errorf("line %d of .nova/merge-sensitive.tsv names prefix %q, which escapes the repository", i+1, prefix)
		}
		if prior, seen := seenPrefix[prefix]; seen && prior != who {
			return nil, fmt.Errorf("prefix %q is declared twice with different reviewers (%q and %q)", prefix, prior, who)
		}
		seenPrefix[prefix] = who
		rules = append(rules, SensitiveRule{Prefix: prefix, Who: who})
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf(".nova/merge-sensitive.tsv declares no rule; an empty rule would pass everything")
	}
	for _, a := range rules {
		for _, b := range rules {
			if a.Prefix == b.Prefix {
				continue
			}
			if strings.HasPrefix(a.Prefix, b.Prefix) || strings.HasPrefix(b.Prefix, a.Prefix) {
				return nil, fmt.Errorf("the prefixes %q and %q overlap ambiguously; a diff under one must belong to exactly one row", a.Prefix, b.Prefix)
			}
		}
	}
	return rules, nil
}
