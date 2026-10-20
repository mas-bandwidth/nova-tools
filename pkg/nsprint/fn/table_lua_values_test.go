package fn

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// fakeCJSON is the cjson module the store's Lua environment has, made from
// encoding/json: decode reads one JSON text (objects and arrays as tables,
// null as cjson.null) and appends the text it was given to *decodes; encode
// writes a table with keys 1..n as an array and any other table as an object
// (keys sorted, an empty table as {}), and escapes a string as cjson does
// (quote, backslash, slash, the control bytes; bytes 0x80-0xff as they are).
func fakeCJSON(L *lua.LState, decodes *[]string) *lua.LTable {
	m := L.NewTable()
	null := L.NewUserData()
	L.SetField(m, "null", null)
	L.SetField(m, "decode", L.NewFunction(func(L *lua.LState) int {
		text := L.CheckString(1)
		*decodes = append(*decodes, text)
		if !cjsonSurrogatesOK(text) {
			L.RaiseError("Expected value but found invalid unicode escape code")
			return 0
		}
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			L.RaiseError("Expected value but found invalid token: %v", err)
			return 0
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			L.RaiseError("Expected the end but found trailing text")
			return 0
		}
		L.Push(goToLua(L, v, null))
		return 1
	}))
	L.SetField(m, "encode", L.NewFunction(func(L *lua.LState) int {
		var b strings.Builder
		if err := encodeLua(&b, L.Get(1), null); err != nil {
			L.RaiseError("Cannot serialise: %v", err)
			return 0
		}
		L.Push(lua.LString(b.String()))
		return 1
	}))
	return m
}

// cjsonSurrogatesOK holds text's \u escapes to cjson's rule, which
// encoding/json is looser about: a high surrogate is followed by a \u low
// surrogate, and a low surrogate never comes first.
func cjsonSurrogatesOK(text string) bool {
	hex4 := func(i int) (uint64, bool) {
		if i+4 > len(text) {
			return 0, false
		}
		v, err := strconv.ParseUint(text[i:i+4], 16, 16)
		return v, err == nil
	}
	in := false
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '"':
			in = !in
		case c == '\\' && in:
			if i+1 < len(text) && text[i+1] == 'u' {
				cp, ok := hex4(i + 2)
				if ok && cp >= 0xDC00 && cp <= 0xDFFF {
					return false
				}
				if ok && cp >= 0xD800 && cp <= 0xDBFF {
					low, lok := hex4(i + 8)
					if i+8 > len(text) || text[i+6:i+8] != `\u` || !lok || low < 0xDC00 || low > 0xDFFF {
						return false
					}
					i += 10 // past the pair's first escape and the second's backslash
				}
			}
			i++
		}
	}
	return true
}

func goToLua(L *lua.LState, v any, null lua.LValue) lua.LValue {
	switch v := v.(type) {
	case nil:
		return null
	case bool:
		return lua.LBool(v)
	case string:
		return lua.LString(v)
	case json.Number:
		f, _ := strconv.ParseFloat(string(v), 64)
		return lua.LNumber(f)
	case []any:
		t := L.NewTable()
		for _, e := range v {
			t.Append(goToLua(L, e, null))
		}
		return t
	case map[string]any:
		t := L.NewTable()
		for k, e := range v {
			t.RawSetString(k, goToLua(L, e, null))
		}
		return t
	}
	panic(fmt.Sprintf("fake cjson: no Lua form for %T", v))
}

