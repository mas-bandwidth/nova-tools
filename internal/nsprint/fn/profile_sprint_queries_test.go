package fn

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

func queryFragment(t *testing.T) SprintFragment {
	t.Helper()
	frags, err := SprintQueryFragments()
	if err != nil {
		t.Fatal(err)
	}
	if len(frags) != 1 || frags[0].Name != "lua/sprint_queries.lua" {
		t.Fatalf("query fragments %+v, want lua/sprint_queries.lua", frags)
	}
	return frags[0]
}

// TestSprintQueriesLuaParses (IT30): the queries' Lua file is Lua in Redis's
// dialect (5.1, as gopher-lua parses and compiles it), and opens with the
// NS.tset_profile guard, so the legacy library, which globs lua/*.lua, holds
// it inert. No store runs it before G0; this is the check it gets tonight, and
// TestSprintQueriesLuaRegisters loads it.
func TestSprintQueriesLuaParses(t *testing.T) {
	t.Parallel()
	f := queryFragment(t)
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
	code := regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(f.Source, "")
	for _, banned := range []string{"redis.call", "redis.pcall", "S.readcmd", "readcmd("} {
		if strings.Contains(code, banned) {
			t.Errorf("%s calls %s: a query reads only through Layer 1's checked helpers (E6)", f.Name, banned)
		}
	}
}

// TestSprintQueriesLuaRegisters: loaded after the core, the file registers the
// eight composite kinds and the nine sprint-key kinds and no other, each with
// a pure validate and a read, under a kind Layer 1 accepts (the identifier
// grammar) and neither Layer 1's nor Layer 2's; and loaded in the legacy
// prelude, where NS.tset_profile is unset, it registers nothing.
func TestSprintQueriesLuaRegisters(t *testing.T) {
	t.Parallel()
	frags, err := SprintFragments()
	if err != nil {
		t.Fatal(err)
	}
	f := queryFragment(t)
	load := func(profile bool) *lua.LState {
		L := lua.NewState()
		t.Cleanup(L.Close)
		src := "NS = {}\n"
		if profile {
			src = "NS = {tset_profile = 'sprint'}\n"
		}
		if err := L.DoString(src); err != nil {
			t.Fatal(err)
		}
		if err := L.DoString(frags[0].Source); err != nil {
			t.Fatalf("core: %v", err)
		}
		if err := L.DoString(f.Source); err != nil {
			t.Fatalf("queries: %v", err)
		}
		return L
	}
	kinds := func(L *lua.LState) []string {
		sp, ok := L.GetGlobal("NS").(*lua.LTable).RawGetString("SP").(*lua.LTable)
		if !ok {
			return nil
		}
		var out []string
		sp.RawGetString("queries").(*lua.LTable).ForEach(func(k, v lua.LValue) {
			spec := v.(*lua.LTable)
			if _, ok := spec.RawGetString("validate").(*lua.LFunction); !ok {
				t.Errorf("%s has no validate", k)
			}
			if _, ok := spec.RawGetString("read").(*lua.LFunction); !ok {
				t.Errorf("%s has no read", k)
			}
			out = append(out, k.String())
		})
		sort.Strings(out)
		return out
	}
	want := append(append([]string(nil), sprintfn.CompositeKinds...), sprintfn.SprintKeyKinds...)
	sort.Strings(want)
	got := kinds(load(true))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("registered %v, want %v", got, want)
	}
	grammar := regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	lower := map[string]bool{"range": true, "count": true, "rcount": true, "ids": true, "rows": true, "done": true,
		"last": true, "lines": true, "cardlines": true}
	for _, k := range got {
		if !grammar.MatchString(k) || len(k) > 256 || lower[k] {
			t.Errorf("kind %q is not a kind Layer 1 admits for an extension", k)
		}
	}
	if legacy := kinds(load(false)); len(legacy) != 0 {
		t.Fatalf("the legacy prelude registered %v", legacy)
	}
}

