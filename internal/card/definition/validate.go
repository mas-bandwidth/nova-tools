package definition

import (
	"fmt"
	"strings"
)

// keyBrief is the pseudo-key checkDefinition uses for the brief.
const keyBrief = "BRIEF"

// checkDefinition checks one definition's fields by the profile's rules. Parse
// calls it after reading a file, and Validate calls it on every definition it is
// given, so a definition built by hand meets the same rules a parsed one did.
// skip names the keys already refused; lines gives the line of each key (nil for
// a definition built by hand).
func checkDefinition(op string, d Definition, skip map[string]bool, lines map[string]int, file string) []Refusal {
	var out []Refusal
	add := func(key string, c Cause, found, next string) {
		if skip[key] {
			return
		}
		out = append(out, Refusal{Operation: op, File: file, Line: lines[key], Key: key, Cause: c, Found: found, Next: next})
	}
	if d.Schema != SchemaV2 {
		if d.Schema == "v3" {
			add(KeySchema, CauseUnsupportedSchema, "SCHEMA v3 is refused by name: the Work-path profile is not implemented", "write SCHEMA: v2 and card IDs in DEPENDS-ON")
		} else {
			add(KeySchema, CauseUnsupportedSchema, fmt.Sprintf("SCHEMA %q is not a version this reader supports", d.Schema), "write SCHEMA: v2")
		}
	}
	if w := idWhy(d.ID); w != "" {
		add(KeyID, CauseInvalidID, w, "use an ID of ASCII letters, digits, underscore and hyphen")
	}
	if d.Entry != "" {
		if w := entryWhy(d.Entry); w != "" {
			add(KeyEntry, CauseInvalidEntry, w, "write one entry path with no comma and no control character")
		}
	}
	if w := textWhy(KeyTitle, d.Title, MaxValueBytes); w != "" {
		add(KeyTitle, CauseInvalidValue, w, "write a short one-line title")
	}
	class := Completion("")
	if c, w := kindWhy(d.Kind); c != "" {
		add(KeyKind, c, w, "use a kind internal/hygiene/kinds.txt declares and internal/card/definition/completion.txt classifies")
	} else {
		cs, _ := Classify([]string{d.Kind})
		class = cs[0]
	}
	if len(d.Paths) > 0 {
		if w := pathsWhy(d.Paths); w != "" {
			add(KeyPaths, CauseInvalidPaths, w, "write `PATHS: none` or up to eight repository-relative globs")
		}
	}
	if w := dependsWhy(d.DependsOn); w != "" {
		add(KeyDependsOn, CauseInvalidDependsOn, w, "write `DEPENDS-ON: -` or comma-separated card IDs")
	}
	if w := tierWhy(d.Tier); w != "" {
		add(KeyTier, CauseInvalidTier, w, "write one route: frontier, pro or flash")
	}
	if class != "" {
		if w := testWhy(d.Test, class); w != "" {
			add(KeyTest, CauseInvalidTest, w, "write `TEST: <package> <TestName>`, or `TEST: none <why>` for a kind that completes without a pull request")
		}
	}
	for _, kv := range []struct{ k, v string }{{KeyDoneWhen, d.DoneWhen}, {KeyDoors, d.Doors}, {KeyProbes, d.Probes}} {
		if w := textWhy(kv.k, kv.v, MaxValueBytes); w != "" {
			add(kv.k, CauseInvalidValue, w, "write one line of text, or `none` for DOORS and PROBES")
		}
	}
	if strings.TrimSpace(d.Brief) == "" {
		add(keyBrief, CauseNoBrief, "the brief is empty", "add the brief below the header")
	}
	return out
}

// External is a dependency that names an ID outside the array: the card that
// declares it, its file, and the ID. It is not an error here; the caller guards
// the ID against what is already admitted.
type External struct {
	ID         string
	File       string
	Dependency string
}

// Report is what a valid array says about itself.
type Report struct {
	// IDs are the array's card IDs, in array order.
	IDs []string
	// External are the dependencies on IDs not in the array, in array order.
	External []External
}

func fileLabel(i int, d Definition) string {
	if d.File != "" {
		return d.File
	}
	return fmt.Sprintf("#%d", i+1)
}

