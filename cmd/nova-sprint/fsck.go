package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// fsck: a read-only verb that checks store-vs-git invariants (docs/SPEC-SPRINT.md section 7,
// fsck-verb-landed-on-base-b.w2): fsck prints one FSCK VIOLATION line for each check that
// fails, a summary FSCK OK or FSCK FAIL, and exits 0/1/2. It writes nothing and repairs
// nothing: a violation is reported, a separate verb repairs it.

func init() { verbClasses["fsck"] = classRead }

type fsckResult struct {
	Check    string `json:"check"`
	ID       string `json:"id,omitempty"`
	Stream   string `json:"stream,omitempty"`
	Head     string `json:"head,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Base     string `json:"base,omitempty"`
	Tip      string `json:"tip,omitempty"`
	Ancestor bool   `json:"ancestor,omitempty"`
	Why      string `json:"why,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

func (a *app) cmdFsck(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("fsck")
	var checks []string
	fs.Func("check", "filter to named checks only (repeatable)", func(s string) error {
		checks = append(checks, sprint.Split(s)...)
		return nil
	})
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "fsck", argErr("takes no words; run: fsck [--json] [--check <name>]...", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "fsck", err.Error())
	}
	ctx := context.Background()
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return a.readFailed("fsck", err, stderr)
	}
	land := &lander{a: a, c: *c}
	g := &fsckGit{a: a, ctx: ctx, land: land, dirs: map[string]string{}, fetched: map[string]bool{}}
	results := sprint.LandOnBase(snap, checks, g.OnBase)
	if c.json {
		var items []fsckResult
		for _, f := range results {
			items = append(items, fsckResult{
				Check: f.Check, ID: f.ID, Stream: f.Stream,
				Head: f.Head, Repo: f.Repo, Base: f.Base, Tip: f.Tip,
				Ancestor: f.Ancestor, Why: f.Why, Fix: f.Fix,
			})
		}
		b, _ := json.Marshal(map[string]any{
			"verb":   "fsck",
			"status": statusFromResults(results),
			"exit":   exitFromResults(results),
			"checks": items,
		})
		fmt.Fprintln(stdout, string(b))
		return exitFromResults(results)
	}
	for _, f := range results {
		fmt.Fprintln(stdout, f.Line())
	}
	switch {
	case fsckUnreadable(results) > 0:
		return 2
	case fsckViolations(results) > 0:
		fmt.Fprintf(stdout, "FSCK FAIL violations=%d\n", fsckViolations(results))
		return 1
	}
	fmt.Fprintln(stdout, "FSCK OK checks=1")
	return 0
}

// unreadable is how many findings are a check git could not answer (exit 2).
func fsckUnreadable(results []sprint.LandOnBaseFinding) int {
	n := 0
	for _, f := range results {
		if f.Why != "" {
			n++
		}
	}
	return n
}

// violations is how many findings are a record that failed its check (exit 1).
func fsckViolations(results []sprint.LandOnBaseFinding) int {
	n := 0
	for _, f := range results {
		if f.Why == "" && !f.Ancestor {
			n++
		}
	}
	return n
}

func statusFromResults(results []sprint.LandOnBaseFinding) string {
	switch {
	case fsckUnreadable(results) > 0:
		return "unreadable"
	case fsckViolations(results) > 0:
		return "violation"
	}
	return "ok"
}

func exitFromResults(results []sprint.LandOnBaseFinding) int {
	switch {
	case fsckUnreadable(results) > 0:
		return 2
	case fsckViolations(results) > 0:
		return 1
	}
	return 0
}

// gitRunner returns a function that runs one git in dir, the repository's clone under the
// sprint root, set as the child's working directory, so a command that needs repository
// context (merge-base, fetch, cat-file) reads the clone the check names and never the
// directory nova-sprint happens to run in. Every child goes through gitrun, the one door a
// git child uses, so it carries a deadline, a WaitDelay and the caller's environment.
func (a *app) gitRunner(ctx context.Context, dir string) func(args ...string) (int, string, string) {
	return func(args ...string) (int, string, string) {
		res, err := gitrun.Run(ctx, gitrun.Options{Dir: dir, Env: a.gitEnv, OwnRepo: dir != ""}, args...)
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return exit.ExitCode(), string(res.Stdout), string(res.Stderr)
			}
			return 2, string(res.Stdout), string(res.Stderr)
		}
		return 0, string(res.Stdout), string(res.Stderr)
	}
}

// fsckGit is the git facts fsck's landed-on-base check reads: one clone per repository under
// the sprint root, where git ls-remote and git merge-base --is-ancestor run. It reuses
// verify-landed's clone and ancestry test (lander.clone, lander.ancestor), so a head is
// judged in its repository's clone directory and never in the process's own.
type fsckGit struct {
	a       *app
	ctx     context.Context
	land    *lander
	dirs    map[string]string // repository -> the clone under the sprint root
	fetched map[string]bool   // clone and base -> its fetch is done
}

// cloneDir is the repository's clone under the sprint root, made once (lander.clone).
func (g *fsckGit) cloneDir(repo string) (string, string) {
	if dir, ok := g.dirs[repo]; ok {
		return dir, ""
	}
	dir, why := g.land.clone(g.ctx, repo)
	if why != "" {
		return "", why
	}
	g.dirs[repo] = dir
	return dir, ""
}

// OnBase is the git fact for one card: origin/base's tip (git ls-remote in the repository's
// clone), then whether head is an ancestor of that tip (git merge-base --is-ancestor in the
// same clone, after fetching the base so its ancestors are present). A head the clone does
// not hold after the fetch is not on the base: a violation, not a check that could not be
// read.
func (g *fsckGit) OnBase(repo, base, head string) (tip string, on bool, why string) {
	dir, why := g.cloneDir(repo)
	if why != "" {
		return "", false, why
	}
	run := g.a.gitRunner(g.ctx, dir)
	code, out, errs := run("ls-remote", "--", repo, "refs/heads/"+base)
	tip = strings.TrimSpace(out)
	if i := strings.IndexAny(tip, " \t"); i >= 0 {
		tip = tip[:i]
	}
	if code != 0 || tip == "" {
		return "", false, "origin/" + base + " could not be read in " + dir + ": " + firstLine(errs, nil)
	}
	key := dir + "\x00" + base
	if !g.fetched[key] {
		if _, err := g.land.git(g.ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/"+base+":refs/remotes/origin/"+base); err != nil {
			return tip, false, "the fetch of " + base + " in " + dir + " failed: " + firstLine("", err)
		}
		g.fetched[key] = true
	}
	if _, err := g.land.git(g.ctx, dir, "cat-file", "-e", head+"^{commit}"); err != nil {
		return tip, false, ""
	}
	on, why = g.land.ancestor(g.ctx, dir, head, tip)
	return tip, on, why
}
