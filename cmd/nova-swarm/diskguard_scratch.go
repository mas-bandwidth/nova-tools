package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
)

// ------------------------------------------------------------------------------- scratch
//
// The job and scratch directories a bench's workers leave (~/rowan-jobs, ~/jobs, ~/tmp, and
// the go-build and mktemp leftovers under /tmp) were looked at by no rule: on 2026-10-17
// hetzner1's 437 GB root fell to 14 MB free while this guard reported nothing it could
// remove, and a hand-run sweep found over a hundred such entries no process used. So the
// guard sweeps each --scratch match, a file or a directory, when every one of these holds:
//
//   - nothing in it (the entry itself, or any file or directory under it) changed for
//     --scratch-age, default a day;
//   - it is this login's own (its owner is the user the guard runs as), never a link;
//   - its name is none of the kept names (guardScratchKeep, and each --scratch-keep): a
//     friend's working tree, a secrets store;
//   - no live process names it or holds a path under it (its cwd, an open file);
//   - no git repository at most guardScratchGitDepth levels inside it has an uncommitted
//     change to a tracked file or a commit on a local branch no remote has: the guard
//     cannot prove that work is anywhere else, so it stays, said on a KEPT line.
//
// A --scratch match under ~/nova-bench is refused when the flags are read: the release,
// the pools and the mirrors there have their own rules above.

const (
	guardScratchAge      = 24 * time.Hour
	guardScratchGitDepth = 3
)

// guardScratchKeep is the names never swept, whatever --scratch matches: a friend's
// working tree and anything named for secrets (filepath.Match against the entry's name).
var guardScratchKeep = []string{"*-working", "*secret*", "stella-*", "johnny-*"}

// scratchKept is the kept name that matches name, "" when none does.
func scratchKept(name string, keep []string) string {
	for _, k := range keep {
		if ok, err := filepath.Match(k, name); err == nil && ok {
			return k
		}
	}
	return ""
}

// scratchNewest is the newest modification time of path and everything under it, links
// never followed; it stops as soon as it finds one at or after since (young), since a
// younger entry is kept whatever the rest holds.
func scratchNewest(path string, since time.Time) (newest time.Time, young bool) {
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable part is skipped, never the end of the walk
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		if !fi.ModTime().Before(since) {
			young = true
			return fs.SkipAll
		}
		return nil
	})
	return newest, young
}

// scratch removes every --scratch match that every rule above lets go.
func (g *guard) scratch() {
	since := g.now.Add(-g.scratchAge)
	for _, path := range g.scratchPaths {
		fi, err := os.Lstat(path)
		if err != nil {
			continue // gone since the glob, or unreadable: nothing to remove
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if k := scratchKept(filepath.Base(path), g.scratchKeep); k != "" {
			continue
		}
		if !g.owned(fi) {
			continue
		}
		if _, young := scratchNewest(path, since); young {
			continue
		}
		list, ok := g.processes()
		if !ok {
			return
		}
		if naming(list, path) != "" {
			g.say(fmt.Sprintf("KEPT scratch %s: a live process names it", oneline.Field(path)))
			continue
		}
		held, ok := g.holds(path)
		if !ok {
			return
		}
		if held != "" {
			g.say(fmt.Sprintf("KEPT scratch %s: a live process holds %s", oneline.Field(path), oneline.Field(held)))
			continue
		}
		if fi.IsDir() {
			if why := g.scratchWork(path); why != "" {
				g.say(fmt.Sprintf("KEPT scratch %s: %s", oneline.Field(path), oneline.Escape(why)))
				continue
			}
		}
		size := fi.Size()
		if fi.IsDir() {
			size = treeSize(path)
		}
		if err := g.unless(func() error { return safepath.RemoveUnderRoots(path, filepath.Dir(path)) }); err != nil {
			g.fail(fmt.Sprintf("the scratch entry %s was not removed (%s)", oneline.Field(path), oneline.Err(err)))
			continue
		}
		g.freed += size
		g.say(fmt.Sprintf("REMOVED scratch %s freed=%d", oneline.Field(path), size))
	}
}

// scratchRepos is every git repository at most depth levels inside dir (a .git directory,
// or a .git file a worktree keeps), links never followed.
func scratchRepos(dir string, depth int) (repos []string) {
	base := strings.Count(filepath.Clean(dir), string(filepath.Separator))
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == ".git" {
			repos = append(repos, filepath.Dir(p))
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() && strings.Count(p, string(filepath.Separator))-base >= depth {
			return fs.SkipDir
		}
		return nil
	})
	return repos
}

// scratchGitWork is why the repository at repo holds work the guard cannot prove is
// elsewhere ("" when it holds none): an uncommitted change to a tracked file, or a commit
// on a local branch that no remote-tracking ref has. A repository whose state cannot be
// read holds work. Git is run without its optional index lock, so the check never
// refreshes the index and never makes the entry look used.
func scratchGitWork(repo string) string {
	env := append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	o := gitrun.Options{C: repo, OwnRepo: true, Env: env}
	res, err := gitrun.Run(context.Background(), o, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return fmt.Sprintf("the status of %s could not be read (%s)", oneline.Field(repo), oneline.Err(errors.New(strings.TrimSpace(string(res.Stderr)))))
	}
	if strings.TrimSpace(string(res.Stdout)) != "" {
		return fmt.Sprintf("%s has uncommitted work; run: git -C %s status", oneline.Field(repo), oneline.Field(repo))
	}
	res, err = gitrun.Run(context.Background(), o, "rev-list", "-n", "1", "--branches", "--not", "--remotes")
	if err != nil {
		return fmt.Sprintf("the branches of %s could not be read (%s)", oneline.Field(repo), oneline.Err(errors.New(strings.TrimSpace(string(res.Stderr)))))
	}
	if strings.TrimSpace(string(res.Stdout)) != "" {
		return fmt.Sprintf("%s has commits no remote has; run: git -C %s log --branches --not --remotes", oneline.Field(repo), oneline.Field(repo))
	}
	return ""
}

// scratchWork is why a scratch directory holds work ("" when none of its repositories do).
func (g *guard) scratchWork(dir string) string {
	for _, repo := range scratchRepos(dir, guardScratchGitDepth) {
		if why := g.gitWork(repo); why != "" {
			return why
		}
	}
	return ""
}

// scratchMatches is every match of the --scratch globs, once, each refused when it lies
// under refuse (the bench's own dir, whose contents other rules hold) or is the home.
func scratchMatches(globs []string, tilde func(string) string, home string, refuse []string) ([]string, error) {
	var out []string
	for _, pat := range globs {
		matches, err := filepath.Glob(tilde(pat))
		if err != nil {
			return nil, fmt.Errorf("--scratch %q is not a glob: %s", pat, oneline.Err(err))
		}
		for _, m := range matches {
			m = filepath.Clean(m)
			if m == home || m == string(filepath.Separator) || strings.HasPrefix(home, m+string(filepath.Separator)) {
				return nil, fmt.Errorf("--scratch %q matches %s, which holds the home: a scratch entry is a job's, never the login's", pat, oneline.Field(m))
			}
			for _, r := range refuse {
				if m == r || strings.HasPrefix(m, r+string(filepath.Separator)) {
					return nil, fmt.Errorf("--scratch %q matches %s, under %s, whose contents the guard's other rules hold", pat, oneline.Field(m), oneline.Field(r))
				}
			}
			out = append(out, m)
		}
	}
	return unique(out), nil
}
