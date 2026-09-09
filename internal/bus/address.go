package bus

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Address resolution: turning the text of a To, Cc or From line into names the roster
// knows.
//
// This is deliberately a SMALL, ENUMERATED set of tolerances rather than a fuzzy match.
// Tables in this shape write "To: Bo Quill; Ada (active line)" and "To: Everybody at
// the table — Dana, Ada (all instances), Bo", and a tool that refused those would
// be refusing the table rather than checking it. It is also true that a matcher nobody can
// predict is worse than one that refuses: a misspelling silently resolving to the wrong
// reader is the failure this verb exists to stop. So every tolerance below is listed here,
// listed in SPEC.md, and pinned by a test, and anything outside the list is refused with
// the token quoted.
//
// The tolerances, applied in this order:
//
//  1. The line is split on ";" and "," -- but NEVER inside parentheses, because
//     "Ada (day shift, the west host, the shared account)" is one name and the comma inside the
//     parenthetical is not a separator.
//  2. Each piece is split again on an em dash or an en dash, so that a salutation like
//     "Everybody at the table — Dana" names the group AND the person.
//  3. It is split again on " and " and " & ", because "To: Ada and Bo" is two
//     readers and resolving it to Ada alone -- which the prefix rule below did, since
//     "Ada and Bo" begins with "Ada " -- is the silent wrong-reader failure this
//     whole file exists to prevent. A leading "and " left over from ", and X" is dropped.
//  4. A leading "for " is dropped ("for Bo when they arrive").
//  5. A trailing parenthetical is dropped, repeatedly ("Ada (day shift, the west host)").
//  6. What remains must EQUAL a known name, alias or group, case-insensitively, or BEGIN
//     with one followed by a space -- the instance qualifier, so that "Ada a1b2c3d4"
//     and "Ada Vale" are both Ada. The longest known name that matches wins, so a
//     roster holding both "Bo" and "Bo Quill" resolves "Bo Quill Two" to
//     Bo Quill rather than to Bo. The prefix rule REFUSES rather than resolves
//     when what follows the known name is itself a known name: whatever "Ada Bo" is,
//     it is not a note to Ada, and a tool that decided which of the two was meant would
//     be guessing about a reader.
//  7. A group expands to its members.
//
// An empty piece is skipped. Everything else is unresolved, and an unresolved name is a
// refusal at send and a FAIL at check.