func encodeLua(b *strings.Builder, v lua.LValue, null lua.LValue) error {
	switch v := v.(type) {
	case lua.LString:
		b.WriteByte('"')
		for i := 0; i < len(v); i++ {
			c := v[i]
			switch {
			case c == '"':
				b.WriteString(`\"`)
			case c == '\\':
				b.WriteString(`\\`)
			case c == '/':
				b.WriteString(`\/`)
			case c == '\b':
				b.WriteString(`\b`)
			case c == '\f':
				b.WriteString(`\f`)
			case c == '\n':
				b.WriteString(`\n`)
			case c == '\r':
				b.WriteString(`\r`)
			case c == '\t':
				b.WriteString(`\t`)
			case c < 0x20 || c == 0x7f:
				fmt.Fprintf(b, `\u%04x`, c)
			default:
				b.WriteByte(c)
			}
		}
		b.WriteByte('"')
	case lua.LNumber:
		f := float64(v)
		if f == math.Trunc(f) && math.Abs(f) < 1e15 {
			b.WriteString(strconv.FormatInt(int64(f), 10))
		} else {
			b.WriteString(strconv.FormatFloat(f, 'g', 14, 64))
		}
	case lua.LBool:
		b.WriteString(strconv.FormatBool(bool(v)))
	case *lua.LNilType:
		b.WriteString("null")
	case *lua.LTable:
		size, keys, array := 0, []string{}, true
		v.ForEach(func(k, _ lua.LValue) {
			size++
			if n, ok := k.(lua.LNumber); !ok || float64(n) != math.Trunc(float64(n)) || n < 1 {
				array = false
			}
			keys = append(keys, k.String())
		})
		if size > 0 && array && v.Len() == size {
			b.WriteByte('[')
			for i := 1; i <= size; i++ {
				if i > 1 {
					b.WriteByte(',')
				}
				if err := encodeLua(b, v.RawGetInt(i), null); err != nil {
					return err
				}
			}
			b.WriteByte(']')
			return nil
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := encodeLua(b, lua.LString(k), null); err != nil {
				return err
			}
			b.WriteByte(':')
			e := v.RawGetString(k)
			if e == lua.LNil {
				n, _ := strconv.ParseFloat(k, 64)
				e = v.RawGet(lua.LNumber(n))
			}
			if err := encodeLua(b, e, null); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		if v == null {
			b.WriteString("null")
			return nil
		}
		return fmt.Errorf("type %s", v.Type())
	}
	return nil
}

// luaReply is a function's reply as the store sends it: a table is its array
// part, a string or a number as it is, false and nil as nil.
func luaReply(v lua.LValue) any {
	switch v := v.(type) {
	case lua.LString:
		return string(v)
	case lua.LNumber:
		return float64(v)
	case *lua.LTable:
		out := []any{}
		for i := 1; i <= v.Len(); i++ {
			out = append(out, luaReply(v.RawGetInt(i)))
		}
		return out
	}
	return nil
}

// fcallReply runs one registered function and returns its whole reply.
func (s *fakeStore) fcallReply(t *testing.T, name string, args ...string) []any {
	t.Helper()
	f, ok := s.funcs[name]
	require.True(t, ok, "%s is not registered", name)
	argv := s.L.NewTable()
	for _, a := range args {
		argv.Append(lua.LString(a))
	}
	require.NoError(t, s.L.CallByParam(lua.P{Fn: f, NRet: 1, Protect: true}, s.L.NewTable(), argv))
	ret := s.L.Get(-1)
	s.L.Pop(1)
	reply, ok := luaReply(ret).([]any)
	require.True(t, ok, "%s returned %v, not a table", name, ret)
	return reply
}

// event is the newest entry of a stream as field -> value.
func (s *fakeStore) event(t *testing.T, stream string) map[string]string {
	t.Helper()
	entries := s.streams[stream]
	require.NotEmpty(t, entries, "no event on %s", stream)
	last := entries[len(entries)-1]
	out := map[string]string{}
	for i := 1; i+1 < len(last); i += 2 {
		out[last[i]] = last[i+1]
	}
	return out
}

// jsonText is v as compact JSON text with no HTML escaping, the form a
// writer sends.
func jsonText(t *testing.T, v any) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(v))
	return strings.TrimSuffix(b.String(), "\n")
}

// cjsonCall is one call of cjson.decode or cjson.encode in a Lua source line.
var cjsonCall = regexp.MustCompile(`cjson\.(decode|encode)\b`)

