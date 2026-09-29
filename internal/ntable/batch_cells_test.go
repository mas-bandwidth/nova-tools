package ntable

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// batchWorld is the store a batch apply sees, in one process. Cell scores are
// ZSCORE commands on an owned cell key (table:<t>:cell:...), which is the stray
// walk. It is not a Redis server.
type batchWorld struct {
	rows, cols int
	member     bool
	strayKey   string
	cellScores int
	zrange     int
	zcard      int
}

func (w *batchWorld) reset() {
	w.cellScores = 0
	w.zrange = 0
	w.zcard = 0
}

func (w *batchWorld) rowsKey() string { return "table:demo:rows" }

func (w *batchWorld) cellKey(row, col int) string {
	return fmt.Sprintf("table:demo:cell:r%d:c%d", row, col)
}

func (w *batchWorld) definition() map[string]string {
	names := make([]string, w.cols)
	h := map[string]string{}
	for i := 0; i < w.cols; i++ {
		names[i] = fmt.Sprintf("c%d", i)
		h["col:"+names[i]] = "count:none:8:"
	}
	h["order"] = strings.Join(names, ",")
	return h
}

// scriptTable loads table.lua and keeps the functions it registers.
type scriptTable struct {
	L     *lua.LState
	funcs map[string]*lua.LFunction
	world *batchWorld
}

func loadTableScript(t testing.TB, w *batchWorld) *scriptTable {
	t.Helper()
	src, err := os.ReadFile("../nsprint/fn/lua/table.lua")
	if err != nil {
		t.Fatal(err)
	}
	L := lua.NewState()
	t.Cleanup(L.Close)
	st := &scriptTable{L: L, funcs: map[string]*lua.LFunction{}, world: w}
	redis := L.NewTable()
	cmd := L.NewFunction(st.redisCommand)
	L.SetField(redis, "call", cmd)
	L.SetField(redis, "pcall", cmd)
	L.SetField(redis, "sha1hex", L.NewFunction(func(L *lua.LState) int {
		sum := sha1.Sum([]byte(L.CheckString(1)))
		L.Push(lua.LString(hex.EncodeToString(sum[:])))
		return 1
	}))
	L.SetField(redis, "acl_check_cmd", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LTrue)
		return 1
	}))
	L.SetField(redis, "register_function", L.NewFunction(st.register))
	L.SetGlobal("redis", redis)

	cjson := L.NewTable()
	L.SetField(cjson, "decode", L.NewFunction(st.decodeJSON))
	null := L.NewUserData()
	L.SetField(cjson, "null", null)
	L.SetGlobal("cjson", cjson)

	if err := L.DoString(string(src)); err != nil {
		t.Fatalf("load table.lua: %v", err)
	}
	if st.funcs["ns_table_apply"] == nil {
		t.Fatal("table.lua did not register ns_table_apply")
	}
	return st
}

func (st *scriptTable) register(L *lua.LState) int {
	var name string
	var fn *lua.LFunction
	if L.Get(1).Type() == lua.LTString {
		name = L.CheckString(1)
		fn = L.CheckFunction(2)
	} else {
		spec := L.CheckTable(1)
		name = spec.RawGetString("function_name").String()
		cb := spec.RawGetString("callback")
		f, ok := cb.(*lua.LFunction)
		if !ok {
			L.RaiseError("register_function %s: callback is %s", name, cb.Type().String())
			return 0
		}
		fn = f
	}
	st.funcs[name] = fn
	return 0
}

func (st *scriptTable) decodeJSON(L *lua.LState) int {
	var v any
	if err := json.Unmarshal([]byte(L.CheckString(1)), &v); err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	L.Push(toLua(L, v))
	return 1
}

func toLua(L *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case string:
		return lua.LString(x)
	case []any:
		t := L.NewTable()
		for i, e := range x {
			t.RawSetInt(i+1, toLua(L, e))
		}
		return t
	case map[string]any:
		t := L.NewTable()
		for k, e := range x {
			t.RawSetString(k, toLua(L, e))
		}
		return t
	default:
		L.RaiseError("json %T", v)
		return lua.LNil
	}
}

