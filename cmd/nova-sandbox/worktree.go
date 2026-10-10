// The worktree verb materialises one pull request's exact head in a scratch
// tree of its own. It is not a wrapper and builds no wall: it uses SPEC.md's
// 0/1/2 grammar (0 the verb ran, 2 could not run),
// reads the repository through git on PATH, and reads the pull request through
// a forge client seam so a test can put a fake there. Every seam the verb
// reaches the outside world through is a package-level var, because the tests
// in worktree_test.go replace each one and no test touches a network, a real
// forge, the real clock or a real process.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// worktreeRemedy is the one remedy line a bad flag carries.
const worktreeRemedy = "run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>"

// worktreeRetryRemedy is the one remedy line a forge that did not answer
// carries: it names the flag and says to retry.
const worktreeRetryRemedy = "run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>; retry once the forge answers"

// staleAfter is how long a guid directory may sit before --prune may call it
// abandoned, if no process is using it.
const staleAfter = 24 * time.Hour

// gitRunner is the one seam the verb reaches git through. dir is the -C
// directory and args are the subcommand's own words.
type gitRunner func(dir string, args ...string) (string, error)

var worktreeGit gitRunner = runGit

// runGit is the production seam: a real git, its stderr folded into the error.
func runGit(dir string, args ...string) (string, error) {
	res, err := gitrun.Run(context.Background(), gitrun.Options{Dir: dir}, args...)
	if err != nil {
		return string(res.Stdout), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(res.Stderr)))
	}
	return string(res.Stdout), nil
}

// worktreePR is what the forge seam answers for one pull request.
type worktreePR struct {
	Head  string
	Base  string
	State string // open, merged or closed
}

// worktreeForge is the forge client seam.
type worktreeForge interface {
	PR(id int) (worktreePR, error)
}

// worktreeForgeFactory builds the client for one repository and environment.
// The token arrives in the environment and is printed nowhere.
var worktreeForgeFactory = func(repo string, env []string) worktreeForge {
	return ghForge{repo: repo, env: env}
}

// worktreeNow and worktreeInUse are the injected clock and process probe
// --prune's stale rule uses. The default probe never claims a tree is idle on a
// platform it cannot read, because a wrong stale verdict deletes work.
var (
	worktreeNow   = time.Now
	worktreeInUse = inUseByAProcess
	worktreeGUID  = newGUID
)

// worktreeSeams is what one worktree verb reaches the machine through. A test builds its own
// instead of assigning the package-level defaults above, so every worktree test opens with
// t.Parallel() (the ledger in internal/ci/testdata/serial-tests_allowlist.txt).
type worktreeSeams struct {
	Git          gitRunner
	ForgeFactory func(repo string, env []string) worktreeForge
	Now          func() time.Time
	InUse        func(dir string) bool
	GUID         func() string
}

// prodWorktreeSeams is the production wiring: the package-level defaults.
func prodWorktreeSeams() *worktreeSeams {
	return &worktreeSeams{Git: worktreeGit, ForgeFactory: worktreeForgeFactory, Now: worktreeNow, InUse: worktreeInUse, GUID: worktreeGUID}
}

// The sentinel failures the forge seam can report, which the verb turns into
// reason=no_pr, reason=no_forge, reason=bad_origin and reason=bad_head.
// errBadOrigin and errBadHead are bad input rather than an outage: nothing was
// asked of the forge at all.
var (
	errNoPR      = errors.New("the forge does not know this pull request")
	errNoForge   = errors.New("the forge could not be reached")
	errBadOrigin = errors.New("--repo wants an origin remote whose path names <owner>/<name>")
	errBadHead   = errors.New("the forge reported a head that is not forty lowercase hex characters")
)

// badOrigin names the origin remote no owner and name could be read out of.
func badOrigin(url string) error {
	return fmt.Errorf("%w, and origin reads %q", errBadOrigin, url)
}

