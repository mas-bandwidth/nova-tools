package sprintfn

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// IT19's two additions to the write path's reads and guards: the sprint-key
// read of {p}next@e (KeyNext) and X's set guard (XGuardSet, S.zguard over a
// stream's index), each held equal in Go and in the Lua half of X.

// withZGuard gives the mirror of X's Lua what the store's Lua has and the
// mirror did not need before the set guard: cjson.decode, Layer 1's S.bound
// and S.zguard (a ZCOUNT over the call's keys, RANGECOUNT past its bounds).
func withZGuard(h *xh) {
	m := h.mirror
	L := m.L
	cj := L.GetGlobal("cjson").(*lua.LTable)
	cj.RawSetString("decode", L.NewFunction(func(L *lua.LState) int {
		var v any
		if err := json.Unmarshal([]byte(L.CheckString(1)), &v); err != nil {
			L.RaiseError("cjson.decode: %v", err)
		}
		L.Push(m.toLua(v))
		return 1
	}))
	s := L.GetGlobal("NS").(*lua.LTable).RawGetString("tset").(*lua.LTable)
	s.RawSetString("bound", L.NewFunction(func(L *lua.LState) int {
		_, _, ok := parseBound(L.CheckString(1))
		L.Push(lua.LBool(ok))
		return 1
	}))
	s.RawSetString("zguard", L.NewFunction(func(L *lua.LState) int {
		key, g := L.CheckString(2), L.CheckTable(3)
		m.probes++
		n := 0
		for _, p := range m.cur.Keys.ks.zpairs(key) {
			if inBounds(p.score, g.RawGetString("min").String(), g.RawGetString("max").String()) {
				n++
			}
		}
		lo, hi := g.RawGetString("atleast"), g.RawGetString("atmost")
		if (lo != lua.LNil && float64(n) < float64(lo.(lua.LNumber))) || (hi != lua.LNil && float64(n) > float64(hi.(lua.LNumber))) {
			L.Push(lua.LNil)
			L.Push(m.refusal("RANGECOUNT", lua.LNil, lua.LNil))
			return 2
		}
		L.Push(lua.LNumber(n))
		L.Push(lua.LNil)
		return 2
	}))
}

// setGuard is a set guard as a plan carries it.
func setGuard(key, min, max string, atLeast, atMost *int) XGuard {
	return sprint.SetGuard{Kind: sprint.GuardZGuard, Key: key, Min: min, Max: max, AtLeast: atLeast, AtMost: atMost}.XGuard()
}

func ip(n int) *int { return &n }

// TestXSetGuard: a set guard over sent:s holds when the count of the
// sentinels within its bounds is within its count bounds (the add's ready
// guard, 1.5.4: S.zguard(sent:s, rcount, -inf, max, atmost 0)), and refuses
// RANGECOUNT otherwise; a set guard that is not a zguard over a stream's index
// with bounds and a count is REQUEST. The Lua half of X agrees on every one.
func TestXSetGuard(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	withZGuard(h)
	h.fixture() // g1, a sentinel of s1 at score 4
	h.write(xLease("1"), xRunning(300))
	guard := func(g ...XGuard) *Request {
		r := xTick(1)
		r.Body.Guards = g
		return r
	}
	h.applies("no sentinel at or below 3", guard(setGuard("sent:s1", "-inf", "3", nil, ip(0))))
	h.applies("one sentinel in [4, 4]", guard(setGuard("sent:s1", "4", "4", ip(1), ip(1))))
	h.applies("no sentinel of s2", guard(setGuard("sent:s2", "-inf", "+inf", nil, ip(0))))
	for _, g := range []XGuard{setGuard("sent:s1", "-inf", "4", nil, ip(0)), setGuard("sent:s1", "(3", "+inf", ip(2), nil)} {
		ref := h.wantRefusal(guard(g), "RANGECOUNT")
		if !strings.Contains(ref.Message, "RANGECOUNT") {
			t.Fatalf("the refusal %q does not name RANGECOUNT", ref.Message)
		}
	}
	for _, g := range []XGuard{
		setGuard("sent:s1", "-inf", "3", nil, nil),                                                    // no count bound
		setGuard("sent:s1", "-inf", "3", ip(2), ip(1)),                                                // lower above upper
		setGuard("wait:s1", "-inf", "3", nil, ip(0)),                                                  // not a stream's index
		setGuard("sent:s 1", "-inf", "3", nil, ip(0)),                                                 // not a stream's name
		setGuard("sent:s1", "x", "3", nil, ip(0)),                                                     // not a bound
		{Kind: XGuardSet, Key: `{"kind":"rcount","key":"sent:s1","min":"-inf","max":"3","atmost":0}`}, // Layer 1's own entry
		{Kind: XGuardSet, Key: "not json"},
	} {
		h.wantRefusal(guard(g), CodeRequest)
	}
}

