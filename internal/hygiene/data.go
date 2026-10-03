package hygiene

import (
	_ "embed"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed stray.txt
var strayData string

//go:embed keyshapes.txt
var keyShapeData string

// strayRule is one row of stray.txt: a pattern, and the card kinds it does not apply
// to. Except is a set and never a path: an exception is granted to a SHAPE of work the
// tool declares, not to a file a worker names.
type strayRule struct {
	pattern string
	except  map[string]bool
}

// keyShape is one row of keyshapes.txt: a name a finding may print, and a regexp that
// recognises a form. The value a key would have is nowhere in this program.
type keyShape struct {
	name string
	re   *regexp.Regexp
}

var (
	loadOnce  sync.Once
	strayList []strayRule
	keyShapes []keyShape
	loadErr   error
)

func load() ([]strayRule, []keyShape, error) {
	loadOnce.Do(func() {
		strayList, loadErr = parseStray(strayData)
		// ignored: loadErr is the package variable load returns below; the Once only stops the second parse
		if loadErr != nil {
			return
		}
		keyShapes, loadErr = parseKeyShapes(keyShapeData)
	})
	return strayList, keyShapes, loadErr
}

func parseStray(data string) ([]strayRule, error) {
	var out []strayRule
	for n, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		rule := strayRule{pattern: strings.TrimSpace(fields[0]), except: map[string]bool{}}
		if rule.pattern == "" {
			return nil, fmt.Errorf("stray.txt:%d: a row with no pattern", n+1)
		}
		if len(fields) == 2 {
			for _, k := range strings.Split(fields[1], ",") {
				if k = strings.TrimSpace(k); k != "" {
					rule.except[k] = true
				}
			}
		}
		out = append(out, rule)
	}
	return out, nil
}

func parseKeyShapes(data string) ([]keyShape, error) {
	var out []keyShape
	for n, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			return nil, fmt.Errorf("keyshapes.txt:%d: want <name>\\t<regexp>", n+1)
		}
		name := strings.TrimSpace(fields[0])
		re, err := regexp.Compile(fields[1])
		if err != nil {
			return nil, fmt.Errorf("keyshapes.txt:%d: %s: %v", n+1, name, err)
		}
		out = append(out, keyShape{name: name, re: re})
	}
	return out, nil
}

//go:embed kinds.txt
var kindData string

var (
	kindOnce    sync.Once
	kindNames   []string
	kindSet     map[string]bool
	kindUngated map[string]bool
)

// parseKindRows reads kinds.txt (docs/SPEC-TOOLWORK.md hygiene rule 6). The name
// is the first field. The third field is the classification: only the exact
// value ungated is ungated. A missing field or any other value is gated, which
// is recorded by leaving the name out of the ungated set. The first row of a
// repeated name wins. Blank lines and comments are not rows. A name the data
// does not hold is not returned and is not ungated.
func parseKindRows(data string) (names []string, ungated map[string]bool) {
	ungated = map[string]bool{}
	seen := map[string]bool{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		name := strings.TrimSpace(fields[0])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(fields) >= 3 && strings.TrimSpace(fields[2]) == "ungated" {
			ungated[name] = true
		}
	}
	return names, ungated
}

func loadKinds() ([]string, map[string]bool) {
	kindOnce.Do(func() {
		var ungated map[string]bool
		kindNames, ungated = parseKindRows(kindData)
		kindSet = make(map[string]bool, len(kindNames))
		kindUngated = ungated
		for _, name := range kindNames {
			kindSet[name] = true
		}
	})
	return kindNames, kindSet
}

// Kinds is the card kinds this toolchain declares, in the spec's order. A caller that
// refuses an unknown kind prints this, because a refusal a reader cannot act on is a
// refusal that sends them to the source.
func Kinds() []string {
	names, _ := loadKinds()
	return append([]string(nil), names...)
}

// KindDeclared says whether the KIND: line names a shape of work the tool declares.
// SPEC-TOOLWORK.md hygiene rule 6: there is no default kind, and a kind the table does not hold
// is refused. It was accepted silently and unlocked nothing, so a card carrying a kind
// nobody had ever implemented came back clean (#1848).
func KindDeclared(name string) bool {
	_, set := loadKinds()
	return set[name]
}

// KindUngated reports whether name is a declared kind whose third field in
// kinds.txt is exactly ungated. TEST: none is a declaration only then
// (docs/SPEC-TOOLWORK.md hygiene rule 6). A missing third field, any other
// value, or a name the file does not hold is not ungated. The field selects
// no command.
func KindUngated(name string) bool {
	loadKinds()
	return kindUngated[name]
}

// StrayKinds is every kind named in the stray list's exception column, so a test can
// hold the two files to each other: an exception granted to a kind that does not exist
// is an exception granted to nobody, and nothing used to notice.
func StrayKinds() []string {
	rules, _, err := load()
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range rules {
		for k := range r.except {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}
