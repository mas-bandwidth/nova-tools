package sprintfn

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestR3ReachedCauseJAccepts: R3's reach opens "sentinel reached" and its
// unreach closes it, each with the cause sprint.ReachedCause (the model's "-",
// tla/SprintEvents.tla), which J's check of a note accepts (a state op with an
// empty cause is REQUEST); and both name the same judgment (type and cause),
// so the unreach closes the field the reach wrote.
func TestR3ReachedCauseJAccepts(t *testing.T) {
	t.Parallel()
	var r3 sprint.Rule
	for _, r := range sprint.RuleTable() {
		if r.Name == "resolve" {
			r3 = r
		}
	}
	if r3.Plan == nil {
		t.Fatal("no rule resolve registered")
	}
	keys := []sprint.AgendaKey{{Key: "resolve:s1", Seq: 1}}
	rp, left := r3.Read(keys, extBounds, 0)
	if len(left) > 0 || len(rp.Sprint) != 1 {
		t.Fatalf("R3's read: %+v, left %v", rp, left)
	}
	// plan is R3 on a stream whose first sentinel g is at 5 with nBefore open
	// cards before it and the judgments open on it.
	plan := func(nBefore int, open []string) sprint.RulePlan {
		t.Helper()
		g := &sprint.Card{ID: "g", Row: "s1", Col: sprint.Waiting, Score: 5, Rev: 1,
			Fields: map[string]string{"kind": sprint.Sentinel, "open": "0", "refused": ""}}
		a := sprint.Answer{Kind: sprint.QueryFront, Front: &sprint.FrontAnswer{Stream: "s1", G: "g", Sigma: 5, NBefore: nBefore},
			Records: []sprint.TableCard{{Table: sprint.Work, Card: g}},
			Heads:   []sprint.HeadAnswer{{Index: sprint.HeadEligBelow, IDs: []string{}}},
			Keys:    []sprint.KeyAnswer{{Key: sprint.KeyJOpenG, Subject: "g", Open: open}, {Key: sprint.KeyDropping}}}
		s, err := sprint.LoadPartial(rp, sprint.ReadAnswer{Epoch: "0", ActiveEpoch: "0", TimeMS: "1790000000123", Sprint: []sprint.Answer{a}})
		if err != nil {
			t.Fatalf("the answer does not load: %v", err)
		}
		p := r3.Plan(s, keys, sprint.Now{R: 1790000000123, Wall: 1790000000123})
		if err := s.UnloadedErr(); err != nil {
			t.Fatalf("R3 read what its read did not load: %v", err)
		}
		return p
	}
	reach := plan(0, nil)
	unreach := plan(1, []string{sprint.NSentinelReached})
	var fields []string
	for name, p := range map[string]sprint.RulePlan{"reach": reach, "unreach": unreach} {
		if len(p.Notes) != 1 || p.Notes[0].Type != sprint.NSentinelReached {
			t.Fatalf("%s: notes %+v, want one of %q", name, p.Notes, sprint.NSentinelReached)
		}
		n := p.Notes[0]
		if n.Cause != sprint.ReachedCause {
			t.Fatalf("%s: the cause is %q, want %q", name, n.Cause, sprint.ReachedCause)
		}
		if ref := jCheck(0, n); ref != nil {
			t.Fatalf("%s: J refuses the note: %+v", name, ref)
		}
		bare := n
		bare.Cause = ""
		if ref := jCheck(0, bare); ref == nil || ref.Code != CodeRequest {
			t.Fatalf("%s: J takes the note with an empty cause (%v): the check proves nothing", name, ref)
		}
		fields = append(fields, n.Type+jFieldSep+n.Cause)
	}
	if reach.Notes[0].Op != "open" || unreach.Notes[0].Op != "close" || fields[0] != fields[1] {
		t.Fatalf("the reach %s and the unreach %s of one judgment: %v", reach.Notes[0].Op, unreach.Notes[0].Op, fields)
	}
}
