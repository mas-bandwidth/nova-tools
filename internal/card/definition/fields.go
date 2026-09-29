package definition

import (
	"fmt"
	"strings"
	"unicode"

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

// requiredKeys is every key a card must carry, in the order Render writes them.
// ENTRY, the one optional key, is written after ID.
var requiredKeys = []string{KeySchema, KeyID, KeyTitle, KeyKind, KeyPaths, KeyDependsOn, KeyTier, KeyTest, KeyDoneWhen, KeyDoors, KeyProbes}

var knownKey = func() map[string]bool {
	m := map[string]bool{KeyEntry: true}
	for _, k := range requiredKeys {
		m[k] = true
	}
	return m
}()

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

// hasControl reports whether s holds a control character or a Unicode line or
// paragraph separator: nothing a one-line value may carry.
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 {
			return true
		}
	}
	return false
}

// idWhy is why s is not a card ID, "" when it is one: nonempty ASCII letters,
// digits, underscore and hyphen, at most MaxIDBytes, and not the lone "-" that
// DEPENDS-ON reserves for none.
func idWhy(s string) string {
	switch {
	case s == "":
		return "an ID is empty"
	case len(s) > MaxIDBytes:
		return fmt.Sprintf("an ID is %d bytes, at most %d", len(s), MaxIDBytes)
	case s == "-":
		return `"-" is the DEPENDS-ON none token, never an ID`
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !ok {
			return fmt.Sprintf("an ID is ASCII letters, digits, underscore and hyphen; %q is not", s)
		}
	}
	return ""
}

// entryWhy is why s is not an ENTRY value, "" when it is one.
func entryWhy(s string) string {
	switch {
	case s == "":
		return "ENTRY is empty; leave the line out when there is no entry"
	case len(s) > MaxEntryBytes:
		return fmt.Sprintf("ENTRY is %d bytes, at most %d", len(s), MaxEntryBytes)
	case strings.Contains(s, ","):
		return "ENTRY holds a comma; member identifiers are joined with commas"
	case hasControl(s):
		return "ENTRY holds a control character"
	case strings.TrimSpace(s) != s:
		return "ENTRY is padded with whitespace"
	}
	return ""
}

// textWhy is why s is not a one-line prose value of at most max bytes.
func textWhy(key, s string, max int) string {
	switch {
	case strings.TrimSpace(s) == "":
		return key + " is empty"
	case len(s) > max:
		return fmt.Sprintf("%s is %d bytes, at most %d", key, len(s), max)
	case hasControl(s):
		return key + " holds a control character"
	case strings.TrimSpace(s) != s:
		return key + " is padded with whitespace"
	}
	return ""
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

// parseDepends reads a DEPENDS-ON value: "-" for none, else card IDs.
func parseDepends(v string) (ids []string, why string) {
	if v == "-" {
		return nil, ""
	}
	items, empty := splitList(v)
	if empty {
		return nil, fmt.Sprintf("DEPENDS-ON %q has an empty entry between its commas", v)
	}
	if w := dependsWhy(items); w != "" {
		return nil, w
	}
	return items, ""
}

// dependsWhy is why the IDs of a DEPENDS-ON are refused, "" when they are not: at
// most MaxDependsOn, each a card ID, none repeated.
func dependsWhy(items []string) string {
	if len(items) > MaxDependsOn {
		return fmt.Sprintf("DEPENDS-ON names %d IDs, at most %d", len(items), MaxDependsOn)
	}
	seen := map[string]bool{}
	for _, id := range items {
		if w := idWhy(id); w != "" {
			return "DEPENDS-ON: " + w
		}
		if seen[id] {
			return fmt.Sprintf("DEPENDS-ON names %s twice", id)
		}
		seen[id] = true
	}
	return ""
}

// pathsWhy is why the PATHS globs are refused, "" when they are not. The
// grammar is hygiene.ValidatePaths; a repeated glob is refused on top of it.
func pathsWhy(globs []string) string {
	if err := hygiene.ValidatePaths(globs); err != nil {
		return err.Error()
	}
	seen := map[string]bool{}
	for _, g := range globs {
		if hasControl(g) {
			return fmt.Sprintf("PATHS: %q holds a control character", g)
		}
		if seen[g] {
			return fmt.Sprintf("PATHS names %s twice", g)
		}
		seen[g] = true
	}
	return ""
}

// parsePaths reads a PATHS value: "none" or comma-separated globs.
func parsePaths(v string) (globs []string, why string) {
	if v == "none" {
		return nil, ""
	}
	items, empty := splitList(v)
	switch {
	case empty:
		return nil, fmt.Sprintf("PATHS %q has an empty entry between its commas", v)
	case len(items) == 0:
		return nil, fmt.Sprintf("PATHS %q names no glob; a card that changes nothing says `PATHS: none`", v)
	}
	if w := pathsWhy(items); w != "" {
		return nil, w
	}
	return items, ""
}

// testWhy is why t is not a valid TEST for a card of the given completion class,
// "" when it is. The grammar is cardhdr.ParseTest; the package runs the same path
// rule as PATHS (as the swarm header lint does), the test name is a Test function,
// and `none <why>` is permitted only for a kind that completes without a pull
// request.
func testWhy(t cardhdr.TestLine, class Completion) string {
	if t.None {
		if strings.TrimSpace(t.Why) == "" || hasControl(t.Why) {
			return "TEST: none needs a one-line why"
		}
		if class != CompletionNoPR {
			return "TEST: none is permitted only for a kind that completes without a pull request; name `TEST: <package> <TestName>`"
		}
		return ""
	}
	back, why := cardhdr.ParseTest(t.String())
	if why != "" {
		return why
	}
	if back != t {
		return fmt.Sprintf("TEST %q does not read back as itself", t.String())
	}
	pkg := strings.Trim(t.Package, "/")
	if pkg == "" {
		pkg = "."
	}
	if err := hygiene.ValidatePaths([]string{pkg}); err != nil {
		return "TEST package: " + err.Error()
	}
	if !strings.HasPrefix(t.Name, "Test") {
		return fmt.Sprintf("TEST %q is not a Test function name (Example and Fuzz names are refused)", t.Name)
	}
	return ""
}

// kindWhy is why kind is not usable, with the cause, "" when it is: a kind
// internal/hygiene/kinds.txt declares and the completion policy classifies.
func kindWhy(kind string) (Cause, string) {
	p, err := loadPolicy()
	if err != nil {
		return CauseUnclassifiedKind, "the completion policy cannot be read: " + err.Error()
	}
	return kindWhyIn(p, kind)
}

// tierWhy is why tier is not a route.
func tierWhy(tier string) string {
	if !cardhdr.IsRoute(tier) {
		return fmt.Sprintf("TIER %q is not %s", tier, cardhdr.RouteList)
	}
	return ""
}
