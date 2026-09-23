package harvest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// GitHub is the Forge over GitHub REST through `gh api` (GH_CONFIG_DIR picks
// the account). A repo without an owner is under Owner.
type GitHub struct {
	Bin   string // default "gh"
	Owner string // default "mas-bandwidth"
}

type ghPull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Head    struct {
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
		return nil, fmt.Errorf("gh api %s: %w: %s", args[len(args)-1], err, oneLine(strings.TrimSpace(stderr.String())))
	}
	return stdout.Bytes(), nil
}

func (p ghPull) pr() PR { return PR{Number: p.Number, Head: p.Head.SHA, URL: p.HTMLURL} }

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

// SSHPusher pushes from the bench over one `ssh <user@host> bash -s` call
// per card (never zsh: the remote runs bash with the script on stdin). The
// card's git checkout is <results>/<RepoSubdir>, where results is the
// absolute card hash field written by card end (#3329): there is no root.
type SSHPusher struct {
	SSH            string        // default "ssh"
	RepoSubdir     string        // default "repo"
	ConnectTimeout time.Duration // default 5 s
}

// pushScript is idempotent: a branch already at the sha is success, a branch
// at any other sha is refused (exit 4) and never overwritten.
const pushScript = `set -euo pipefail
dir=$1 sha=$2 branch=$3
case "$branch" in nova/*) ;; *) echo "PUSH REFUSED $branch: not under nova/" >&2; exit 4 ;; esac
cd "$dir"
git cat-file -e "$sha^{commit}"
remote=$(git ls-remote origin "refs/heads/$branch" | cut -f1)
if [ "$remote" = "$sha" ]; then echo "PUSH ALREADY $branch"; exit 0; fi
if [ -n "$remote" ]; then echo "PUSH REFUSED $branch at $remote, not $sha" >&2; exit 4; fi
git push -q origin "$sha:refs/heads/$branch"
echo "PUSH OK $branch"
`

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (p SSHPusher) repoDir(c Card) (string, error) {
	// repoDir starts nothing itself, but its receiver names the seam Push
	// reaches through it: guard here too, before Push ever builds the ssh
	// command line, so the seam is refused at every frame that names it.
	bin := p.SSH
	if bin == "" {
		bin = "ssh"
	}
	testguard.RefuseHosts(bin)
	sub := p.RepoSubdir
	if sub == "" {
		sub = "repo"
	}
	if c.Results == "" {
		return "", fmt.Errorf("%s: no results dir", c.Label)
	}
	if !path.IsAbs(c.Results) {
		return "", fmt.Errorf("%s: results %s is relative; the card hash field results must be absolute (card end refuses a relative dir)", c.Label, c.Results)
	}
	return path.Join(c.Results, sub), nil
}

func (p SSHPusher) Push(ctx context.Context, b BenchInfo, c Card) error {
	dir, err := p.repoDir(c)
	if err != nil {
		return err
	}
	host := b.Host
	if host == "" {
		host = b.Name
	}
	target := host
	if b.User != "" {
		target = b.User + "@" + host
	}
	timeout := p.ConnectTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	bin := p.SSH
	if bin == "" {
		bin = "ssh"
	}
	remote := "bash -s -- " + shellQuote(dir) + " " + shellQuote(c.PushedSHA) + " " + shellQuote(c.Branch)
	args := []string{"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(int(timeout.Seconds())), target, remote}
	testguard.RefuseHosts(bin, args...)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(pushScript)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh %s: %w: %s", target, err, oneLine(strings.TrimSpace(out.String())))
	}
	return nil
}
