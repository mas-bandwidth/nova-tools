package sprint

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// RebaseReq moves every unlanded card whose BASE is From to To. Contains,
// when set, is the git merge-base --is-ancestor check that To holds From; a
// non-nil error refuses the whole rebase and names it. It is not part of the
// request's arguments, so a retry under one --op is held to the branches
// alone.
type RebaseReq struct {
	From, To string
	Who      string
	Contains func(from, to string) error `json:"-"`
}

// Rebase moves every unlanded card whose brief's BASE line names From to To
// (docs/SPEC-SPRINT.md, the rebase verb): an undealt card's brief changes; a
// dealt or merging card keeps its head, and the new base must contain the old
// one (Contains, git merge-base --is-ancestor), so the head it holds still
// lands. A card landed, a sentinel and a card whose BASE is another branch
// are left alone. Every move is one line in the log, one line per card.
func Rebase(s *Snapshot, r RebaseReq) Plan {
	var p Plan
	from, to := strings.TrimSpace(r.From), strings.TrimSpace(r.To)
	switch {
	case from == "" || to == "":
		p.refuse("rebase", "wants --from <branch> and --to <branch>; run: nova-sprint help rebase")
		return p
	case from == to:
		p.refuse("rebase", "--from and --to name the same branch "+from+"; nothing was changed")
		return p
	}
	if r.Contains != nil {
		if err := r.Contains(from, to); err != nil {
			p.refuse("rebase", "the base "+to+" does not contain "+from+", so a dealt card's head would not land on it: "+err.Error()+"; nothing was changed")
			return p
		}
	}
	for _, c := range s.Work.Cards() {
		if !c.Placed() || IsSentinel(c) || c.Col == Landed {
			continue
		}
		brief := c.F("brief")
		v, ok := cardhdr.Value(brief, "BASE")
		if !ok {
			continue
		}
		base, _, ok := cardhdr.ParseBase(v)
		if !ok || base != from {
			continue
		}
		next := rebaseBrief(brief, to)
		what := fmt.Sprintf("%s BASE %s -> %s (%s)", c.ID, from, to, c.Col)
		if head := c.F("head"); head != "" {
			what += " head=" + head + " kept"
		}
		n := happened("card rebased", c.Row, s.Now, c.ID)
		n.Who, n.What = r.Who, what
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row,
			Changes: []Change{change(Work, setEntry(c, map[string]string{"brief": next}))},
			Notes:   []Note{n}, Moved: what})
	}
	return p
}

// rebaseBrief rewrites a brief's BASE line to the branch to: the card keeps
// every other byte. A brief with no BASE line is returned as it is.
func rebaseBrief(brief, to string) string {
	lines := strings.Split(brief, "\n")
	for i, l := range lines {
		if k, _, ok := cardhdr.KeyValue(l); ok && k == "BASE" {
			lines[i] = "BASE: " + to
			return strings.Join(lines, "\n")
		}
	}
	return brief
}

// MissingBaseCard is one card cut on a base that is gone.
type MissingBaseCard struct {
	ID     string
	Stream string
}

// MissingBaseBase is the branch every card of a missing-base judgment is cut
// on, and the cards: the rebase line that fixes them names it.
type MissingBase struct {
	Base  string
	Cards []MissingBaseCard
}

// MissingBaseJudgment is the one judgment a land whose base branch is missing
// raises, naming every card on that base and the rebase line that fixes it
// (docs/SPEC-SPRINT.md, the rebase verb). cards is every unlanded card whose
// BASE is base, in id order.
func MissingBaseJudgment(base string, cards []MissingBaseCard) Note {
	var ids, streams []string
	seen := map[string]bool{}
	for _, c := range cards {
		ids = append(ids, c.ID)
		if c.Stream != "" && !seen[c.Stream] {
			seen[c.Stream] = true
			streams = append(streams, c.Stream)
		}
	}
	j := Note{Kind: Judgment, Type: NMissingBase, Count: len(ids), Primaries: ids,
		Decisions: []string{"rebase", "wait", "drop"}}
	stream := ""
	if len(streams) == 1 {
		stream = streams[0]
	}
	j.Stream = stream
	j.Other = base
	j.What = fmt.Sprintf("the base %s is gone, and %d unlanded card(s) still name it: %s; run: nova-sprint rebase --from %s --to <the branch that replaces it>",
		base, len(ids), Preview(ids, ", "), base)
	return j
}

// MissingBaseCards is every unlanded card whose brief's BASE line names base,
// in id order: the cards a missing-base judgment names.
func MissingBaseCards(s *Snapshot, base string) []MissingBaseCard {
	var out []MissingBaseCard
	for _, c := range s.Work.Cards() {
		if !c.Placed() || IsSentinel(c) || c.Col == Landed {
			continue
		}
		v, ok := cardhdr.Value(c.F("brief"), "BASE")
		if !ok {
			continue
		}
		if b, _, ok := cardhdr.ParseBase(v); ok && b == base {
			out = append(out, MissingBaseCard{ID: c.ID, Stream: c.Row})
		}
	}
	return out
}