func luaFlat(L *lua.LState, kv map[string]string) *lua.LTable {
	t := L.NewTable()
	i := 1
	for k, v := range kv {
		t.RawSetInt(i, lua.LString(k))
		t.RawSetInt(i+1, lua.LString(v))
		i += 2
	}
	return t
}

func (st *scriptTable) redisCommand(L *lua.LState) int {
	cmd := strings.ToUpper(L.CheckString(1))
	key := ""
	if L.GetTop() >= 2 && L.Get(2).Type() == lua.LTString {
		key = L.CheckString(2)
	}
	w := st.world
	switch cmd {
	case "HGETALL":
		if key == "table:demo" {
			L.Push(luaFlat(L, w.definition()))
			return 1
		}
		L.Push(L.NewTable())
		return 1
	case "HGET":
		L.Push(lua.LFalse)
		return 1
	case "HLEN":
		n := 0
		if w.member && key == "table::member:m" {
			n = 1
		}
		L.Push(lua.LNumber(n))
		return 1
	case "HMGET":
		t := L.NewTable()
		if w.member && key == "table::member:m" {
			t.RawSetInt(1, lua.LString("0"))
			t.RawSetInt(2, lua.LString("1"))
		}
		L.Push(t)
		return 1
	case "HSTRLEN":
		L.Push(lua.LNumber(0))
		return 1
	case "HEXISTS":
		L.Push(lua.LNumber(0))
		return 1
	case "ZCARD":
		w.zcard++
		if key == w.rowsKey() {
			L.Push(lua.LNumber(w.rows))
			return 1
		}
		L.Push(lua.LNumber(0))
		return 1
	case "ZRANGE":
		w.zrange++
		t := L.NewTable()
		if key == w.rowsKey() {
			for i := 0; i < w.rows; i++ {
				t.RawSetInt(i+1, lua.LString(fmt.Sprintf("r%d", i)))
			}
		}
		L.Push(t)
		return 1
	case "ZSCORE":
		if strings.Contains(key, ":cell:") {
			w.cellScores++
			if key == w.strayKey {
				L.Push(lua.LString("1"))
				return 1
			}
		}
		L.Push(lua.LFalse)
		return 1
	case "TYPE":
		t := L.NewTable()
		t.RawSetString("ok", lua.LString("none"))
		L.Push(t)
		return 1
	default:
		L.RaiseError("unexpected redis %s %s", cmd, key)
		return 0
	}
}

func (st *scriptTable) apply(manifest string) ([]lua.LValue, error) {
	st.world.reset()
	L := st.L
	L.SetTop(0)
	L.Push(st.funcs["ns_table_apply"])
	L.Push(L.NewTable())
	args := L.NewTable()
	args.RawSetInt(1, lua.LString("demo"))
	args.RawSetInt(2, lua.LString(manifest))
	L.Push(args)
	if err := L.PCall(2, lua.MultRet, nil); err != nil {
		return nil, err
	}
	var out []lua.LValue
	for i := 1; i <= L.GetTop(); i++ {
		out = append(out, L.Get(i))
	}
	return out, nil
}

func replyFields(v lua.LValue) []string {
	t, ok := v.(*lua.LTable)
	if !ok {
		return []string{v.String()}
	}
	n := t.MaxN()
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, t.RawGetInt(i).String())
	}
	return out
}

func createManifest() string {
	return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"0","operation_id":"op-create","actor":"p","members":[{"id":"m","expect":{"absent":true},"create":{"row":"build","col":"c0","score":1}}]}`
}

func moveManifest() string {
	return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"0","operation_id":"op-move","actor":"p","members":[{"id":"m","expect":{"revision":"1"},"move":{"row":"build","col":"c0"}}]}`
}

