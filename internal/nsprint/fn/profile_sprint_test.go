package fn

import (
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// firstStatement is the first line of src that is not blank or a comment.
func firstStatement(src string) string {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			return line
		}
	}
	return ""
}

// sprintFileProblem is what is wrong with one sprint file, "" when nothing is:
// it is Lua in Redis's dialect (5.1, as gopher-lua parses and compiles it),
// and it opens with the NS.tset_profile guard, so the legacy library, which
// globs lua/*.lua, holds it inert.
func sprintFileProblem(name, src string) string {
	chunk, err := parse.Parse(strings.NewReader(src), name)
	if err != nil {
		return fmt.Sprintf("parse %s: %v", name, err)
	}
	if _, err := lua.Compile(chunk, name); err != nil {
		return fmt.Sprintf("compile %s: %v", name, err)
	}
	if first := firstStatement(src); first != "if NS.tset_profile then" {
		return fmt.Sprintf("%s first statement is %q, want the tset profile guard", name, first)
	}
	return ""
}

// fragmentNames is the names of frags.
func fragmentNames(frags []SprintFragment) []string {
	out := make([]string, len(frags))
	for i, f := range frags {
		out[i] = f.Name
	}
	return out
}

// TestSprintLuaParses: each of the sprint's Lua files, every lua/sprint_*.lua
// and so every file a later item adds, is Lua in Redis's dialect and opens
// with the NS.tset_profile guard, so the legacy library, which globs
// lua/*.lua, holds it inert. No store runs it before G0; this is the check it
// gets tonight.
func TestSprintLuaParses(t *testing.T) {
	t.Parallel()
	frags, err := SprintFragments()
	if err != nil {
		t.Fatal(err)
	}
	if len(frags) < 2 {
		t.Fatalf("%d sprint fragments, want at least sprint_00_core.lua and sprint_zz_fn.lua", len(frags))
	}
	for _, f := range frags {
		if p := sprintFileProblem(f.Name, f.Source); p != "" {
			t.Error(p)
		}
	}
}

// TestEverySprintFileOnDiskIsCovered is the listing test: every sprint_*.lua
// in the lua/ directory, read from the disk and not through the glob that
// SprintFragments uses, is one of the sprint fragments, in sorted order, with
// the core first and sprint_zz_fn.lua last (the core defines the registry the
// rest register into; sprint_zz_fn.lua registers the two functions once all
// are in). A sprint_ file the profile does not cover fails here, so a later
// item's file cannot load outside the guard, the seams and this order.
func TestEverySprintFileOnDiskIsCovered(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir("lua")
	if err != nil {
		t.Fatal(err)
	}
	var disk []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "sprint_") && strings.HasSuffix(e.Name(), ".lua") {
			disk = append(disk, "lua/"+e.Name())
		}
	}
	sort.Strings(disk)
	frags, err := SprintFragments()
	if err != nil {
		t.Fatal(err)
	}
	if got := fragmentNames(frags); !reflect.DeepEqual(got, disk) {
		t.Fatalf("the sprint profile covers %v, the lua/ directory holds %v", got, disk)
	}
	if len(disk) < 2 || disk[0] != sprintCoreFragment || disk[len(disk)-1] != sprintFnFragment {
		t.Fatalf("the sprint files are %v, want %s first and %s last", disk, sprintCoreFragment, sprintFnFragment)
	}
	for _, name := range disk {
		if !isSprintFragment(name) {
			t.Errorf("%s is on the disk and in the profile, and isSprintFragment refuses it", name)
		}
	}
}

// reversedDirFS is a tree whose directories list their entries in reverse
// name order. It has no Glob of its own, so fs.Glob lists through ReadDir.
type reversedDirFS struct{ inner fstest.MapFS }

func (r reversedDirFS) Open(name string) (fs.File, error) { return r.inner.Open(name) }

func (r reversedDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := r.inner.ReadDir(name)
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, err
}

