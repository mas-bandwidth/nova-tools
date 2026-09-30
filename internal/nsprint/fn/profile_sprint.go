package fn

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
)

// TSetSprint is the sprint's profile of the tset library: Layer 1, Layer 2 and
// the sprint's one write path and one read path in one library (L1 1.2 names
// the server-selected profiles l1_only, composed and sprint; the upper design,
// version 2.1, 1.0; item IT12).
//
// The sprint profile is the composed profile's fragments, Layer 2's log among
// them, followed by SprintFragments, under a registration filter that admits
// Layer 1's functions (TSetFunctions) and SprintFunctions (tset_loader.go's TSetSource and
// registrationFilter). It loads only on a test's own store: a production
// loader, which drops ns_tset_step (L1 9), waits on gate G0 (Layer 2's
// contract accepted again against Layer 1's revision 4).
const TSetSprint TSetProfile = "sprint"

// SprintFunctions are the two functions the sprint profile registers: the one
// write function and the one read function (1.0).
var SprintFunctions = []string{"ns_sprint_step", "ns_sprint_read"}

// sprintGlob names the sprint profile's Lua files: every lua/sprint_*.lua, in
// sorted order, which is the load order of the legacy library's glob and so of
// the sprint profile after the composed fragments. A later item adds its file
// by placing it in lua/ under such a name; no list changes, and none can be
// forgotten, because the profile seams cover a file by its name: the
// NS.tset_profile guard every fragment opens with, and the NS.tset and
// NS.tlog that TestNoBareCrossFileReferences lets every fragment after the
// core resolve when a call runs. A covered file is still bounded: every
// fragment must open with the guard, and only sprint_zz_fn.lua may register
// a function (both tested over every fragment), so the registration filter
// has SprintFunctions to admit and nothing else. The composed tset library,
// unlike this profile, lists its files by hand (tset_loader.go).
const sprintGlob = "lua/sprint_*.lua"

// The first and the last of the sprint's files: the core defines the registry
// (NS.SP) the others register into, and sprint_zz_fn.lua registers the two
// functions once every phase and part is in. The names sort first and last.
const (
	sprintCoreFragment = "lua/sprint_00_core.lua"
	sprintFnFragment   = "lua/sprint_zz_fn.lua"
)

// isSprintFragment reports whether name, a path in the embedded lua/ tree, is
// one of the sprint profile's files. It is the one definition of membership,
// the same pattern sprintFragments globs.
func isSprintFragment(name string) bool {
	ok, err := path.Match(sprintGlob, name)
	return err == nil && ok
}

// sprintFragments lists the sprint's Lua files in fsys in load order: every
// file the pattern names, sorted.
func sprintFragments(fsys fs.FS) ([]string, error) {
	names, err := fs.Glob(fsys, sprintGlob)
	if err != nil {
		return nil, fmt.Errorf("list the sprint files: %w", err)
	}
	sort.Strings(names)
	return names, nil
}

// SprintFragment is one of the sprint's Lua files: its name and its source.
type SprintFragment struct {
	Name, Source string
}

// SprintFragments are the sprint profile's Lua files, in load order.
func SprintFragments() ([]SprintFragment, error) {
	names, err := sprintFragments(sources)
	if err != nil {
		return nil, err
	}
	out := make([]SprintFragment, 0, len(names))
	for _, name := range names {
		b, err := sources.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		out = append(out, SprintFragment{Name: name, Source: string(b)})
	}
	return out, nil
}
