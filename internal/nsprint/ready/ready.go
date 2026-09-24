// Package ready is `nova-sprint ready`: the ready antichain and, for one id,
// the first blocker (nova-tools #3109, #2756 v6 build line v6-51, spec 4.11
// and 3.1).
//
// THE HURT (2026-09-23). Six build children closed blocked because their
// DEPENDS-ON named unmerged PRs; Stella's HOLD2 on #3080 found that a merged
// PR whose REST base was unknown released its dependent fail-open; Johnny:
// the five names on a watch were not startable, and nothing said so. Nobody
// could ask "is this startable, and if not, what is in the way" in one call.
//
// THE RULE. A candidate (a queued card, an open task) is READY when every
// DEPENDS-ON entry is merged on its base AND its PATHS are disjoint from
// everything in flight (dealt or running cards, claimed or working tasks)
// and from every candidate already READY ahead of it in deal order, so the
// READY set is an antichain: any two of them can run at once.
//
// Fail closed, everywhere:
//
//   - an unknown base is not merged: a PR merged into a base the forge did
//     not name, or a dependent with no BASE, prints UNKNOWN <dep>
//     base-unresolved and the candidate is not ready (HOLD2 on #3080);
//   - a forge that did not answer is UNKNOWN, never merged;
//   - an unset PATHS is the whole repo: it overlaps everything in that repo;
//   - an unset REPO matches every repo.
//
// The first blocker is one line, its first word the class:
//
//	WAIT <dep> pr#<n> open
//	WAIT PATHS <other>
//	DEAD <dep> pr#<n> closed-unmerged
//	UNKNOWN <dep> pr#<n> base-unresolved
//
// plus the rarer WAIT <dep> issue#<n> open, WAIT <dep> card-<state> no-pr,
// WAIT <dep> pr#<n> merged-into <b> not <base>, DEAD <dep> no-such-card,
// DEAD <dep> card-<state>, and UNKNOWN <dep> ... forge/state/repo lines.
//
// Evaluate never writes; the forge is the dealer's one seam (deal.PRs), so
// a test hands in a map and no host is reached.
package ready

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
)

// Item kinds.
const (
	KindCard = "card"
	KindTask = "task"
)

var outcomeDone = "DONE"

// Item is one card or task as ready sees it.
type Item struct {
	Sprint    string
	ID        string
	Kind      string // KindCard or KindTask
	State     string
	Priority  float64 // lower goes first
	DependsOn []string
	Paths     []string // repo-relative; empty is the whole repo
	Repo      string
	Base      string
}

// Snapshot is one read of what ready needs.
type Snapshot struct {
	// Candidates are the queued cards and open tasks, in deal order (sprint
	// order, then priority, then id). Order decides which of two overlapping
	// candidates is ready.
	Candidates []Item
	// InFlight are the dealt and running cards and the claimed and working
	// tasks: their PATHS are taken.
	InFlight []Item
	// Deps is every card or task a candidate names in DEPENDS-ON, keyed
	// <sprint>/<id>; an id the sprint does not have is absent.
	Deps map[string]deal.DepCard
}

// Verdict is one candidate's answer.
type Verdict struct {
	Item    Item
	Ready   bool
	Blocker string // empty when Ready
}

// Evaluate classifies every candidate in order. Each <repo>#<n> is asked of
// the forge once.
func Evaluate(ctx context.Context, snap Snapshot, prs deal.PRs) []Verdict {
	f := &forge{prs: prs, cache: map[string]answer{}}
	out := make([]Verdict, 0, len(snap.Candidates))
	var claimed []Item
	for _, c := range snap.Candidates {
		v := Verdict{Item: c}
		if b := f.depBlocker(ctx, snap, c); b != "" {
			v.Blocker = b
		} else if b := pathsBlocker(c, snap.InFlight, claimed); b != "" {
			v.Blocker = b
		} else {
			v.Ready = true
			claimed = append(claimed, c)
		}
		out = append(out, v)
	}
	return out
}

// Find returns the verdict for id: "<sprint>/<id>" or a bare id that names
// exactly one candidate. ok is false when no candidate matches, and err
// names an ambiguous bare id.
func Find(vs []Verdict, id string) (Verdict, bool, error) {
	var hits []Verdict
	for _, v := range vs {
		if v.Item.Sprint+"/"+v.Item.ID == id || v.Item.ID == id {
			hits = append(hits, v)
		}
	}
	switch len(hits) {
	case 0:
		return Verdict{}, false, nil
	case 1:
		return hits[0], true, nil
	}
	var names []string
	for _, h := range hits {
		names = append(names, h.Item.Sprint+"/"+h.Item.ID)
	}
	return Verdict{}, false, fmt.Errorf("%s names %d candidates (%s); give <sprint>/<id>", id, len(hits), strings.Join(names, ", "))
}

func isNone(e string) bool { return e == "" || e == "-" || e == "none" }

// parseRef splits `<owner>/<repo>#<n>`; ok is false for anything else, which
// reads as a card or task id in the candidate's sprint.
func parseRef(entry string) (repo string, n int, ok bool) {
	i := strings.LastIndexByte(entry, '#')
	if i <= 0 || !strings.Contains(entry[:i], "/") {
		return "", 0, false
	}
	n, err := strconv.Atoi(entry[i+1:])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return entry[:i], n, true
}

