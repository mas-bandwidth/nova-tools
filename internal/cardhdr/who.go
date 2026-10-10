package cardhdr

import (
	"regexp"
	"strings"
)

// Who is a preference or a hard pin from the brief header (docs/SPEC-SPRINT.md,
// WHO preference). `WHO: friend` is any friend, `WHO: friend <name>` prefers that
// friend, and `WHO: only friend <name>` waits for her alone. `WHO: friend <name> only`
// is the same hard pin with the new word order (Tail). A card with no WHO
// line, or `WHO: -`, is unpinned: friends whose class covers its tier, then the fleet.
type Who struct {
	Only   bool   // only friend is a hard pin
	Friend bool   // a friend's card: WHO: friend [<name>] or WHO: only friend <name>
	Name   string // the friend it names, "" for any friend
	Tail   bool   // WHO: friend <name> only, the hard pin in that word order
}

// friendNameRE is a friend's name as a friend row names her: letters, digits, _ and -.
var friendNameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)

// ReadWho reads a brief's WHO line from its header block (the `key: value` lines under
// line 1, up to the first blank line or line of prose, the key in any case), the one
// parser of it: the zero Who when the brief has none or says `WHO: -`. why is "" or the
// one line naming what is wrong and what to write.
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
		case len(f) == 1 && f[0] == "-":
			return Who{}, ""
		case len(f) == 3 && strings.EqualFold(f[0], "only") && strings.EqualFold(f[1], "friend") && friendNameRE.MatchString(f[2]):
			return Who{Friend: true, Only: true, Name: f[2]}, ""
		case len(f) == 3 && strings.EqualFold(f[0], "friend") && friendNameRE.MatchString(f[1]) && strings.EqualFold(f[2], "only"):
			return Who{Friend: true, Only: true, Name: f[1], Tail: true}, ""
		case len(f) == 1 && strings.EqualFold(f[0], "friend"):
			return Who{Friend: true}, ""
		case len(f) == 2 && strings.EqualFold(f[0], "friend") && friendNameRE.MatchString(f[1]):
			return Who{Friend: true, Name: f[1]}, ""
		}
		return Who{}, "WHO: " + v + " is not `friend` or `friend <name>` or `only friend <name>` or `friend <name> only` (a friend row's name: letters, digits, _ and -); a card with no WHO line, or WHO: -, is dealt to the fleet"
	}
	return Who{}, ""
}
