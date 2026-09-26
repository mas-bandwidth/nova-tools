package fn

import (
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// depRule loads lua/01_dep.lua alone into a Lua 5.1 state with NS as the
// prelude hands it (loader.go), and returns NS.dep.
func depRule(t *testing.T) (*lua.LState, *lua.LTable) {
	t.Helper()
	src, err := sources.ReadFile("lua/01_dep.lua")
	if err != nil {
		t.Fatal(err)
	}
	L := lua.NewState()
	t.Cleanup(L.Close)
	L.SetGlobal("NS", L.NewTable())
	if err := L.DoString(string(src)); err != nil {
		t.Fatalf("load 01_dep.lua: %v", err)
	}
	dep, ok := L.GetField(L.GetGlobal("NS"), "dep").(*lua.LTable)
	if !ok {
		t.Fatal("01_dep.lua exports no NS.dep")
	}
	return L, dep
}

func depCall(t *testing.T, L *lua.LState, dep *lua.LTable, fn string, args ...string) lua.LValue {
	t.Helper()
	vals := make([]lua.LValue, len(args))
	for i, a := range args {
		vals[i] = lua.LString(a)
	}
	if err := L.CallByParam(lua.P{Fn: L.GetField(dep, fn), NRet: 1, Protect: true}, vals...); err != nil {
		t.Fatalf("NS.dep.%s: %v", fn, err)
	}
	v := L.Get(-1)
	L.Pop(1)
	return v
}

// TestDepRuleOneTable is the one dependency rule's class test: NS.dep.met_of
// (lua/01_dep.lua, which the task queue's DEP.holds, the release on a move
// and the dealer's refusal call) and ws.DepMet (every Go reader: the waiting
// resolver, ws show, card push and release, the dealer, ready --why) give
// the same verdict on every row. A sentinel is met by its landing alone,
// never by a done by hand; an ordinary card landed is met, a task closed
// (done/ok) is met, a card done/fail is not, and a missing record is not.
func TestDepRuleOneTable(t *testing.T) {
	t.Parallel()
	L, dep := depRule(t)
	for _, tc := range []struct {
		name, id, state, where, ok string
		met                        bool
	}{
		{"sentinel landed", "alpha:sentinel", "landed", "landed", "ok", true},
		{"sentinel waiting", "alpha:sentinel", "waiting", "waiting", "-", false},
		{"sentinel done by hand (done/ok, not landed)", "alpha:sentinel", "closed", "done", "ok", false},
		{"sentinel done/fail (a rename)", "alpha:sentinel", "closed", "done", "fail", false},
		{"sentinel parked", "alpha:sentinel", "parked", "parked", "-", false},
		{"sentinel, no record", "nosuch:sentinel", "", "", "", false},
		{"card landed", "A1", "landed", "landed", "ok", true},
		{"task closed (done/ok)", "T1", "closed", "done", "ok", true},
		{"card done/fail", "X1", "closed", "done", "fail", false},
		{"task cancelled", "X2", "cancelled", "done", "fail", false},
		{"card working", "W1", "working", "working", "-", false},
		{"card merging", "M1", "merging", "merging", "-", false},
		{"card review", "R1", "review", "review", "-", false},
		{"legacy closed, no where", "L1", "closed", "", "", true},
		{"legacy landed, no where", "L2", "landed", "", "", true},
		{"legacy open, no where", "L3", "open", "", "", false},
		{"no record", "ghost", "", "", "", false},
	} {
		lv := depCall(t, L, dep, "met_of", tc.id, tc.state, tc.where, tc.ok) == lua.LTrue
		gv := ws.DepMet(tc.id, tc.state, tc.where, tc.ok)
		if lv != tc.met || gv != tc.met {
			t.Errorf("%s: lua %v go %v, want %v", tc.name, lv, gv, tc.met)
		}
	}
}

// TestDepIDsNamesTaskEdges: the ids a DEPENDS-ON value names, in both
// stores' spellings (a card's blocked_on, a queue task's depends_on).
func TestDepIDsNamesTaskEdges(t *testing.T) {
	t.Parallel()
	L, dep := depRule(t)
	got := depCall(t, L, dep, "ids", "task:A, beta:sentinel;o/r#12 key:k=v spec:9 B none task:alpha:sentinel")
	tbl, ok := got.(*lua.LTable)
	if !ok {
		t.Fatalf("ids returned %v", got)
	}
	var ids []string
	tbl.ForEach(func(_, v lua.LValue) { ids = append(ids, v.String()) })
	if strings.Join(ids, " ") != "A beta:sentinel B alpha:sentinel" {
		t.Fatalf("ids %v", ids)
	}
}

// TestDepClassOneTable is the class table's test: NS.dep.class_of
// (lua/01_dep.lua: task take's needs, task push's waits and its DEAD
// refusal, the dealer's DEPENDS line) and ws.DepClass (ready --why, the
// dealer's gate, the waiting resolver, ws show) give the same class and
// detail on every row, and the class agrees with the one rule: met exactly
// when NS.dep.met_of says met.
func TestDepClassOneTable(t *testing.T) {
	t.Parallel()
	L, dep := depRule(t)
	for _, tc := range []struct {
		name, id, state, where, ok string
		class, detail              string
	}{
		{"card landed", "A1", "landed", "landed", "ok", "met", "landed"},
		{"task closed (done/ok)", "T1", "closed", "done", "ok", "met", "done/ok"},
		{"legacy closed, no where", "L1", "closed", "", "", "met", "done"},
		{"card working", "W1", "working", "working", "-", "waiting", "working"},
		{"card waiting", "W2", "waiting", "waiting", "-", "waiting", ""},
		{"card ready", "W3", "open", "ready", "-", "waiting", "ready"},
		{"card review", "R1", "review", "review", "-", "waiting", "review"},
		{"legacy open, no where", "L3", "open", "", "", "waiting", "ready"},
		{"card parked", "P1", "parked", "parked", "-", "parked", ""},
		{"card done/fail (cancelled)", "X1", "closed", "done", "fail", "dead", "done/fail"},
		{"task cancelled", "X2", "cancelled", "done", "fail", "dead", "done/fail"},
		{"legacy cancelled, no where", "X3", "cancelled", "", "", "dead", "done/fail"},
		{"no record", "ghost", "", "", "", "unknown", ""},
		{"sentinel landed", "alpha:sentinel", "landed", "landed", "ok", "met", "landed"},
		{"sentinel waiting", "alpha:sentinel", "waiting", "waiting", "-", "waiting", ""},
		{"sentinel parked", "alpha:sentinel", "parked", "parked", "-", "parked", ""},
		{"sentinel done by hand (done/ok)", "alpha:sentinel", "closed", "done", "ok", "dead", "done/ok"},
		{"sentinel done/fail (a rename)", "alpha:sentinel", "closed", "done", "fail", "dead", "done/fail"},
		{"sentinel, no record", "nosuch:sentinel", "", "", "", "unknown", ""},
	} {
		fn := L.GetField(dep, "class_of")
		if err := L.CallByParam(lua.P{Fn: fn, NRet: 2, Protect: true},
			lua.LString(tc.id), lua.LString(tc.state), lua.LString(tc.where), lua.LString(tc.ok)); err != nil {
			t.Fatalf("NS.dep.class_of: %v", err)
		}
		lc, ld := L.Get(-2).String(), L.Get(-1).String()
		L.Pop(2)
		gc, gd := ws.DepClass(tc.id, tc.state, tc.where, tc.ok)
		if lc != tc.class || ld != tc.detail || gc != tc.class || gd != tc.detail {
			t.Errorf("%s: lua %s %q go %s %q, want %s %q", tc.name, lc, ld, gc, gd, tc.class, tc.detail)
		}
		met := depCall(t, L, dep, "met_of", tc.id, tc.state, tc.where, tc.ok) == lua.LTrue
		if met != (tc.class == ws.DepClassMet) || ws.DepMet(tc.id, tc.state, tc.where, tc.ok) != met {
			t.Errorf("%s: class %s but met_of %v", tc.name, tc.class, met)
		}
		if lt, gt := depCall(t, L, dep, "text", lc, ld).String(), ws.DepText(gc, gd); lt != gt {
			t.Errorf("%s: text lua %q go %q", tc.name, lt, gt)
		}
	}
}
