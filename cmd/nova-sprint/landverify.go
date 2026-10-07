package main

// verify-landed: every card the store records landed is checked against its base on
// origin, by git and no model (docs/SPEC-SPRINT.md section 7, land-verify-landed-ancestry-rb-b.w3):
// its head is an ancestor of origin/<base> at its tip, or the card is printed as one
// LANDED-MISSING line and the verb exits 1. The store records no merge commit of a landing,
// so the head is what is checked; a landing that regenerated ledgers merges the head, so the
// head is an ancestor all the same. Nothing is written: landed stays final.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

func init() { verbClasses["verify-landed"] = classRead }

// landedCheck is one landed card and what its check found: missing says its head is not
// on origin's base; why, when set, is why it could not be checked.
type landedCheck struct {
	ID      string `json:"id"`
	Stream  string `json:"stream"`
	Head    string `json:"head"`
	Repo    string `json:"repo,omitempty"`
	Base    string `json:"base"`
	Tip     string `json:"tip,omitempty"`
	Missing bool   `json:"missing"`
	Why     string `json:"why,omitempty"`
}

func (c landedCheck) line() string {
	l := fmt.Sprintf("LANDED-MISSING %s stream=%s head=%s base=%s tip=%s", c.ID, oneline.Field(c.Stream), c.Head, oneline.Field(c.Base), dashed(c.Tip))
	if c.Repo != "" {
		l += " repo=" + oneline.Field(c.Repo)
	}
	return l
}

func (a *app) cmdVerifyLanded(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("verify-landed")
	var streams listFlag
	fs.Var(&streams, "stream", "a stream whose landed cards are checked (again, or comma separated, for more; default: every stream)")
	repoDir := fs.String("repo-dir", "", "the clone to check in, its origin the card's repository (default: land's clone per repository)")
	base := fs.String("base", "", "the base branch of a card whose brief names no BASE: line")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "verify-landed", argErr("takes no words; a stream is --stream <s> ", err, pos...))
	}
	if *repoDir != "" {
		fi, err := os.Stat(*repoDir)
		if err != nil || !fi.IsDir() {
			return refuse(stderr, "verify-landed", "--repo-dir wants an existing clone; "+*repoDir+" is not a directory")
		}
		if abs, err := filepath.Abs(*repoDir); err == nil {
			*repoDir = abs
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "verify-landed", err.Error())
	}
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return a.readFailed("verify-landed", err, stderr)
	}
	l := &lander{a: a, c: *c, repoDir: *repoDir}
	checks := landedChecks(s, streams, *base)
	l.verify(ctx, checks)
	missing, unchecked := 0, 0
	for _, ch := range checks {
		switch {
		case ch.Why != "":
			unchecked++
		case ch.Missing:
			missing++
		}
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"cards": checks, "checked": len(checks) - unchecked, "missing": missing, "unchecked": unchecked}) // ignored: strings, bools and ints always encode
		fmt.Fprintln(stdout, string(b))
	} else {
		for _, ch := range checks {
			switch {
			case ch.Why != "":
				fmt.Fprintf(stdout, "LANDED-UNCHECKED %s stream=%s reason=%s\n", ch.ID, oneline.Field(ch.Stream), oneline.Escape(ch.Why))
			case ch.Missing:
				fmt.Fprintln(stdout, ch.line())
			}
		}
		fmt.Fprintf(stdout, "VERIFY-LANDED checked=%d missing=%d unchecked=%d\n", len(checks)-unchecked, missing, unchecked)
	}
	switch {
	case unchecked > 0:
		return 2
	case missing > 0:
		return 1
	}
	return 0
}

// landedChecks is every landed primary of the named streams (all, for none) with a head,
// where it lands as land reads it (the brief's REPO: and BASE: lines, else base). A
// sentinel lands by release and has no head: it is not checked.
func landedChecks(s *sprint.Snapshot, streams []string, base string) []landedCheck {
	var out []landedCheck
	for _, pr := range s.Work.Column(sprint.Landed) {
		if sprint.IsSentinel(pr) || len(streams) > 0 && !slices.Contains(streams, pr.Row) {
			continue
		}
		out = append(out, cardCheck(pr, base, "verify-landed"))
	}
	return out
}

// cardCheck is a card's check before git: its head, repository and base as land reads
// them, and why it cannot be checked when it cannot, naming verb's --base.
func cardCheck(pr *sprint.Card, base, verb string) landedCheck {
	cb := swarm.ReadCardBase([]byte(pr.F("brief")))
	ch := landedCheck{ID: pr.ID, Stream: pr.Row, Head: pr.F("head"), Repo: cb.Repo, Base: base}
	switch {
	case ch.Head == "":
		ch.Why = "no head recorded"
	case !shaRE.MatchString(ch.Head):
		ch.Why = "the head " + ch.Head + " is not a commit id"
	case ch.Base == "":
		ch.Why = "its brief names no BASE: line; run: nova-sprint verify-landed --base <branch>"
	}
	if cb.Ref != "" {
		ch.Base = cb.Ref
	}
	return ch
}

// verify checks each card still unjudged, one fetch of the base per repository and base:
// a head the fetched base does not hold is missing (every ancestor of the base came with
// it), else git merge-base --is-ancestor says.
func (l *lander) verify(ctx context.Context, checks []landedCheck) {
	type fetched struct{ tip, why string }
	bases := map[string]fetched{} // by clone and base: one fetch each
	for i := range checks {
		ch := &checks[i]
		if ch.Why != "" {
			continue
		}
		if ch.Repo == "" && l.repoDir == "" {
			ch.Why = "its brief names no REPO: line; run: nova-sprint verify-landed --repo-dir <clone>"
			continue
		}
		dir, why := l.clone(ctx, ch.Repo)
		if why != "" {
			ch.Why = why
			continue
		}
		key := dir + "\x00" + ch.Base
		f, done := bases[key]
		if !done {
			ref := "refs/remotes/origin/" + ch.Base
			if _, err := l.git(ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/"+ch.Base+":"+ref); err != nil {
				f.why = "the fetch of " + ch.Base + " in " + dir + " failed: " + firstLine("", err)
			} else if f.tip, err = l.git(ctx, dir, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
				f.why = "the base " + ch.Base + " could not be read in " + dir + ": " + firstLine("", err)
			}
			bases[key] = f
		}
		if f.why != "" {
			ch.Why = f.why
			continue
		}
		ch.Tip = f.tip
		if _, err := l.git(ctx, dir, "cat-file", "-e", ch.Head+"^{commit}"); err != nil {
			ch.Missing = true
			continue
		}
		res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: l.a.gitEnv, OwnRepo: true}, "merge-base", "--is-ancestor", ch.Head, ch.Tip)
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			ch.Missing = true
		default:
			ch.Why = "git merge-base --is-ancestor " + ch.Head + " " + ch.Tip + " in " + dir + " failed: " + firstLine(string(res.Stderr), err)
		}
	}
}
