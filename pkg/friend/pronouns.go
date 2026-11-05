package friend

import (
	"fmt"
	"strings"
)

// Pronouns are the pronouns a friend goes by: the friend row's `pronouns`
// setting (pkg/config/kind.go, "friend"), one of the three sets below. Pronouns
// are read, never inferred: a friend whose pronouns are not stated goes by
// they/them, and no text calls a friend "it". The zero value is they/them, so
// a text built without them is already right.
type Pronouns string

// The pronoun sets a friend row may name, and the default a row without one
// has. pkg/config's FriendPronounSets lists the same three
// (TestPronounSetsMatchConfig holds the two lists equal).
const (
	SheHer   Pronouns = "she/her"
	HeHim    Pronouns = "he/him"
	TheyThem Pronouns = "they/them"

	DefaultPronouns = TheyThem
)

// AllPronouns is every set a row may name.
var AllPronouns = []Pronouns{SheHer, HeHim, TheyThem}

// ParsePronouns is the set a row's or a flag's text names: "" is the default,
// the three sets are themselves, and anything else is false with the default,
// never a guess.
func ParsePronouns(s string) (Pronouns, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return DefaultPronouns, true
	}
	for _, p := range AllPronouns {
		if string(p) == s {
			return p, true
		}
	}
	return DefaultPronouns, false
}

// forms are a set's subject, object, possessive adjective, possessive pronoun
// and reflexive.
func (p Pronouns) forms() (they, them, their, theirs, themself string) {
	switch p {
	case SheHer:
		return "she", "her", "her", "hers", "herself"
	case HeHim:
		return "he", "him", "his", "his", "himself"
	}
	return "they", "them", "their", "theirs", "themself"
}

// Say is the one helper every text about a friend renders its pronouns with:
// s with {they}, {them}, {their}, {theirs} and {themself} (and {They}, {Them},
// {Their}, {Theirs}, {Themself} at the head of a sentence) replaced by the
// forms of p. A text is written once in terms of those words, and a verb that
// would change with the subject ("she beats", "they beat") is not written
// after one: the text names the friend, or has the possessive ("{their} beat
// says down").
func (p Pronouns) Say(s string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	they, them, their, theirs, themself := p.forms()
	return strings.NewReplacer(
		"{they}", they, "{them}", them, "{their}", their, "{theirs}", theirs, "{themself}", themself,
		"{They}", capital(they), "{Them}", capital(them), "{Their}", capital(their), "{Theirs}", capital(theirs), "{Themself}", capital(themself),
	).Replace(s)
}

func capital(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// Sprintf is fmt.Sprintf of the format with its pronoun words said for p
// (Say), so a text that carries {their} and the like is written once.
func (p Pronouns) Sprintf(format string, a ...any) string {
	return fmt.Sprintf(p.Say(format), a...)
}

// PronounList is the three sets a row may name, for a flag's help and refusal.
func PronounList() string {
	var s []string
	for _, p := range AllPronouns {
		s = append(s, string(p))
	}
	return strings.Join(s, ", ")
}
