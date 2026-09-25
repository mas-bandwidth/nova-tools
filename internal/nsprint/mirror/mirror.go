// Package mirror keeps a bench's bare --mirror clones under ~/nova-bench/mirror
// fresh and records each pass in Redis (nova-tools #2922). It replaces
// rowan-tools bin/mirror-refresh.
//
// One bench is the source (space, the remote cache): it alone fetches GitHub.
// Every other bench fetches from the source's mirror (`space:nova-bench/mirror`
// over ssh), so the fleet makes one GitHub fetch per repo per tick, not seven.
// Each pass writes one receipt, the hash bench:<b>:mirrors (at from Redis TIME,
// then per repo <repo>, <repo>:from, <repo>:pulls, <repo>:err), in one pipeline.
//
// A failed refresh cannot corrupt a mirror: a first clone lands in a tmp dir and
// is renamed into place only after it passes Check; a refresh is `git fetch
// --atomic`, so every ref moves or none does; and a mirror is never shallow
// (rowan-tools#275): no argv here carries a shallow flag, and a shallow mirror
// is repaired by --unshallow, else re-mirrored and swapped in.
package mirror

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// Refspecs are fixed: every branch and every pull-request head.
var Refspecs = []string{"+refs/heads/*:refs/heads/*", "+refs/pull/*/head:refs/pull/*/head"}

// DefaultRepos is the pair every bench must hold, each with the ref it is judged by.
const DefaultRepos = "nova-tools:dev,schema:main"

// DefaultUpstream is where the source fetches: <upstream>/<repo>.git.
const DefaultUpstream = "https://github.com/mas-bandwidth"

// gitSSHCommand is what a follower's fetch from the source runs under when the
// environment names none: BatchMode, so a missing key fails in seconds, never a prompt.
const gitSSHCommand = "ssh -o BatchMode=yes -o ConnectTimeout=8"

// Repo is one mirrored repository and the ref its tip is read from.
type Repo struct{ Name, Ref string }

var repoName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ParseRepos reads "nova-tools:dev,schema:main".
func ParseRepos(s string) ([]Repo, error) {
	var out []Repo
	seen := map[string]bool{}
	for _, item := range strings.Split(s, ",") {
		name, ref, ok := strings.Cut(strings.TrimSpace(item), ":")
		if !ok || !repoName.MatchString(name) || ref == "" || strings.ContainsAny(ref, " \t") || name == "." || name == ".." {
			return nil, fmt.Errorf("--repos item %q: want <repo>:<ref>", item)
		}
		if seen[name] {
			return nil, fmt.Errorf("--repos names %s twice", name)
		}
		seen[name] = true
		out = append(out, Repo{name, ref})
	}
	return out, nil
}

// Git runs one git command and returns its combined output.
type Git interface {
	Run(ctx context.Context, args ...string) (string, error)
}

// ExecGit is the real git. Log, when set, sees every argv before it runs.
type ExecGit struct {
	Timeout time.Duration
	Log     func(args []string)
}

// Run execs git with no prompt and a low-speed abort, so a hung transfer ends.
// A fetch or clone from a remote URL is a host seam: the testguard refuses it
// under NOVA_TEST_NO_HOST, and tests use local fixture paths.
func (g ExecGit) Run(ctx context.Context, args ...string) (string, error) {
	for _, a := range args {
		if IsRemoteURL(a) {
			testguard.RefuseHosts("git", args...)
			break
		}
	}
	if g.Log != nil {
		g.Log(append([]string(nil), args...))
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_HTTP_LOW_SPEED_LIMIT=1000", "GIT_HTTP_LOW_SPEED_TIME=30")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND="+gitSSHCommand)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// IsRemoteURL reports whether s names another machine: a scheme other than
// file://, or scp-like host:path. A local path is not remote.
func IsRemoteURL(s string) bool {
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") {
		return false
	}
	if i := strings.Index(s, "://"); i > 0 {
		return s[:i] != "file"
	}
	host, _, ok := strings.Cut(s, ":")
	return ok && host != "" && !strings.Contains(host, "/")
}

// Config is one bench's pass.
type Config struct {
	Bench     string // this bench
	Source    string // the source bench's name: the one that fetches Upstream
	SourceURL string // where followers fetch: <SourceURL>/<repo>.git
	Upstream  string // where the source fetches: <Upstream>/<repo>.git
	Dir       string // mirror = <Dir>/<repo>.git
	Repos     []Repo
	Git       Git
}