// Validate checks an array of definitions: every definition's fields, repeated
// IDs across the array (naming both files), a card that depends on itself, and a
// dependency cycle inside the array (naming the cycle). It returns the report, or
// no report and every refusal. A dependency on an ID that is not in the array is
// reported as external, never refused.
func Validate(defs []Definition) (Report, []Refusal) {
	if len(defs) == 0 {
		return Report{}, []Refusal{{Operation: OpValidate, Cause: CauseEmptyArray,
			Found: "no definitions", Next: "pass at least one definition; a single card is an array of one"}}
	}
	if len(defs) > MaxFiles {
		return Report{}, []Refusal{{Operation: OpValidate, Cause: CauseTooManyFiles,
			Found: fmt.Sprintf("%d definitions, the limit is %d", len(defs), MaxFiles),
			Next:  fmt.Sprintf("narrow the request to at most %d; nothing is chunked for you", MaxFiles)}}
	}
	var refs []Refusal
	for i, d := range defs {
		refs = append(refs, checkDefinition(OpValidate, d, nil, d.Lines, fileLabel(i, d))...)
	}
	first := map[string]int{}
	for i, d := range defs {
		if j, dup := first[d.ID]; dup && d.ID != "" {
			refs = append(refs, Refusal{Operation: OpValidate, File: fileLabel(i, d), Line: d.Lines[KeyID], Key: KeyID, Cause: CauseDuplicateID,
				Found: fmt.Sprintf("ID %s is declared by %s and by %s", d.ID, fileLabel(j, defs[j]), fileLabel(i, d)),
				Next:  "give each card its own ID; a replacement is a new card with a new ID",
				Also:  []string{fileLabel(j, defs[j])}})
			continue
		}
		first[d.ID] = i
	}
	for i, d := range defs {
		for _, dep := range d.DependsOn {
			if dep == d.ID && d.ID != "" {
				refs = append(refs, Refusal{Operation: OpValidate, File: fileLabel(i, d), Line: d.Lines[KeyDependsOn], Key: KeyDependsOn, Cause: CauseSelfDependent,
					Found: fmt.Sprintf("%s depends on itself", d.ID), Next: "remove the card's own ID from DEPENDS-ON"})
			}
		}
	}
	refs = append(refs, cycles(defs, first)...)
	if len(refs) > 0 {
		return Report{}, refs
	}
	rep := Report{}
	for i, d := range defs {
		rep.IDs = append(rep.IDs, d.ID)
		for _, dep := range d.DependsOn {
			if _, in := first[dep]; !in {
				rep.External = append(rep.External, External{ID: d.ID, File: fileLabel(i, d), Dependency: dep})
			}
		}
	}
	return rep, nil
}

// cycles finds the dependency cycles among the array's own cards, by a depth-first
// walk in array order. Each back edge names one cycle, rotated to start at the
// member that comes first in the array. A self-dependency is refused separately.
func cycles(defs []Definition, index map[string]int) []Refusal {
	const white, grey, black = 0, 1, 2
	color := make([]int, len(defs))
	var stack []int
	var out []Refusal
	var visit func(int)
	visit = func(u int) {
		color[u] = grey
		stack = append(stack, u)
		for _, dep := range defs[u].DependsOn {
			v, in := index[dep]
			if !in || v == u {
				continue
			}
			switch color[v] {
			case white:
				visit(v)
			case grey:
				at := 0
				for k, m := range stack {
					if m == v {
						at = k
					}
				}
				out = append(out, cycleRefusal(defs, stack[at:]))
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
	}
	for u := range defs {
		if color[u] == white {
			visit(u)
		}
	}
	return out
}

func cycleRefusal(defs []Definition, members []int) Refusal {
	lo := 0
	for k, m := range members {
		if m < members[lo] {
			lo = k
		}
	}
	rot := append(append([]int(nil), members[lo:]...), members[:lo]...)
	ids := make([]string, 0, len(rot)+1)
	var also []string
	for _, m := range rot {
		ids = append(ids, defs[m].ID)
		if m != rot[0] {
			also = append(also, fileLabel(m, defs[m]))
		}
	}
	ids = append(ids, defs[rot[0]].ID)
	head := defs[rot[0]]
	return Refusal{Operation: OpValidate, File: fileLabel(rot[0], head), Line: head.Lines[KeyDependsOn], Key: KeyDependsOn, Cause: CauseCycle,
		Found: strings.Join(ids, " -> "), Also: also,
		Next: "remove one DEPENDS-ON edge of the cycle"}
}