// worktreeFlags is the worktree verb's own argv.
type worktreeFlags struct {
	repo, scratch, base     string
	pr                      int
	prNumeric, prSet, prune bool
	baseSet                 bool
	unknown                 []string // a refusal per argument the verb has no flag for
}

func parseWorktree(args []string) worktreeFlags {
	var f worktreeFlags
	for i := 0; i < len(args); i++ {
		if !worktreeFlagNames[args[i]] {
			text, took := unknownArg(args, i, "worktree")
			f.unknown = append(f.unknown, text)
			i += took
			continue
		}
		value := func() (string, bool) {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch args[i] {
		case "--repo":
			if v, ok := value(); ok {
				f.repo = v
			}
		case "--scratch":
			if v, ok := value(); ok {
				f.scratch = v
			}
		case "--base":
			if v, ok := value(); ok {
				f.base, f.baseSet = v, true
			}
		case "--pr":
			f.prSet = true
			if v, ok := value(); ok {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					f.pr, f.prNumeric = n, true
				}
			}
		case "--prune":
			f.prune = true
		}
	}
	return f
}

var worktreeFlagNames = map[string]bool{"--repo": true, "--scratch": true, "--base": true, "--pr": true, "--prune": true}

// worktreeRecord is what <scratch>/<id>.pr holds: the guid that names the tree,
// the base branch compared against, and the head the tree was placed at.
type worktreeRecord struct {
	id   int
	guid string
	base string
	head string
}

func (r worktreeRecord) path(scratch string) string { return filepath.Join(scratch, r.guid) }

func (ws *worktreeSeams) worktreeVerb(args []string, stdout, stderr io.Writer, env []string) int {
	f := parseWorktree(args)
	// Every independent problem in one run, one line each, then the one remedy line
	// (docs/STANDARD.md §3 point 2): a flag the verb has no use for is one of them,
	// never ignored.
	var bad [][2]string
	for _, text := range f.unknown {
		bad = append(bad, [2]string{"bad_flag", text})
	}
	// --pr and --prune are two modes and not one call.
	if (f.prune && f.prSet) || (!f.prune && !f.prNumeric) {
		bad = append(bad, [2]string{"bad_pr", "--pr wants one pull-request number and one mode"})
	}
	if !ws.isGitWorkTree(f.repo) {
		bad = append(bad, [2]string{"bad_repo", "--repo wants an existing repository named by an absolute path"})
	}
	if !isDir(f.scratch) {
		bad = append(bad, [2]string{"bad_scratch", "--scratch wants an existing directory and is not created"})
	}
	if len(bad) > 0 {
		for _, b := range bad {
			fmt.Fprintf(stderr, "WORKTREE REFUSED reason=%s: %s\n", oneline.Field(b[0]), oneline.WithRemedy(b[1], "nova-sandbox help worktree"))
		}
		fmt.Fprintln(stderr, worktreeRemedy)
		return sandbox.ExitCannotRun
	}
	if f.prune {
		return ws.worktreePrune(f, stdout, stderr, env)
	}
	return ws.worktreeOne(f, stdout, stderr, env)
}

