// Package harvestcopy is the boundary step of a work copy (nova-tools #4227):
// the one outward write a bench makes for a consumer copy. The model's
// contract on a work copy's card (card.RenderCopy) is to change only PATHS,
// commit on the copy's branch in the job's repo checkout, write RESULT.md and
// exit; it never pushes and never opens a PR, and a sandboxed swarm model
// could not (GitHub is a git remote only; the sandbox shims gh). The wrapper
// (nova-card copy, through card.CopyLedger.End) then calls Harvest, which
//
//  1. pushes the commit from the local repo dir to the primary's repository
//     on GitHub as refs/heads/<branch>, idempotently and never with force: a
//     remote branch already at the sha is success, a branch at any other sha
//     is the typed refusal ErrBranchMoved (the rules of the retired card
//     harvest's push script), and the tip is read
//     back after the push so "pushed" means ls-remote showed it. A fix copy
//     (#4270) names the head it built on (Request.Onto): the PR's branch at
//     that head moves forward to the commit, at any other sha it is
//     ErrBranchMoved;
//  2. opens the PR with the GitHub REST API (net/http, a bearer token): an
//     open PR for that head already on the repository is success, else one
//     POST with the primary's BASE as base, its title, and a body carrying
//     STREAM, ORIGIN and DONE-WHEN and ending with the Claude Code line;
//  3. returns the PR number and the head sha, which the caller records on the
//     PR record and ends the copy with (--ok --pr <owner/name>#<n> --head).
//
// The token (GH_PUSH_TOKEN in the nova-card process, TokenEnv) reaches
// git through GIT_ASKPASS: the nova-card binary re-executes itself in askpass
// mode (Askpass, AskpassEnv) and prints the token from its own environment,
// so the token is never in argv and never on disk. A path remote (a local
// bare repository, what a test pushes to) needs no askpass and no credential.
// The harness and the runner never see the token: card.harnessEnv and
// card.runnerEnv strip TokenEnv explicitly.
package harvestcopy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/redis/go-redis/v9"
)

// TokenEnv names the bench's push credential in the nova-card process's
// environment (the fleet's push-credential play sets it for the bench
// session). It is stripped from the harness's and the runner's environment.
const TokenEnv = "GH_PUSH_TOKEN"

// AskpassEnv set to "1" puts the nova-card binary in askpass mode (Askpass).
const AskpassEnv = "NOVA_CARD_ASKPASS"

// ClaudeLine is the last line of every PR body the wrapper opens.
const ClaudeLine = "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

// DefaultAPI is the GitHub REST endpoint a Request without API uses.
const DefaultAPI = gh.DefaultAPI

// The typed refusals. Reason maps any error Harvest returns to the one word
// the copy's end carries.
var (
	// ErrNoToken: the request carries no token; nothing is pushed.
	ErrNoToken = errors.New("no-token")
	// ErrPushRefused: the push did not happen or could not be verified.
	ErrPushRefused = errors.New("push-refused")
	// ErrBranchMoved: the branch on the remote is at another sha; the push
	// never forces. It is also an ErrPushRefused.
	ErrBranchMoved = errors.New("branch-moved")
	// ErrPRRefused: the PR could not be looked up or opened.
	ErrPRRefused = errors.New("pr-refused")
)

// Reason is the typed word for err: no-token, pr-refused, else push-refused
// (ErrBranchMoved included; an error of no type is a push that did not
// happen). The caller's why carries the error text beside it either way.
func Reason(err error) string {
	switch {
	case errors.Is(err, ErrNoToken):
		return "no-token"
	case errors.Is(err, ErrPRRefused):
		return "pr-refused"
	default:
		return "push-refused"
	}
}

// Request is one harvest: the commit, where it is, where it goes and what
// the PR says. The seams (Remote, API, HTTP, Askpass, Git) default to
// GitHub and this binary; a test sets a path remote and an httptest API.
type Request struct {
	// RepoDir is the local checkout holding SHA: the job's out/repo the
	// commit step committed in.
	RepoDir string
	// SHA is the commit to push; Branch is the copy's branch (under nova/).
	SHA, Branch string
	// Onto is the sha the remote branch is expected at before the push: ""
	// for a work copy (the branch does not exist yet), the PR's head for a
	// fix copy (#4270: the fix commits on top of the PR's head and the push
	// moves the PR's own branch forward, never with force). A branch at
	// any other sha is ErrBranchMoved either way.
	Onto string
	// Repo is the primary's repository, owner/name or a bare name under
	// prkey.DefaultOwner.
	Repo string
	// Base is the PR's base (the primary's BASE); Title its title. Stream,
	// Origin and DoneWhen are the primary's, carried in the PR body.
	Base, Title, Stream, Origin, DoneWhen string
	// Token is the push credential and the REST bearer token.
	Token string

	// Remote is the push URL; "" is https://github.com/<owner>/<name>.git.
	// A path or file:// remote is pushed without a credential.
	Remote string
	// API is the REST base; "" is DefaultAPI.
	API string
	// HTTP is the client for the REST calls; nil is a 30 s client.
	HTTP *http.Client
	// Redis counts the REST calls under card-harvest when set.
	Redis redis.Cmdable
	// Askpass is the program git asks for the credential, run with AskpassEnv
	// set and TokenEnv holding Token; "" is this executable (os.Executable).
	Askpass string
	// Git is the git program; "" is "git" on PATH.
	Git string
}

