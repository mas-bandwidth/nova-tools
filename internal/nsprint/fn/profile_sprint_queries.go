package fn

import "fmt"

// sprintQueryFragments are the Lua files of the sprint's composite queries and
// its bounded sprint-key reads (upper design 2.1, 1.0 and errata 1 E4 to E6;
// item IT30). They register with NS.SP.query, which sprint_00_core.lua
// defines, and ns_sprint_read (sprint_zz_fn.lua) reads NS.SP.queries when a
// call runs, so the file must load after the core and its place beside
// sprint_zz_fn.lua is free. Like every sprint fragment it opens with the
// NS.tset_profile guard, so the legacy library, which globs lua/*.lua, holds
// it inert.
//
// IT12's SprintFragments lists two files and IT12's test holds the list at
// two, so this list is IT30's own and is not merged into it: at gate G0 the
// assembler of the sprint profile takes both lists, in file-name order
// (sprint_00_core.lua, sprint_queries.lua, sprint_zz_fn.lua). Before G0 no
// store loads it.
var sprintQueryFragments = []string{"lua/sprint_queries.lua"}

// SprintQueryFragments are the sprint profile's query Lua files, in load order.
func SprintQueryFragments() ([]SprintFragment, error) {
	out := make([]SprintFragment, 0, len(sprintQueryFragments))
	for _, name := range sprintQueryFragments {
		b, err := sources.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		out = append(out, SprintFragment{Name: name, Source: string(b)})
	}
	return out, nil
}
