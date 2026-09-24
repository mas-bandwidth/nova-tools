package harvest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/benchsh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// GitHub is the Forge over GitHub REST through `gh api` (GH_CONFIG_DIR picks
// the account). A repo without an owner is under Owner.
type GitHub struct {
	Bin   string // default "gh"
	Owner string // default "mas-bandwidth"
}

type ghPull struct {
	Number   int     `json:"number"`
	HTMLURL  string  `json:"html_url"`
	State    string  `json:"state"`
	MergedAt *string `json:"merged_at"`
	Head     struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
}

func (g GitHub) full(repo string) (full, owner string) {
	owner = g.Owner
	if owner == "" {
		owner = "mas-bandwidth"
	}
	if o, _, ok := strings.Cut(repo, "/"); ok {
		return repo, o
	}
	return owner + "/" + repo, owner
}

func (g GitHub) api(ctx context.Context, args ...string) ([]byte, error) {
	bin := g.Bin
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.CommandContext(ctx, bin, append([]string{"api"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := oneLine(strings.TrimSpace(stderr.String()))
		if ctx.Err() != nil || ambiguousText(msg) {
			return nil, fmt.Errorf("gh api %s: %w: %w: %s", args[len(args)-1], ErrAmbiguous, err, msg)
		}
		return nil, fmt.Errorf("gh api %s: %w: %s", args[len(args)-1], err, msg)
	}
	return stdout.Bytes(), nil
}

// ambiguousText is a REST failure after which the request may have been
// applied: a timeout, a reset connection, a 5xx, or GitHub's own 422 "A pull
// request already exists" (#2932 step d).
func ambiguousText(msg string) bool {
	m := strings.ToLower(msg)
	for _, s := range []string{"http 5", "timeout", "timed out", "connection reset", "eof", "already exists"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

func (p ghPull) pr() PR {
	return PR{Number: p.Number, Head: p.Head.SHA, Ref: p.Head.Ref, URL: p.HTMLURL,
		State: p.State, Merged: p.MergedAt != nil && *p.MergedAt != ""}
}

// ListPRs is the lookup before a create (#2932 step c): every PR, in any
// state, whose head is <owner>:<branch>.
func (g GitHub) ListPRs(ctx context.Context, repo, branch string) ([]PR, error) {
	full, owner := g.full(repo)
	out, err := g.api(ctx, "-X", "GET", "-f", "state=all", "-f", "head="+owner+":"+branch, "repos/"+full+"/pulls")
	if err != nil {
		return nil, err
	}
	var pulls []ghPull
	if err := json.Unmarshal(out, &pulls); err != nil {
		return nil, fmt.Errorf("pulls for %s: %w", branch, err)
	}
	var prs []PR
	for _, p := range pulls {
		if p.Head.Ref == branch {
			prs = append(prs, p.pr())
		}
	}
	return prs, nil
}

// FindOpenPR looks up the open PR whose head is branch (5.4 "PR open").
func (g GitHub) FindOpenPR(ctx context.Context, repo, branch string) (PR, bool, error) {
	full, owner := g.full(repo)
	out, err := g.api(ctx, "-X", "GET", "-f", "state=open", "-f", "head="+owner+":"+branch, "repos/"+full+"/pulls")
	if err != nil {
		return PR{}, false, err
	}
	var pulls []ghPull
	if err := json.Unmarshal(out, &pulls); err != nil {
		return PR{}, false, fmt.Errorf("pulls for %s: %w", branch, err)
	}
	for _, p := range pulls {
		if p.Head.Ref == branch {
			return p.pr(), true, nil
		}
	}
	return PR{}, false, nil
}

// OpenPR opens the PR once; the caller has already looked it up.
func (g GitHub) OpenPR(ctx context.Context, repo, branch, base, title, body string) (PR, error) {
	full, _ := g.full(repo)
	out, err := g.api(ctx, "-X", "POST", "-f", "title="+title, "-f", "head="+branch, "-f", "base="+base,
		"-f", "body="+body, "repos/"+full+"/pulls")
	if err != nil {
		return PR{}, err
	}
	var p ghPull
	if err := json.Unmarshal(out, &p); err != nil || p.Number == 0 {
		return PR{}, fmt.Errorf("open PR on %s: unexpected reply", branch)
	}
	return p.pr(), nil
}

// ReadPR reads the PR back by REST; its head is the verified head.
func (g GitHub) ReadPR(ctx context.Context, repo string, number int) (PR, error) {
	full, _ := g.full(repo)
	out, err := g.api(ctx, "repos/"+full+"/pulls/"+strconv.Itoa(number))
	if err != nil {
		return PR{}, err
	}
	var p ghPull
	if err := json.Unmarshal(out, &p); err != nil || p.Number != number {
		return PR{}, fmt.Errorf("read PR %d: unexpected reply", number)
	}
	return p.pr(), nil
}

// SSHPusher pushes from the bench through internal/benchsh: one
// `ssh <user@host> bash -s -- <quoted args>` per card with the script on
// stdin, so the bench's login shell (zsh on the Macs, #3291) parses nothing of
// ours. The card's git checkout is <results>/<RepoSubdir>, where results is
// the absolute card hash field written by card end (#3329): there is no root.
type SSHPusher struct {
	SSH            string        // default "ssh"
	RepoSubdir     string        // default "repo"
	ConnectTimeout time.Duration // default 5 s
}

// ErrBranchMoved: the card's branch on the remote is at another sha; the
// harvest never force-pushes (#2932 step a).
var ErrBranchMoved = errors.New("branch-moved")

// pushScript is idempotent: a branch already at the sha is success, a branch
// at any other sha is refused (exit 4) and never overwritten. After a push the
// remote tip is read back; only a tip equal to the sha is PUSH OK (exit 5
// otherwise), so the caller's `pushed` step means ls-remote showed it.
const pushScript = `set -euo pipefail
dir=$1 sha=$2 branch=$3
case "$branch" in nova/*) ;; *) echo "PUSH REFUSED $branch: not under nova/" >&2; exit 3 ;; esac
cd "$dir"
git cat-file -e "$sha^{commit}"
remote=$(git ls-remote origin "refs/heads/$branch" | cut -f1)
if [ "$remote" = "$sha" ]; then echo "PUSH ALREADY $branch"; exit 0; fi
if [ -n "$remote" ]; then echo "PUSH REFUSED $branch at $remote, not $sha" >&2; exit 4; fi
git push -q origin "$sha:refs/heads/$branch"
tip=$(git ls-remote origin "refs/heads/$branch" | cut -f1)
if [ "$tip" != "$sha" ]; then echo "PUSH UNVERIFIED $branch at ${tip:-nothing}, not $sha" >&2; exit 5; fi
echo "PUSH OK $branch"
`

func (p SSHPusher) repoDir(c Card) (string, error) {
	// repoDir starts nothing itself, but its receiver names the seam Push
	// reaches through it: guard here too, before Push ever builds the ssh
	// command line, so the seam is refused at every frame that names it.
	testguard.RefuseHosts(benchsh.Program(p.SSH))
	sub := p.RepoSubdir
	if sub == "" {
		sub = "repo"
	}
	if c.Results == "" {
		return "", fmt.Errorf("%s: no results dir", c.Label)
	}
	if !card.AbsResults(c.Results) {
		return "", fmt.Errorf("%s: %w: results %q is not a Unix absolute path; the card hash field results must be absolute (leading /, no //, no backslash, no ..; card end refuses anything else)", c.Label, ErrResultsRelative, c.Results)
	}
	return path.Join(c.Results, sub), nil
}

// ErrResultsRelative: the card hash's results is not absolute; no ssh runs.
var ErrResultsRelative = errors.New("results-relative")

func (p SSHPusher) Push(ctx context.Context, b BenchInfo, c Card) error {
	dir, err := p.repoDir(c)
	if err != nil {
		return err
	}
	host := b.Host
	if host == "" {
		host = b.Name
	}
	t := benchsh.Target{Host: host, User: b.User, SSH: p.SSH, ConnectTimeout: p.ConnectTimeout}
	testguard.RefuseHosts(benchsh.Program(p.SSH), benchsh.Argv(t, dir, c.PushedSHA, c.Branch)...)
	_, err = benchsh.Run(ctx, t, pushScript, dir, c.PushedSHA, c.Branch)
	var ee *benchsh.ExitError
	if errors.As(err, &ee) && ee.Code == 4 {
		return fmt.Errorf("%w: %s", ErrBranchMoved, oneLine(strings.TrimSpace(ee.Output)))
	}
	return err
}