// ResolveList resolves one address line into canonical participant names, in the order
// they were first named, plus the tokens it could not resolve. Duplicates collapse: a
// person named directly and again through a group is one recipient.
func (c *Config) ResolveList(line string) (names []string, unresolved []string) {
	seen := make(map[string]bool)
	for _, tok := range splitAddresses(line) {
		i, ok := c.byName[fold(tok)]
		if !ok {
			if gi, isGroup := c.byGroup[fold(tok)]; isGroup {
				for _, m := range c.Groups[gi].Members {
					mi := c.byName[fold(strings.TrimSpace(m))]
					if n := c.Participants[mi].Name; !seen[n] {
						seen[n] = true
						names = append(names, n)
					}
				}
				continue
			}
			// The prefix rule, over names, aliases and groups together, longest first.
			if key, found := c.longestKnownPrefix(tok); found {
				if gi, isGroup := c.byGroup[key]; isGroup {
					for _, m := range c.Groups[gi].Members {
						mi := c.byName[fold(strings.TrimSpace(m))]
						if n := c.Participants[mi].Name; !seen[n] {
							seen[n] = true
							names = append(names, n)
						}
					}
					continue
				}
				i = c.byName[key]
			} else {
				unresolved = append(unresolved, tok)
				continue
			}
		}
		if n := c.Participants[i].Name; !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return names, unresolved
}

// ResolveOne resolves a From line to the single participant that wrote it. A From line
// naming two people is not a sender, and is refused rather than resolved to the first.
func (c *Config) ResolveOne(line string) (Participant, bool) {
	toks := splitAddresses(line)
	if len(toks) != 1 {
		return Participant{}, false
	}
	tok := toks[0]
	if i, ok := c.byName[fold(tok)]; ok {
		return c.Participants[i], true
	}
	if key, found := c.longestKnownPrefix(tok); found {
		if _, isGroup := c.byGroup[key]; isGroup {
			return Participant{}, false
		}
		return c.Participants[c.byName[key]], true
	}
	return Participant{}, false
}

// longestKnownPrefix implements tolerance 6. It scans every known key rather than
// indexing, because the roster is a handful of names and a wrong answer here is worse
// than a slow one.
//
// It reports NOT FOUND when the remainder after the longest match is itself a name this
// roster knows. That is the difference between an instance qualifier and a second reader:
// "Ada reads in place" is Ada with a qualifier nobody else answers to, and
// "Ada Bo" is two names in one token, which is a refusal rather than a note
// delivered to the first of them.
func (c *Config) longestKnownPrefix(tok string) (key string, found bool) {
	low := fold(tok)
	best := ""
	consider := func(k string) {
		if len(k) <= len(best) {
			return
		}
		if strings.HasPrefix(low, k+" ") {
			best = k
		}
	}
	for k := range c.byName {
		consider(k)
	}
	for k := range c.byGroup {
		consider(k)
	}
	if best == "" {
		return "", false
	}
	if c.knows(strings.TrimSpace(low[len(best):])) {
		return "", false
	}
	return best, true
}

// knows reports whether a token names somebody this roster holds, either exactly or under
// the prefix rule. It is the remainder test above, and it does not recurse: the prefix
// scan below it never calls back into this.
func (c *Config) knows(tok string) bool {
	low := fold(strings.TrimSpace(tok))
	if low == "" {
		return false
	}
	if _, ok := c.byName[low]; ok {
		return true
	}
	if _, ok := c.byGroup[low]; ok {
		return true
	}
	for k := range c.byName {
		if strings.HasPrefix(low, k+" ") {
			return true
		}
	}
	for k := range c.byGroup {
		if strings.HasPrefix(low, k+" ") {
			return true
		}
	}
	return false
}

// wordSeparators are tolerance 3: the separators a person writes as words rather than as
// punctuation. They carry their surrounding spaces, so "Alexander and Sons" -- a single
// name with "and" inside it -- would still be one token if a roster held it, and only a
// space-delimited "and" splits.
var wordSeparators = []string{" and ", " & "}

// splitAddresses applies tolerances 1 to 5 and returns the cleaned pieces.
//
// The split is parenthesis-aware. An earlier version used strings.FieldsFunc and broke
// "Ada (day shift, the west host, the shared account)" -- the From line of most of the notes on the
// table this was written for -- into three unresolvable pieces.
func splitAddresses(line string) []string {
	var fields []string
	depth := 0
	start := 0
	for i := 0; i < len(line); {
		r, size := utf8.DecodeRuneInString(line[i:])
		if depth == 0 {
			if sep, cut := wordSeparatorAt(line, i); cut {
				fields = append(fields, line[start:i])
				i += len(sep)
				start = i
				continue
			}
		}
		switch {
		case r == '(':
			depth++
		case r == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0 && (r == ';' || r == ',' || r == '—' || r == '–'):
			fields = append(fields, line[start:i])
			start = i + size
		}
		i += size
	}
	fields = append(fields, line[start:])
	var out []string
	for _, f := range fields {
		tok := strings.TrimSpace(f)
		// ", and Bo" leaves "and Bo" behind once the comma has split it.
		if rest, cut := cutPrefixFold(tok, "and "); cut {
			tok = strings.TrimSpace(rest)
		}
		if rest, cut := cutPrefixFold(tok, "for "); cut {
			tok = strings.TrimSpace(rest)
		}
		tok = stripParentheticals(tok)
		if tok == "" {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// wordSeparatorAt reports the word separator beginning at byte i, case-insensitively. It
// compares in place rather than over a lower-cased copy of the line, because lower-casing
// can change a string's LENGTH -- U+0130 is one rune and lower-cases to two -- and an
// index taken from a folded copy would then point into the middle of a rune of the
// original.
func wordSeparatorAt(line string, i int) (string, bool) {
	for _, sep := range wordSeparators {
		if len(line)-i >= len(sep) && strings.EqualFold(line[i:i+len(sep)], sep) {
			return sep, true
		}
	}
	return "", false
}

// stripParentheticals removes trailing "(...)" groups, repeatedly, so that
// "Ada a1b2c3d4 (active line) (the west host)" is Ada. Only TRAILING ones: a parenthesis
// in the middle of a name is part of the name as far as this is concerned, and will
// simply not resolve.
func stripParentheticals(tok string) string {
	for {
		tok = strings.TrimSpace(tok)
		if !strings.HasSuffix(tok, ")") {
			return tok
		}
		open := strings.LastIndex(tok, "(")
		if open < 0 {
			return tok
		}
		tok = tok[:open]
	}
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && fold(s[:len(prefix)]) == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

// UnknownNames is ResolveList's second return, sorted and deduplicated, for a refusal
// message that names each bad token once.
func UnknownNames(unresolved []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, u := range unresolved {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	sort.Strings(out)
	return out
}
