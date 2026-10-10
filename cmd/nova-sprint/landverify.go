package main

// verify-landed: every card the store records landed is checked against its base on
// origin, by git and no model (docs/SPEC-SPRINT.md section 7, land-verify-landed-ancestry-r.w1):
// its head is an ancestor of origin/<base> at its tip, or the card is printed as one
// LANDED-MISSING line and the verb exits 1. The store records no merge commit of a landing,
// so the head is what is checked; a landing that regenerated ledgers merges the head, so the
// head is an ancestor all the same. Nothing is written: landed stays final.
//
// The reverse is listed too (land-record-unreported-push-bc.w2): a card in review or merging
// whose head is already an ancestor of origin/<base> at its tip is one LANDED-UNRECORDED
// line, work on the branch that the store does not record landed (a push never reported, a
// pull request outside the deal). landed <card>... --sha <commit> --reason <text> records it.

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
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

func init() {
	verbClasses["verify-landed"] = classRead
	verbClasses["landed"] = classCoordinator
}

// landedCheck is one card and what its check found: missing says its head is not on
// origin's base; why, when set, is why it could not be checked. State is a card's state when
// it is not landed (review or merging): such a card whose head is on the base is unrecorded.
type landedCheck struct {
	ID      string `json:"id"`
	Stream  string `json:"stream"`
	State   string `json:"state,omitempty"`
	Head    string `json:"head"`
	Repo    string `json:"repo,omitempty"`
	Base    string `json:"base"`
	Tip     string `json:"tip,omitempty"`
	Missing bool   `json:"missing"`
	Why     string `json:"why,omitempty"`
	dir     string // the clone it was checked in
}

// unrecorded says an open card's head is on its base: work there, not recorded landed.
func (c landedCheck) unrecorded() bool { return c.State != "" && c.Why == "" && !c.Missing }

func (c landedCheck) line() string {
	l := fmt.Sprintf("LANDED-MISSING %s stream=%s head=%s base=%s tip=%s", c.ID, oneline.Field(c.Stream), c.Head, oneline.Field(c.Base), dashed(c.Tip))
	if c.Repo != "" {
		l += " repo=" + oneline.Field(c.Repo)
	}
	return l
}

func (c landedCheck) unrecordedLine() string {
	l := fmt.Sprintf("LANDED-UNRECORDED %s stream=%s state=%s head=%s base=%s tip=%s", c.ID, oneline.Field(c.Stream), c.State, c.Head, oneline.Field(c.Base), c.Tip)
	if c.Repo != "" {
		l += " repo=" + oneline.Field(c.Repo)
	}
	return l + "; run: nova-sprint landed " + c.ID + " --sha " + c.Tip + " --reason <text>"
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
	all := append(checks, openChecks(s, streams, *base)...)
	l.verify(ctx, all)
	checks, open := all[:len(checks)], all[len(checks):]
	missing, unchecked, unrecorded, openUnchecked := 0, 0, 0, 0
	for _, ch := range checks {
		switch {
		case ch.Why != "":
			unchecked++
		case ch.Missing:
			missing++
		}
	}
	for _, ch := range open {
		switch {
		case ch.Why != "":
			openUnchecked++
		case ch.unrecorded():
			unrecorded++
		}
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"cards": checks, "checked": len(checks) - unchecked, "missing": missing, "unchecked": unchecked, // ignored: strings, bools and ints always encode
			"open": open, "open_checked": len(open) - openUnchecked, "unrecorded": unrecorded, "open_unchecked": openUnchecked})
		fmt.Fprintln(stdout, string(b))
	} else {
		for _, ch := range all {
			switch {
			case ch.Why != "":
				fmt.Fprintf(stdout, "LANDED-UNCHECKED %s stream=%s reason=%s\n", ch.ID, oneline.Field(ch.Stream), oneline.Escape(ch.Why))
			case ch.Missing && ch.State == "":
				fmt.Fprintln(stdout, ch.line())
			case ch.unrecorded():
				fmt.Fprintln(stdout, ch.unrecordedLine())
			}
		}
		fmt.Fprintf(stdout, "VERIFY-UNRECORDED checked=%d unrecorded=%d unchecked=%d\n", len(open)-openUnchecked, unrecorded, openUnchecked)
		fmt.Fprintf(stdout, "VERIFY-LANDED checked=%d missing=%d unchecked=%d\n", len(checks)-unchecked, missing, unchecked)
	}
	switch {
	case unchecked+openUnchecked > 0:
		return 2
	case missing+unrecorded > 0:
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

// openChecks is every card in review or merging of the named streams (all, for none) with a
// head, where it lands as landedChecks reads it: the cards whose work may be on the branch
// unrecorded. A card with no head has pushed nothing and is not checked. A dropped card is
// kept off the table, and the store holds no list of kept records to find it by.
func openChecks(s *sprint.Snapshot, streams []string, base string) []landedCheck {
	var out []landedCheck
	for _, pr := range s.Work.Column(sprint.Review, sprint.Merging) {
		if pr.F("head") == "" || len(streams) > 0 && !slices.Contains(streams, pr.Row) {
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
	if pr.Col != sprint.Landed {
		ch.State = pr.Col
	}
	if cb.Ref != "" {
		ch.Base = cb.Ref
	}
	switch {
	case ch.Head == "":
		ch.Why = "no head recorded"
	case !shaRE.MatchString(ch.Head):
		ch.Why = "the head " + ch.Head + " is not a commit id"
	case ch.Base == "":
		ch.Why = "its brief names no BASE: line; run: nova-sprint " + verb + " --base <branch>"
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
		ch.Tip, ch.dir = f.tip, dir
		if _, err := l.git(ctx, dir, "cat-file", "-e", ch.Head+"^{commit}"); err != nil {
			ch.Missing = true
			continue
		}
		in, why := l.ancestor(ctx, dir, ch.Head, ch.Tip)
		ch.Missing, ch.Why = !in && why == "", why
	}
}

// ancestor is git's word, and no model's, that a is an ancestor of b in dir: exit 1 is "not
// an ancestor", a fact; anything else is why it could not be said.
func (l *lander) ancestor(ctx context.Context, dir, a, b string) (bool, string) {
	res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: l.a.gitEnv, OwnRepo: true}, "merge-base", "--is-ancestor", a, b)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, ""
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, ""
	}
	return false, "git merge-base --is-ancestor " + a + " " + b + " in " + dir + " failed: " + firstLine(string(res.Stderr), err)
}