// IsSource reports whether this bench is the one that talks to GitHub.
func (c Config) IsSource() bool { return c.Bench == c.Source }

func (c Config) remote(r Repo) (url, from string) {
	if c.IsSource() {
		return strings.TrimSuffix(c.Upstream, "/") + "/" + r.Name + ".git", "github"
	}
	return strings.TrimSuffix(c.SourceURL, "/") + "/" + r.Name + ".git", c.Source
}

// Validate names the first missing input.
func (c Config) Validate() error {
	switch {
	case c.Bench == "":
		return fmt.Errorf("--bench is required")
	case c.Source == "":
		return fmt.Errorf("--source <bench> is required: the one bench that fetches GitHub")
	case !c.IsSource() && c.SourceURL == "":
		return fmt.Errorf("--source-url is required on a follower (for example space:nova-bench/mirror)")
	case c.IsSource() && c.Upstream == "":
		return fmt.Errorf("--upstream is required on the source")
	case c.Dir == "":
		return fmt.Errorf("--dir is required")
	case len(c.Repos) == 0:
		return fmt.Errorf("--repos is empty")
	case c.Git == nil:
		return fmt.Errorf("no git runner")
	}
	return nil
}

// Result is one repo's outcome on one pass.
type Result struct {
	Repo     string
	OK       bool
	Tip      string // 40-hex, or "none" when the ref is unreadable
	Pulls    int
	From     string // github, the source bench's name, or "-" for check
	Reason   string // clone|fetch|auth|shallow|ref|missing on a refusal
	Err      string // one line
	Repaired bool   // a shallow mirror was repaired this pass
}

// Line is the receipt: OK <bench> <repo> tip=<sha> from=<from> pulls=<n>, or
// REFUSED <bench> <repo> reason=<why> err=<one line>.
func (r Result) Line(bench string) string {
	if !r.OK {
		return fmt.Sprintf("REFUSED %s %s reason=%s err=%s", bench, r.Repo, r.Reason, oneLine(r.Err))
	}
	s := fmt.Sprintf("OK %s %s tip=%s from=%s pulls=%d", bench, r.Repo, r.Tip, r.From, r.Pulls)
	if r.Repaired {
		s += " repaired=shallow"
	}
	return s
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		return "-"
	}
	return s
}

func (c Config) path(r Repo) string { return filepath.Join(c.Dir, r.Name+".git") }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// shallow reports whether the mirror at m is shallow: the shallow file, or git says so.
func shallow(ctx context.Context, g Git, m string) bool {
	if exists(filepath.Join(m, "shallow")) {
		return true
	}
	out, err := g.Run(ctx, "--git-dir", m, "rev-parse", "--is-shallow-repository")
	return err == nil && strings.TrimSpace(out) == "true"
}

func (c Config) read(ctx context.Context, r Repo, m string, res *Result) {
	out, err := c.Git.Run(ctx, "--git-dir", m, "rev-parse", "-q", "--verify", r.Ref+"^{commit}")
	tip := strings.TrimSpace(out)
	if err != nil || len(tip) != 40 {
		res.OK, res.Tip, res.Reason, res.Err = false, "none", "ref", "rev-parse "+r.Ref+" failed"
		return
	}
	res.Tip = tip
	if out, err := c.Git.Run(ctx, "--git-dir", m, "for-each-ref", "--format=x", "refs/pull"); err == nil {
		res.Pulls = strings.Count(out, "x\n")
	}
}

// Check is read-only: no fetch. A missing, shallow or ref-less mirror is refused.
func Check(ctx context.Context, c Config) []Result {
	var out []Result
	for _, r := range c.Repos {
		res := Result{Repo: r.Name, OK: true, From: "-"}
		m := c.path(r)
		switch {
		case !exists(filepath.Join(m, "objects")):
			res.OK, res.Reason, res.Err = false, "missing", m+" has no objects"
		case shallow(ctx, c.Git, m):
			res.OK, res.Reason, res.Err = false, "shallow", m+" is shallow"
		default:
			c.read(ctx, r, m, &res)
		}
		out = append(out, res)
	}
	return out
}

