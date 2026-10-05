package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// THE MIRRORS (docs/FLEET.md; docs/COORDINATOR-TOOLS.md, the mirror-refresh row).
//
// A card's checkout borrows objects from a bare mirror of its repository (git clone
// --reference), so a machine keeps one mirror per repository the sprint works in, fresh to
// the minute. One coordinator kept them with a bash loop from its own tool repository;
// `nova-swarm mirror` is the same work as a verb of the installed tool, one pass, or a pass
// every --every until interrupted:
//
//   - a repository with no mirror is cloned with `git clone --mirror` into a temporary
//     directory beside it and renamed into place, so a half clone is never a mirror;
//   - a mirror there is fetched with `git fetch --prune origin`: a mirror's refspec is every
//     ref, the branches and the pull requests' refs both;
//   - the URL is --url with {repo} replaced, so each repository may have its own (a deploy
//     key's ssh alias, git@github-{repo}:<org>/{repo}.git). The verb holds no credential:
//     git's own (an ssh key, a credential helper) is what it uses.
//
// One MIRROR line per repository and one at the end of each pass; a pass with a failure
// ends at exit 1, and the loop goes on to its next pass.

// mirrorRepoRe is a repository's name: a path segment and nothing that leaves the --dir.
var mirrorRepoRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// mirrorTimeout bounds one clone or fetch: a network git, the long budget.
const mirrorTimeout = subproc.GitLongBudget

// mirrors is one mirror run: where, what, from where, and its world (the git a test fakes).
type mirrors struct {
	dir   string
	repos []string
	url   string
	dry   bool
	git   func(ctx context.Context, dir string, args ...string) error
	out   io.Writer
	pid   int
}

// pass mirrors every repository once; failed is how many could not be.
func (m *mirrors) pass(ctx context.Context) (failed int) {
	for _, repo := range m.repos {
		if ctx.Err() != nil {
			break
		}
		target := filepath.Join(m.dir, repo+".git")
		url := strings.ReplaceAll(m.url, "{repo}", oneline.Field(repo))
		_, err := os.Stat(target)
		switch {
		case err == nil:
			if m.dry {
				m.say(fmt.Sprintf("MIRROR WOULD-FETCH repo=%s dir=%s", oneline.Field(repo), oneline.Field(target)))
				continue
			}
			if err := m.git(ctx, target, "fetch", "--prune", "--quiet", "origin"); err != nil {
				failed++
				m.say(fmt.Sprintf("MIRROR FAILED repo=%s: the fetch failed: %s", oneline.Field(repo), oneline.Err(err)))
				continue
			}
			m.say(fmt.Sprintf("MIRROR FETCHED repo=%s dir=%s", oneline.Field(repo), oneline.Field(target)))
		case os.IsNotExist(err):
			if m.dry {
				m.say(fmt.Sprintf("MIRROR WOULD-CLONE repo=%s dir=%s", oneline.Field(repo), oneline.Field(target)))
				continue
			}
			if err := m.clone(ctx, url, target); err != nil {
				failed++
				m.say(fmt.Sprintf("MIRROR FAILED repo=%s: the clone failed: %s", oneline.Field(repo), oneline.Err(err)))
				continue
			}
			m.say(fmt.Sprintf("MIRROR CLONED repo=%s dir=%s", oneline.Field(repo), oneline.Field(target)))
		default:
			failed++
			m.say(fmt.Sprintf("MIRROR FAILED repo=%s: %s", oneline.Field(repo), oneline.Err(err)))
		}
	}
	return failed
}

// clone clones url as a mirror into a temporary directory beside target and renames it
// into place; a failed clone's directory is removed.
func (m *mirrors) clone(ctx context.Context, url, target string) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	tmp := target + ".tmp-" + strconv.Itoa(m.pid)
	if err := m.git(ctx, m.dir, "clone", "--mirror", "--quiet", url, tmp); err != nil {
		_ = safepath.RemoveUnder(m.dir, tmp) // ignored: the clone's error is the one said
		return err
	}
	return os.Rename(tmp, target)
}

