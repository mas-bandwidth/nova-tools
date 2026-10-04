package cardhdr

import (
	"regexp"
	"strings"
)

// Who is what a card's WHO line says of the worker it is dealt to (the owner,
// 2026-10-03: "Could we try expressing the work left for nova-tools-1.1.0 into
// cards, and doing it via the sprint, but doing parts on friends where we would
// normally do friend work."): `WHO: friend` is any friend, `WHO: friend <name>` the
// friend of that name, and a card with no WHO line is a machine's, dealt to the
// fleet as every card before it was (docs/SPEC-SPRINT.md, the friends).
type Who struct {
	Friend bool   // a friend's card: WHO: friend [<name>]
	Name   string // the friend it names, "" for any friend
}

// friendNameRE is a friend's name as a friend row names her: letters, digits, _ and -.
var friendNameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)

// ReadWho reads a brief's WHO line from its header block (the `key: value` lines under
// line 1, up to the first blank line or line of prose, the key in any case), the one
// parser of it: the zero Who when the brief has none. why is "" or the one line naming
// what is wrong and what to write.
func ReadWho(brief string) (w Who, why string) {
	_, rest, _ := strings.Cut(brief, "\n")
	for rest != "" {
		var l string
		l, rest, _ = strings.Cut(rest, "\n")
		k, v, ok := KeyValue(l)
		if !ok {
			break
		}
		if !strings.EqualFold(k, "who") {
			continue
		}
		f := strings.Fields(v)
		switch {
		case len(f) == 1 && strings.EqualFold(f[0], "friend"):
			return Who{Friend: true}, ""
		case len(f) == 2 && strings.EqualFold(f[0], "friend") && friendNameRE.MatchString(f[1]):
			return Who{Friend: true, Name: f[1]}, ""
		}
		return Who{}, "WHO: " + v + " is not `friend` or `friend <name>` (a friend row's name: letters, digits, _ and -); a card with no WHO line is dealt to the fleet"
	}
	return Who{}, ""
}
