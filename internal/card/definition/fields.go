package definition

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/card"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// The header keys of the v2 profile.
const (
	KeySchema    = "SCHEMA"
	KeyID        = "ID"
	KeyEntry     = "ENTRY"
	KeyTitle     = "TITLE"
	KeyKind      = "KIND"
	KeyPaths     = "PATHS"
	KeyDependsOn = "DEPENDS-ON"
	KeyTier      = "TIER"
	KeyTest      = "TEST"
	KeyDoneWhen  = "DONE-WHEN"
	KeyDoors     = "DOORS"
	KeyProbes    = "PROBES"
)

// SchemaV2 is the one schema version this package reads.
const SchemaV2 = "v2"

// requiredKeys is every key a card must carry, in the order render writes them.
// ENTRY, the one optional key, is written after ID.
var requiredKeys = []string{KeySchema, KeyID, KeyTitle, KeyKind, KeyPaths, KeyDependsOn, KeyTier, KeyTest, KeyDoneWhen, KeyDoors, KeyProbes}

var knownKey = func() map[string]bool {
	m := map[string]bool{KeyEntry: true}
	for _, k := range requiredKeys {
		m[k] = true
	}
	return m
}()

// headerLine reads a header line: an upper-case key of letters, digits and hyphen
// starting with a letter, a colon, then a blank and a value or the end of the line;
// it returns the key and the value with its padding removed. Nothing else is a
// header line: a line that begins `Goal: `, `Context: ` or a URL is not one, and
// ends the header. It allocates nothing, because a hostile file can be all header
// lines.
func headerLine(line string) (key, value string, ok bool) {
	i := 0
	for i < len(line) {
		c := line[i]
		if c >= 'A' && c <= 'Z' || i > 0 && (c >= '0' && c <= '9' || c == '-') {
			i++
			continue
		}
		break
	}
	if i == 0 || i >= len(line) || line[i] != ':' {
		return "", "", false
	}
	rest := line[i+1:]
	if rest != "" && rest[0] != ' ' {
		return "", "", false
	}
	return line[:i], strings.TrimSpace(rest), true
}

