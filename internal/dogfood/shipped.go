package dogfood

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Shipped is the set of tools a release ships: the directories under cmd/ at
// the release commit that hold a Go program. A tool parked under deprecated/
// is not built, not tested and not shipped, so no receipt about it can speak
// for or against the release.
//
// THE GATE JUDGES WHAT SHIPS. A receipt names a tool, and a tool that is not
// in the set is a tool this release does not contain: its open edges and its
// not-ok runs are true about that tool and say nothing about the fifteen that
// ship. Counting them made the 1.0.0 cut refuse on 34 findings against tools
// the release does not hold, which is a gate nobody can pass and therefore a
// gate somebody waives.
type Shipped struct {
	Dir   string
	tools map[string]bool
}

// ReadShipped reads the shipped set from a cmd/ directory. A directory that
// cannot be read, or that holds no tool, is refused: a scope that came back
// empty would set aside every receipt and pass the gate on nothing.
func ReadShipped(cmdDir string) (Shipped, error) {
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return Shipped{}, err
	}
	s := Shipped{Dir: cmdDir, tools: map[string]bool{}}
	for _, e := range entries {
		if !e.IsDir() || !isToolName(e.Name()) {
			continue
		}
		if hasProgram(filepath.Join(cmdDir, e.Name())) {
			s.tools[e.Name()] = true
		}
	}
	if len(s.tools) == 0 {
		return Shipped{}, fmt.Errorf("%s holds no nova-* program", cmdDir)
	}
	return s, nil
}

// hasProgram reports whether a directory holds Go source other than tests. An
// empty directory left behind by a move is not a tool.
func hasProgram(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			return true
		}
	}
	return false
}

// NewShipped is a shipped set named directly, for callers that already know it.
func NewShipped(tools ...string) Shipped {
	s := Shipped{tools: map[string]bool{}}
	for _, t := range tools {
		s.tools[strings.TrimSpace(t)] = true
	}
	return s
}

// Has reports whether the release ships this tool.
func (s Shipped) Has(tool string) bool { return s.tools[strings.TrimSpace(tool)] }

// Tools is the set, sorted.
func (s Shipped) Tools() []string {
	out := make([]string, 0, len(s.tools))
	for t := range s.tools {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Scope keeps the verbs and receipts of shipped tools and returns the receipts
// it set aside, so the caller can COUNT them on its line: evidence left out of
// the arithmetic is named, never dropped in silence.
func (s Shipped) Scope(verbs []Verb, receipts []Receipt) ([]Verb, []Receipt, []Receipt) {
	var keptVerbs []Verb
	for _, v := range verbs {
		if s.Has(v.Tool) {
			keptVerbs = append(keptVerbs, v)
		}
	}
	var kept, outside []Receipt
	for _, r := range receipts {
		if s.Has(r.Tool) {
			kept = append(kept, r)
		} else {
			outside = append(outside, r)
		}
	}
	return keptVerbs, kept, outside
}
