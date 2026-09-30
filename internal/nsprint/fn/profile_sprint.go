package fn

import "fmt"

// TSetSprint is the sprint's profile of the tset library: Layer 1, Layer 2 and
// the sprint's one write path and one read path in one library (L1 1.2 names
// the server-selected profiles l1_only, composed and sprint; the upper design,
// version 2.1, 1.0; item IT12).
//
// Before gate G0 (Layer 1's revision 4 pinned, Layer 2 accepted again against
// its hash) no store loads it: TSetSource refuses the profile, and nothing
// here assembles or loads its source. After G0 the sprint profile is the
// composed profile's fragments followed by SprintFragments, under a
// registration filter that admits SprintFunctions and, in production, drops
// ns_tset_step (L1 9); that filter is tset_loader.go's, Layer 1's file.
const TSetSprint TSetProfile = "sprint"

// SprintFunctions are the two functions the sprint profile registers: the one
// write function and the one read function (1.0).
var SprintFunctions = []string{"ns_sprint_step", "ns_sprint_read"}

// sprintFragments are the sprint's Lua files in load order. Like every tset
// fragment, each opens with the NS.tset_profile guard, so the legacy library,
// which globs lua/*.lua, holds them inert.
var sprintFragments = []string{"lua/sprint_00_core.lua", "lua/sprint_zz_fn.lua"}

// SprintFragment is one of the sprint's Lua files: its name and its source.
type SprintFragment struct {
	Name, Source string
}

// SprintFragments are the sprint profile's Lua files, in load order.
func SprintFragments() ([]SprintFragment, error) {
	out := make([]SprintFragment, 0, len(sprintFragments))
	for _, name := range sprintFragments {
		b, err := sources.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		out = append(out, SprintFragment{Name: name, Source: string(b)})
	}
	return out, nil
}
