package tokens

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// Unit attribution: which PIECE OF WORK a transcript's spend belongs to.
//
// The repo column answers "what did this month cost on nova-tools". Glenn's obligation is
// the other question: what did THIS PIECE OF WORK cost. A work set already names the
// pieces -- `(unit "u3" :pr 1412 :branch "rowan/ci-…" :lane "three" …)` -- so the tool does
// not invent a taxonomy; it reads the coordinator's own.
//
// ONE RULE, ONE FUNCTION, like repo.go, and the rule is deliberately coarser than repo's:
// a repo is attributed per MESSAGE, because one window touches three repos in an hour; a
// unit is attributed per TRANSCRIPT, because a child is spawned for one unit and works on
// that unit until it stops. Attributing per message would put a child's `gh pr view` of a
// sibling's PR onto the sibling's unit.

// NoUnit is the cell a row carries when nothing named a unit: the `-` every absent value
// in this tool is written as, never an empty cell and never a guess.
const NoUnit = Dash

// Unit is one work-set unit reduced to the three things a transcript can name. The id is
// what a row is attributed to; the other three are how it is found.
type Unit struct {
	ID     string
	PR     string // the :pr number as decimal digits, "" when the unit has none
	Branch string // the :branch, "" when the unit has none
	Lane   string // the :lane, which names the clone directory a lane works in
}

// Units is a work set's units in written order. Order is the tie-break: two units that
// both match one token is a work set whose own keys collide, and the FIRST as written
// wins, so two runs over one file answer the same.
type Units struct {
	Set   string // the work set's id, for the line that says what was loaded
	Units []Unit
}

// LoadUnits reads a work set and keeps the units that can be matched at all. A unit with
// no :pr, no :branch and no :lane names nothing a transcript writes, so it is loaded and
// simply never matches -- it is still counted in Len, because a work set whose units are
// all unmatchable is a fact a person wants told rather than a silent zero.
func LoadUnits(path string) (*Units, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ws, err := worklang.ParseWorkSet(path, raw, worklang.DefaultLimits())
	if err != nil {
		return nil, err
	}
	u := &Units{Set: ws.ID}
	for _, w := range ws.Units {
		if w.ID == "" {
			// A member that is not a `(unit "id" …)` form has nothing to attribute TO.
			// `nova-work plan check` is the verb that reports it; this one steps over it.
			continue
		}
		u.Units = append(u.Units, Unit{ID: w.ID, PR: digitsOnly(w.PR()), Branch: w.Branch(), Lane: w.Lane()})
	}
	if len(u.Units) == 0 {
		return nil, fmt.Errorf("%s: the work set carries no unit with an id; --units wants a (work-set … :units ((unit \"id\" …) …)) file", path)
	}
	return u, nil
}

// Len is how many units were loaded.
func (u *Units) Len() int {
	if u == nil {
		return 0
	}
	return len(u.Units)
}

// Match is THE statement of the rule, and every reader reaches a unit id through it:
//
//	take every path-like and text token in the transcript's tool-call inputs, in order
//	for each token, in order: the first unit the token names wins; stop
//	a token names a unit when it carries that unit's PR number, its branch, or its
//	lane's clone directory
//	no token named a unit -> "-"
//
// FIRST MATCH WINS, over the tokens and then over the units, because a child that opened
// its own PR and then read a sibling's is still working on its own: the first thing it
// named is the piece of work it was given.
func (u *Units) Match(inputs []string) string {
	if u == nil || len(u.Units) == 0 {
		return NoUnit
	}
	for _, tok := range inputs {
		if tok == "" {
			continue
		}
		for _, unit := range u.Units {
			if unit.names(tok) {
				return unit.ID
			}
		}
	}
	return NoUnit
}

// names reports whether one token names this unit. Each of the three is BOUNDED, because
// an unbounded substring is the defect this rule exists to avoid: `#141` inside `#1412`
// would put lane three's spend on somebody else's unit, and `rowan/x` inside
// `rowan/xylem` would do the same for a branch.
func (u Unit) names(tok string) bool {
	if u.PR != "" {
		for _, lead := range []string{"#", "/pull/", "pull/", "pr-", "pr="} {
			if boundedAfter(tok, lead+u.PR, isDigit) {
				return true
			}
		}
	}
	if u.Branch != "" && boundedAfter(tok, u.Branch, isBranchByte) && boundedBefore(tok, u.Branch, isBranchLead) {
		return true
	}
	if u.Lane != "" {
		// The lane clone directory, as the card for a lane writes it: `tmp/lane-three/`
		// under a working root, and `~/lane-three` on a bench. Both are the element
		// `lane-<name>`, so the rule is written once over the element and not twice over
		// the two spellings.
		name := "lane-" + u.Lane
		if boundedAfter(tok, name, isLaneByte) && boundedBefore(tok, name, isLaneByte) {
			return true
		}
	}
	return false
}

// boundedAfter reports whether tok holds want at least once with a byte after it that
// `cont` does not accept -- or with nothing after it at all.
func boundedAfter(tok, want string, cont func(byte) bool) bool {
	for i := 0; ; {
		j := strings.Index(tok[i:], want)
		if j < 0 {
			return false
		}
		end := i + j + len(want)
		if end == len(tok) || !cont(tok[end]) {
			return true
		}
		i += j + 1
		if i >= len(tok) {
			return false
		}
	}
}

// boundedBefore is the same at the other end: at least one occurrence whose preceding byte
// is not a continuation, so `x/rowan/a` does not name the branch `rowan/a` only by
// accident of being inside `their-rowan/a`.
func boundedBefore(tok, want string, cont func(byte) bool) bool {
	for i := 0; ; {
		j := strings.Index(tok[i:], want)
		if j < 0 {
			return false
		}
		start := i + j
		if start == 0 || !cont(tok[start-1]) {
			return true
		}
		i += j + 1
		if i >= len(tok) {
			return false
		}
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isBranchByte is what a git ref name may carry, so a longer ref is not read as a shorter
// one with something after it.
func isBranchByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-' || c == '_' || c == '.' || c == '/':
		return true
	}
	return false
}

// isBranchLead is isBranchByte WITHOUT the slash, and it is the rule at the FRONT of a
// branch name. `origin/rowan/bus-host-header` is that branch qualified by its remote and
// names it; `their-rowan/bus-host-header` is a different branch that happens to end with
// it. The slash is what tells those two apart, so it is a boundary before the name and a
// continuation after it -- `rowan/ci/x` is not the branch `rowan/ci`.
func isBranchLead(c byte) bool { return c != '/' && isBranchByte(c) }

// isLaneByte is what a directory element may carry beside the lane's own name. The slash
// is NOT one: `lane-three/repo` names lane three, and `lane-threes` does not.
func isLaneByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-' || c == '_' || c == '.':
		return true
	}
	return false
}

// digitsOnly is a :pr value reduced to its digits: `1412`, `#1412` and `"#1412"` are one
// number, and anything with no digits in it is no number at all.
func digitsOnly(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if isDigit(s[i]) {
			b.WriteByte(s[i])
		}
	}
	out := b.String()
	if out == "" {
		return ""
	}
	// A leading zero is not a PR number, and neither is a value so long it is not one.
	if n, err := strconv.ParseInt(out, 10, 64); err != nil || n <= 0 {
		return ""
	}
	return strings.TrimLeft(out, "0")
}
