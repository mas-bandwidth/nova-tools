package main

// The land pass reconciles the landed records against the base (docs/SPEC-SPRINT.md section
// 7, no-merge-step-prints-land-and-merge-refuses-a-head-off-the-base): after its batches, land
// checks each landed card of the streams it was given (every stream, for none) against the
// origin tip of its base, and prints each whose head the base does not hold as one
// LANDED-MISSING line, with a line on stderr naming the false landing and why nothing was
// moved. It writes nothing and changes no exit: landed is final in the lifecycle (section 3),
// and the move that returns a false landing to merging is not one of its moves yet, so the
// report names what blocks it instead.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// headCheck is one landed card's head checked against one base tip: Missing says the base does
// not hold it, Why, when set, is why it could not be checked.
type headCheck struct {
	ID      string
	Stream  string
	Head    string
	Repo    string
	Base    string
	Tip     string
	Missing bool
	Why     string
}

// line is the one LANDED-MISSING line a false landing prints.
func (c headCheck) line() string {
	l := fmt.Sprintf("LANDED-MISSING %s stream=%s head=%s base=%s tip=%s", c.ID, oneline.Field(c.Stream), c.Head, oneline.Field(c.Base), dashed(c.Tip))
	if c.Repo != "" {
		l += " repo=" + oneline.Field(c.Repo)
	}
	return l
}

// landedHeadChecks is every landed primary of the named streams (all, for none), where it
// lands as land reads it (the brief's REPO: and BASE: lines, else base). A sentinel lands by
// release and has no head: it is not checked.
func landedHeadChecks(s *sprint.Snapshot, streams []string, base string) []headCheck {
	var out []headCheck
	for _, pr := range s.Work.Column(sprint.Landed) {
		if sprint.IsSentinel(pr) || len(streams) > 0 && !slices.Contains(streams, pr.Row) {
			continue
		}
		cb := swarm.ReadCardBase([]byte(pr.F("brief")))
		ch := headCheck{ID: pr.ID, Stream: pr.Row, Head: pr.F("head"), Repo: cb.Repo, Base: base}
		if cb.Ref != "" {
			ch.Base = cb.Ref
		}
		switch {
		case ch.Head == "":
			ch.Why = "no head recorded"
		case !shaRE.MatchString(ch.Head):
			ch.Why = "the head " + ch.Head + " is not a commit id"
		case ch.Base == "":
			ch.Why = "its brief names no BASE: line; run: nova-sprint land --base <branch>"
		}
		out = append(out, ch)
	}
	return out
}

// reconcileLanded checks the landed records of streams and prints the false ones; a card it
// cannot check (no head, no base, no repository) names why on stderr, since nothing else
// reports it here.
func (l *lander) reconcileLanded(ctx context.Context, streams []string, stdout, stderr io.Writer) {
	if l.dry || l.twin {
		return
	}
	l.a.serial.Lock()
	s, err := l.st.Load(ctx, []string{sprint.Work}, nil)
	l.a.serial.Unlock()
	if err != nil {
		fmt.Fprintf(stderr, "%s land: the landed records were not reconciled with the base: %s; run: nova-sprint where\n", prog, firstLine("", err))
		return
	}
	checks := landedHeadChecks(s, streams, l.base)
	if len(checks) == 0 {
		return
	}
	l.checkHeads(ctx, checks)
	for i := range checks {
		ch := &checks[i]
		if ch.Why == "" && ch.Missing {
			fmt.Fprintln(stdout, ch.line())
			fmt.Fprintf(stderr, "%s land: %s is recorded landed and its head %s is not on %s at %s: a false landing; landed is final, so nothing was moved: tell the owner, and land its head again once it is back in merging\n", prog, ch.ID, ch.Head, ch.Base, dashed(ch.Tip))
		}
	}
}

// checkHeads checks each check's head against its base tip, fetching the base once per clone
// and base: a head the fetched base does not hold is Missing (every ancestor of the base came
// with it), else git merge-base --is-ancestor says. Why, when set, is why it could not be
// checked. It runs no model.
func (l *lander) checkHeads(ctx context.Context, checks []headCheck) {
	type fetched struct{ tip, why string }
	bases := map[string]fetched{} // by clone and base: one fetch each
	for i := range checks {
		ch := &checks[i]
		if ch.Why != "" {
			continue
		}
		if ch.Repo == "" && l.repoDir == "" {
			ch.Why = "its brief names no REPO: line; run: nova-sprint land --repo-dir <clone>"
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
			var err error
			if !l.fetched[key] { // a land pass reads a base it fetched as is: its own push moved the ref
				if _, err = l.git(ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/"+ch.Base+":"+ref); err != nil {
					f.why = "the fetch of " + ch.Base + " in " + dir + " failed: " + firstLine("", err)
				}
			}
			if err == nil {
				if f.tip, err = l.git(ctx, dir, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
					f.why = "the base " + ch.Base + " could not be read in " + dir + ": " + firstLine("", err)
				}
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
		_, err := l.git(ctx, dir, "merge-base", "--is-ancestor", ch.Head, ch.Tip)
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			ch.Missing = true
		default:
			ch.Why = "git merge-base --is-ancestor " + ch.Head + " " + ch.Tip + " in " + dir + " failed: " + firstLine("", err)
		}
	}
}