func (m *mirrors) say(line string) {
	fmt.Fprintln(m.out, oneline.Escape(line))
}

// gitMirror runs one network git in dir, bounded by mirrorTimeout.
func gitMirror(ctx context.Context, dir string, args ...string) error {
	_, err := gitrun.Run(ctx, gitrun.Options{Dir: dir, Timeout: mirrorTimeout, Env: gitrun.WithoutRepoVars(os.Environ())}, args...)
	return err
}

// mirrorLoop runs a pass, then one each time every passes (0: the one pass), until ctx
// ends; wait is the pause between passes, false when the run should end. The exit is the
// last pass's.
func (m *mirrors) loop(ctx context.Context, every time.Duration, wait func(context.Context, time.Duration) bool) int {
	for {
		failed := m.pass(ctx)
		if failed > 0 {
			m.say(fmt.Sprintf("MIRROR FAILED repos=%d failed=%d", len(m.repos), failed))
		} else {
			m.say(fmt.Sprintf("MIRROR OK repos=%d failed=0", len(m.repos)))
		}
		if every <= 0 || !wait(ctx, every) {
			if failed > 0 {
				return 1
			}
			return 0
		}
	}
}

// pauseFor waits d, false when ctx ends first.
func pauseFor(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func cmdMirror(args []string, stdout, stderr io.Writer) int {
	return runMirror(args, stdout, stderr, gitMirror, pauseFor)
}

// runMirror is cmdMirror with its git and its pause given, the seams a test fakes.
func runMirror(args []string, stdout, stderr io.Writer, git func(context.Context, string, ...string) error, wait func(context.Context, time.Duration) bool) int {
	f := newFlags("mirror")
	dir := f.fs.String("dir", "~/nova-bench/mirror", "the `dir` the mirrors are kept in, one <repo>.git each (default ~/nova-bench/mirror)")
	repos := f.fs.String("repos", "", "the repositories to mirror, a comma-separated `list` of names")
	url := f.fs.String("url", "", "the `template` of a repository's URL, {repo} replaced by its name: https://github.com/<org>/{repo}.git, or a deploy key's ssh alias git@github-{repo}:<org>/{repo}.git")
	every := f.fs.Duration("every", 0, "mirror again each time this `duration` passes, until interrupted (default 0: one pass)")
	dry := f.fs.Bool("dry-run", false, "say WOULD-CLONE or WOULD-FETCH for each repository and run no git")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*repos, "repos", "the repositories to mirror, a comma-separated list of names")
	f.want(*url, "url", "a repository's URL with {repo} where its name goes")
	var names []string
	for _, r := range strings.Split(*repos, ",") {
		if r = strings.TrimSpace(r); r == "" {
			continue
		}
		if !mirrorRepoRe.MatchString(r) || strings.Contains(r, "..") {
			f.add(fmt.Sprintf("--repos: %q is not a repository's name", r))
			continue
		}
		names = append(names, r)
	}
	if *url != "" && !strings.Contains(*url, "{repo}") {
		f.add("--url names no {repo}: every repository would be fetched from one URL")
	}
	if *every < 0 || (*every > 0 && *every < time.Second) {
		f.add("--every is 0 (one pass) or at least 1s, got " + oneline.Field(every.String()))
	}
	if f.refused(stderr) {
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return refuse(stderr, " mirror", "the user's home could not be read: "+err.Error())
	}
	d := filepath.Clean(*dir)
	if rest, ok := strings.CutPrefix(*dir, "~/"); ok {
		d = filepath.Join(home, rest)
	}
	m := &mirrors{dir: d, repos: names, url: *url, dry: *dry, git: git, out: stdout, pid: os.Getpid()}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return m.loop(ctx, *every, wait)
}
