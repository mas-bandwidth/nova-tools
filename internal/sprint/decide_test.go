package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
)

// The decide lane's work, read from the work table alone (DecideDue): every card never
// dealt and ungraded is graded (not a sentinel, not one dealt, not one graded); every
// decision on a card that landed or was dropped has its outcome due, labelled by the card's
// fate: an attempt decided at the landing attempt `landed`, one decided earlier
// `later-<tier>`, the grade the tier that landed it; every decision of a dropped card
// `dropped`. A card still in flight has no outcome due.
func TestDecideDueGradesAndLabelsEveryDecisionByTheCardsFate(t *testing.T) {
	t.Parallel()
	w := NewTable(Work)
	put := func(id, col string, placed bool, f map[string]string) {
		c := &Card{ID: id, Row: "s1", Fields: f}
		if placed {
			c.Col = col
		}
		w.Put(c)
	}
	grade := func(v string) string { return decide.Decided{Value: v, P: 0.8, Op: "g-" + v}.String() }
	put("ready-1", Ready, true, map[string]string{"brief": "b1", "attempt": "0"})
	put("waiting-1", Waiting, true, map[string]string{"brief": "b2", "attempt": "0"})
	put("graded-1", Ready, true, map[string]string{"brief": "b3", "attempt": "0", FieldGrade: grade("flash")})
	put("sentinel-1", Waiting, true, map[string]string{"kind": "sentinel", "attempt": "0"})
	put("reworked-1", Ready, true, map[string]string{"brief": "b4", "attempt": "1"})
	put("landed-1", Landed, true, map[string]string{"brief": "c: work (s1) tier: pro\n", "attempt": "2", FieldTierNow: "pro", FieldGrade: grade("pro"),
		PrefixDecided + "landed-1@1.aaaaaaaaaaaa": "needs-pro p=0.900 used=yes", PrefixDecided + "landed-1@2.bbbbbbbbbbbb": "done p=0.950 used=no"})
	put("dropped-1", "", false, map[string]string{"brief": "b5", "attempt": "1", "outcome": "dropped", "reason": "out of scope",
		PrefixDecided + "dropped-1@1.cccccccccccc": "wrong-scope p=0.800 used=yes", FieldGrade: grade("flash")})
	put("review-1", Review, true, map[string]string{"brief": "b6", "attempt": "1", PrefixDecided + "review-1@1.dddddddddddd": "done p=0.6 used=no"})
	grades, outcomes := DecideDue(&Snapshot{Work: w})
	assert.Equal(t, []GradeAsk{{Card: "ready-1", Brief: "b1"}, {Card: "waiting-1", Brief: "b2"}}, grades)
	assert.ElementsMatch(t, []DecideOutcome{
		{Decision: decide.AttemptName, Op: "landed-1@1.aaaaaaaaaaaa", Label: "later-pro", Note: "landed-1 landed at attempt 2 on pro"},
		{Decision: decide.AttemptName, Op: "landed-1@2.bbbbbbbbbbbb", Label: decide.LabelLanded, Note: "landed-1 landed at attempt 2 on pro"},
		{Decision: decide.GradeName, Op: "g-pro", Label: "pro", Note: "landed-1 landed at attempt 2 on pro"},
		{Decision: decide.AttemptName, Op: "dropped-1@1.cccccccccccc", Label: decide.LabelDropped, Note: "dropped-1 dropped: out of scope"},
		{Decision: decide.GradeName, Op: "g-flash", Label: decide.LabelDropped, Note: "dropped-1 dropped: out of scope"},
	}, outcomes)
}

// A landed card of script steps alone landed as a script, whatever tier it was on.
func TestALandedScriptCardsTierIsScript(t *testing.T) {
	t.Parallel()
	script := "c: rename (s1) tier: flash\nPATHS: a.go\nSTEP 1. Rename.\nCOMMIT: rename\nPATHS: a.go\nVERDICT: ok\nSCRIPT: regex\n```\ns/old/new/ a.go\n```\nPOST: exit0 go vet ./...\n"
	assert.Equal(t, decide.GradeScript, LandedTier(&Card{ID: "c", Fields: map[string]string{"brief": script}}))
	assert.Equal(t, "flash", LandedTier(&Card{ID: "c", Fields: map[string]string{"brief": "c: x (s1) tier: pro\n", FieldTierNow: "flash"}}))
}
