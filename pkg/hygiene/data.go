package hygiene

import (
	_ "embed"
	"fmt"
	"regexp"
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

var strayLoaded = sync.OnceValues(func() (strayLoad, error) {
	rules, err := parseStray(strayData)
	if err != nil {
		return strayLoad{}, err
	}
	keys, err := parseKeyShapes(keyShapeData)
	if err != nil {
		return strayLoad{rules: rules}, err
	}
	return strayLoad{rules: rules, keys: keys}, nil
})

// strayLoad is the parsed stray and key-shape tables, kept once.
type strayLoad struct {
	rules []strayRule
	keys  []keyShape
}

func load() ([]strayRule, []keyShape, error) {
	got, err := strayLoaded()
	return got.rules, got.keys, err
}

func parseStray(data string) ([]strayRule, error) {
	var out []strayRule
	for n, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		pattern, rest, found := strings.Cut(line, "\t")
		rule := strayRule{pattern: strings.TrimSpace(pattern), except: map[string]bool{}}
		if rule.pattern == "" {
			return nil, fmt.Errorf("stray.txt:%d: a row with no pattern", n+1)
		}
		if found {
			for _, k := range strings.Split(rest, ",") {
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
		name, expr, found := strings.Cut(line, "\t")
		if !found {
			return nil, fmt.Errorf("keyshapes.txt:%d: want <name>\\t<regexp>", n+1)
		}
		name = strings.TrimSpace(name)
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("keyshapes.txt:%d: %s: %v", n+1, name, err)
		}
		out = append(out, keyShape{name: name, re: re})
	}
	return out, nil
}

//go:embed kinds.txt
var kindData string

// kindRow is one row of the one kinds list. The instruction kind is a column of
// that list (docs/SPEC-ISA.md), not a second vocabulary.
type kindRow struct {
	gated       bool
	instruction string
}

var kindTableOnce = sync.OnceValue(func() kindTable {
	set := map[string]bool{}
	rows := map[string]kindRow{}
	var names []string
	for _, line := range strings.Split(kindData, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		name := strings.TrimSpace(fields[0])
		if name == "" || set[name] {
			continue
		}
		row := kindRow{gated: true}
		if len(fields) >= 3 && strings.TrimSpace(fields[2]) == "ungated" {
			row.gated = false
		}
		if len(fields) >= 4 {
			row.instruction = strings.TrimSpace(fields[3])
		}
		set[name] = true
		rows[name] = row
		names = append(names, name)
	}
	return kindTable{names: names, set: set, rows: rows}
})

// kindTable is the one kinds list, kept once.
type kindTable struct {
	names []string
	set   map[string]bool
	rows  map[string]kindRow
}

func loadKinds() ([]string, map[string]bool) {
	k := kindTableOnce()
	return k.names, k.set
}

// InstructionKind is the instruction kind of a declared work kind, the column
// of the one list (docs/SPEC-ISA.md). ok is false when name is not declared or
// the row names no instruction kind.
func InstructionKind(name string) (string, bool) {
	row, ok := kindTableOnce().rows[name]
	if !ok || row.instruction == "" {
		return "", false
	}
	return row.instruction, true
}

// KindGated reports the gated column of the one kinds list (docs/SPEC-ISA.md
// keeps that column on the same list). A kind the list does not declare is
// gated: TEST: none is not a declaration for it.
func KindGated(name string) bool {
	row, ok := kindTableOnce().rows[name]
	if !ok {
		return true
	}
	return row.gated
}

// Kinds is the card kinds this toolchain declares, in the spec's order. A caller that
// refuses an unknown kind prints this, because a refusal a reader cannot act on is a
// refusal that sends them to the source.
func Kinds() []string {
	names, _ := loadKinds()
	return append([]string(nil), names...)
}

// KindDeclared says whether the KIND: line names a shape of work the tool declares.
// There is no default kind, and a kind not present in the table is refused.
func KindDeclared(name string) bool {
	_, set := loadKinds()
	return set[name]
}