// Refresh is one pass: clone what is missing, repair what is shallow, fetch the rest.
func Refresh(ctx context.Context, c Config) []Result {
	var out []Result
	for _, r := range c.Repos {
		out = append(out, c.refreshOne(ctx, r))
	}
	return out
}

var authErr = regexp.MustCompile(`(?i)authentication failed|could not read username|returned error: 40[134]|permission denied \(publickey`)

func (c Config) refused(r Repo, reason string, err error, out string) Result {
	msg := oneLine(out)
	if msg == "-" && err != nil {
		msg = oneLine(err.Error())
	}
	if (reason == "fetch" || reason == "clone") && authErr.MatchString(out) {
		reason = "auth"
	}
	return Result{Repo: r.Name, Tip: "none", Reason: reason, Err: msg}
}

func (c Config) refreshOne(ctx context.Context, r Repo) Result {
	m := c.path(r)
	url, from := c.remote(r)
	res := Result{Repo: r.Name, OK: true, From: from}
	switch {
	case !exists(filepath.Join(m, "objects")):
		if bad, ok := c.cloneInto(ctx, r, m, url); !ok {
			return bad
		}
	case shallow(ctx, c.Git, m):
		if bad, ok := c.repair(ctx, r, m, url); !ok {
			return bad
		}
		res.Repaired = true
	default:
		args := append([]string{"--git-dir", m, "fetch", "--atomic", "--prune", "--no-tags", "-q", url}, Refspecs...)
		if out, err := c.Git.Run(ctx, args...); err != nil {
			return c.refused(r, "fetch", err, out)
		}
	}
	c.read(ctx, r, m, &res)
	return res
}

// cloneInto mirrors url into a tmp dir beside m, checks it, and renames it into
// place; any failure removes the tmp dir and leaves no <repo>.git behind.
func (c Config) cloneInto(ctx context.Context, r Repo, m, url string) (Result, bool) {
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return c.refused(r, "clone", err, ""), false
	}
	tmp := m + ".tmp." + strconv.Itoa(os.Getpid())
	_ = safepath.RemoveUnder(c.Dir, tmp)
	if out, err := c.Git.Run(ctx, "clone", "-q", "--mirror", "--no-tags", url, tmp); err != nil {
		_ = safepath.RemoveUnder(c.Dir, tmp)
		return c.refused(r, "clone", err, out), false
	}
	if shallow(ctx, c.Git, tmp) {
		_ = safepath.RemoveUnder(c.Dir, tmp)
		return Result{Repo: r.Name, Tip: "none", Reason: "shallow", Err: "the new clone of " + url + " is shallow"}, false
	}
	if out, err := c.Git.Run(ctx, "--git-dir", tmp, "rev-parse", "-q", "--verify", r.Ref+"^{commit}"); err != nil {
		_ = safepath.RemoveUnder(c.Dir, tmp)
		return c.refused(r, "ref", err, "the new clone has no "+r.Ref+": "+out), false
	}
	if err := os.Rename(tmp, m); err != nil {
		_ = safepath.RemoveUnder(c.Dir, tmp)
		return c.refused(r, "clone", err, ""), false
	}
	return Result{}, true
}

// repair unshallows m from url; failing that it re-mirrors into tmp and swaps
// it in (old -> .old, tmp -> m, remove .old).
func (c Config) repair(ctx context.Context, r Repo, m, url string) (Result, bool) {
	args := append([]string{"--git-dir", m, "fetch", "--unshallow", "--no-tags", "-q", url}, Refspecs...)
	if _, err := c.Git.Run(ctx, args...); err == nil && !shallow(ctx, c.Git, m) {
		return Result{}, true
	}
	fresh := m + ".new"
	_ = safepath.RemoveUnder(c.Dir, fresh)
	if bad, ok := c.cloneInto(ctx, r, fresh, url); !ok {
		bad.Reason = "shallow"
		return bad, false
	}
	old := m + ".old"
	_ = safepath.RemoveUnder(c.Dir, old)
	if err := os.Rename(m, old); err != nil {
		_ = safepath.RemoveUnder(c.Dir, fresh)
		return c.refused(r, "shallow", err, ""), false
	}
	if err := os.Rename(fresh, m); err != nil {
		_ = os.Rename(old, m)
		_ = safepath.RemoveUnder(c.Dir, fresh)
		return c.refused(r, "shallow", err, ""), false
	}
	_ = safepath.RemoveUnder(c.Dir, old)
	return Result{}, true
}