// TestKeyNext: the sprint-key read of {p}next@e names its fields and returns
// those the hash holds (1.3.1); a name that is not a field of the counter is
// REQUEST, before any read.
func TestKeyNext(t *testing.T) {
	t.Parallel()
	h := newXHarnessMirrored(t, false)
	h.fixture() // the counter: score 1000, streams 1
	res, _, err := h.tw.KeyQuery(KeyQ{Kind: KeyNext, Names: []string{"score", "streams", "id:s1", "gate:s1"}})
	if err != nil {
		t.Fatal(err)
	}
	f := res.(NextResult).Fields
	if len(f) != 2 || f["score"] != "1000" || f["streams"] != "1" {
		t.Fatalf("the counter read %v, want score 1000 and streams 1 alone", f)
	}
	for _, bad := range []KeyQ{{Kind: KeyNext}, {Kind: KeyNext, Names: []string{"other"}}, {Kind: KeyNext, Names: []string{"id:"}},
		{Kind: KeyNext, Names: []string{"score", "score"}}, {Kind: KeyNext, Names: []string{"score"}, Streams: []string{"s1"}}} {
		if _, _, err := h.tw.KeyQuery(bad); err == nil {
			t.Fatalf("%+v was read, want REQUEST", bad)
		}
	}
	q, ref := EncodeKeyQ(KeyQ{Kind: KeyNext, Names: []string{"score", "gate:s2"}})
	if ref != nil {
		t.Fatal(ref)
	}
	back, ref := DecodeKeyQ(q)
	if ref != nil || back.Kind != KeyNext || strings.Join(back.Names, ",") != "score,gate:s2" {
		t.Fatalf("the wire round trip gave %+v, %v", back, ref)
	}
}

// TestLuaKeyNextEqualsTwin: the Lua read of {p}next@e (sprint_queries.lua)
// answers what the twin does, with the counter absent and present, declares
// the probes Go does, and refuses the malformed reads the twin refuses, with
// the same code.
func TestLuaKeyNextEqualsTwin(t *testing.T) {
	t.Parallel()
	w := standard(t)
	h := newLuaHarness(t, w)
	reads := []KeyQ{{Kind: KeyNext, Names: []string{"score", "streams", "id:s1", "gate:s1"}}, {Kind: KeyNext, Names: []string{}}}
	for i, q := range reads {
		h.agree(fmt.Sprintf("absent #%d", i), mustEncodeKey(t, q))
	}
	w.seed(w.hset("next", "score", "90088", "streams", "3", "id:s1", "30001"))
	for i, q := range reads {
		h.agree(fmt.Sprintf("present #%d", i), mustEncodeKey(t, q))
		if records, ranged, probes := h.declared(mustEncodeKey(t, q)); records != 0 || ranged != 0 || probes != KeyProbes(q) {
			t.Fatalf("%+v: the Lua declares %d, %d, %d; Go %d probes", q, records, ranged, probes, KeyProbes(q))
		}
	}
	for _, raw := range []string{`{"kind":"next","fields":[]}`, `{"kind":"next","fields":[],"names":["other"]}`,
		`{"kind":"next","fields":[],"names":["id:"]}`, `{"kind":"next","fields":[],"names":["score","score"]}`,
		`{"kind":"next","fields":[],"names":["score"],"streams":["s1"]}`, `{"kind":"next","fields":["x"],"names":["score"]}`} {
		q := SprintQuery{Kind: KeyNext, Query: json.RawMessage(raw)}
		lr := h.validate(q)
		gr := checkWire(q)
		if lr == nil || gr == nil || lr.Code != gr.Code {
			t.Fatalf("%s: the Lua refused %+v, the twin %v", raw, lr, gr)
		}
	}
}
