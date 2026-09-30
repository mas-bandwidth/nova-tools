package fn

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

// intentsLuaFile is the derive phase's Lua (the upper design, 1.3.3; item
// IT14). It is not one of sprintFragments: IT12's list of the profile's files
// is IT12's to extend, and its tests hold it to two files. This test holds
// this file to what a sprint file is held to, so that it is ready to be added
// the day G0 opens.
const intentsLuaFile = "lua/sprint_intents.lua"

func intentsSource(t *testing.T) string {
	t.Helper()
	b, err := sources.ReadFile(intentsLuaFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSprintIntentsLuaParses: the derive phase's file is Lua in Redis's dialect
// (5.1, as gopher-lua parses and compiles it), opens with the NS.tset_profile
// guard, so the legacy library, which globs lua/*.lua, holds it inert, and
// reads and writes no global Redis refuses: its free names are the Redis
// Function environment and NS. No store runs it before G0; this is the check
// it gets tonight (and the crossfile test of this package, red at its base for
// the tset files, cannot be relied on to say so).
func TestSprintIntentsLuaParses(t *testing.T) {
	t.Parallel()
	src := intentsSource(t)
	chunk, err := parse.Parse(strings.NewReader(src), intentsLuaFile)
	if err != nil {
		t.Fatalf("parse %s: %v", intentsLuaFile, err)
	}
	if _, err := lua.Compile(chunk, intentsLuaFile); err != nil {
		t.Fatalf("compile %s: %v", intentsLuaFile, err)
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
		t.Errorf("%s first statement is %q, want the tset profile guard", intentsLuaFile, first)
	}
	reads, writes := freeNames(t, intentsLuaFile, src)
	for _, n := range reads {
		if !allowedGlobals[n] {
			t.Errorf("%s reads free name %q", intentsLuaFile, n)
		}
	}
	if len(writes) != 0 {
		t.Errorf("%s assigns globals %v; Redis Functions refuse global writes", intentsLuaFile, writes)
	}
	// The NS fields it reads are the registry the core builds (SP), the profile
	// guard, and Layer 1's S, which it resolves when a call runs as the core does.
	code := luaComment.ReplaceAllString(src, "")
	var fields []string
	seen := map[string]bool{}
	for _, m := range nsRead.FindAllStringSubmatch(code, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			fields = append(fields, m[1])
		}
	}
	sort.Strings(fields)
	if got := strings.Join(fields, ","); got != "SP,tset,tset_profile" {
		t.Errorf("%s reads NS fields %s; want SP (the core's), tset_profile and tset (at call time)", intentsLuaFile, got)
	}
	if nsWrite.MatchString(code) {
		t.Errorf("%s assigns an NS field; the registry is the core's", intentsLuaFile)
	}
}

// TestSprintIntentsLuaRegistersOnlyDerive: the file registers exactly the
// phase 'derive' through the core's registry, loads between the core and the
// function file (filename order), and registers no Redis function and no part.
func TestSprintIntentsLuaRegistersOnlyDerive(t *testing.T) {
	t.Parallel()
	src := luaComment.ReplaceAllString(intentsSource(t), "")
	regs := regexp.MustCompile(`SP\.(phase|part|query)\(([^,)]*)`).FindAllStringSubmatch(src, -1)
	if len(regs) != 1 || regs[0][1] != "phase" || regs[0][2] != "'derive'" {
		t.Errorf("the file registers %v; want the phase 'derive' and nothing else", regs)
	}
	if strings.Contains(src, "register_function") {
		t.Error("the file registers a function; only sprint_zz_fn.lua does")
	}
	names := []string{"lua/sprint_00_core.lua", intentsLuaFile, "lua/sprint_zz_fn.lua"}
	if !sort.StringsAreSorted(names) {
		t.Errorf("the file does not load after the core and before the function file: %v", names)
	}
	for _, n := range []string{"SP.derive = derive", "SP.intent_commands = intent_commands"} {
		if !strings.Contains(src, n) {
			t.Errorf("the file does not export %q", n)
		}
	}
}
