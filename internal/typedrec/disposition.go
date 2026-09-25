package typedrec

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// DispositionWord is the first token of a typed DISPOSITION v1 line.
const DispositionWord = "DISPOSITION"

// ReadClaim is one typed DISPOSITION v1 line (#2506 rev 4, part B):
//
//	DISPOSITION who=<name> head=<hex7-40> verdict=APPROVE|HOLD [score=<0-10>] [scope="<text>"]
//
// It is a claim, never an authority: parsing authenticates nobody, and the
// reviewer principal, head match and release rules stay in merge.
type ReadClaim struct {
	Who     string // lower-cased and trimmed, so who=Johnny and who=johnny are one friend
	Head    string // lower-cased as typed; empty when the line names no head
	Verdict string // upper-cased as typed
	Score   string
	Scope   string
	// Whole is the strict reading (#2550): the first token is exactly
	// DISPOSITION and everything after it is key=value fields with bare keys,
	// closed quotes and no repeated key. An APPROVE must be whole; a HOLD
	// stays lenient, so a sloppily typed HOLD still holds.
	Whole bool
	// Valid is false when the claim is refused; Field and Defect name why,
	// with the RESULT v2 defect words (missing, malformed).
	Valid  bool
	Field  string
	Defect string
}

// Refusal is the claim's refusal line, or "" for a valid claim.
func (c ReadClaim) Refusal() string {
	if c.Valid {
		return ""
	}
	return fmt.Sprintf("DISPOSITION REFUSED field=%s defect=%s", c.Field, c.Defect)
}

// ParseDisposition reads one line. ok is false when the line is not a typed
// DISPOSITION line at all: it does not start with the word, or it names no
// verdict. ok is true for every line that does, valid or refused, so a
// consumer can keep a lenient HOLD while refusing a malformed APPROVE.
//
// The rules, in the order the first refusal is named:
//   - who is required (field=who defect=missing);
//   - verdict is APPROVE or HOLD (field=verdict defect=malformed);
//   - an APPROVE names its head (field=head defect=missing): a missing head
//     is refused, never read as the current head;
//   - a head, when present, is 7-40 hex digits (field=head defect=malformed);
//   - a score, when present, is an integer 0-10, optionally written N/10
//     (field=score defect=malformed);
//   - an APPROVE is the whole line (field=line defect=malformed).
func ParseDisposition(line string) (ReadClaim, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, DispositionWord) {
		return ReadClaim{}, false
	}
	rest := t[len(DispositionWord):]
	fields := lenientDispositionKV(strings.TrimSpace(rest))
	c := ReadClaim{
		Who:     strings.ToLower(strings.TrimSpace(fields["who"])),
		Head:    strings.ToLower(fields["head"]),
		Verdict: strings.ToUpper(fields["verdict"]),
		Score:   fields["score"],
		Scope:   fields["scope"],
	}
	if c.Verdict == "" {
		return ReadClaim{}, false
	}
	if rest == "" || isDispositionSpace(rest[0]) {
		_, c.Whole = strictDispositionKV(rest)
	}
	c.Valid = true
	refuse := func(field, defect string) {
		if c.Valid {
			c.Valid, c.Field, c.Defect = false, field, defect
		}
	}
	if c.Who == "" {
		refuse("who", DefectMissing)
	}
	if c.Verdict != "APPROVE" && c.Verdict != "HOLD" {
		refuse("verdict", DefectMalformed)
	}
	if c.Verdict == "APPROVE" && strings.TrimSpace(c.Head) == "" {
		refuse("head", DefectMissing)
	}
	if c.Head != "" && !isHexHead(c.Head) {
		refuse("head", DefectMalformed)
	}
	if _, present := fields["score"]; present && !isDispositionScore(c.Score) {
		refuse("score", DefectMalformed)
	}
	if c.Verdict == "APPROVE" && !c.Whole {
		refuse("line", DefectMalformed)
	}
	return c, true
}

func isHexHead(h string) bool {
	if len(h) < 7 || len(h) > 40 {
		return false
	}
	for i := 0; i < len(h); i++ {
		b := h[i]
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

func isDispositionScore(s string) bool {
	s = strings.TrimSuffix(s, "/10")
	n, err := strconv.Atoi(s)
	return err == nil && n >= 0 && n <= 10 && s == strconv.Itoa(n)
}

func isDispositionSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\v' || b == '\f'
}

// lenientDispositionKV reads key=value fields and stops quietly at the first
// token that is not one; an unclosed quote runs to the end of the line.
func lenientDispositionKV(s string) map[string]string {
	res := make(map[string]string)
	for len(s) > 0 {
		s = strings.TrimSpace(s)
		if len(s) == 0 {
			break
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			break
		}
		key := strings.TrimSpace(s[:eq])
		s = s[eq+1:]
		var val string
		if len(s) > 0 && s[0] == '"' {
			s = s[1:]
			if closeQuote := strings.IndexByte(s, '"'); closeQuote >= 0 {
				val, s = s[:closeQuote], s[closeQuote+1:]
			} else {
				val, s = s, ""
			}
		} else if sp := strings.IndexFunc(s, unicode.IsSpace); sp >= 0 {
			val, s = s[:sp], s[sp+1:]
		} else {
			val, s = s, ""
		}
		res[key] = val
	}
	return res
}

// strictDispositionKV is lenientDispositionKV with the tolerance taken out:
// every token belongs to a key=value field, keys are bare words, a quoted
// value closes and is followed by space or the end, and no key repeats.
func strictDispositionKV(s string) (map[string]string, bool) {
	res := make(map[string]string)
	for {
		s = strings.TrimLeft(s, " \t\r\n\v\f")
		if s == "" {
			return res, true
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			return nil, false
		}
		key := s[:eq]
		if !isBareDispositionKey(key) {
			return nil, false
		}
		s = s[eq+1:]
		var val string
		if len(s) > 0 && s[0] == '"' {
			s = s[1:]
			closeQuote := strings.IndexByte(s, '"')
			if closeQuote < 0 {
				return nil, false
			}
			val, s = s[:closeQuote], s[closeQuote+1:]
			if s != "" && !isDispositionSpace(s[0]) {
				return nil, false
			}
		} else if sp := strings.IndexFunc(s, unicode.IsSpace); sp >= 0 {
			val, s = s[:sp], s[sp:]
		} else {
			val, s = s, ""
		}
		if _, dup := res[key]; dup {
			return nil, false
		}
		res[key] = val
	}
}

func isBareDispositionKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
