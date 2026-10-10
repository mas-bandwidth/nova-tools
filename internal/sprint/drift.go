package sprint

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// The drift alarms (docs/SPEC-SPRINT.md section 8, "Drift alarms"). On 2026-10-04 and
// 2026-10-05 the branches drifted for hours before anyone looked: the live server on a side
// branch, cards on temporary and dead branches, and the base red three times through the
// lander's narrow gate, each blocking the promotion to dev. The owner, 2026-10-05: "How can
// we ensure that you ALWAYS do the merging properly from now on, vs. drifting and
// forgetting?" and "Prevention is better than cure". So every drift is a fact the machine
// raises: four judgments, each written once when its drift starts, raised again every
// PassEvery of running time while it holds (the judgment rewritten with the latest facts and
// its raises counted in Before, and a push NRaisedAgain to the coordinator, as the
// coordinator's pass does), never one a tick, and closed when it stops. The episode
// machine is the pass's, tla/CoordinatorPass.tla.
const (
	NDriftAhead    = "the base is ahead of dev past its drift"
	NDriftCardBase = "an open card is cut on another base"
	NDriftServer   = "the live server runs off the base"
	NDriftBaseRed  = "the base is red at its tip"

	PropDriftCommits = "drift_commits" // a count: the base may be this many commits ahead of dev
	PropDriftHours   = "drift_hours"   // hours: the oldest commit of the base not on dev may be this old
)

// DriftTypes is the drift judgments' types, in the order the tick checks them.
var DriftTypes = []string{NDriftAhead, NDriftCardBase, NDriftServer, NDriftBaseRed}

// The thresholds of the base ahead of dev when the coordinator set none: as many commits
// as PromoteCards, and two hours.
const (
	DriftCommitsDefault = PromoteCards
	DriftHoursDefault   = 2
)

// DriftFacts is what the binding read of the repository for a tick (ReadDrift; the gate's
// record beside it). Base empty, or a fact nil, is no fact this tick: the judgments it
// would judge are neither raised nor closed.
type DriftFacts struct {
	Repo   string       // the repository, owner/name; a card naming another REPO: is not judged
	Base   string       // the sprint base, the branch every stream lands on
	Dev    string       // the development branch, DevBranch when ""
	Ahead  *DriftAhead  // the base against dev
	Server *DriftServer // the live server's build commit against the base
	Gate   *DriftGate   // the last gate run at the base, with its scope
}

// DriftAhead is the base against dev: the commits on the base and not on dev, the oldest
// of them by committer time (zero when none), and the base's tip.
type DriftAhead struct {
	Commits int
	Oldest  time.Time
	Tip     string
}

// DriftServer is the live server's build commit and whether it is an ancestor of the
// base's tip; Why says why not, when it is not.
type DriftServer struct {
	Commit string
	On     bool
	Why    string
}

// DriftGate is one gate run at a tip of the base: its scope (the whole tree, and the
// functional class tests included), whether it was red, and what failed. Only a run of the
// whole tree with the functional class judges the base: the lander's narrow gate alone
// never does, green or red.
type DriftGate struct {
	Tip        string
	Whole      bool
	Functional bool
	Red        bool
	Failed     []string
}

// DriftCommits and DriftHours are the base-ahead thresholds: the sprint's settings
// (set --drift-commits, --drift-hours), else the defaults.
func (s *Snapshot) DriftCommits() int { return s.driftSetting(PropDriftCommits, DriftCommitsDefault) }

// DriftHours is the hours threshold (DriftCommits).
func (s *Snapshot) DriftHours() int { return s.driftSetting(PropDriftHours, DriftHoursDefault) }

func (s *Snapshot) driftSetting(prop string, def int) int {
	if s.Work != nil {
		if v, ok := s.Work.Prop(prop); ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 1 {
				return n
			}
		}
	}
	return def
}

// driftValid says v is a drift threshold set takes: a whole number from 1, or default.
func driftValid(v string) bool {
	if v == ReadTierDefault {
		return true
	}
	n, err := strconv.Atoi(v)
	return err == nil && n >= 1
}

