package decide

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The shadow of the read decision (SPEC-NOVA-DECIDE section 13, jev-shadow-heavy-read.w1): Jev
// is asked the read decision over each card entering review and its diff, and the answer is
// recorded under its own decision name with every answer marked shadow. It is never a read:
// it holds no place in the read record, applies nothing, and no reader sees it. Its broken
// answer (the verdict BOUNCE) is scored against the heavy-read verdicts and the readers' outcome.

const (
	// ShadowReadName is the shadow reads' decision name; nova-sprint run --decide keeps them in
	// <dir>/read-shadow.jsonl.
	ShadowReadName = "read-shadow"
	// ScoreHeavy and ScoreReaders name what a ReadScore is scored against.
	ScoreHeavy   = "heavy"
	ScoreReaders = "readers"
)

// ShadowReadAsk asks the read decision over the card's brief and diff through b and appends
// the answer, marked shadow, to record under an id per card and state. A card already
// shadow-read at that diff is returned as recorded (existing) and nothing is asked. broken is
// the card's count of broken reads now, which ShadowReadOutcome reads the later ones against.
func ShadowReadAsk(ctx context.Context, b Backend, card, brief, diff string, broken int, record string, at time.Time) (d Decision, existing bool, err error) {
	s, state := ReadSchema(), ReadState(brief, diff, "")
	s.Name = ShadowReadName
	id := Op(card+"@shadow-read", state)
	ds, err := Load(record)
	if err != nil {
		return Decision{}, false, err
	}
	if have := Find(ds, id); have != nil {
		return *have, true, nil
	}
	answers, usage, err := Ask(ctx, b, s, state)
	if err != nil {
		return Decision{}, false, &BackendError{Backend: b.Name(), Err: err}
	}
	for q, a := range answers {
		a.Method = ShadowMethod
		answers[q] = a
	}
	d = Decision{ID: id, Decision: ShadowReadName, Schema: s.Hash(), Backend: b.Name(), At: at.UTC().Format(time.RFC3339), State: state, Answers: answers, Usage: usage,
		Inputs: map[string]string{"card": card, "verdict": answers["verdict"].Value, "shadow": "true", "broken_reads": strconv.Itoa(broken),
			"card_sha256": Sum([]byte(brief)), "diff_sha256": Sum([]byte(diff))}}
	have, err := Append(record, d)
	if have != nil {
		return *have, true, err
	}
	return d, false, err
}

// ShadowReadOutcome is the readers' outcome of the shadow read d from the card's mark now: Bounce
// when more reads have found the card broken than when it was shadow-read, Land when the card
// landed without that, "" while it stands with nothing new, and Dropped when it left the table
// (a card dropped has no outcome to score).
func ShadowReadOutcome(d Decision, now CardMark) (label, note string) {
	broken, _ := strconv.Atoi(d.Inputs["broken_reads"])
	switch {
	case now.Broken > broken:
		return Bounce, fmt.Sprintf("a later read found it broken (%d reads broken, %d when shadow-read)", now.Broken, broken)
	case now.Landed:
		return Land, "the card landed"
	case !now.Placed && now.Dropped:
		return LabelDropped, "the card was dropped"
	}
	return "", ""
}

// ReadScore is the shadow read's broken answer (verdict BOUNCE) scored against one gold: Count
// cards joined, then TP (broken, and gold says broken), FP, FN and TN, with precision and recall
// as percents (0 with nothing to divide).
type ReadScore struct {
	Against        string
	Count          int
	TP, FP, FN, TN int
	Precision      int
	Recall         int
}

// heavyCard is the card a heavy-read verdict import names (the second word of its first line)
// and whether the verdict calls the work broken: any label but ACCEPT.
func heavyCard(d Decision) (card string, broken, ok bool) {
	if d.Decision != "import-"+ImportVerdict || d.Outcome == nil {
		return "", false, false
	}
	f := strings.Fields(firstLine(d.State))
	if len(f) < 2 {
		return "", false, false
	}
	return f[1], d.Outcome.Label != "ACCEPT", true
}

// ShadowReadScores scores the shadow reads against the heavy verdicts (joined by card, a
// card's first verdict) and against the readers' outcome (the label on the shadow read itself),
// heavy first. A shadow read with no gold yet, or whose card was dropped, is not counted.
func ShadowReadScores(shadows, heavy []Decision) []ReadScore {
	gold := map[string]bool{}
	for _, h := range heavy {
		if card, broken, ok := heavyCard(h); ok {
			if _, seen := gold[card]; !seen {
				gold[card] = broken
			}
		}
	}
	scores := []ReadScore{{Against: ScoreHeavy}, {Against: ScoreReaders}}
	tally := func(s *ReadScore, said, broken bool) {
		s.Count++
		switch {
		case said && broken:
			s.TP++
		case said:
			s.FP++
		case broken:
			s.FN++
		default:
			s.TN++
		}
	}
	for _, d := range shadows {
		said := d.Answers["verdict"].Value == Bounce
		if broken, ok := gold[d.Inputs["card"]]; ok {
			tally(&scores[0], said, broken)
		}
		if d.Outcome != nil && (d.Outcome.Label == Bounce || d.Outcome.Label == Land) {
			tally(&scores[1], said, d.Outcome.Label == Bounce)
		}
	}
	for i := range scores {
		s := &scores[i]
		s.Precision, s.Recall = percent(s.TP, s.TP+s.FP), percent(s.TP, s.TP+s.FN)
	}
	return scores
}