type answer struct {
	ref deal.Ref
	err error
}

type forge struct {
	prs   deal.PRs
	cache map[string]answer
}

func (f *forge) ref(ctx context.Context, repo string, n int) (deal.Ref, error) {
	k := repo + "#" + strconv.Itoa(n)
	if a, ok := f.cache[k]; ok {
		return a.ref, a.err
	}
	var a answer
	if f.prs == nil {
		a.err = fmt.Errorf("no forge seam")
	} else {
		a.ref, a.err = f.prs.Ref(ctx, repo, n)
	}
	f.cache[k] = a
	return a.ref, a.err
}

// depBlocker is the first DEPENDS-ON entry of c that is not merged on c's
// base, as a blocker line; empty when every entry is.
func (f *forge) depBlocker(ctx context.Context, snap Snapshot, c Item) string {
	for _, e := range c.DependsOn {
		if isNone(e) {
			continue
		}
		if b := f.entryBlocker(ctx, snap, c, e); b != "" {
			return b
		}
	}
	return ""
}

func (f *forge) entryBlocker(ctx context.Context, snap Snapshot, c Item, e string) string {
	if repo, n, ok := parseRef(e); ok {
		ref, err := f.ref(ctx, repo, n)
		return refBlocker(e, n, ref, err, c.Base)
	}
	d, ok := snap.Deps[c.Sprint+"/"+e]
	if !ok || !d.Found {
		return "DEAD " + e + " no-such-card"
	}
	switch {
	case d.State == "landed":
		// pr-to-read's merge commit on the dependency's base; it counts only
		// when both bases are known and the same.
		switch {
		case c.Base == "" || d.Base == "":
			return "UNKNOWN " + e + " base-unresolved"
		case d.Base != c.Base:
			return "WAIT " + e + " landed-on " + d.Base + " not " + c.Base
		}
		return ""
	case d.PR > 0:
		repo := d.Repo
		if repo == "" {
			repo = c.Repo
		}
		if repo == "" {
			return fmt.Sprintf("UNKNOWN %s pr#%d repo-unresolved", e, d.PR)
		}
		ref, err := f.ref(ctx, repo, d.PR)
		return refBlocker(e, d.PR, ref, err, c.Base)
	case d.State == "ended" && d.Outcome == outcomeDone && d.PushedSHA == "":
		return "" // closed with no PR and no commit: nothing to land
	case d.State == "cancelled" || d.State == "superseded":
		return "DEAD " + e + " card-" + d.State
	}
	state := d.State
	if state == "" {
		state = "unknown"
	}
	return "WAIT " + e + " card-" + state + " no-pr"
}

// refBlocker reads one forge answer against the dependent's base. An unknown
// base on either side is not merged.
func refBlocker(dep string, n int, ref deal.Ref, err error, base string) string {
	if err != nil {
		return fmt.Sprintf("UNKNOWN %s pr#%d forge: %s", dep, n, oneLine(err.Error()))
	}
	if !ref.IsPR {
		switch ref.State {
		case "closed":
			return ""
		case "open":
			return fmt.Sprintf("WAIT %s issue#%d open", dep, n)
		}
		return fmt.Sprintf("UNKNOWN %s issue#%d state-unresolved", dep, n)
	}
	switch {
	case ref.Merged && (base == "" || ref.Base == ""):
		return fmt.Sprintf("UNKNOWN %s pr#%d base-unresolved", dep, n)
	case ref.Merged && ref.Base != base:
		return fmt.Sprintf("WAIT %s pr#%d merged-into %s not %s", dep, n, ref.Base, base)
	case ref.Merged:
		return ""
	case ref.State == "closed":
		return fmt.Sprintf("DEAD %s pr#%d closed-unmerged", dep, n)
	case ref.State == "open":
		return fmt.Sprintf("WAIT %s pr#%d open", dep, n)
	}
	return fmt.Sprintf("UNKNOWN %s pr#%d state-unresolved", dep, n)
}

// pathsBlocker is WAIT PATHS <other> for the first in-flight item, then the
// first candidate already ready, whose PATHS overlap c's.
func pathsBlocker(c Item, inFlight, claimed []Item) string {
	for _, group := range [][]Item{inFlight, claimed} {
		for _, o := range group {
			if o.Sprint == c.Sprint && o.ID == c.ID {
				continue
			}
			if overlap(c, o) {
				return "WAIT PATHS " + o.ID
			}
		}
	}
	return ""
}

// overlap is true when a and b may touch the same file: the same repo (an
// unset repo matches any), and a path of one equal to or under a path of the
// other (an unset PATHS is the whole repo).
func overlap(a, b Item) bool {
	if a.Repo != "" && b.Repo != "" && a.Repo != b.Repo {
		return false
	}
	if len(a.Paths) == 0 || len(b.Paths) == 0 {
		return true
	}
	for _, p := range a.Paths {
		for _, q := range b.Paths {
			if under(p, q) || under(q, p) {
				return true
			}
		}
	}
	return false
}

// under reports whether p is q or inside the directory q.
func under(p, q string) bool {
	p, q = clean(p), clean(q)
	if q == "" || q == "." {
		return true
	}
	return p == q || strings.HasPrefix(p, q+"/")
}

func clean(p string) string {
	p = strings.TrimPrefix(strings.TrimSpace(p), "./")
	return strings.TrimRight(p, "/")
}

func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
