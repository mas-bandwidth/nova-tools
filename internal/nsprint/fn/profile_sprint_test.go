package fn

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// TestSprintLuaParses: each of the sprint's Lua files is Lua in Redis's
// dialect (5.1, as gopher-lua parses and compiles it), and opens with the
// NS.tset_profile guard, so the legacy library, which globs lua/*.lua, holds
// it inert. No store runs it before G0; this is the check it gets tonight.
func TestSprintLuaParses(t *testing.T) {
	t.Parallel()
	frags, err := SprintFragments()
	if err != nil {
		t.Fatal(err)
	}
	if len(frags) != 2 {
		t.Fatalf("%d sprint fragments, want sprint_00_core.lua and sprint_zz_fn.lua", len(frags))
	}
	for _, f := range frags {
		chunk, err := parse.Parse(strings.NewReader(f.Source), f.Name)
		if err != nil {
			t.Fatalf("parse %s: %v", f.Name, err)
		}
		if _, err := lua.Compile(chunk, f.Name); err != nil {
			t.Fatalf("compile %s: %v", f.Name, err)
		}
		first := ""
		for _, line := range strings.Split(f.Source, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "--") {
				first = line
				break
			}
		}
		if first != "if NS.tset_profile then" {
			t.Errorf("%s first statement is %q, want the tset profile guard", f.Name, first)
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
	core, fns := frags[0].Source, frags[1].Source
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
	if strings.Contains(core, "register_function") {
		t.Fatal("the core registers a function; only sprint_zz_fn.lua does")
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
	for _, name := range sprintFragments {
		if strings.Contains(standalone, "\n-- "+name+"\n") {
			t.Fatalf("the standalone tset profile carries %s", name)
		}
	}
}