// cmdLanded is the coordinator's record of work found on the branch but not recorded landed
// (docs/SPEC-SPRINT.md section 7, land-record-unreported-push-bc.w2): a push that happened and
// was never reported, or work landed by a pull request outside the deal. Each card is
// recorded landed only when git, and no model, says its head is an ancestor of --sha and
// --sha is on origin/<base> at its tip; otherwise the verb is refused, naming what git found
// for each card, and nothing is written. The store's step lands the cards as merge --landed
// does (sprint.RecordLanded): all or none, merging cards of one stream only.
func (a *app) cmdLanded(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("landed")
	sha := fs.String("sha", "", "the commit on origin/<base> the cards' work is in, 7 to 40 hex digits (required)")
	reason := fs.String("reason", "", "how the work got there, in a few words (required): a pass cut short after its push, a pull request")
	repoDir := fs.String("repo-dir", "", "the clone to check in, its origin the card's repository (default: land's clone per repository)")
	base := fs.String("base", "", "the base branch of a card whose brief names no BASE: line")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "landed", err.Error())
	}
	if len(pos) == 0 || *sha == "" || strings.TrimSpace(*reason) == "" {
		return refuse(stderr, "landed", "wants <card>... --sha <commit> --reason <text>")
	}
	if !shaRE.MatchString(*sha) || len(*sha) > 40 {
		return refuse(stderr, "landed", "--sha wants a commit id, 7 to 40 hex digits; "+*sha+" is not one")
	}
	if *repoDir != "" {
		fi, err := os.Stat(*repoDir)
		if err != nil || !fi.IsDir() {
			return refuse(stderr, "landed", "--repo-dir wants an existing clone; "+*repoDir+" is not a directory")
		}
		if abs, err := filepath.Abs(*repoDir); err == nil {
			*repoDir = abs
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "landed", err.Error())
	}
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work}, func(*sprint.Snapshot) map[string][]string { return map[string][]string{sprint.Work: pos} })
	if err != nil {
		return a.readFailed("landed", err, stderr)
	}
	var checks []landedCheck
	var refused []string
	for _, id := range pos {
		pr := s.Work.Card(id)
		if pr == nil {
			refused = append(refused, id+": no such card")
			continue
		}
		checks = append(checks, cardCheck(pr, *base, "landed"))
	}
	l := &lander{a: a, c: *c, repoDir: *repoDir}
	l.verify(ctx, checks)
	pins := make([]sprint.LandedPin, 0, len(checks))
	full := ""
	for _, ch := range checks {
		why := ch.Why
		if why == "" {
			why = l.landedAt(ctx, ch, *sha, &full)
		}
		if why != "" {
			refused = append(refused, ch.ID+": "+why)
			continue
		}
		pins = append(pins, sprint.LandedPin{ID: ch.ID, Head: ch.Head, InBase: true})
	}
	if len(refused) > 0 {
		// what git found is a refusal of the cards, as a step's is (exit 1), never of the words
		refuse(stderr, "landed", strings.Join(refused, "; ")+"; nothing was changed")
		return 1
	}
	return a.runStep("landed", *c, st, store.LandedStep(sprint.LandedReq{Pins: pins, Sha: full, Reason: *reason, Who: c.actor}), stdout, stderr)
}

// landedAt is what git found against recording ch landed at sha, "" when nothing: the commit
// is on origin/<base> at its tip (fetched by verify) and the card's head is an ancestor of
// it. full is the commit's full id, set by the first card read; every card names one commit.
func (l *lander) landedAt(ctx context.Context, ch landedCheck, sha string, full *string) string {
	at, err := l.git(ctx, ch.dir, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if err != nil || at == "" {
		return "commit " + sha + " is not in " + ch.dir + " after the fetch of origin/" + ch.Base + " (tip " + ch.Tip + "): it is not on the base"
	}
	if *full != "" && *full != at {
		return "commit " + sha + " is " + at + " here and " + *full + " for another card; record each repository on its own"
	}
	*full = at
	in, why := l.ancestor(ctx, ch.dir, at, ch.Tip)
	switch {
	case why != "":
		return why
	case !in:
		return "commit " + at + " is not an ancestor of origin/" + ch.Base + " at its tip " + ch.Tip + ": it is not on the base"
	case ch.Missing:
		// the commit is on the base and the head is not (verify): the head is not in the commit
		return "its head " + ch.Head + " is not an ancestor of commit " + at + ": its work is not in it"
	}
	if in, why = l.ancestor(ctx, ch.dir, ch.Head, at); why != "" {
		return why
	} else if !in {
		return "its head " + ch.Head + " is not an ancestor of commit " + at + ": its work is not in it"
	}
	return ""
}
