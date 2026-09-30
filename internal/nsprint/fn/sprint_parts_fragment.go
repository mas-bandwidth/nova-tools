package fn

import "fmt"

// sprintPartsFile is the Lua file of the tick's parts (upper design version
// 2.1, 8.1's IT16; errata 2, item 3): lease, pop, ingest, beat, clock and
// sprint. It sorts between the sprint core and the function file
// (sprint_00_core, sprint_parts, sprint_zz_fn), so a part is registered before
// any call runs, and like every sprint file it opens with the NS.tset_profile
// guard.
const sprintPartsFile = "lua/sprint_parts.lua"

// SprintPartsFragment is the parts file: its name and its source.
//
// IT12's SprintFragments lists the core and the function file and its tests
// count two; the parts file is not on that list, and the list is IT12's to
// change. This accessor is the adapter: the parts file is embedded with every
// lua/ file and is held inert by its guard in the legacy library, and the tests
// of this item read it here. When IT12's list takes the file between the other
// two, this accessor and its test go. The one line this item adds to IT12's
// function file, which calls the parts file's static quarantine check, is
// guarded and does nothing when the parts file is not loaded.
func SprintPartsFragment() (SprintFragment, error) {
	b, err := sources.ReadFile(sprintPartsFile)
	if err != nil {
		return SprintFragment{}, fmt.Errorf("read %s: %w", sprintPartsFile, err)
	}
	return SprintFragment{Name: sprintPartsFile, Source: string(b)}, nil
}