func isDir(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isGitWorkTree is true only for an absolute existing directory git itself
// calls a work tree.
func (ws *worktreeSeams) isGitWorkTree(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) || !isDir(dir) {
		return false
	}
	out, err := ws.Git(dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "true"
}

// worktreeOne is the materialise path: fetch the head the forge reports, reuse
// the recorded tree when it is still there, clean and at that head, and rebuild
// it otherwise. Either way one WORKTREE OK line.
func (ws *worktreeSeams) worktreeOne(f worktreeFlags, stdout, stderr io.Writer, env []string) int {
	recPath := filepath.Join(f.scratch, strconv.Itoa(f.pr)+".pr")
	rec, hasRec := readRecord(f.scratch, f.pr)
	forge := ws.ForgeFactory(f.repo, env)
	head, base, err := forgeHead(forge, f.pr)
	if err != nil {
		return worktreeForgeRefuse(stderr, err)
	}
	if f.baseSet {
		base = f.base
	}

	guid := ""
	if hasRec {
		guid = rec.guid
		path := rec.path(f.scratch)
		if pathIsWorktree(path) {
			if h, herr := ws.worktreeHead(path); herr == nil && h == head && ws.treeClean(path) {
				fmt.Fprintf(stdout, "WORKTREE OK path=%s head=%s\n", oneline.Escape(path), oneline.Field(head))
				return 0
			}
		}
		// ignored: a removal that failed shows as the add below failing on the same path, which is refused
		_ = ws.removeWorktree(f.repo, f.scratch, path)
	}
	if guid == "" {
		guid = ws.GUID()
	}
	path := filepath.Join(f.scratch, guid)
	if err := ws.addWorktree(f.repo, path, head); err != nil {
		fmt.Fprintf(stderr, "WORKTREE REFUSED reason=bad_repo: git could not make the worktree: %s\n%s\n", oneline.Err(err), worktreeRemedy)
		return sandbox.ExitCannotRun
	}
	if err := writeRecord(recPath, worktreeRecord{id: f.pr, guid: guid, base: base, head: head}); err != nil {
		fmt.Fprintf(stderr, "WORKTREE REFUSED reason=bad_scratch: the record could not be written: %s\n%s\n", oneline.Err(err), worktreeRemedy)
		return sandbox.ExitCannotRun
	}
	fmt.Fprintf(stdout, "WORKTREE OK path=%s head=%s\n", oneline.Escape(path), oneline.Field(head))
	return 0
}

func forgeHead(forge worktreeForge, id int) (head, base string, err error) {
	pr, err := forge.PR(id)
	if err != nil {
		return "", "", err
	}
	head, base = strings.TrimSpace(pr.Head), strings.TrimSpace(pr.Base)
	if err := headShape(head); err != nil {
		return "", "", err
	}
	return head, base, nil
}

// headShape refuses any head that is not a whole commit id: exactly forty
// lowercase hex characters. A value of any other shape -- a branch name, a
// short or uppercase string, or one beginning with "-" such as an
// --upload-pack= option -- is refused here, before any git argv is built, so
// git never reads it as an option.
func headShape(head string) error {
	if !isHeadSHA(head) {
		return fmt.Errorf("%w, and the head reads %q", errBadHead, head)
	}
	return nil
}

// isHeadSHA is true only for exactly forty lowercase hex characters.
func isHeadSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// worktreeForgeRefuse tells the forge's own failures from the input the forge was
// never asked about: only a forge that did not answer is told to retry.
func worktreeForgeRefuse(stderr io.Writer, err error) int {
	reason, detail, remedy := "no_forge", "the forge could not be reached", worktreeRetryRemedy
	switch {
	case errors.Is(err, errNoPR):
		reason, detail = "no_pr", "the forge does not know this pull request"
	case errors.Is(err, errBadOrigin):
		reason, detail, remedy = "bad_origin", err.Error(), worktreeRemedy
	case errors.Is(err, errBadHead):
		reason, detail, remedy = "bad_head", err.Error(), worktreeRemedy
	}
	fmt.Fprintf(stderr, "WORKTREE REFUSED reason=%s: %s\n%s\n", oneline.Field(reason), oneline.Escape(detail), remedy)
	return sandbox.ExitCannotRun
}

func pathIsWorktree(path string) bool {
	if !isDir(path) {
		return false
	}
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && !info.IsDir()
}

func (ws *worktreeSeams) worktreeHead(path string) (string, error) {
	out, err := ws.Git(path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (ws *worktreeSeams) treeClean(path string) bool {
	out, err := ws.Git(path, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) == ""
}

func (ws *worktreeSeams) addWorktree(repo, path, head string) error {
	if err := fetchHead(ws.Git, repo, head); err != nil {
		return err
	}
	_, err := ws.Git(repo, "worktree", "add", "--detach", "--", path, head)
	return err
}

// fetchHead is the best-effort fetch before a worktree is added (the head may already be
// here), except that a fetch that hit its deadline is reported: the add would otherwise
// fail with only an "invalid reference" and no word that the network was the cause.
func fetchHead(run gitRunner, repo, head string) error {
	_, err := run(repo, "fetch", "origin", "--", head)
	var timedOut *subproc.TimeoutError
	if errors.As(err, &timedOut) {
		return fmt.Errorf("fetching %s from origin: %w", head, err)
	}
	return nil
}

// removeWorktree takes the registration and the directory out, in that order,
// and the directory only through safepath so a computed path can never reach
// outside the scratch root the caller named.
func (ws *worktreeSeams) removeWorktree(repo, scratch, path string) error {
	// ignored: a stale git record is pruned by the next worktree prune; the directory removal below is the one returned
	_, _ = ws.Git(repo, "worktree", "remove", "--force", path)
	return safepath.RemoveUnder(scratch, path)
}

func (ws *worktreeSeams) listWorktrees(repo string) map[string]bool {
	out, err := ws.Git(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return map[string]bool{}
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			listed[p] = true
		}
	}
	return listed
}

// worktreePrune removes only trees this tool made, named by its own record
// files, whose pull request the forge reports merged or closed, plus the ones
// its guid directory ages out and no process is using. A hand-made worktree and
// a tree whose PR state is unknown are kept.
func (ws *worktreeSeams) worktreePrune(f worktreeFlags, stdout, stderr io.Writer, env []string) int {
	listed := ws.listWorktrees(f.repo)
	forge := ws.ForgeFactory(f.repo, env)
	now := ws.Now()
	removed := 0
	for _, rec := range readRecords(f.scratch) {
		path := rec.path(f.scratch)
		if !listed[path] {
			continue
		}
		reason := ""
		if pr, err := forge.PR(rec.id); err == nil {
			switch strings.ToLower(pr.State) {
			case "merged":
				reason = "pr_merged"
			case "closed":
				reason = "pr_closed"
			case "open":
				if info, serr := os.Stat(path); serr == nil && now.Sub(info.ModTime()) > staleAfter && !ws.InUse(path) {
					reason = "stale"
				}
			}
		}
		if reason == "" {
			continue
		}
		if err := ws.removeWorktree(f.repo, f.scratch, path); err != nil {
			continue
		}
		fmt.Fprintf(stdout, "WORKTREE REMOVED path=%s reason=%s\n", oneline.Escape(path), oneline.Field(reason))
		delete(listed, path)
		removed++
	}
	kept := 0
	for p := range listed {
		if under(f.scratch, p) {
			kept++
		}
	}
	fmt.Fprintf(stdout, "WORKTREE OK removed=%d kept=%d\n", removed, kept)
	return 0
}

func readRecord(scratch string, id int) (worktreeRecord, bool) {
	raw, err := os.ReadFile(filepath.Join(scratch, strconv.Itoa(id)+".pr"))
	if err != nil {
		return worktreeRecord{}, false
	}
	rec := worktreeRecord{id: id}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "guid":
			rec.guid = v
		case "base":
			rec.base = v
		case "head":
			rec.head = v
		}
	}
	if rec.guid == "" {
		return worktreeRecord{}, false
	}
	return rec, true
}