// driftConds is each drift that holds on the facts, one condition each (an open card cut
// off the base, one for each such card), and the types the facts say nothing of this tick.
func driftConds(s *Snapshot, r TickReq, f *DriftFacts) (conds []cond, unknown []string) {
	if f == nil || f.Base == "" {
		return nil, DriftTypes
	}
	dev := f.Dev
	if dev == "" {
		dev = DevBranch
	}
	if a := f.Ahead; a == nil {
		unknown = append(unknown, NDriftAhead)
	} else if a.Commits > 0 {
		n, h := s.DriftCommits(), s.DriftHours()
		age, _ := r.running(s.Now, stamp(a.Oldest))
		if a.Oldest.IsZero() {
			age = 0
		}
		if a.Commits > n || age > time.Duration(h)*time.Hour {
			conds = append(conds, cond{typ: NDriftAhead, streamLevel: true, decisions: []string{"act", "wait"},
				what: fmt.Sprintf("origin/%s is %d commits ahead of origin/%s, the oldest %s old (at %s), past the drift of %d commits or %d hours; promote: merge origin/%s into %s, open the PR to %s, run the functional tier; then: nova-sprint promoted --sha <merge sha>",
					f.Base, a.Commits, dev, age.Round(time.Minute), stamp(a.Oldest), n, h, dev, f.Base, dev)})
		}
	}
	for _, c := range s.Work.Cards() {
		if !c.Placed() || IsSentinel(c) || c.Col == Landed || IsPromotionStream(s, c.Row) {
			continue
		}
		brief := c.F("brief")
		if repo, ok := cardhdr.Value(brief, "REPO"); ok && f.Repo != "" && repo != f.Repo {
			continue
		}
		v, ok := cardhdr.Value(brief, "BASE")
		if !ok {
			continue
		}
		base, _, ok := cardhdr.ParseBase(v)
		if !ok || base == f.Base {
			continue
		}
		conds = append(conds, cond{typ: NDriftCardBase, primaries: []string{c.ID}, decisions: []string{"act", "wait"},
			what: fmt.Sprintf("%s (%s) is cut on %s, not the base %s: it would land off the base; re-cut it on BASE: %s (nova-sprint add --replaces %s), or drop it (nova-sprint drop %s --reason <why>)", c.ID, c.Col, base, f.Base, f.Base, c.ID, c.ID)})
	}
	if sv := f.Server; sv == nil {
		unknown = append(unknown, NDriftServer)
	} else if !sv.On {
		why := sv.Why
		if why == "" {
			why = "not an ancestor of origin/" + f.Base
		}
		conds = append(conds, cond{typ: NDriftServer, streamLevel: true, decisions: []string{"act", "wait"},
			what: fmt.Sprintf("the live server was built from %s, %s; remedy: build nova-sprint from origin/%s at its tip, then run: nova-sprint server switch <that binary>", shortCommit(sv.Commit), why, f.Base)})
	}
	if g := f.Gate; g == nil || !g.Whole || !g.Functional || f.Ahead == nil || g.Tip != f.Ahead.Tip {
		// no whole-tree run at the tip the base is at now: the last judgment stands
		unknown = append(unknown, NDriftBaseRed)
	} else if g.Red {
		conds = append(conds, cond{typ: NDriftBaseRed, streamLevel: true, decisions: []string{"act", "wait"},
			what: fmt.Sprintf("origin/%s is red at its tip %s by the whole-tree gate with the functional class: %s; nothing lands green on it and dev cannot take it; fix it on %s first", f.Base, shortCommit(g.Tip), Preview(g.Failed, ", "), f.Base)})
	}
	return conds, unknown
}

// TickDrift is the tick's drift alarms, planned in the deadlines part on the facts the
// binding read (TickReq.Drift; docs/SPEC-SPRINT.md section 8, "Drift alarms"): a judgment
// for each drift that starts, the open one raised again every PassEvery of running time
// while it holds, and closed when it stops. A type the facts say nothing of keeps its open
// judgment, and the coordinator's wait on it, as they are: no fact is never a clear.
func TickDrift(s *Snapshot, r TickReq, f *DriftFacts) (Plan, int) {
	var p Plan
	conds, unknown := driftConds(s, r, f)
	carry := func(o Open) {
		// held as it stands: the same key, so notify neither writes nor closes it (a wait
		// run out is closed by notify, and raised again on its last facts)
		conds = append(conds, cond{typ: o.Note.Type, stream: o.Note.Stream, primaries: []string{o.Subject()}, streamLevel: o.Note.StreamLevel, what: o.Note.What})
	}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && slices.Contains(unknown, o.Note.Type) {
			carry(o)
		}
	}
	for _, o := range s.Acked {
		if !o.Note.Review.IsZero() && slices.Contains(unknown, o.Note.Type) {
			carry(o)
		}
	}
	due := notify(&p, s, conds, DriftTypes, r)
	reraiseDrift(&p, s, conds, r)
	return p, due
}

// reraiseDrift raises again each open drift judgment whose drift holds and whose next
// raise is due (Before+1 times PassEvery of running time after it was written): the
// judgment rewritten with the latest facts and the raises counted in Before, and the push
// to the coordinator. Between raises a judgment whose facts moved is rewritten in place,
// with no push; one the facts said nothing of this tick is raised again as it was last
// judged. The coordinator's wait closes the judgment and holds the drift (Wait, a condition
// the tick keeps), so a waited drift is not open here and is not raised until its time.
func reraiseDrift(p *Plan, s *Snapshot, conds []cond, r TickReq) {
	holding := map[string]cond{}
	for _, c := range conds {
		for _, sub := range c.subjects() {
			holding[condKey(c.typ, sub, c.card, c.what)] = c
		}
	}
	to := s.Coordinator
	if to == "" {
		to = "coordinator"
	}
	done := map[string]bool{}
	for _, o := range s.Open {
		n := o.Note
		if n.Kind != Judgment || !slices.Contains(DriftTypes, n.Type) || done[n.ID] {
			continue
		}
		c, ok := holding[condKey(n.Type, o.Subject(), n.Card, n.What)]
		if !ok {
			continue
		}
		done[n.ID] = true
		d, ok := r.running(s.Now, stamp(n.At))
		k := int(d / PassEvery)
		if !ok || k <= n.Before {
			if n.What != c.what {
				n.What = c.what
				p.Updates = append(p.Updates, n)
			}
			continue
		}
		n.Before, n.What = k, c.what
		p.Updates = append(p.Updates, n)
		p.Notes = append(p.Notes, Note{Kind: Happened, Type: NRaisedAgain, Stream: n.Stream, Primaries: n.Primaries, Count: n.Count, Who: r.who(), To: to, At: s.Now,
			What: fmt.Sprintf("%s (%s) still holds, open since %s: %s", n.ID, n.Type, stamp(n.At), c.what),
			Hint: "run: nova-sprint inbox; wait it to quiet it"})
	}
}

// shortCommit is a commit as a judgment names it: twelve digits, or why there is none.
func shortCommit(sha string) string {
	switch {
	case sha == "":
		return "no source commit"
	case len(sha) > 12:
		return "commit " + sha[:12]
	}
	return "commit " + strings.TrimSpace(sha)
}