// TestSprintQueriesLuaTablesMatchGo: the tables and bounds the Lua copies are
// the Go's: the indexes a place could put a card in and the due kinds a state
// could give it (IT02's IndexDefs and DueKinds), the follows' costs (IT05's
// QueryCost) and Layer 1's read bounds, so a change of the design is a changed
// row in both.
func TestSprintQueriesLuaTablesMatchGo(t *testing.T) {
	t.Parallel()
	src := queryFragment(t).Source

	indexRow := regexp.MustCompile(`\{index = '(\w+)', table = '(\w+)', col = '(\w+)'\}`)
	var gotIdx []string
	for _, m := range indexRow.FindAllStringSubmatch(src, -1) {
		gotIdx = append(gotIdx, strings.Join(m[1:], "/"))
	}
	var wantIdx []string
	for _, d := range sprint.IndexDefs {
		wantIdx = append(wantIdx, d.Index+"/"+d.Table+"/"+d.Col)
	}
	if strings.Join(gotIdx, ",") != strings.Join(wantIdx, ",") {
		t.Errorf("index definitions: Lua %v, Go %v", gotIdx, wantIdx)
	}

	dueRow := regexp.MustCompile(`\{kind = '(\w+)', table = '(\w+)', col = '(\w+)', of_row = (true|false)\}`)
	var gotDue []string
	for _, m := range dueRow.FindAllStringSubmatch(src, -1) {
		gotDue = append(gotDue, strings.Join(m[1:], "/"))
	}
	var wantDue []string
	for _, k := range sprint.DueKinds {
		wantDue = append(wantDue, k.Kind+"/"+k.Table+"/"+k.Col+"/"+strconv.FormatBool(k.OfRow))
	}
	if strings.Join(gotDue, ",") != strings.Join(wantDue, ",") {
		t.Errorf("due kinds: Lua %v, Go %v", gotDue, wantDue)
	}

	num := func(name string) int {
		t.Helper()
		m := regexp.MustCompile(`Q\.` + name + `\s*=\s*(\d+)`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("no Q.%s in the Lua", name)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	for name, want := range map[string]int{
		"MAX_RECORDS": sprint.MaxReadRecords, "MAX_RANGE_IDS": sprint.MaxReadRangeIDs, "MAX_HEAD": sprint.MaxRangeLimit,
		"MAX_LINE_IDS": sprint.MaxLineIDs, "MAX_ABOUT": sprint.MaxAboutIDs, "MAX_NEEDS": 64, "MAX_RCARDS": 15,
		"PROBE_CHUNK": 2000, "MAX_FIELDS": 128, "MAX_NAME": 256, "MAX_PROJECTION": 124, "MAX_COLUMNS": 32, "MAX_PROBES": 20000,
	} {
		if got := num(name); got != want {
			t.Errorf("Q.%s is %d in the Lua, %d in the Go", name, got, want)
		}
	}
	if m := regexp.MustCompile(`Q\.MAX_STREAMS, Q\.MAX_MEMBERS, Q\.MAX_READERS = (\d+), (\d+), (\d+)`).FindStringSubmatch(src); m == nil ||
		m[1] != strconv.Itoa(sprint.MaxStreams) || m[2] != strconv.Itoa(sprint.MaxMembers) || m[3] != strconv.Itoa(sprint.MaxReaders) {
		t.Errorf("the most streams, members and readers: %v", m)
	}

	// Each follow's cost in the Lua is IT05's: the records a follow adds to a card.
	m := regexp.MustCompile(`Q\.FOLLOWS = \{([^}]*)\}`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("no Q.FOLLOWS in the Lua")
	}
	costs := map[string]int{}
	for _, kv := range regexp.MustCompile(`(\w+) = (\d+)`).FindAllStringSubmatch(m[1], -1) {
		costs[kv[1]], _ = strconv.Atoi(kv[2])
	}
	if len(costs) != len(sprint.Follows) {
		t.Errorf("the Lua has %d follows, 1.0 has %d", len(costs), len(sprint.Follows))
	}
	for _, f := range sprint.Follows {
		q := sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: sprint.IDSource{Kind: sprint.SourceIDs, IDs: []string{"a"}},
			Fields: []string{}, Follow: []string{f}}
		if want := sprint.QueryCost(q).Records - 1; costs[f] != want {
			t.Errorf("follow %s costs %d in the Lua, %d by QueryCost", f, costs[f], want)
		}
	}
}

// TestSprintQueriesLuaNamesResolve: the file is checked for the mistakes a
// store would find only when a path runs, since no store runs it before G0.
// Compiled, it refers to no global but the runtime's (NS and the cjson that
// Redis exposes when a function runs, and Lua's own libraries), and it writes
// no global; and every member of its helper table Q that it uses is defined
// there.
func TestSprintQueriesLuaNamesResolve(t *testing.T) {
	t.Parallel()
	f := queryFragment(t)
	chunk, err := parse.Parse(strings.NewReader(f.Source), f.Name)
	if err != nil {
		t.Fatal(err)
	}
	proto, err := lua.Compile(chunk, f.Name)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"NS": true, "cjson": true, "string": true, "table": true, "math": true, "type": true,
		"pairs": true, "ipairs": true, "tonumber": true, "tostring": true, "pcall": true, "next": true, "getmetatable": true}
	read, written := map[string]bool{}, map[string]bool{}
	var walk func(p *lua.FunctionProto)
	walk = func(p *lua.FunctionProto) {
		for _, inst := range p.Code {
			op := int(inst >> 26)
			if op != lua.OP_GETGLOBAL && op != lua.OP_SETGLOBAL {
				continue
			}
			name := p.Constants[int(inst&0x3ffff)].String()
			if op == lua.OP_GETGLOBAL {
				read[name] = true
			} else {
				written[name] = true
			}
		}
		for _, sub := range p.FunctionPrototypes {
			walk(sub)
		}
	}
	walk(proto)
	for name := range read {
		if !allowed[name] {
			t.Errorf("%s reads the global %q, which the runtime does not give it", f.Name, name)
		}
	}
	for name := range written {
		t.Errorf("%s writes the global %q", f.Name, name)
	}

	code := regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(f.Source, "")
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`function Q\.(\w+)`).FindAllStringSubmatch(code, -1) {
		defined[m[1]] = true
	}
	// Q.A = ..., and the list form Q.A, Q.B = ... at the start of a statement.
	for _, line := range strings.Split(code, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Q.") {
			continue
		}
		lhs, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		for _, m := range regexp.MustCompile(`Q\.(\w+)`).FindAllStringSubmatch(lhs, -1) {
			defined[m[1]] = true
		}
	}
	for _, m := range regexp.MustCompile(`\bQ\.(\w+)`).FindAllStringSubmatch(code, -1) {
		if !defined[m[1]] {
			t.Errorf("%s uses Q.%s, which it never defines", f.Name, m[1])
		}
	}
}