// TestTableLuaRowValuesOpaque: a row value is opaque text to table.lua
// (schema-table adoption, Aspect 8: nova-tables is kept, only the encoding of
// its row values changes, so the Redis function must carry any encoding
// unchanged). A value in each form (padded standard base64 of a fixed image,
// one of them holding every byte 0-255, a schema JSON text form, and today's
// legacy JSON) passes byte for byte through every verb that stores or moves a
// value: a row's text cell, a batch's fields set and unset, the event's cells
// and members, the receipt, the operation record and its replay, and the
// FIELDGUARD equals and one_of compares, whose refusal names the option count
// and never re-encodes the options. No cjson.decode is ever given a value's
// own string token, and every cjson call left in table.lua says in the
// comment above it which of the envelope, the event or the operation record
// it reads or writes.
func TestTableLuaRowValuesOpaque(t *testing.T) {
	t.Parallel()

	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	values := map[string]string{
		// a fixed image whose bytes hold every value: 344 bytes of base64, padded
		"img_all": base64.StdEncoding.EncodeToString(all),
		// a short fixed image, recorded in full by the receipt, padded
		"img_small": base64.StdEncoding.EncodeToString([]byte{0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff, '"', '\\', '/', '\n'}),
		// a schema JSON text form: a slash, an escaped quote, a \u escape
		"text": `{"form":"text","path":"a\/b","q":"\"x\"","e":"é","n":3}`,
		// today's legacy JSON: unsorted keys, spaces, 1.50, null
		"legacy": `{"z": 1.50, "a": [ ], "id":"m1", "nil":null}`,
	}
	require.True(t, strings.HasSuffix(values["img_all"], "=") && strings.HasSuffix(values["img_small"], "="), "the images are padded base64")
	for _, f := range []string{"img_small", "text", "legacy"} {
		require.LessOrEqual(t, len(values[f]), 64, "%s is recorded in full by a receipt", f)
	}
	require.Greater(t, len(values["img_all"]), 64, "img_all is recorded by length and sha1")
	sha := func(v string) string { sum := sha1.Sum([]byte(v)); return hex.EncodeToString(sum[:]) }
	isValue := map[string]bool{}
	for _, v := range values {
		isValue[v] = true
	}

	s := newFakeStoreOf(t, "lua/table.lua")
	const name, member = "demo", "table::member:m1"
	opts := `{"epoch":"0","actor":"t"}`
	ok := func(reply []any, what string) {
		t.Helper()
		require.Equal(t, "OK", reply[0], "%s: %v", what, reply)
	}
	ok(s.fcallReply(t, "ns_table_create", name, jsonText(t, map[string]string{
		"order": "todo,done,note", "col:todo": "members:none:20:", "col:done": "members:none:20:", "col:note": "text:none:40:",
	}), opts), "create")
	require.Equal(t, "ROW", s.fcallReply(t, "ns_table_row_add", name, "r1", `{"label":"R1"}`, opts)[0], "row add")

	// A row's text cell: stored, and carried by the event's args, as it came.
	for _, f := range []string{"img_all", "img_small", "text", "legacy"} {
		cell := jsonText(t, map[string]string{"note": values[f]})
		ok(s.fcallReply(t, "ns_table_row_set", name, "r1", cell, opts), "row set "+f)
		require.Equal(t, values[f], s.hashes["table:demo:row:r1"]["text:note"], "row set %s: the text cell", f)
		var args []string
		require.NoError(t, json.Unmarshal([]byte(s.event(t, "table:demo:changes")["args"]), &args))
		require.Equal(t, []string{name, "r1", cell}, args, "row set %s: the event's args", f)
	}

	revision := func() string { return s.hashes["table:demo:revision"]["n"] }
	batch := func(op string, entries ...map[string]any) string {
		return jsonText(t, map[string]any{"schema": 1, "table": name, "epoch": "0", "expected_table_revision": revision(),
			"operation_id": op, "actor": "t", "members": entries})
	}
	type change struct {
		Before      *string `json:"before"`
		After       *string `json:"after"`
		AfterBytes  int     `json:"after_bytes"`
		AfterSHA1   string  `json:"after_sha1"`
		BeforeBytes int     `json:"before_bytes"`
		BeforeSHA1  string  `json:"before_sha1"`
	}
	type delta struct {
		Members []struct {
			ID          string            `json:"id"`
			AfterPlace  string            `json:"after_place"`
			FieldsSet   map[string]string `json:"fields_set"`
			FieldsUnset json.RawMessage   `json:"fields_unset"`
			Fields      map[string]change `json:"fields"`
		} `json:"members"`
	}
	receipt := func(reply []any) (string, delta) {
		t.Helper()
		rec, isList := reply[1].([]any)
		require.True(t, isList && len(rec) == 7 && rec[0] == "RECEIPT", "a batch receipt: %v", reply)
		text := rec[6].(string)
		var d delta
		require.NoError(t, json.Unmarshal([]byte(text), &d), "the receipt's delta: %s", text)
		require.Len(t, d.Members, 1)
		return text, d
	}
	stored := func(what string, want map[string]string) {
		t.Helper()
		for f, v := range want {
			require.Equal(t, v, s.hashes[member][f], "%s: field %s is stored as it came", what, f)
		}
	}

	// A batch creates the member with a field in each form.
	raw := batch("op1", map[string]any{"id": "m1", "expect": map[string]any{"absent": true},
		"create": map[string]any{"row": "r1", "col": "todo", "score": 1}, "set": values})
	first := s.fcallReply(t, "ns_table_apply", name, raw)
	ok(first, "create batch")
	stored("create", values)
	text, d := receipt(first)
	for f, v := range values {
		c := d.Members[0].Fields[f]
		require.Nil(t, c.Before, "create %s: no value before", f)
		if len(v) <= 64 {
			require.Equal(t, v, d.Members[0].FieldsSet[f], "create %s: the receipt's fields_set", f)
			require.NotNil(t, c.After, "create %s", f)
			require.Equal(t, v, *c.After, "create %s: the receipt's after", f)
		} else {
			require.Equal(t, len(v), c.AfterBytes, "create %s: after_bytes", f)
			require.Equal(t, sha(v), c.AfterSHA1, "create %s: after_sha1", f)
		}
	}
	ev := s.event(t, "table:demo:changes")
	require.Equal(t, text, ev["batch_delta"], "the event's delta is the receipt's, byte for byte")
	require.Equal(t, `["r1:todo"]`, ev["cells"], "the event's cells")
	var evMembers []map[string]string
	require.NoError(t, json.Unmarshal([]byte(ev["members"]), &evMembers))
	require.Equal(t, []map[string]string{{"id": "m1", "from": "", "to": "r1:todo", "score": "1"}}, evMembers, "the event's members")

	// The operation record holds the result as it was; a replay answers the
	// original result with REPLAY. Its request is only a digest now, which
	// table_retention_test.go checks.
	var record struct {
		Result string `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(s.hashes["table:demo:ops"]["0:op1"]), &record))
	var result []any
	require.NoError(t, json.Unmarshal([]byte(record.Result), &result))
	recorded, _ := receipt(result)
	require.Equal(t, text, recorded, "the operation record's result")
	again := s.fcallReply(t, "ns_table_apply", name, raw)
	require.Equal(t, "REPLAY", again[2], "a replay: %v", again)
	replayed, _ := receipt(again)
	require.Equal(t, text, replayed, "the replay's delta, byte for byte")
	stored("replay", values)

	// FIELDGUARD: equals and one_of compare a value by its exact bytes.
	guards := map[string]any{
		"img_all":   map[string]any{"equals": values["img_all"]},
		"img_small": map[string]any{"one_of": []string{values["img_all"], values["img_small"]}},
		"text":      map[string]any{"one_of": []string{values["text"]}},
		"legacy":    map[string]any{"equals": values["legacy"]},
	}
	ok(s.fcallReply(t, "ns_table_apply", name, batch("op2", map[string]any{"id": "m1", "expect": map[string]any{"fields": guards}})), "guards that hold")
	before := revision()
	// legacy JSON that decodes to the same value is a different value
	same := `{"z":1.5,"a":[],"id":"m1","nil":null}`
	refused := s.fcallReply(t, "ns_table_apply", name, batch("op3", map[string]any{"id": "m1", "expect": map[string]any{
		"fields": map[string]any{"legacy": map[string]any{"one_of": []string{same, values["text"]}}}}}))
	require.Equal(t, []any{"REFUSED", "FIELDGUARD", "m1", "legacy", "one_of", "2 options", values["legacy"]}, refused,
		"a one_of guard compares bytes, and its refusal names the option count")
	refused = s.fcallReply(t, "ns_table_apply", name, batch("op3b", map[string]any{"id": "m1", "expect": map[string]any{
		"fields": map[string]any{"legacy": map[string]any{"equals": same}}}}))
	require.Equal(t, []any{"REFUSED", "FIELDGUARD", "m1", "legacy", "equals", same, values["legacy"]}, refused, "an equals guard compares bytes")
	require.Equal(t, before, revision(), "a refused batch writes nothing")

	// Fields set and unset: the new value and the before value travel as
	// they are.
	fourth := s.fcallReply(t, "ns_table_apply", name, batch("op4", map[string]any{"id": "m1", "expect": map[string]any{},
		"set": map[string]string{"legacy": values["text"], "img_small": values["img_all"]}, "unset": []string{"text"}}))
	ok(fourth, "set and unset")
	_, d = receipt(fourth)
	stored("set", map[string]string{"legacy": values["text"], "img_small": values["img_all"], "img_all": values["img_all"]})
	_, has := s.hashes[member]["text"]
	require.False(t, has, "unset removes the field")
	fc := d.Members[0].Fields
	require.Equal(t, values["legacy"], *fc["legacy"].Before, "set: legacy's before")
	require.Equal(t, values["text"], *fc["legacy"].After, "set: legacy's after")
	require.Equal(t, values["img_small"], *fc["img_small"].Before, "set: img_small's before")
	require.Equal(t, len(values["img_all"]), fc["img_small"].AfterBytes)
	require.Equal(t, sha(values["img_all"]), fc["img_small"].AfterSHA1)
	require.Equal(t, values["text"], *fc["text"].Before, "unset: text's before")
	require.Nil(t, fc["text"].After, "unset: no value after")
	require.JSONEq(t, `["text"]`, string(d.Members[0].FieldsUnset))

	// Moves, by a batch and by the cell verb, leave every value as it is.
	ok(s.fcallReply(t, "ns_table_apply", name, batch("op5", map[string]any{"id": "m1", "expect": map[string]any{},
		"move": map[string]any{"row": "r1", "col": "done"}})), "batch move")
	ok(s.fcallReply(t, "ns_table_cell_move", name, "r1", "done", "todo", "m1", opts), "cell move")
	require.Equal(t, "r1:todo", s.hashes[member]["place:demo"])
	stored("moves", map[string]string{"legacy": values["text"], "img_small": values["img_all"], "img_all": values["img_all"]})

	// A value's escapes are checked by cjson's rules without decoding the
	// value: each good one is stored as the envelope decodes it, each bad one
	// refuses the batch by name before the store is read.
	for i, esc := range []struct{ token, want string }{
		{`"\u00e9\/\b\f\n\r\t\"\\"`, "\u00e9/\b\f\n\r\t\"\\"},
		{`"\ud83d\ude00"`, "\U0001F600"},
		{`"\x"`, ""}, {`"\udc00"`, ""}, {`"\ud800x"`, ""}, {`"\ud800\u0041"`, ""}, {`"\u12"`, ""}, {`"\u12g4"`, ""},
	} {
		op := fmt.Sprintf("esc%d", i)
		bare := batch(op)
		require.Contains(t, bare, `"members":null`)
		env := strings.Replace(bare, `"members":null`, `"members":[{"id":"m1","expect":{},"set":{"esc":`+esc.token+`}}]`, 1)
		reply := s.fcallReply(t, "ns_table_apply", name, env)
		if esc.want == "" {
			require.Equal(t, []any{"REFUSED", "MANIFEST", "invalid string escape"}, reply, "%s is refused", esc.token)
			continue
		}
		ok(reply, esc.token)
		require.Equal(t, esc.want, s.hashes[member]["esc"], "%s is stored as decoded", esc.token)
	}

	// No cjson.decode was given a value's own string token: a value is read
	// only as part of the envelope it came in.
	require.NotEmpty(t, s.decodes)
	for _, in := range s.decodes {
		if !strings.HasPrefix(in, `"`) {
			continue
		}
		var v string
		require.NoError(t, json.Unmarshal([]byte(in), &v))
		require.False(t, isValue[v] || v == same, "cjson.decode was given a row value's own token: %.80s", in)
	}

	// Every cjson call in table.lua says above it which of the envelope, the
	// event or the operation record it reads or writes.
	src, err := sources.ReadFile("lua/table.lua")
	require.NoError(t, err)
	lines := strings.Split(string(src), "\n")
	calls := 0
	for i, line := range lines {
		if !cjsonCall.MatchString(line) {
			continue
		}
		calls++
		require.Greater(t, i, 0)
		note := strings.TrimSpace(lines[i-1])
		require.Regexp(t, `^-- cjson \((envelope|event|op record)\): `, note,
			"table.lua:%d: %s\n  a cjson call carries the comment line above it naming what it reads or writes", i+1, strings.TrimSpace(line))
	}
	require.Equal(t, 14, calls, "the cjson calls of table.lua (15 at the aspect's audit, less the FIELDGUARD one_of encode)")
}
