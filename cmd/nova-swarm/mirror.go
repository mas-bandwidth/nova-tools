package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE MIRROR (docs/COORDINATOR-TOOLS.md, the mirror-refresh row; docs/FLEET.md).
//
// `nova-swarm mirror` keeps the bench's bare mirrors fresh, so a card's `git clone
// --reference` finds every repository it may borrow from: for each --repos name it clones
// <base>/<name>.git into <dir>/<name>.git when the mirror is absent, then fetches every
// head and every pull-request head into it. It replaces the coordinator's bash
// mirror-refresh loop. Never git prune and never a forced removal: the disk guard owns
// what a mirror leaves behind (diskguard.go), and a clone borrowing a mirror reads its
// objects.

// mirrorRefspecs are the refs a mirror holds: every branch and every pull request's head.
var mirrorRefspecs = []string{"+refs/heads/*:refs/heads/*", "+refs/pull/*/head:refs/pull/*/head"}

// mirrorNameRe is a repository's name: one path element, never a path.
var mirrorNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// mirrorGit is one git run for the mirror verb, a seam for its tests: dir is the
// directory it runs in, "" for none.
type mirrorGit func(ctx context.Context, dir string, args ...string) error

func realMirrorGit(ctx context.Context, dir string, args ...string) error {
	_, err := gitrun.Combined(ctx, gitrun.Options{Dir: dir, Env: append(os.Environ(), "GIT_TERMINAL_PROMPT=0")}, args...)
	return err
}

// mirrorRun is one mirror pass's inputs.
type mirrorRun struct {
	dir, base string
	repos     []string
	git       mirrorGit
	out       io.Writer
}

// pass refreshes every repository once, one MIRROR OK or MIRROR FAILED line each, and
// returns how many failed: one repository's failure never stops the others.
func (m *mirrorRun) pass(ctx context.Context) (failed int) {
	for _, name := range m.repos {
		if err := m.refresh(ctx, name); err != nil {
			failed++
			fmt.Fprintf(m.out, "MIRROR FAILED %s: %s\n", oneline.Field(name), oneline.Err(err))
			continue
		}
		fmt.Fprintf(m.out, "MIRROR OK %s\n", oneline.Field(name))
	}
	return failed
}

func (m *mirrorRun) refresh(ctx context.Context, name string) error {
	bare := filepath.Join(m.dir, name+".git")
	url := strings.TrimRight(m.base, "/") + "/" + name + ".git"
	if _, err := os.Stat(bare); os.IsNotExist(err) {
		if err := m.git(ctx, "", "init", "--bare", "--quiet", "--", bare); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return m.git(ctx, bare, append([]string{"fetch", "--quiet", "--no-tags", "--prune", "--", url}, mirrorRefspecs...)...)
}

// loop runs pass, then waits every between passes with wait until ctx ends; every of 0
// is one pass. wait returns false when it was cut short by ctx.
func (m *mirrorRun) loop(ctx context.Context, every time.Duration, wait func(context.Context, time.Duration) bool) int {
	failed := m.pass(ctx)
	for every > 0 && wait(ctx, every) {
		failed = m.pass(ctx)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func waitFor(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// cmdMirror is `nova-swarm mirror`.
func cmdMirror(args []string, stdout, stderr io.Writer) int {
	return runMirror(args, stdout, stderr, realMirrorGit, waitFor)
}

func runMirror(args []string, stdout, stderr io.Writer, git mirrorGit, wait func(context.Context, time.Duration) bool) int {
	f := newFlags("mirror")
	dir := f.fs.String("dir", "", "the `dir` the bare mirrors <repo>.git live in (default: layout.Root.Mirrors)")
	repos := f.fs.String("repos", "", "the repository names to mirror, a comma-separated `list`")
	base := f.fs.String("base", "", "the `url` each repository is fetched from, <url>/<name>.git, such as https://github.com/<org> or git@<alias>:<org>")
	every := f.fs.Duration("every", 0, "refresh again after this `duration`, until stopped; 0 refreshes once (default 0)")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*base, "base", "the url the repositories live under, <url>/<name>.git")
	f.want(*repos, "repos", "the repository names to mirror, a,b")
	var names []string
	for _, n := range strings.Split(*repos, ",") {
		if n = strings.TrimSpace(n); n == "" {
			continue
		}
		if !mirrorNameRe.MatchString(n) || strings.Contains(n, "..") {
			f.add("--repos names " + oneline.Field(n) + ", which is no repository name: one name, never a path")
			continue
		}
		names = append(names, n)
	}
	// Separators alone parse to no repository: refuse before --dir is made, so a
	// comma-only list leaves no empty mirror directory behind.
	if len(names) == 0 && strings.TrimSpace(*repos) != "" {
		f.add("--repos names no repository, got " + oneline.Field(*repos) + ": it wants the repository names to mirror, a,b")
	}
	if *every < 0 {
		f.add("--every is 0 or a positive duration, got " + oneline.Field(every.String()))
	}
	if f.refused(stderr) {
		return 2
	}
	d := *dir
	if rest, ok := strings.CutPrefix(d, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return refuse(stderr, " mirror", "the user's home could not be read: "+err.Error())
		}
		d = filepath.Join(home, rest)
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return refuse(stderr, " mirror", "--dir could not be made: "+oneline.Err(err))
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	m := &mirrorRun{dir: d, base: *base, repos: names, git: git, out: stdout}
	return m.loop(ctx, *every, wait)
}
