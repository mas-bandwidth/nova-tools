package sprint

import (
	"slices"
	"strings"
)

// The paths rule's tick step. paths_proposed.go classifies a HOLD that proposes
// PATHS; this step is the one the tick runs, so a proposal the rule would twin
// is twinned only when pathsHoldJudgment allows it. A shared proposal is still
// the one judgment with its command, as before.

func init() {
	for i := range TickRules {
		if TickRules[i].Name == PartRulePaths {
			TickRules[i].Fn = TickPathsHold
		}
	}
}

// TickPathsHold answers the held cards the paths rule answers. A proposal that
// shares a file has its brief file written and its judgment's text made the
// complete command, once. The first proposal that shares none, and that
// pathsHoldJudgment admits, replaces its card by the widened twin (Recut). The
// rule is recorded on the twin and on the card it replaces. One twin a tick.
func TickPathsHold(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	var twin *RuleAnswer
	done := map[string]bool{}
	for _, a := range acting(s, r, ActTwin, ActTwinCmd) {
		if done[a.Card] || a.paths == nil {
			continue
		}
		done[a.Card] = true
		if a.Act == ActTwin {
			pr := s.Work.Placed(a.Card)
			if pr == nil {
				continue
			}
			if pathsHoldJudgment(pr, *a.paths) != "" {
				continue
			}
			aa := a
			prop := *aa.paths
			prop.Brief = pathsHoldTask(prop.Brief, prop.Head, pathsHoldBranch(s, pr))
			aa.paths = &prop
			if twin == nil {
				twin = &aa
			}
			continue
		}
		pr, prop := s.Work.Placed(a.Card), *a.paths
		file, err := writeTwinBrief(s, pr.ID, prop)
		if err != nil {
			p.refuse(pr.ID, "the paths rule could not write the twin's brief "+file+": "+err.Error())
			continue
		}
		n := a.open.Note
		n.What = pathsCmdAt + "; " + a.Why + "; the brief is written at " + file + "; " + n.What
		p.Updates = append(p.Updates, n)
		p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row,
			Changes: []Change{change(Work, setEntry(pr, map[string]string{FieldPathsProposed: prop.mark(), FieldRuleAnswer: RulePaths + ": " + a.Act + " at " + stamp(s.Now)}))},
			Moved:   pr.ID + " " + RuleSaid(RulePaths, a.Act+": "+prop.Line()+"; run: "+TwinCommand(s, pr, prop))})
	}
	if twin == nil {
		return p, 0
	}
	pr, prop := s.Work.Placed(twin.Card), *twin.paths
	q := Recut(s, RecutReq{ID: pr.ID, New: prop.Twin, Brief: prop.Brief, Rules: pr.F(FieldRules), Who: r.who()})
	if len(q.Refused) > 0 {
		p.Refused = append(p.Refused, q.Refused...)
		return p, 0
	}
	said := pathsSaid + pr.ID + " attempt " + itoa(prop.Attempt) + " held with " + prop.Line() + ": replaced by " + prop.Twin + ", PATHS widened by " + strings.Join(prop.New, ",")
	if prop.Head != "" {
		said += ", carrying on from " + prop.Head
	}
	o := twin.open
	at := -1
	for i := range q.Units {
		u := &q.Units[i]
		for j := range u.Changes {
			if c := &u.Changes[j]; c.Table == Work && c.Entry.ID == prop.Twin && c.Entry.Create != nil {
				c.Entry.Set[FieldPathsProposed] = prop.mark()
				c.Entry.Set[FieldRuleAnswer] = RulePaths + ": " + twin.Act + " at " + stamp(s.Now)
				if w := pr.F(FieldWho); w != "" && c.Entry.Set[FieldWho] == "" {
					c.Entry.Set[FieldWho] = w
				}
				u.Moved += "; " + RuleSaid(RulePaths, prop.Line())
			}
		}
		if slices.ContainsFunc(u.Closes, func(c Open) bool { return c.Note.ID == o.Note.ID }) {
			at = i
		}
	}
	if len(q.Units) > 0 {
		if at < 0 {
			at = 0
			q.Units[0].Closes = append(q.Units[0].Closes, o)
		}
		q.Units[at].Notes = append(q.Units[at].Notes, decided(o, said, r.who(), s.Now, pr.ID))
	}
	rule := RulePaths + ": " + twin.Act + " at " + stamp(s.Now)
	q = ruleOnBoth(q, pr.ID, prop.Twin, rule)
	q.Units = append(q.Units, p.Units...)
	q.Updates = append(q.Updates, p.Updates...)
	q.Refused = append(q.Refused, p.Refused...)
	return q, 0
}