// looseKey folds a key to the form that makes case and spacing variants equal:
// upper case, with hyphens, underscores and whitespace removed.
func looseKey(k string) string {
	var b strings.Builder
	for _, r := range k {
		switch {
		case r == '-' || r == '_' || unicode.IsSpace(r):
		default:
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

var looseKnown = func() map[string]string {
	m := map[string]string{}
	for k := range knownKey {
		m[looseKey(k)] = k
	}
	return m
}()

// looseHeaderLine reports whether a line that is not a header line is a variant of
// a known key (`kind: x`, `KIND : x`, ` KIND: x`, `DEPENDS_ON: x`), which key it
// varies, and the key as written. Any other line ends the header.
func looseHeaderLine(t string) (variant, canon string, ok bool) {
	i := strings.IndexByte(t, ':')
	if i <= 0 {
		return "", "", false
	}
	k := t[:i]
	for j := 0; j < len(k); j++ {
		c := k[j]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != ' ' && c != '\t' && c != '_' && c != '-' {
			return "", "", false
		}
	}
	canon, ok = looseKnown[looseKey(k)]
	return strings.TrimSpace(k), canon, ok
}

// problem is why a value is refused: the cause, what was found (bounded), the rule
// it broke and the next action. A nil *problem is a value that is fine.
type problem struct {
	cause              Cause
	found, limit, next string
}

func bad(c Cause, found, limit, next string) *problem { return &problem{c, found, limit, next} }

// oneLineText is a problem with a one-line prose value of at most max bytes.
func oneLineText(key, s string, max int) *problem {
	switch {
	case strings.TrimSpace(s) == "":
		return bad(CauseEmptyValue, "", key+" has a value", "write a value after "+key+":")
	case card.TextFault(s, max) == card.CauseTooLong:
		return bad(CauseInvalidValue, card.Value(s), fmt.Sprintf("%s is at most %d bytes", key, max), "shorten the value")
	case card.TextFault(s, max) != "":
		return bad(CauseInvalidValue, card.Value(s), key+" is printable text on one line: no control, bidirectional, zero-width or line-separator character", "keep the value to printable text on one line")
	case strings.TrimSpace(s) != s:
		return bad(CauseInvalidValue, card.Value(s), key+" is not padded with whitespace", "remove the padding")
	}
	return nil
}

// idProblem is why s is not a card ID, nil when it is one.
func idProblem(key, s string) *problem {
	c, why := card.IDFault(s)
	switch c {
	case "":
		return nil
	case CauseReservedWord:
		return bad(CauseInvalidID, card.Value(s), why, `use an ID of ASCII letters, digits, underscore and hyphen; "-" is the DEPENDS-ON none word and "none" the PATHS, DOORS and PROBES one`)
	}
	return bad(CauseInvalidID, card.Value(s), why, "use an ID of ASCII letters, digits, underscore and hyphen, at most 64 bytes")
}

// entryProblem is why s is not an ENTRY value, nil when it is one.
func entryProblem(s string) *problem {
	switch {
	case s == "":
		return bad(CauseInvalidEntry, "", "ENTRY has a value", "leave the line out when there is no entry")
	case len(s) > MaxEntryBytes:
		return bad(CauseInvalidEntry, card.Value(s), fmt.Sprintf("ENTRY is at most %d bytes", MaxEntryBytes), "shorten the entry")
	case strings.Contains(s, ","):
		return bad(CauseInvalidEntry, card.Value(s), "ENTRY holds no comma: member identifiers are joined with commas", "write one entry path with no comma")
	case card.TextFault(s, MaxEntryBytes) != "":
		return bad(CauseInvalidValue, card.Value(s), "ENTRY holds no control character", "write one entry path with no control character")
	case strings.TrimSpace(s) != s:
		return bad(CauseInvalidEntry, card.Value(s), "ENTRY is not padded with whitespace", "remove the padding")
	}
	return nil
}

// splitList splits a comma-separated value into trimmed entries, and reports
// whether any entry is empty.
func splitList(v string) (items []string, empty bool) {
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			empty = true
			continue
		}
		items = append(items, p)
	}
	return items, empty
}

// parseDepends reads a DEPENDS-ON value: `-` for none, else card IDs. The word
// `none` is not a dependency: it is refused, naming `-`.
func parseDepends(v string) ([]string, *problem) {
	if v == card.WordDash {
		return nil, nil
	}
	if v == card.WordNone {
		return nil, bad(CauseInvalidDependsOn, card.Value(v), "DEPENDS-ON takes card IDs, or - for none", "write `DEPENDS-ON: -` for a card with no prerequisite; `none` would name a card called none")
	}
	items, empty := splitList(v)
	if empty {
		return nil, bad(CauseInvalidDependsOn, card.Value(v), "no empty entry between the commas", "write `DEPENDS-ON: -` or comma-separated card IDs")
	}
	if p := dependsProblem(items); p != nil {
		return nil, p
	}
	return items, nil
}

// dependsProblem is why the IDs of a DEPENDS-ON are refused, nil when they are
// not: at most MaxDependsOn, each a card ID, none repeated.
func dependsProblem(items []string) *problem {
	if len(items) > MaxDependsOn {
		return bad(CauseInvalidDependsOn, fmt.Sprintf("%d IDs", len(items)), fmt.Sprintf("DEPENDS-ON names at most %d IDs", MaxDependsOn), "name fewer prerequisites")
	}
	seen := map[string]bool{}
	for _, id := range items {
		if p := idProblem(KeyDependsOn, id); p != nil {
			return bad(CauseInvalidDependsOn, p.found, "DEPENDS-ON: "+p.limit, `write `+"`DEPENDS-ON: -`"+` or comma-separated card IDs`)
		}
		if seen[id] {
			return bad(CauseInvalidDependsOn, card.Value(id), "DEPENDS-ON names each ID once", "name each prerequisite once")
		}
		seen[id] = true
	}
	return nil
}

// pathsProblem is why the PATHS globs are refused, nil when they are not: at most
// MaxPathGlobs, each of at most MaxGlobBytes with no control character, the
// grammar of hygiene.ValidatePaths, none repeated.
func pathsProblem(globs []string) *problem {
	if len(globs) > card.MaxPathGlobs {
		return bad(CauseInvalidPaths, fmt.Sprintf("%d globs", len(globs)), fmt.Sprintf("PATHS names at most %d globs", card.MaxPathGlobs), "name fewer globs")
	}
	seen := map[string]bool{}
	for _, g := range globs {
		if len(g) > MaxGlobBytes {
			return bad(CauseInvalidPaths, card.Value(g), fmt.Sprintf("a glob is at most %d bytes", MaxGlobBytes), "shorten the glob")
		}
		if card.TextFault(g, MaxGlobBytes) != "" {
			return bad(CauseInvalidPaths, card.Value(g), "a glob holds no control character", "write printable globs")
		}
	}
	if err := hygiene.ValidatePaths(globs); err != nil {
		return bad(CauseInvalidPaths, short(err.Error()), "the PATHS grammar of internal/hygiene", "write `PATHS: none` or up to eight repository-relative globs")
	}
	for _, g := range globs {
		if seen[g] {
			return bad(CauseInvalidPaths, card.Value(g), "PATHS names each glob once", "name each glob once")
		}
		seen[g] = true
	}
	return nil
}

// parsePaths reads a PATHS value: `none` or comma-separated globs. The word `-` is
// the DEPENDS-ON none, not a glob: it is refused, naming `none`.
func parsePaths(v string) ([]string, *problem) {
	if v == card.WordNone {
		return nil, nil
	}
	if v == card.WordDash {
		return nil, bad(CauseInvalidPaths, card.Value(v), "PATHS takes globs, or none", "write `PATHS: none` for a card that changes nothing; `-` would be a glob named -")
	}
	items, empty := splitList(v)
	switch {
	case empty:
		return nil, bad(CauseInvalidPaths, card.Value(v), "no empty entry between the commas", "write `PATHS: none` or up to eight repository-relative globs")
	case len(items) == 0:
		return nil, bad(CauseInvalidPaths, card.Value(v), "PATHS names a glob or says none", "write `PATHS: none` for a card that changes nothing")
	}
	if p := pathsProblem(items); p != nil {
		return nil, p
	}
	return items, nil
}

// noneWord refuses `-` for a key whose empty word is `none`.
func noneWord(key, v string) *problem {
	if v == card.WordDash {
		return bad(CauseInvalidValue, card.Value(v), key+" is text, or none", "write `"+key+": none` for nothing declared; - is the DEPENDS-ON none")
	}
	return nil
}

// testProblem is why t is not a valid TEST for a card of the given completion
// class, nil when it is. The grammar is cardhdr.ParseTest; the package runs the
// same path rule as PATHS (as the swarm header lint does), the test name is a Test
// function, and `none <why>` is permitted only for a kind that completes without a
// pull request.
func testProblem(raw string, t cardhdr.TestLine, class Completion) *problem {
	fix := "write `TEST: <package> <TestName>`, or `TEST: none <why>` for a kind that completes without a pull request"
	if len(raw) > MaxTestBytes {
		return bad(CauseInvalidTest, card.Value(raw), fmt.Sprintf("TEST is at most %d bytes", MaxTestBytes), fix)
	}
	if t.None {
		if strings.TrimSpace(t.Why) == "" || card.TextFault(t.Why, MaxTestBytes) != "" {
			return bad(CauseInvalidTest, card.Value(raw), "TEST: none needs a one-line why", fix)
		}
		if class != CompletionNoPR {
			return bad(CauseInvalidTest, card.Value(raw), "TEST: none is permitted only for a kind that completes without a pull request", fix)
		}
		return nil
	}
	back, why := cardhdr.ParseTest(t.String())
	if why != "" {
		return bad(CauseInvalidTest, card.Value(raw), short(why), fix)
	}
	if back != t {
		return bad(CauseInvalidTest, card.Value(raw), "TEST reads back as itself", fix)
	}
	pkg := strings.Trim(t.Package, "/")
	if pkg == "" {
		pkg = "."
	}
	if err := hygiene.ValidatePaths([]string{pkg}); err != nil {
		return bad(CauseInvalidTest, card.Value(raw), "TEST package: "+short(err.Error()), fix)
	}
	if !strings.HasPrefix(t.Name, "Test") {
		return bad(CauseInvalidTest, card.Value(raw), "TEST names a Test function (Example and Fuzz names are refused)", fix)
	}
	return nil
}

// kindProblem is why kind is not usable, nil when it is: a kind
// internal/hygiene/kinds.txt declares and the completion policy classifies.
func kindProblem(kind string) *problem {
	p, err := loadPolicy()
	if err != nil {
		return bad(CauseUnclassifiedKind, card.Value(kind), "the completion policy is readable: "+short(err.Error()), "report this as a defect")
	}
	return kindProblemIn(p, kind)
}

// tierProblem is why tier is not a route, nil when it is.
func tierProblem(tier string) *problem {
	if !cardhdr.IsRoute(tier) {
		return bad(CauseInvalidTier, card.Value(tier), "TIER is one of "+cardhdr.RouteList, "write one route: frontier, pro or flash")
	}
	return nil
}
