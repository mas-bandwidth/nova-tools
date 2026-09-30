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
	for _, n := range []string{"SP.derive = derive", "SP.intent_commands = intent_commands",
		"SP.intent_commands_pending = intent_commands_pending"} {
		if !strings.Contains(src, n) {
			t.Errorf("the file does not export %q", n)
		}
	}
}

// TestSprintLuaNeedmetNeedgoneNeedNoCard: 8.0 gives a needmet and a needgone a
// need and its waiters and no card (the wire sends "card":""), and the core's
// before asks S.before for the need and the waiters, never for the empty card,
// which Layer 1 refuses REQUEST (S.name(”) is false). The S.before here refuses
// an empty id as Layer 1's does.
func TestSprintLuaNeedmetNeedgoneNeedNoCard(t *testing.T) {
	t.Parallel()
	h := newLuaSprint(t)
	h.setup(nil)
	h.do(`BEFORE_IDS = {}
function NS.tset.before(ctx, t, ids, fields)
  trace('before')
  for _, id in ipairs(ids) do
    if id == '' then return nil, NS.tset.refuse('REQUEST') end
    BEFORE_IDS[#BEFORE_IDS + 1] = t .. ':' .. id
  end
  return {}, nil
end`)
	reply := h.step(`{"meta":{"verb":"tick"},"intents":[` +
		`{"kind":"needmet","card":"","need":"n","waiters":["w1","w2"]},` +
		`{"kind":"needgone","card":"","need":"g","waiters":["x"]}]}`)
	if code := refusalCode(reply); code != "" {
		t.Fatalf("a needmet and a needgone with no card were refused %s: %s", code, reply)
	}
	h.do("BEFORE_LIST = table.concat(BEFORE_IDS, ',')")
	got := h.L.GetGlobal("BEFORE_LIST").String()
	for _, want := range []string{"work:n", "work:w1", "work:w2", "work:g", "work:x"} {
		if !strings.Contains(","+got+",", ","+want+",") {
			t.Errorf("S.before was not asked for %s; asked %s", want, got)
		}
	}
	if strings.Contains(","+got+",", ",work:,") {
		t.Errorf("S.before was asked for the empty card: %s", got)
	}
}

// TestSprintLuaRefusesCommandsLeftBehind: the commands the derive phase stages
// on the call's ctx are for x_cmds to append (NS.SP.intent_commands). If
// x_cmds leaves them, the core refuses CONFIG before prepare and nothing is
// committed: dropping them would lower open with the card still in wait:<n>
// (I2). Commands that were taken, and a call with none, go on to prepare and
// commit. The functions are the real ones of sprint_intents.lua, loaded after
// the core; the stand-ins stage the commands as derive does.
func TestSprintLuaRefusesCommandsLeftBehind(t *testing.T) {
	t.Parallel()
	staged := func(taken string) string {
		return `T.ctx = function() return {request = {entries = {}}, notes = {},
  intent_cmds = {{argv = {'ZREM', 'k', 'm'}}}, intent_cmds_taken = ` + taken + `} end`
	}
	run := func(t *testing.T, ctx string, x string) (*luaSprint, string) {
		h := newLuaSprint(t)
		h.setup([]string{"derive", "x_cmds"})
		h.do(intentsSource(t))
		h.do(ctx)
		h.do(x)
		return h, h.step(`{"meta":{"verb":"tick"}}`)
	}
	leaves := `NS.SP.phase('x_cmds', function(ctx, tp, lp) trace('x_cmds'); return {commands = {}} end)`
	takes := `NS.SP.phase('x_cmds', function(ctx, tp, lp) trace('x_cmds'); return {commands = NS.SP.intent_commands(ctx)} end)`

	t.Run("commands left behind are refused before prepare", func(t *testing.T) {
		t.Parallel()
		h, reply := run(t, staged("false"), leaves)
		if refusalCode(reply) != "CONFIG" {
			t.Fatalf("reply %s; want CONFIG", reply)
		}
		if contains(h.trace, "prepare") || contains(h.trace, "commit") {
			t.Fatalf("the refused step went on: %v", h.trace)
		}
	})
	t.Run("commands x_cmds took go on", func(t *testing.T) {
		t.Parallel()
		h, reply := run(t, staged("false"), takes)
		if refusalCode(reply) != "" || !contains(h.trace, "commit") {
			t.Fatalf("reply %s, trace %v; want the step committed", reply, h.trace)
		}
	})
	t.Run("a call with no commands goes on", func(t *testing.T) {
		t.Parallel()
		h, reply := run(t, `T.ctx = function() return {request = {entries = {}}, notes = {}} end`, leaves)
		if refusalCode(reply) != "" || !contains(h.trace, "commit") {
			t.Fatalf("reply %s, trace %v; want the step committed", reply, h.trace)
		}
	})
}