// TestSprintFragmentsCoverAnyNamedFile: the glob covers a sprint_*.lua file the
// day it is added, in load order, and nothing else. The tree is a fixture:
// a new sprint_zz_extra.lua and a sprint_x.lua beside the two files, and
// neighbours that must stay out (the legacy sprint.lua, another extension, a
// subdirectory, another directory, another prefix). The listing and
// isSprintFragment, which the seams use, agree on every file of it.
func TestSprintFragmentsCoverAnyNamedFile(t *testing.T) {
	t.Parallel()
	guard := []byte("if NS.tset_profile then\ndo\nend\nend\n")
	tree := fstest.MapFS{}
	for _, name := range []string{
		"lua/sprint_zz_fn.lua", "lua/sprint_zz_extra.lua", "lua/sprint_x.lua", "lua/sprint_00_core.lua",
		"lua/sprint.lua", "lua/sprint_notes.txt", "lua/sprint_sub/deep.lua", "lua/xsprint_x.lua", "lua/Sprint_x.lua",
		"lua/sprintx.lua", "lua/task.lua", "sprint_top.lua", "other/sprint_x.lua",
	} {
		tree[name] = &fstest.MapFile{Data: guard}
	}
	got, err := sprintFragments(tree)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lua/sprint_00_core.lua", "lua/sprint_x.lua", "lua/sprint_zz_extra.lua", "lua/sprint_zz_fn.lua"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the fixture's sprint files are %v, want %v", got, want)
	}
	inList := map[string]bool{}
	for _, name := range got {
		inList[name] = true
	}
	for name := range tree {
		if isSprintFragment(name) != inList[name] {
			t.Errorf("%s: isSprintFragment is %v, the listing has it %v", name, isSprintFragment(name), inList[name])
		}
	}
	// The load order is sorted whatever order a directory hands its entries in.
	rev, err := sprintFragments(reversedDirFS{tree})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rev, want) {
		t.Fatalf("a directory listed backwards gives the sprint files %v, want %v", rev, want)
	}
	// A fixture file covered by the glob is held to the guard like the rest.
	if p := sprintFileProblem("lua/sprint_zz_extra.lua", string(guard)); p != "" {
		t.Errorf("the guarded fixture: %s", p)
	}
	for _, bad := range []string{"local x = 1\nif NS.tset_profile then\nend\n", "-- c\nNS.tset_profile = nil\n", "if NS.tset_profile then\nlocal = \nend\n", ""} {
		if p := sprintFileProblem("lua/sprint_zz_extra.lua", bad); p == "" {
			t.Errorf("a sprint file %q passed the guard test", bad)
		}
	}
}

var luaStringList = regexp.MustCompile(`'([^']*)'`)

// luaList is the strings of the Lua table assigned to name in src.
func luaList(t *testing.T, src, name string) []string {
	t.Helper()
	m := regexp.MustCompile(regexp.QuoteMeta(name) + `\s*=\s*\{([^}]*)\}`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no %s in the Lua core", name)
	}
	var out []string
	for _, s := range luaStringList.FindAllStringSubmatch(m[1], -1) {
		out = append(out, s[1])
	}
	return out
}

// TestSprintLuaOrdersMatchGo: the Lua core's phase order and part order are
// sprintfn's, so the twin and the store run one order; and the Lua registers
// exactly the two functions the profile names.
func TestSprintLuaOrdersMatchGo(t *testing.T) {
	t.Parallel()
	frags, err := SprintFragments()
	if err != nil {
		t.Fatal(err)
	}
	source := map[string]string{}
	for _, f := range frags {
		source[f.Name] = f.Source
	}
	core, fns := source[sprintCoreFragment], source[sprintFnFragment]
	if core == "" || fns == "" {
		t.Fatalf("the sprint files %v lack %s or %s", fragmentNames(frags), sprintCoreFragment, sprintFnFragment)
	}
	if got := luaList(t, core, "SP.phase_order"); !reflect.DeepEqual(got, sprintfn.PhaseOrder) {
		t.Fatalf("Lua phase order %v, Go %v", got, sprintfn.PhaseOrder)
	}
	if got := luaList(t, core, "SP.part_order"); !reflect.DeepEqual(got, sprintfn.PartOrder) {
		t.Fatalf("Lua part order %v, Go %v", got, sprintfn.PartOrder)
	}
	var registered []string
	for _, m := range legacyStringRegistration.FindAllStringSubmatch(fns, -1) {
		registered = append(registered, m[1])
	}
	for _, m := range legacyTableRegistration.FindAllStringSubmatch(fns, -1) {
		registered = append(registered, m[1])
	}
	sort.Strings(registered)
	want := append([]string(nil), SprintFunctions...)
	sort.Strings(want)
	if !reflect.DeepEqual(registered, want) {
		t.Fatalf("the Lua registers %v, the profile names %v", registered, want)
	}
	for _, f := range frags {
		if f.Name != sprintFnFragment && strings.Contains(f.Source, "register_function") {
			t.Fatalf("%s registers a function; only %s does", f.Name, sprintFnFragment)
		}
	}
}

// TestSprintProfileIsNotLoadable: before G0 the tset assembler refuses the
// sprint profile, and no tset profile's source carries a sprint fragment.
func TestSprintProfileIsNotLoadable(t *testing.T) {
	t.Parallel()
	if _, err := TSetSource(TSetSprint); err == nil {
		t.Fatal("the tset assembler accepted the sprint profile before G0")
	}
	standalone, err := TSetSource(TSetStandalone)
	if err != nil {
		t.Fatal(err)
	}
	names, err := sprintFragments(sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.Contains(standalone, "\n-- "+name+"\n") {
			t.Fatalf("the standalone tset profile carries %s", name)
		}
	}
}
