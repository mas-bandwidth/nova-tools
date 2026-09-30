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

// partsLuaNames is the six parts of 1.0, as the file registers them.
var partsLuaRegister = regexp.MustCompile(`SP\.part\('([a-z]+)'`)

// TestSprintPartsLuaParses: the parts file is Lua in Redis's dialect (5.1, as
// gopher-lua parses and compiles it), opens with the NS.tset_profile guard so
// the legacy library, which globs lua/*.lua, holds it inert, and registers the
// six parts of 1.0 and no other, each once. No store loads it before G0; this
// is the check it gets here, and sprintfn's differential test runs it.
func TestSprintPartsLuaParses(t *testing.T) {
	t.Parallel()
	f, err := SprintPartsFragment()
	if err != nil {
		t.Fatal(err)
	}
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
	var registered []string
	for _, m := range partsLuaRegister.FindAllStringSubmatch(f.Source, -1) {
		registered = append(registered, m[1])
	}
	sort.Strings(registered)
	want := append([]string(nil), sprintfn.PartOrder...)
	sort.Strings(want)
	if !reflect.DeepEqual(registered, want) {
		t.Fatalf("the Lua registers parts %v, 1.0 has %v", registered, want)
	}
	if strings.Contains(f.Source, "register_function") {
		t.Fatal("the parts file registers a function; only sprint_zz_fn.lua does")
	}
}

// TestSprintPartsLuaSortsBetweenCoreAndFn: the parts file loads after the core,
// whose NS.SP.part it calls, and before the function file, which reads
// NS.SP.parts at call time; the legacy library's glob orders files by name.
func TestSprintPartsLuaSortsBetweenCoreAndFn(t *testing.T) {
	t.Parallel()
	names := []string{"lua/sprint_zz_fn.lua", sprintPartsFile, "lua/sprint_00_core.lua"}
	sort.Strings(names)
	if names[0] != "lua/sprint_00_core.lua" || names[1] != sprintPartsFile || names[2] != "lua/sprint_zz_fn.lua" {
		t.Fatalf("load order %v", names)
	}
	src, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	core, parts, fns := strings.Index(src, "\n-- lua/sprint_00_core.lua\n"), strings.Index(src, "\n-- "+sprintPartsFile+"\n"),
		strings.Index(src, "\n-- lua/sprint_zz_fn.lua\n")
	if core < 0 || parts < core || fns < parts {
		t.Fatalf("the assembled library holds the files at %d, %d, %d; want core, parts, fn in that order", core, parts, fns)
	}
}

// TestSprintPartsLuaTouchesOnlyWhatIsAllowed: the file reads no Lua global that
// the Redis Function environment lacks and writes none (the rule of
// TestNoBareCrossFileReferences, held here for this file alone since that test
// is red on the base for another file's reads).
func TestSprintPartsLuaTouchesOnlyWhatIsAllowed(t *testing.T) {
	t.Parallel()
	f, err := SprintPartsFragment()
	if err != nil {
		t.Fatal(err)
	}
	reads, writes := freeNames(t, f.Name, f.Source)
	for _, n := range reads {
		if !allowedGlobals[n] {
			t.Errorf("%s reads free name %q", f.Name, n)
		}
	}
	if len(writes) != 0 {
		t.Errorf("%s assigns globals %v", f.Name, writes)
	}
}

// TestSprintStaticPhaseRefusesALostQuarantine: the core's static phase, which
// runs before TIME or any store access, refuses REQUEST a body that quarantines
// a card the sprint part does not carry: a part runs only when its field is
// set, so the records would be lost while X acted on the card. A body whose
// cards the part carries, and a step with no quarantine, go on to open.
func TestSprintStaticPhaseRefusesALostQuarantine(t *testing.T) {
	t.Parallel()
	h := newLuaSprint(t)
	parts, err := SprintPartsFragment()
	if err != nil {
		t.Fatal(err)
	}
	h.do(parts.Source)
	h.setup(nil) // the phases, and no part of the test's: the real parts are loaded
	// The test is of the static phase, not of the sprint part, which sprintfn's
	// differential runs: the loaded part is replaced by one that does nothing.
	h.do(`NS.SP.parts.sprint = {pre = function() return {part = 'sprint'}, nil end, cmds = function() return {}, nil end}`)
	for _, c := range []struct {
		name, sprint string
		refused      bool
	}{
		{"none", `{"meta":{}}`, false},
		{"the body alone", `{"meta":{},"quarantine":[{"id":"p1","code":"DRIFT"}]}`, true},
		{"the body and an empty part", `{"meta":{},"quarantine":[{"id":"p1","code":"DRIFT"}],"sprint":{}}`, true},
		{"another card in the part", `{"meta":{},"quarantine":[{"id":"p1","code":"DRIFT"}],"sprint":{"quarantine":[{"id":"p2","code":"DRIFT"}]}}`, true},
		{"the same card in the part", `{"meta":{},"quarantine":[{"id":"p1","code":"DRIFT"}],"sprint":{"quarantine":[{"id":"p1","code":"DRIFT"}]}}`, false},
		// A hand-made request, which the Go client cannot send: the static phase
		// refuses it REQUEST and does not raise a script error (the call would
		// fail the test), since it runs before any validation of the sprint half.
		{"a card of the part with no id", `{"meta":{},"quarantine":[{"id":"p1","code":"DRIFT"}],"sprint":{"quarantine":[{"code":"DRIFT"}]}}`, true},
		{"a card of the part whose id is a number", `{"meta":{},"quarantine":[{"id":"p1","code":"DRIFT"}],"sprint":{"quarantine":[{"id":7,"code":"DRIFT"}]}}`, true},
		{"a card of the body with no id", `{"meta":{},"quarantine":[{"code":"DRIFT"}],"sprint":{"quarantine":[{"id":"p1","code":"DRIFT"}]}}`, true},
		{"the part's list is an object", `{"meta":{},"quarantine":[{"id":"p1","code":"DRIFT"}],"sprint":{"quarantine":{"p1":{"id":"p1"}}}}`, true},
		{"the body's list is a string", `{"meta":{},"quarantine":"p1","sprint":{"quarantine":[{"id":"p1","code":"DRIFT"}]}}`, true},
	} {
		reply := h.step(c.sprint)
		if got := refusalCode(reply) == "REQUEST"; got != c.refused {
			t.Errorf("%s: refused %v, want %v (reply %s)", c.name, got, c.refused, reply)
		}
		if c.refused && contains(h.trace, "open") {
			t.Errorf("%s: the refusal came after open; the static phase runs before any store access", c.name)
		}
		if !c.refused && !contains(h.trace, "open") {
			t.Errorf("%s: the step did not reach open (trace %v)", c.name, h.trace)
		}
	}
}
