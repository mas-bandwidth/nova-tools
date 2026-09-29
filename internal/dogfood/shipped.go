package dogfood

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Shipped is the set of tools a release ships: the nova-* directories under
// cmd/ at the release commit, which is the list `release build` compiles. A tool parked under deprecated/
// is not built, not tested and not shipped, so no receipt about it can speak
// for or against the release.
//
// THE GATE JUDGES WHAT SHIPS. A receipt names a tool, and a tool that is not
// in the set is a tool this release does not contain: its open edges and its
// not-ok runs are true about that tool and say nothing about the sixteen that
// ship. Counting them made the 1.0.0 cut refuse on 34 findings against tools
// the release does not hold, which is a gate nobody can pass and therefore a
// gate somebody waives.
type Shipped struct {
	Dir   string
	tools map[string]bool
}

// CmdTools is the one definition of the tools a checkout ships: every
// directory under cmd/ whose name starts with nova-, sorted. `release build`
// compiles exactly this list (release.Tools calls it) and the gate judges
// exactly this list, so the two cannot disagree about what a release holds.
//
// AN I/O ERROR IS NOT A PARKED TOOL. Each tool directory is read, and a read
// that fails is returned with the tool's path: a tool directory that cannot be
// read is a tool nobody can say anything about, and leaving it out of the set
// would turn its open edges into receipts set aside -- a gate passing because
// of a permission bit.
func CmdTools(cmdDir string) ([]string, error) {
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return nil, err
	}
	var tools []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "nova-") {
			continue
		}
		dir := filepath.Join(cmdDir, e.Name())
		if _, err := os.ReadDir(dir); err != nil {
			return nil, fmt.Errorf("cannot read tool directory %s: %w", dir, err)
		}
		tools = append(tools, e.Name())
	}
	sort.Strings(tools)
	return tools, nil
}

// ReadShipped reads the shipped set from a cmd/ directory, by CmdTools. A
// directory that cannot be read, a tool directory under it that cannot be
// read, or a cmd/ that holds no tool is refused: a scope that came back short
// would set receipts aside that it has no grounds to.
func ReadShipped(cmdDir string) (Shipped, error) {
	tools, err := CmdTools(cmdDir)
	if err != nil {
		return Shipped{}, err
	}
	if len(tools) == 0 {
		return Shipped{}, fmt.Errorf("%s holds no nova-* tool directory", cmdDir)
	}
	s := NewShipped(tools...)
	s.Dir = cmdDir
	return s, nil
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
