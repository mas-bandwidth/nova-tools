package merge

// ExceptionGateRed is the EXCEPTION class a red landing batch earns when the
// base alone is green and every member passes alone on that base. land-lane
// prints it as "no member is red alone ... and the base is green": a
// combination fault, timing under load, not a member's own defect. A red base
// is the base's fault. A member that fails alone is that member's fault.
// Neither is this class. sprint land retries this class once (spec 4.9).
const ExceptionGateRed = "gate-red"

// GateRedException reports whether an alone-bisect is EXCEPTION class=gate-red.
// memberGreenAlone is one bool per member still in the batch, in batch order.
// An empty list is not a combination: there is no member to pass alone.
func GateRedException(baseGreen bool, memberGreenAlone []bool) bool {
	if !baseGreen || len(memberGreenAlone) == 0 {
		return false
	}
	for _, green := range memberGreenAlone {
		if !green {
			return false
		}
	}
	return true
}
