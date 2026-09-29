package definition

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

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

// validate checks an array of definitions that parse read: repeated IDs across the
// array (naming both files), a card that depends on itself, a dependency cycle
// inside the array (naming the cycle) and more distinct outside dependencies than
// the table can guard in one batch. It returns the report, or no report and every
// refusal (at most card.MaxRefusals). A dependency on an ID that is not in the
// array is reported as external, never refused.
func validate(defs []Definition) (Report, *Refusals) {
	if len(defs) == 0 {
		return Report{}, just(ref(OpValidate, CauseEmptyArray, "", 0, "", "no definitions", "at least 1", "pass at least one definition; a single card is an array of one"))
	}
	if len(defs) > MaxFiles {
		return Report{}, just(ref(OpValidate, CauseTooMany, "", 0, "", plural(len(defs), "definition"), fmt.Sprintf("%d definitions", MaxFiles),
			fmt.Sprintf("narrow the request to at most %d; nothing is chunked for you", MaxFiles)))
	}
	c := &card.Collector{}
	first := map[string]int{}
	for i, d := range defs {
		if j, dup := first[d.ID]; dup && d.ID != "" {
			c.Add(ref(OpValidate, CauseRepeatedID, fileLabel(i, d), d.Lines[KeyID], KeyID, card.Value(d.ID), "first declared in "+card.Value(fileLabel(j, defs[j])),
				"give each card its own ID; a replacement is a new card with a new ID"))
			continue
		}
		first[d.ID] = i
	}
	for i, d := range defs {
		for _, dep := range d.DependsOn {
			if dep == d.ID && d.ID != "" {
				c.Add(ref(OpValidate, CauseSelfDependent, fileLabel(i, d), d.Lines[KeyDependsOn], KeyDependsOn, card.Value(d.ID)+" depends on itself", "a card that is not its own prerequisite",
					"remove the card's own ID from DEPENDS-ON"))
			}
		}
	}
	cycles(c, defs, first)
	outsideDependencies(c, defs, first)
	if err := c.Err(); err != nil {
		return Report{}, err
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

// outsideDependencies refuses more distinct prerequisites outside the array than
// the table guards in one batch (card.MaxOutsideDependencies): each is one
// guard-only entry.
func outsideDependencies(c *card.Collector, defs []Definition, index map[string]int) {
	outside := map[string]bool{}
	for _, d := range defs {
		for _, dep := range d.DependsOn {
			if _, in := index[dep]; !in {
				outside[dep] = true
			}
		}
	}
	if len(outside) > card.MaxOutsideDependencies {
		c.Add(ref(OpValidate, CauseTooManyExternal, "", 0, KeyDependsOn, plural(len(outside), "outside dependency"), fmt.Sprintf("%d outside dependencies", card.MaxOutsideDependencies),
			"admit fewer cards, or cards with fewer prerequisites, in one request: each outside prerequisite is guarded by one table entry"))
	}
}

// cycles adds the dependency cycles among the array's own cards, found by a
// depth-first walk in array order. Each back edge names one cycle, rotated to
// start at the member that comes first in the array. A self-dependency is refused
// separately.
func cycles(c *card.Collector, defs []Definition, index map[string]int) {
	const white, grey, black = 0, 1, 2
	color := make([]int, len(defs))
	var stack []int
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
				c.Add(cycleRefusal(defs, stack[at:]))
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
	var files []string
	for _, m := range rot {
		ids = append(ids, defs[m].ID)
		files = append(files, fileLabel(m, defs[m]))
	}
	ids = append(ids, defs[rot[0]].ID)
	head := defs[rot[0]]
	return ref(OpValidate, CauseCycle, fileLabel(rot[0], head), head.Lines[KeyDependsOn], KeyDependsOn, card.Value(strings.Join(ids, " -> ")),
		fmt.Sprintf("no cycle: %s depend on each other, in %s", plural(len(rot), "card"), card.Value(strings.Join(files, ", "))), "remove one DEPENDS-ON edge of the cycle")
}