func TestBatchCreateDoesNotWalkEveryCellPastTheBound(t *testing.T) {
	t.Parallel()
	// Over the bound: the old code scores every cell. This must not.
	w := &batchWorld{rows: 1000, cols: 1}
	st := loadTableScript(t, w)
	got, err := st.apply(createManifest())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	fields := replyFields(got[0])
	t.Logf("1000x1 create: cells scored %d, zcard %d, zrange %d, reply %v", w.cellScores, w.zcard, w.zrange, fields)
	if w.cellScores != 0 {
		t.Fatalf("scored %d cells, want 0: a create on %d cells walked the table", w.cellScores, 1000)
	}
	if len(fields) < 5 || fields[0] != "REFUSED" || fields[1] != "LIMIT" || fields[2] != limitNameBatchCells {
		t.Fatalf("reply %v, want LIMIT %s", fields, limitNameBatchCells)
	}
	if fields[3] != fmt.Sprint(LimitBatchCells) || fields[4] != "1000" {
		t.Fatalf("bound/observed %v/%v, want %d/1000", fields[3], fields[4], LimitBatchCells)
	}

	// Rows under the bound, cells over it: the bound is cells, not rows.
	w.rows, w.cols = 200, 2
	got, err = st.apply(createManifest())
	if err != nil {
		t.Fatalf("200x2: %v", err)
	}
	fields = replyFields(got[0])
	if w.cellScores != 0 || len(fields) < 5 || fields[1] != "LIMIT" || fields[4] != "400" {
		t.Fatalf("200 rows x 2 cols: scored %d, reply %v, want 0 scores and observed 400", w.cellScores, fields)
	}

	// At the bound the scan still runs: a stray in the last cell is found,
	// which means every cell was scored.
	w.rows, w.cols = LimitBatchCells, 1
	w.strayKey = w.cellKey(LimitBatchCells-1, 0)
	got, err = st.apply(createManifest())
	if err != nil {
		t.Fatalf("at bound: %v", err)
	}
	fields = replyFields(got[0])
	if w.cellScores != LimitBatchCells {
		t.Fatalf("at the bound scored %d cells, want %d (the scan stays inside the bound)", w.cellScores, LimitBatchCells)
	}
	if len(fields) < 2 || fields[1] == "LIMIT" {
		t.Fatalf("at the bound refused the scan: %v", fields)
	}
	if fields[1] != "DRIFT" {
		t.Fatalf("at the bound reply %v, want DRIFT from the stray in the last cell", fields)
	}

	// 10,000 rows is a count, not a store: the refusal does not build the cells.
	w.rows, w.cols = 10000, 1
	w.strayKey = ""
	got, err = st.apply(createManifest())
	if err != nil {
		t.Fatalf("10000: %v", err)
	}
	fields = replyFields(got[0])
	if w.cellScores != 0 || w.zrange != 0 || len(fields) < 5 || fields[4] != "10000" {
		t.Fatalf("10000 rows: scored %d, zrange %d, reply %v", w.cellScores, w.zrange, fields)
	}
}

func TestUnplacedMoveDoesNotWalkEveryCellPastTheBound(t *testing.T) {
	t.Parallel()
	w := &batchWorld{rows: 1000, cols: 1, member: true}
	st := loadTableScript(t, w)
	got, err := st.apply(moveManifest())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	fields := replyFields(got[0])
	t.Logf("1000x1 unplaced move: cells scored %d, zcard %d, zrange %d, reply %v", w.cellScores, w.zcard, w.zrange, fields)
	if w.cellScores != 0 {
		t.Fatalf("scored %d cells, want 0", w.cellScores)
	}
	if len(fields) < 5 || fields[1] != "LIMIT" || fields[2] != limitNameBatchCells || fields[4] != "1000" {
		t.Fatalf("reply %v, want LIMIT observed 1000", fields)
	}

	// Inside the bound the scan still runs, finds nothing, and the move is
	// refused because the record has no place.
	w.rows = 2
	got, err = st.apply(moveManifest())
	if err != nil {
		t.Fatalf("2 rows: %v", err)
	}
	fields = replyFields(got[0])
	if w.cellScores != 2 {
		t.Fatalf("2 cells scored %d, want 2", w.cellScores)
	}
	if len(fields) < 2 || fields[1] != "NOTMEMBER" {
		t.Fatalf("inside the bound reply %v, want NOTMEMBER", fields)
	}
}

func BenchmarkBatchCreate1000Rows(b *testing.B) {
	w := &batchWorld{rows: 1000, cols: 1}
	st := loadTableScript(b, w)
	manifest := createManifest()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := st.apply(manifest)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("no reply")
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(w.cellScores), "cell-scores")
}
