package cardcontract

import (
	"fmt"
	"strconv"
	"strings"
)

// Carry is how a rework's checkout was staged (docs/SPEC-CARD-CONTRACT.md, where a rework
// starts): at the tip of its base branch as staging found it on origin, with the work of the
// attempt whose head it continues carried on top when that work applies cleanly there. A
// rework staged at an earlier attempt's head instead stayed on a base hours old, and a fix
// that said to start again from the tip could not be obeyed: the finish refuses a head that
// does not descend from the staged commit (nova-tools#5215).
type Carry struct {
	Base   string // the base branch
	Tip    string // its tip on origin when the checkout was staged, a full sha
	Prev   string // the head carried: the last pushed head of any earlier attempt; "" when none pushed
	From   int    // the attempt Prev is the head of
	Staged string // the commit the checkout is at, the one the finish counts from
	State  string // CarryNone, CarryOK, CarryHeld or CarryConflict
}

// The states of a carry.
const (
	CarryNone     = "none"     // no earlier attempt pushed: the checkout is the tip
	CarryOK       = "carried"  // the earlier work applied cleanly: the checkout is the tip and that work
	CarryHeld     = "held"     // the tip already holds the earlier work: the checkout is the tip
	CarryConflict = "conflict" // the earlier work did not apply cleanly: the checkout is the bare tip
)

// CarryLinePrefix begins native's one line about a rework's carry, which the member reads
// from native's log into the finish's report, so the card's timeline says where the attempt
// was staged and whether the work before it came with it.
const CarryLinePrefix = "STAGE CARRY "

// Line is native's line about the carry: CarryLinePrefix, then its words (Words).
func (c Carry) Line() string { return CarryLinePrefix + c.Words() }

// Words are the carry in one line of key=value words: the staged commit, the tip and its
// branch, and the state, with the attempt carried when there was one.
func (c Carry) Words() string {
	w := "staged=" + short12(c.Staged) + " tip=" + short12(c.Tip) + " of " + orDash(c.Base) + " carry=" + c.State
	if c.Prev != "" {
		w += " attempt=" + strconv.Itoa(c.From) + " prev=" + short12(c.Prev)
	}
	return w
}

// sentence is what JOB.md says of the carry, right after the attempt line.
func (c Carry) sentence() string {
	at := fmt.Sprintf("This checkout is staged at the tip of %s as origin held it when it was staged, %s", orDash(c.Base), c.Tip)
	switch c.State {
	case CarryOK:
		if c.Staged == c.Prev {
			return fmt.Sprintf("%s: attempt %d's head, %s, descends from it, and the checkout starts from that head.", at, c.From, c.Prev)
		}
		return fmt.Sprintf("%s, with the work of attempt %d (its head %s) carried on top as one commit, %s, and the checkout starts from that commit.", at, c.From, c.Prev, c.Staged)
	case CarryHeld:
		return fmt.Sprintf("%s, which already holds the work of attempt %d (its head %s).", at, c.From, c.Prev)
	case CarryConflict:
		return fmt.Sprintf("%s. The work of attempt %d (its head %s) did not apply cleanly at the tip and is not in this checkout.", at, c.From, c.Prev)
	}
	return at + "; no attempt before this one pushed work to carry."
}

// redo is what a rework whose earlier work did not carry is asked first, "" for every other.
func (c Carry) redo() string {
	if c.State != CarryConflict {
		return ""
	}
	return fmt.Sprintf("the work of attempt %d must be redone from this tip: `git diff %s...%s` shows it, and the commit you make holds it again with this attempt's fix.", c.From, c.Tip, c.Prev)
}

// short12 is a sha's first twelve characters, the whole value when it is shorter.
func short12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return orDash(s)
}

// ParseCarryLine is the words of the last carry line in native's log, "" when it has none.
func ParseCarryLine(log []byte) string {
	words := ""
	for _, l := range strings.Split(string(log), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), CarryLinePrefix); ok {
			words = strings.TrimSpace(rest)
		}
	}
	return words
}
