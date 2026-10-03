package sprint

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// THE LANDED SCORE (docs/SPEC-SPRINT.md section 7, the landed score; docs/SPEC-NOVA-DECIDE.md
// section 9). nova-sprint land scores every head of a batch it pushed and reported with
// nova-decide's score decision, then reports the scores in one step: each card's top class,
// its p and the decision's id are written on the landed card, and the cards whose top class
// meets the sprint row's bar (DecideScoreBar) are listed in one judgment for the batch, for
// the coordinator to add a repair card or to accept the landing as it is (ack).

// NScoredLow is the judgment of a batch whose landed work scored at or above the bar.
const NScoredLow = "landed work scored low"

// The landed score's fields on a landed card.
const (
	FieldLandedP     = "landed_p"     // the top class's p, three places
	FieldLandedClass = "landed_class" // the top class
	FieldLandedOp    = "landed_op"    // the score decision's id in the lander's record
)

// CardScore is one landed card's score: its top class, that class's p, and the id of the
// decision in the lander's record.
type CardScore struct {
	ID, Op, Class string
	P             float64
}

// ScoreReq is a batch's scores, as land reports them after the batch landed.
type ScoreReq struct {
	Stream string
	Scores []CardScore
	Who    string
}

// RecordScores writes each score on its landed card and raises one NScoredLow judgment
// listing the cards whose p meets the bar, the highest first. A card not landed is
// refused; a card already carrying the same decision id is recorded already and writes
// nothing, so a replay raises no second judgment. An empty bar (the row's "" or a store
// nova-config never applied) writes the scores and raises no judgment.
func RecordScores(s *Snapshot, r ScoreReq) Plan {
	var p Plan
	bar, barred := scoreBar(s.DecideScoreBar)
	var low []CardScore
	for _, sc := range r.Scores {
		c := s.Work.Placed(sc.ID)
		switch {
		case c == nil || c.Col != Landed:
			p.refuse(sc.ID, "not landed ("+placeWord(orEmpty(c, sc.ID))+"); a score is reported for a card its land pushed and reported")
			continue
		case c.F(FieldLandedOp) == sc.Op:
			continue
		}
		set := map[string]string{FieldLandedP: strconv.FormatFloat(sc.P, 'f', 3, 64), FieldLandedClass: sc.Class, FieldLandedOp: sc.Op}
		p.Units = append(p.Units, Unit{Key: sc.ID, Stream: r.Stream, Changes: []Change{change(Work, setEntry(c, set))},
			Moved: fmt.Sprintf("%s scored %s %s", sc.ID, sc.Class, set[FieldLandedP])})
		if barred && sc.P >= bar {
			low = append(low, sc)
		}
	}
	if len(p.Refused) > 0 || len(low) == 0 {
		return p
	}
	ids, what := make([]string, len(low)), make([]string, len(low))
	slices.SortFunc(low, func(a, b CardScore) int { return cmp.Or(cmp.Compare(b.P, a.P), cmp.Compare(a.ID, b.ID)) })
	for i, sc := range low {
		ids[i], what[i] = sc.ID, fmt.Sprintf("%s %s %.2f", sc.ID, sc.Class, sc.P)
	}
	n := judgment(NScoredLow, r.Stream, s.Now, 0, ids...)
	n.Who = r.Who
	n.What = Preview(what, ", ") + fmt.Sprintf(" (the bar %s; nova-decide findings clusters the classes)", strconv.FormatFloat(bar, 'f', -1, 64))
	last := &p.Units[len(p.Units)-1]
	last.Notes = append(last.Notes, n)
	return p
}

// scoreBar is the row's bar as a probability; false for none ("") or one that does not
// parse, which nova-config's check refuses before it reaches the store.
func scoreBar(raw string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return v, err == nil && v >= 0 && v <= 1
}