// Result is what one harvest did.
type Result struct {
	// Repo is owner/name; Branch the pushed branch; Head the pushed sha.
	Repo, Branch, Head string
	// PR is the PR number, URL its html_url.
	PR  int
	URL string
	// Push is "pushed" or "already" (the remote branch was at Head).
	Push string
	// Open is "opened" or "already" (an open PR for the head existed).
	Open string
	// Title and Body are what the PR was opened with, set when Open is
	// "opened" (the caller keeps them on the PR record, so a read brief has
	// the body from Redis, nova-tools #4335); "" when the PR already existed.
	Title, Body string
}

// Line is the one-line receipt.
func (r Result) Line() string {
	return fmt.Sprintf("HARVEST %s#%d head=%s branch=%s push=%s pr=%s", r.Repo, r.PR, short(r.Head), r.Branch, r.Push, r.Open)
}

// Harvest pushes the commit and opens the PR (the package comment). Nothing
// is force-pushed, and nothing is done twice: a second call for the same
// commit finds the branch and the PR and returns the same Result.
func Harvest(ctx context.Context, req Request) (Result, error) {
	full, err := prkey.Full(req.Repo)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrPushRefused, err)
	}
	owner, _, _ := prkey.Split(full)
	if strings.TrimSpace(req.Token) == "" {
		return Result{}, fmt.Errorf("%w: %s is empty; the bench cannot push %s or open its PR", ErrNoToken, TokenEnv, req.Branch)
	}
	res := Result{Repo: full, Branch: req.Branch, Head: req.SHA}
	remote := req.Remote
	if remote == "" {
		remote = "https://github.com/" + full + ".git"
	}
	push, err := pushBranch(ctx, req, remote)
	if err != nil {
		return res, err
	}
	res.Push = push
	pr, err := openPR(ctx, req, full, owner)
	if err != nil {
		return res, err
	}
	res.PR, res.URL, res.Open = pr.Number, pr.HTMLURL, pr.open
	if pr.open == "opened" {
		res.Title, res.Body = prTitle(req), PRBody(req)
	}
	return res, nil
}

// Askpass is nova-card's askpass mode, called first thing in main: with
// AskpassEnv "1" it prints the user name for a username prompt and the
// token from TokenEnv for any other, writes nothing else and returns true,
// and the caller exits 0. Otherwise it returns false and nothing happened.
func Askpass(args []string, getenv func(string) string, out io.Writer) bool {
	if getenv(AskpassEnv) != "1" {
		return false
	}
	prompt := strings.ToLower(strings.Join(args, " "))
	if strings.Contains(prompt, "username") {
		fmt.Fprintln(out, "x-access-token")
	} else {
		fmt.Fprintln(out, getenv(TokenEnv))
	}
	return true
}

// networkRemote reports whether remote names a host (a URL scheme or a
// user@host), as opposed to a path on this disk.
func networkRemote(remote string) bool {
	if strings.HasPrefix(remote, "file://") {
		return false
	}
	return strings.Contains(remote, "://") || strings.Contains(remote, "@")
}

// gitEnv is the environment of one git child: base minus every credential
// variable and any GIT_ASKPASS, plus no terminal prompt; for a network
// remote, this binary as askpass with the token in TokenEnv for it. The
// token is nowhere in argv.
func gitEnv(base []string, req Request, network bool) ([]string, error) {
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == TokenEnv, k == AskpassEnv, k == "GIT_ASKPASS", k == "SSH_ASKPASS", k == "GIT_TERMINAL_PROMPT":
			continue
		case strings.Contains(strings.ToUpper(k), "TOKEN"):
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if !network {
		return env, nil
	}
	askpass := req.Askpass
	if askpass == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("%w: askpass program: %v", ErrPushRefused, err)
		}
		askpass = exe
	}
	return append(env, "GIT_ASKPASS="+askpass, AskpassEnv+"=1", TokenEnv+"="+req.Token), nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
