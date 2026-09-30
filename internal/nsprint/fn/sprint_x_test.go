package fn

import (
	"fmt"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The Lua half of X (item IT13) is written to IT12's interfaces and not loaded by
// any store before gate G0. IT12's list of the sprint's fragments (profile_sprint.go)
// names two files and is not this item's to change, so these tests read
// lua/sprint_x.lua from the embedded tree themselves; the list must gain it, between
// the core and the functions, when the profile is assembled (listed as an open
// question).

const sprintXFile = "lua/sprint_x.lua"

func sprintXSource(t *testing.T) string {
	t.Helper()
	b, err := sources.ReadFile(sprintXFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSprintXLuaParses: X's Lua is Lua in Redis's dialect (5.1, as gopher-lua parses
// and compiles it) and opens with the NS.tset_profile guard, so the legacy
// library, which globs lua/*.lua, holds it inert.
func TestSprintXLuaParses(t *testing.T) {
	t.Parallel()
	src := sprintXSource(t)
	chunk, err := parse.Parse(strings.NewReader(src), sprintXFile)
	if err != nil {
		t.Fatalf("parse %s: %v", sprintXFile, err)
	}
	if _, err := lua.Compile(chunk, sprintXFile); err != nil {
		t.Fatalf("compile %s: %v", sprintXFile, err)
	}
	first := ""
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			first = line
			break
		}
	}
	if first != "if NS.tset_profile then" {
		t.Errorf("%s first statement is %q, want the tset profile guard", sprintXFile, first)
	}
}

// TestSprintXLuaLoadsAfterTheCoreBeforeTheFunctions: the file sorts between
// sprint_00_core.lua, which defines the registry it registers its phases in, and
// sprint_zz_fn.lua, which calls them, so the library's glob loads it in the order
// it needs (8.0).
func TestSprintXLuaLoadsAfterTheCoreBeforeTheFunctions(t *testing.T) {
	t.Parallel()
	frags, err := sprintFragments(sources)
	if err != nil {
		t.Fatal(err)
	}
	if !(frags[0] < sprintXFile && sprintXFile < frags[len(frags)-1]) {
		t.Fatalf("%s does not sort between %s and %s", sprintXFile, frags[0], frags[len(frags)-1])
	}
}

// renderXDefs is the definitions block of sprint_x.lua as the Go tables render it.
func renderXDefs() string {
	q := func(s string) string { return "'" + s + "'" }
	var b strings.Builder
	b.WriteString("  SP.x_defs = {\n    index = {\n")
	for _, d := range sprint.IndexDefs {
		var where []string
		for _, w := range d.Where {
			where = append(where, fmt.Sprintf("{field = %s, test = %s, value = %s}", q(w.Field), q(string(w.Test)), q(w.Value)))
		}
		fmt.Fprintf(&b, "      {index = %s, table = %s, col = %s, where = {%s}},\n", q(d.Index), q(d.Table), q(d.Col), strings.Join(where, ", "))
	}
	b.WriteString("    },\n    due = {\n")
	for _, k := range sprint.DueKinds {
		fmt.Fprintf(&b, "      {kind = %s, table = %s, col = %s, field = %s, of_row = %t},\n", q(k.Kind), q(k.Table), q(k.Col), q(k.Field), k.OfRow)
	}
	var fields []string
	for _, f := range sprint.IndexFields() {
		fields = append(fields, q(f))
	}
	b.WriteString("    },\n    fields = {" + strings.Join(fields, ", ") + "},\n  }\n")
	return b.String()
}

// TestSprintXLuaDefsGolden: the definitions block of sprint_x.lua equals the
// rendering of IT02's Go tables (sprint.IndexDefs, sprint.DueKinds and
// sprint.IndexFields), so a changed row of the Go table is a failing test until
// the block is re-rendered (1.3.2).
func TestSprintXLuaDefsGolden(t *testing.T) {
	t.Parallel()
	src := sprintXSource(t)
	const begin, end = "  -- x_defs begin\n", "  -- x_defs end\n"
	i, j := strings.Index(src, begin), strings.Index(src, end)
	if i < 0 || j < i {
		t.Fatalf("%s has no x_defs block between %q and %q", sprintXFile, strings.TrimSpace(begin), strings.TrimSpace(end))
	}
	got, want := src[i+len(begin):j], renderXDefs()
	if got != want {
		t.Fatalf("the definitions block of %s is not the rendering of sprint.IndexDefs, DueKinds and IndexFields; it must be:\n%s", sprintXFile, want)
	}
}
