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
//
// The push goes to the URL of the card record's repo (RemoteURL), never to
// the clone's `origin`: a clone the model made itself may have no usable
// origin (superman, quack-0925b: "'origin' does not appear to be a git
// repository", #3712).
type SSHPusher struct {
	SSH            string        // default "ssh"
	RepoSubdir     string        // default "repo"
	ConnectTimeout time.Duration // default 5 s
	// Remote is the push URL for the card's repo; nil is RemoteURL.
	Remote func(repo string) string
}

// RemoteURL is the push URL for a card record's repo: owner/name (a bare name
// is under mas-bandwidth) over https, the URL the harvest clone's own origin
// is set to at staging (internal/swarm/stage.go).
func RemoteURL(repo string) string {
	full := fullRepo(repo)
	if full == "" {
		return ""
	}
	return "https://github.com/" + full + ".git"
}

// pushURL is remote(repo), or RemoteURL when remote is nil.
func pushURL(remote func(string) string, repo string) string {
	if remote != nil {
		return remote(repo)
	}
	return RemoteURL(repo)
}

// RangePusher is a Pusher that also reports the paths the card's commit range
// base..pushed_sha changed, read by git in the harvest clone in the same call
// as the push (#3712). nil paths mean git could not say (no base, a shallow
// clone without it); the push result stands either way.
type RangePusher interface {
	PushRange(ctx context.Context, bench BenchInfo, card Card, base string) ([]string, error)
}

// ErrBranchMoved: the card's branch on the remote is at another sha; the
// harvest never force-pushes (#2932 step a).
var ErrBranchMoved = errors.New("branch-moved")

// pushScript is idempotent: a branch already at the sha is success, a branch
// at any other sha is refused (exit 4) and never overwritten. After a push the
// remote tip is read back; only a tip equal to the sha is PUSH OK (exit 5
// otherwise), so the caller's `pushed` step means ls-remote showed it.
//
// Arguments: dir sha branch [url [base]]. url is the push remote (empty is
// origin, the pre-#3712 shape); with base, the paths base..sha changed follow
// the PUSH line as `PATH <path>` lines, read in the same clone (no line when
// base is not in the clone).
const pushScript = `set -euo pipefail
dir=$1 sha=$2 branch=$3 url=${4:-origin} base=${5:-}
case "$branch" in nova/*) ;; *) echo "PUSH REFUSED $branch: not under nova/" >&2; exit 3 ;; esac
cd "$dir"
git cat-file -e "$sha^{commit}"
paths() {
  if [ -n "$base" ] && git cat-file -e "$base^{commit}" 2>/dev/null; then
    git diff --name-only "$base" "$sha" | sed 's/^/PATH /' || true
  fi
}
remote=$(git ls-remote "$url" "refs/heads/$branch" | cut -f1)
if [ "$remote" = "$sha" ]; then echo "PUSH ALREADY $branch"; paths; exit 0; fi
if [ -n "$remote" ]; then echo "PUSH REFUSED $branch at $remote, not $sha" >&2; exit 4; fi
git push -q "$url" "$sha:refs/heads/$branch"
tip=$(git ls-remote "$url" "refs/heads/$branch" | cut -f1)
if [ "$tip" != "$sha" ]; then echo "PUSH UNVERIFIED $branch at ${tip:-nothing}, not $sha" >&2; exit 5; fi
echo "PUSH OK $branch"
paths
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
	testguard.RefuseHosts(benchsh.Program(p.SSH))
	_, err := p.PushRange(ctx, b, c, "")
	return err
}

// PushRange pushes as Push does and, with base, returns the paths
// base..pushed_sha changed in the same clone, in the same ssh call.
func (p SSHPusher) PushRange(ctx context.Context, b BenchInfo, c Card, base string) ([]string, error) {
	dir, err := p.repoDir(c)
	if err != nil {
		return nil, err
	}
	url := pushURL(p.Remote, c.Repo)
	if url == "" {
		return nil, fmt.Errorf("%s: no repo on the card record to push to", c.Label)
	}
	// A fake ssh runs the script on this machine, so a network push URL
	// would reach the forge from a test: the guard refuses it (a local bare
	// repository is what a test pushes to).
	if strings.Contains(url, "://") || strings.Contains(url, "@") {
		testguard.RefuseHosts("git", "push", url)
	}
	host := b.Host
	if host == "" {
		host = b.Name
	}
	t := benchsh.Target{Host: host, User: b.User, SSH: p.SSH, ConnectTimeout: p.ConnectTimeout}
	testguard.RefuseHosts(benchsh.Program(p.SSH), benchsh.Argv(t, dir, c.PushedSHA, c.Branch, url, base)...)
	out, err := benchsh.Run(ctx, t, pushScript, dir, c.PushedSHA, c.Branch, url, base)
	var ee *benchsh.ExitError
	if errors.As(err, &ee) && ee.Code == 4 {
		return nil, fmt.Errorf("%w: %s", ErrBranchMoved, oneLine(strings.TrimSpace(ee.Output)))
	}
	if err != nil {
		return nil, err
	}
	return PushPaths(out.Output), nil
}

// PushPaths is the `PATH <path>` lines of the push script's output.
func PushPaths(out string) []string {
	var paths []string
	for _, l := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), "PATH "); ok && strings.TrimSpace(p) != "" {
			paths = append(paths, strings.TrimSpace(p))
		}
	}
	return paths
}
