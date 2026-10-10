package sprint

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// externalAsk is the interrupt controller of the external operands, resolve's ask of a
// waiting primary that still waits for one (external.go; tla/CardISA.tla, IsaTick and
// Wait): each of its operands is asked through the tick's answers (one ask per distinct
// operand a tick, the answer kept for that tick). met says every operand holds, and set is
// what the card's move to ready writes with it (FieldExternalMet), so the card is released
// the first tick its operands hold and never before. An ask that failed does not hold: with
// the tick's answers, a failure that is new is written on the card (FieldExternalAsk) by
// the unit returned, so where and card say it, and the next tick asks again; a resolve with
// no answers (the verb) writes nothing.
func externalAsk(s *Snapshot, c *Card, answers *ExternalAnswers) (met bool, set map[string]string, u *Unit) {
	repo, _ := cardhdr.Value(c.F("brief"), "REPO") // ignored: no REPO line is "", and the ask says so
	met, failed := true, ""
	for _, e := range Split(c.F(FieldExternal)) {
		op, _, err := parseExternal(e)
		if err == nil {
			met, err = answers.Holds(op, repo, s.Now)
		}
		if err != nil {
			met, failed = false, err.Error()
		}
		if !met {
			break
		}
	}
	if met {
		return true, map[string]string{FieldExternalMet: stamp(s.Now)}, nil
	}
	if answers == nil || failed == c.F(FieldExternalAsk) {
		return false, nil, nil
	}
	e := setEntry(c, nil, FieldExternalAsk) // the ask answered again: the failure is over
	if failed != "" {
		e = setEntry(c, map[string]string{FieldExternalAsk: failed})
	}
	return false, nil, &Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, e)},
		Moved: c.ID + " external ask: " + orDash(failed)}
}

// externalUnmet is why a move waiting -> ready releases a primary whose external operands
// the tick has not seen hold (the lifecycle's guard: tla/CardISA.tla,
// WaitDispatchesOnlyWhenOperandHolds); "" when it does not.
func externalUnmet(pc *Card, set map[string]string) string {
	if ext := ExternalWaits(pc); len(ext) > 0 && set[FieldExternalMet] == "" {
		return pc.ID + " waits for " + strings.Join(ext, ", ") + ": the tick releases it when they hold"
	}
	return ""
}
