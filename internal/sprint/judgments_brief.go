package sprint

import "strings"

// Brief at the bound cuts the twin (the owner, 2026-10-05: the bound's judgment printed
// `nova-sprint brief <id> --brief-file <path>` for eleven cards and brief refused every one,
// "a card in review keeps its brief"; "a coordinator of any model follows the printed
// command; when it is refused the machine has lied to it"). A card dealt keeps its brief:
// its attempts ran on it. But a card a judgment holds at its bound, the judgment offering
// brief (NBriefWrong, or NBound at the brief's bound), is one whose brief is wrong, and the
// new brief is a new card: brief replaces it by its twin (Recut with the brief), the drop
// and the add as one step, every waiting card that needed it re-pointed to the twin, and
// the judgments open on the old card answered by the drop.

// BriefJudged is the judgments open on the primary id whose decisions offer brief: the
// judgments a brief answers.
func BriefJudged(s *Snapshot, id string) []Open {
	var out []Open
	for _, o := range s.Open {
		if !o.Note.StreamLevel && o.Subject() == id && contains(o.Note.Decisions, "brief") {
			out = append(out, o)
		}
	}
	return out
}

// BriefOrTwin is the brief verb's step: Brief, except for one card dealt that a judgment
// offering brief holds (BriefJudged), which is re-cut as its twin with the new brief
// (Recut); the twin's id is TwinID's. The plan's moved line names the judgments answered.
func BriefOrTwin(s *Snapshot, r BriefReq) Plan {
	if r.Tier != "" {
		return Brief(s, r)
	}
	b := BriefCard{ID: r.ID, Brief: r.Brief, Rules: r.Rules, Needs: r.Needs}
	if len(r.Cards) == 1 {
		b = r.Cards[0]
	} else if len(r.Cards) > 1 {
		return Brief(s, r)
	}
	c := s.Work.Placed(b.ID)
	if c == nil || IsSentinel(c) || unstarted(s, b.ID, "its brief") == "" || dependsOnly(c.F("brief"), b.Brief) {
		return Brief(s, r)
	}
	judged := BriefJudged(s, b.ID)
	if len(judged) == 0 {
		return Brief(s, r)
	}
	p := Recut(s, RecutReq{ID: b.ID, Brief: b.Brief, Rules: b.Rules, Needs: b.Needs, Who: r.Who})
	if len(p.Refused) > 0 {
		return p
	}
	var ids []string
	for _, o := range judged {
		if !contains(ids, o.Note.ID) {
			ids = append(ids, o.Note.ID)
		}
	}
	for i := range p.Units {
		if strings.Contains(p.Units[i].Moved, "; replaces "+b.ID) {
			p.Units[i].Moved += "; the brief's bound: " + b.ID + " cut as its twin, answering " + strings.Join(ids, ",")
		}
	}
	return p
}