func readRecords(scratch string) []worktreeRecord {
	matches, _ := filepath.Glob(filepath.Join(scratch, "*.pr"))
	out := make([]worktreeRecord, 0, len(matches))
	for _, m := range matches {
		id, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(m), ".pr"))
		if err != nil {
			continue
		}
		if rec, ok := readRecord(scratch, id); ok {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func writeRecord(path string, rec worktreeRecord) error {
	var b strings.Builder
	fmt.Fprintf(&b, "guid=%s\n", rec.guid)
	fmt.Fprintf(&b, "base=%s\n", rec.base)
	fmt.Fprintf(&b, "head=%s\n", rec.head)
	fmt.Fprintf(&b, "pid=%d\n", os.Getpid())
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func newGUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// under is the strict-inside test kept readably here; the deletion itself goes
// through pkg/safepath.
func under(root, path string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// inUseByAProcess answers whether any process holds dir as its working
// directory. On linux that reads /proc; anywhere the answer cannot be read it
// says yes, because a stale verdict that is wrong deletes work.
func inUseByAProcess(dir string) bool {
	if runtime.GOOS != "linux" {
		return true
	}
	target, err := filepath.EvalSymlinks(dir)
	if err != nil {
		target = dir
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil {
			continue
		}
		if cwd == dir || cwd == target {
			return true
		}
	}
	return false
}

// ghForge is the production forge client: `gh` reads the pull request and a
// GH_TOKEN/GITHUB_TOKEN in the environment is the only credential. The token is
// never read, echoed or written by this tool.
type ghForge struct {
	repo string
	env  []string
	// git is how the origin is read; nil is the package default.
	git gitRunner
}

func (g ghForge) PR(id int) (worktreePR, error) {
	git := g.git
	if git == nil {
		git = worktreeGit
	}
	url, err := git(g.repo, "remote", "get-url", "origin")
	if err != nil {
		return worktreePR{}, badOrigin("")
	}
	ownerRepo := parseOwnerRepo(strings.TrimSpace(url))
	if ownerRepo == "" {
		return worktreePR{}, badOrigin(strings.TrimSpace(url))
	}
	cmd, cancel := subproc.Command(context.Background(), subproc.GH, "gh", "pr", "view", strconv.Itoa(id), "--repo", ownerRepo,
		"--json", "headRefOid,baseRefName,state")
	defer cancel()
	cmd.Env = g.env
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && strings.Contains(strings.ToLower(string(ee.Stderr)), "not found") {
			return worktreePR{}, errNoPR
		}
		return worktreePR{}, errNoForge
	}
	var v struct{ HeadRefOid, BaseRefName, State string }
	if err := json.Unmarshal(out, &v); err != nil {
		return worktreePR{}, errNoForge
	}
	return worktreePR{Head: v.HeadRefOid, Base: v.BaseRefName, State: strings.ToLower(v.State)}, nil
}

// parseOwnerRepo reads owner/name out of the path of every shape git stores a
// remote in: a scheme's URL, the scp-like git@host:owner/name, and owner/name
// alone. The host is read by nobody here, because an ssh Host alias from
// ~/.ssh/config stands where the forge's own name would and `gh` is the one that
// resolves the forge. A remote naming a place on this machine rather than a forge
// names no owner and name at all, so a bare push target or a test fixture is
// never read as two path words that look like one. Empty is the answer for a
// remote whose path names no owner and name, which the caller reports as bad
// input.
func parseOwnerRepo(url string) string {
	path := strings.TrimSuffix(strings.TrimSpace(url), ".git")
	if localRemote(path) {
		return ""
	}
	if scheme, rest, ok := strings.Cut(path, "://"); ok && scheme != "" {
		// Everything to the first slash is [user@]host[:port].
		_, path, _ = strings.Cut(rest, "/")
	} else if _, rest, ok := strings.Cut(path, ":"); ok {
		path = rest
	}
	parts := strings.Split(path, "/")
	if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

// localRemote is true for a remote naming a place on this machine: a file URL, a
// path that is absolute or begins with . or .., and a windows drive letter, which
// is also why the drive is decided here rather than by the scp-like host:path
// split, where C:/repos would read as a host.
func localRemote(path string) bool {
	if scheme, _, ok := strings.Cut(path, "://"); ok {
		return strings.EqualFold(scheme, "file")
	}
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, ".") {
		return true
	}
	if len(path) >= 3 && path[1] == ':' && (path[2] == '/' || path[2] == '\\') {
		r := path[0] | 0x20
		return r >= 'a' && r <= 'z'
	}
	return false
}
