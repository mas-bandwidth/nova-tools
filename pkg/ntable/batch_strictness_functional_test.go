//go:build functional

package ntable_test

// The server accepts exactly the manifests the Go validator accepts. Every
// malformed manifest below is sent through raw FCALL and through the validator;
// both refuse, the server without a raw error reply and without a write.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	baseCreate = `{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`
	baseMove   = `{"id":"a","expect":{"revision":"1","place":{"row":"build","col":"ready"}},"move":{"row":"test","col":"done"}}`
	baseSet    = `{"id":"a","expect":{},"set":{"k":"v"}}`
)

func manifestWith(rev, op, members string) string {
	return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"` + op + `","actor":"p","members":[` + members + `]}`
}

func createWithScore(score string) string {
	return `{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"ready","score":` + score + `}}`
}

func moveWithScore(score string) string {
	return `{"id":"a","expect":{},"move":{"row":"test","col":"done","score":` + score + `}}`
}

// malformed lists whole-manifest templates; REV and OP are filled per run.
func malformedManifests() []struct{ name, raw string } {
	head := func(fields string) string { return `{` + fields + `}` }
	members := func(m string) string {
		return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"REV","operation_id":"OP","members":[` + m + `]}`
	}
	root := func(field, value string) string {
		f := map[string]string{
			"schema": `"schema":1`, "table": `"table":"demo"`, "epoch": `"epoch":"0"`, "expected_table_revision": `"expected_table_revision":"REV"`,
			"operation_id": `"operation_id":"OP"`, "actor": `"actor":"p"`, "members": `"members":[` + baseSet + `]`,
		}
		var parts []string
		for _, k := range []string{"schema", "table", "epoch", "expected_table_revision", "operation_id", "actor", "members"} {
			if k == field {
				if value != "" {
					parts = append(parts, `"`+k+`":`+value)
				}
				continue
			}
			parts = append(parts, f[k])
		}
		return head(strings.Join(parts, ","))
	}
	var cs []struct{ name, raw string }
	add := func(name, raw string) { cs = append(cs, struct{ name, raw string }{name, raw}) }

	// header
	for _, v := range []string{`2`, `0`, `"1"`, `1.0`, `1e0`, `null`, `true`, `[]`} {
		add("schema "+v, root("schema", v))
	}
	add("schema missing", root("schema", ""))
	add("table invalid name", root("table", `"bad name"`))
	add("table number", root("table", `5`))
	add("table missing", root("table", ""))
	for _, v := range []string{`""`, `"a\nb"`, `"a\u0001b"`, `5`, `null`, `["x"]`} {
		add("operation_id "+v, root("operation_id", v))
	}
	add("operation_id missing", root("operation_id", ""))
	for _, v := range []string{`0`, `"abc"`, `"-1"`, `"01"`, `"18446744073709551616"`, `"1e3"`, `""`, `" 0"`, `"+1"`, `null`, `1.5`} {
		add("epoch "+v, root("epoch", v))
	}
	add("epoch missing", root("epoch", ""))
	for _, v := range []string{`0`, `"01"`, `""`, `"x"`, `"1.0"`, `null`, `"-0"`} {
		add("expected_table_revision "+v, root("expected_table_revision", v))
	}
	add("expected_table_revision missing", root("expected_table_revision", ""))
	for _, v := range []string{`5`, `null`, `["p"]`, `true`} {
		add("actor "+v, root("actor", v))
	}
	for _, v := range []string{`{}`, `"m"`, `null`, `5`, `[5]`, `["a"]`, `[null]`, `[[]]`} {
		add("members "+v, root("members", v))
	}
	add("members empty", root("members", `[]`))
	add("members missing", root("members", ""))
	add("unknown root key", strings.Replace(root("actor", `"p"`), `"actor"`, `"actorx":1,"actor"`, 1))
	add("duplicate root key", strings.Replace(root("actor", `"p"`), `"actor":"p"`, `"actor":"p","actor":"q"`, 1))
	add("trailing garbage", root("actor", `"p"`)+` x`)
	add("trailing object", root("actor", `"p"`)+`{}`)
	add("root array", `[]`)
	add("root string", `"x"`)
	add("truncated", `{"schema":1,"table":"demo"`)
	add("invalid utf-8 in id", "{\"schema\":1,\"table\":\"demo\",\"epoch\":\"0\",\"expected_table_revision\":\"REV\",\"operation_id\":\"OP\",\"members\":[{\"id\":\"a\xff\",\"expect\":{}}]}")

	add("invalid utf-8 in set value", members("{\"id\":\"a\",\"expect\":{},\"set\":{\"k\":\"v\xff\"}}"))
	add("invalid utf-8 in actor", strings.Replace(root("actor", `"p"`), `"p"`, "\"p\xc3\"", 1))
	add("overlong utf-8 in id", "{\"schema\":1,\"table\":\"demo\",\"epoch\":\"0\",\"expected_table_revision\":\"REV\",\"operation_id\":\"OP\",\"members\":[{\"id\":\"a\xc0\x80\",\"expect\":{}}]}")

	// members
	for _, v := range []string{`""`, `5`, `null`, `"a\nb"`, `"a\u0000b"`, `["a"]`} {
		add("id "+v, members(`{"id":`+v+`,"expect":{}}`))
	}
	add("id missing", members(`{"expect":{}}`))
	add("duplicate ids", members(baseSet+`,`+baseSet))
	add("duplicate id keys", members(`{"id":"a","id":"a","expect":{}}`))
	add("member unknown key", members(`{"id":"a","expect":{},"bogus":1}`))
	add("member uppercase key", members(`{"id":"a","expect":{},"Set":{"k":"v"}}`))
	add("expect missing", members(`{"id":"a"}`))
	for _, v := range []string{`null`, `"x"`, `[]`, `5`, `true`} {
		add("expect "+v, members(`{"id":"a","expect":`+v+`}`))
	}
	add("expect unknown key", members(`{"id":"a","expect":{"score":1}}`))
	for _, v := range []string{`false`, `"true"`, `1`, `0`, `null`, `[]`, `{}`, `"false"`} {
		add("remove "+v, members(`{"id":"a","expect":{},"remove":`+v+`}`))
	}

	// set and unset
	for _, v := range []string{`[]`, `"x"`, `null`, `5`, `true`} {
		add("set "+v, members(`{"id":"a","expect":{},"set":`+v+`}`))
	}
	for _, v := range []string{`5`, `null`, `true`, `{}`, `[]`, `{"a":"b"}`} {
		add("set value "+v, members(`{"id":"a","expect":{},"set":{"k":`+v+`}}`))
	}
	add("set duplicate key", members(`{"id":"a","expect":{},"set":{"k":"1","k":"2"}}`))
	add("set empty name", members(`{"id":"a","expect":{},"set":{"":"v"}}`))
	add("set control name", members(`{"id":"a","expect":{},"set":{"k\u0001":"v"}}`))
	for _, f := range []string{"epoch", "revision", "place:demo", "place:"} {
		add("set reserved "+f, members(`{"id":"a","expect":{},"set":{"`+f+`":"v"}}`))
		add("unset reserved "+f, members(`{"id":"a","expect":{},"unset":["`+f+`"]}`))
	}
	for _, v := range []string{`{}`, `"x"`, `null`, `5`, `[5]`, `[null]`, `[[]]`, `[""]`, `["a\u0001"]`, `[{}]`} {
		add("unset "+v, members(`{"id":"a","expect":{},"unset":`+v+`}`))
	}
	add("set and unset the same field", members(`{"id":"a","expect":{},"set":{"x":"1"},"unset":["x"]}`))

	// expect
	for _, v := range []string{`0`, `"true"`, `false`, `null`, `[]`, `{}`, `"false"`, `"x"`} {
		add("expect absent "+v, members(`{"id":"n","expect":{"absent":`+v+`}}`))
	}
	add("absent with revision", members(`{"id":"n","expect":{"absent":true,"revision":"1"}}`))
	add("absent with place", members(`{"id":"n","expect":{"absent":true,"place":{"row":"build","col":"ready"}}}`))
	add("absent with fields", members(`{"id":"n","expect":{"absent":true,"fields":{"k":{"absent":true}}}}`))
	for _, v := range []string{`1`, `""`, `"01"`, `"-1"`, `"abc"`, `null`, `true`, `[]`, `{}`, `"1.0"`, `"18446744073709551616"`} {
		add("expect revision "+v, members(`{"id":"a","expect":{"revision":`+v+`}}`))
	}
	for _, v := range []string{`null`, `[]`, `"x"`, `5`, `{}`, `{"row":"build"}`, `{"col":"ready"}`, `{"row":5,"col":"ready"}`, `{"row":"build","col":5}`, `{"row":null,"col":"ready"}`, `{"row":"build","col":"ready","x":1}`} {
		add("expect place "+v, members(`{"id":"a","expect":{"place":`+v+`}}`))
	}
	for _, v := range []string{`null`, `[]`, `"x"`, `5`, `true`} {
		add("expect fields "+v, members(`{"id":"a","expect":{"fields":`+v+`}}`))
	}
	for _, v := range []string{`null`, `[]`, `"x"`, `5`, `{}`, `{"absent":true,"equals":"x"}`, `{"equals":5}`, `{"equals":null}`, `{"equals":["x"]}`,
		`{"absent":false}`, `{"absent":1}`, `{"absent":"true"}`, `{"absent":null}`, `{"one_of":[]}`, `{"one_of":"x"}`, `{"one_of":[1]}`, `{"one_of":[null]}`, `{"one_of":null}`,
		`{"one_of":{}}`, `{"exists":true}`, `{"equals":"x","one_of":["x"]}`} {
		add("field guard "+v, members(`{"id":"a","expect":{"fields":{"role":`+v+`}}}`))
	}
	add("field guard control name", members(`{"id":"a","expect":{"fields":{"r\u0001":{"absent":true}}}}`))
	add("field guard duplicate name", members(`{"id":"a","expect":{"fields":{"role":{"absent":true},"role":{"absent":true}}}}`))

	// create and move
	for _, v := range []string{`{}`, `null`, `[]`, `"x"`, `5`, `{"row":"build","col":"ready"}`, `{"row":"build","score":1}`, `{"col":"ready","score":1}`,
		`{"row":5,"col":"ready","score":1}`, `{"row":"build","col":true,"score":1}`, `{"row":null,"col":"ready","score":1}`, `{"row":"build","col":"ready","score":1,"x":1}`} {
		add("create "+v, members(`{"id":"n","expect":{"absent":true},"create":`+v+`}`))
	}
	for _, v := range []string{`""`, `"1e999"`, `"nan"`, `"-inf"`, `"0x10"`, `" 7 "`, `"1e3"`, `"5"`, `"5.5"`, `[1]`, `[]`, `true`, `false`, `null`, `{"a":1}`, `{}`,
		`1e999`, `-1e999`, `0x10`, `Infinity`, `-Infinity`, `NaN`, `nan`, `inf`, `+5`, `.5`, `5.`, `01`, `1e`, `1e+`, `--1`, `0x`, `1_0`, `1,5`} {
		add("create score "+v, members(createWithScore(v)))
	}
	add("create without expect", members(`{"id":"n","create":{"row":"build","col":"ready","score":1}}`))
	add("create with empty expect", members(`{"id":"n","expect":{},"create":{"row":"build","col":"ready","score":1}}`))
	add("create with revision expect", members(`{"id":"n","expect":{"revision":"1"},"create":{"row":"build","col":"ready","score":1}}`))
	add("create and move", members(`{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1},"move":{"row":"test","col":"done"}}`))
	add("create and remove", members(`{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1},"remove":true}`))
	for _, v := range []string{`{}`, `null`, `[]`, `"x"`, `5`, `{"row":"test"}`, `{"col":"done"}`, `{"row":5,"col":"done"}`, `{"row":"test","col":5}`, `{"row":"test","col":"done","x":1}`} {
		add("move "+v, members(`{"id":"a","expect":{},"move":`+v+`}`))
	}
	for _, v := range []string{`""`, `"abc"`, `"1e999"`, `"0x10"`, `" 5 "`, `"5"`, `null`, `true`, `[1]`, `{}`, `1e999`, `0x10`, `NaN`, `Infinity`, `+5`, `.5`, `5.`} {
		add("move score "+v, members(moveWithScore(v)))
	}
	add("move and remove", members(`{"id":"a","expect":{},"move":{"row":"test","col":"done"},"remove":true}`))
	return cs
}

func TestBatchMalformedManifestsRefusedByServerAndValidator(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)

	// The positive controls: the base manifests are accepted on both paths.
	for i, m := range []string{baseCreate, baseMove, baseSet} {
		raw := manifestWith(probeRev(ctx, c), fmt.Sprintf("control-%d", i), m)
		_, err := ntable.ValidateBatchManifestRaw([]byte(raw))
		require.NoError(t, err, "control %d: the validator refuses a valid manifest: %v", i, err)
		ans, err := rawApply(ctx, c, raw)
		require.True(t, replyOpens(ans, err, "OK"), "control %d: the server refuses a valid manifest: %v: %v", i, trunc(ans), err)
	}

	for n, tc := range malformedManifests() {
		raw := strings.NewReplacer("REV", probeRev(ctx, c), "OP", fmt.Sprintf("m-%d", n)).Replace(tc.raw)
		before := storeImage(t, c)
		ans, err := rawApply(ctx, c, raw)
		_, verr := ntable.ValidateBatchManifestRaw([]byte(raw))
		require.True(t, replyOpens(ans, err, "REFUSED"), "%s: the server accepts: %v: %v", tc.name, trunc(ans), err)
		require.GreaterOrEqual(t, len(ans), 2, "%s: the server accepts: %v", tc.name, trunc(ans))
		assert.Error(t, verr, "%s: the Go validator accepts", tc.name)
		assert.Equal(t, before, storeImage(t, c), "%s: the store changed", tc.name)
	}
}

// The other direction: a manifest the validator accepts is accepted by the
// server, so neither side is stricter than the other for a valid request.
func TestBatchValidManifestsAcceptedByServerAndValidator(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	valid := []string{
		`{"id":"h\u00e9llo w\u00f6rld","expect":{"absent":true}}`,
		`{"id":"a","expect":{}}`,
		`{"id":"a","expect":{"fields":{"role":{"one_of":["x","y"]}}}}`,
		`{"id":"a","expect":{"fields":{"role":{"equals":"x"}}}}`,
		`{"id":"b","expect":{"fields":{"role":{"absent":true}}}}`,
		`{"id":"n1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":-2.5e1}}`,
		`{"id":"b","expect":{},"set":{"x y":"v z","k":""}}`,
		`{"id":"b","expect":{},"unset":["x y"]}`,
		`{"id":"b","expect":{},"move":{"row":"test","col":"working","score":3}}`,
		`{"id":"n2","expect":{"absent":true},"create":{"row":"build","col":"done","score":0},"set":{"d":"e"}}`,
		`{"id":"n2","expect":{"place":{"row":"build","col":"done"}},"remove":true}`,
		`{"id":"a","expect":{},"move":{"row":"test","col":"done"},"set":{"role":"y"},"unset":["gone"]}`,
		"{ \"id\" : \"a\" ,\n \"expect\" : { } }",
	}
	for i, m := range valid {
		raw := manifestWith(probeRev(ctx, c), fmt.Sprintf("valid-%d", i), m)
		_, err := ntable.ValidateBatchManifestRaw([]byte(raw))
		if !assert.NoError(t, err, "%s: the validator refuses", m) {
			continue
		}
		ans, err := rawApply(ctx, c, raw)
		require.True(t, replyOpens(ans, err, "OK"), "%s: the server refuses: %v: %v", m, trunc(ans), err)
	}
}

// A manifest that is not well-formed UTF-8 is refused by name; the server
// never renames an id to U+FFFD and never creates a member from bytes.
func TestBatchInvalidUTF8IsRefusedByName(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	raw := manifestWith(probeRev(ctx, c), "u8", "{\"id\":\"n\xff\",\"expect\":{\"absent\":true},\"create\":{\"row\":\"build\",\"col\":\"ready\",\"score\":1}}")
	before := storeImage(t, c)
	ans, err := rawApply(ctx, c, raw)
	require.True(t, replyOpens(ans, err, "REFUSED", "MANIFEST"), "%v; want REFUSED MANIFEST naming UTF-8: %v", trunc(ans), err)
	require.GreaterOrEqual(t, len(ans), 3, "%v; want REFUSED MANIFEST naming UTF-8", trunc(ans))
	require.Contains(t, fmt.Sprint(ans[2]), "UTF-8", "%v; want REFUSED MANIFEST naming UTF-8", trunc(ans))
	assert.Equal(t, before, storeImage(t, c), "the store changed")
}

// Every score on either path is a finite JSON number; a string never is, even
// when a script's tonumber would read it. A refused create places nothing.
func TestBatchScoresAreFiniteJSONNumbers(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	for _, v := range []string{`"0x10"`, `" 7 "`, `"1e3"`, `"5"`, `""`, `"nan"`, `"1e999"`, `1e999`, `0x10`, `Infinity`} {
		before := storeImage(t, c)
		raw := manifestWith(probeRev(ctx, c), "sc", createWithScore(v))
		ans, err := rawApply(ctx, c, raw)
		assert.True(t, replyOpens(ans, err, "REFUSED"), "create score %s: %v %v", v, trunc(ans), err)
		assert.Equal(t, before, storeImage(t, c), "create score %s wrote", v)
	}
	// a string score names what it found
	ans, err := rawApply(ctx, c, manifestWith(probeRev(ctx, c), "sc-str", createWithScore(`"0x10"`)))
	require.NoError(t, err, "a string score: %v; want SCORE, the member, and the type found", trunc(ans))
	require.GreaterOrEqual(t, len(ans), 4, "a string score: %v; want SCORE, the member, and the type found", trunc(ans))
	assert.Equal(t, "SCORE", ans[1], "a string score: %v; want SCORE, the member, and the type found", trunc(ans))
	assert.Equal(t, "n", ans[2], "a string score: %v; want SCORE, the member, and the type found", trunc(ans))
	assert.Equal(t, "string", ans[3], "a string score: %v; want SCORE, the member, and the type found", trunc(ans))
	// and numbers, integer or fractional, negative or exponent, are placed as given
	for i, v := range []string{`0`, `-3`, `2.5`, `1e3`, `-0.25`} {
		id := fmt.Sprintf("s%d", i)
		raw := manifestWith(probeRev(ctx, c), "ok-"+id, `{"id":"`+id+`","expect":{"absent":true},"create":{"row":"build","col":"ready","score":`+v+`}}`)
		ans, err := rawApply(ctx, c, raw)
		require.True(t, replyOpens(ans, err, "OK"), "create score %s: %v: %v", v, trunc(ans), err)
	}
}

// The request of a read set is one of its defined shapes, each nonempty; the
// server never answers a malformed request as an empty set.
func TestReadSetRefusesRequestsOutsideItsShapes(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	refused := []string{
		`{}`, `[]`, `{"other":1}`, `{"selection":{}}`, `{"selection":[]}`, `{"members":[]}`, `{"members":{}}`,
		`{"members":"a"}`, `{"members":["a"],"other":1}`, `{"members":["a"],"selection":[{"row":"build","col":"ready"}]}`,
		`{"members":[5]}`, `{"members":[["a"]]}`, `{"members":[{"id":"a"}]}`, `{"members":[""]}`, `{"members":[null]}`,
		`{"selection":[{"row":"build"}]}`, `{"selection":[{"col":"ready"}]}`, `{"selection":[{"row":"build","col":""}]}`,
		`{"selection":[{"row":"build","col":5}]}`, `{"selection":[{"row":"build","col":"ready","x":1}]}`, `{"selection":["build"]}`,
		`{"selection":[null]}`, `[5]`, `[null]`, `[["a"]]`, `[""]`, `"a"`, `5`, `null`, `true`, `not json`, ``,
	}
	for _, scope := range refused {
		before := storeImage(t, c)
		rs, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", scope).Slice()
		require.True(t, replyOpens(rs, err, "REFUSED", "ARGS"), "scope %q: %v; want REFUSED ARGS: %v", scope, trunc(rs), err)
		assert.Equal(t, before, storeImage(t, c), "scope %q: the store changed", scope)
	}
	// the library refuses an empty request the same way
	for name, scope := range map[string]ntable.ReadSetScope{"no scope": {}, "empty members": {Members: []string{}}, "empty selection": {Selection: []ntable.CellSelection{}}} {
		_, err := ntable.ReadSet(ctx, c, "demo", scope)
		assert.ErrorContains(t, err, "changed=no", "%s: %v; want a refusal ending changed=no", name, err)
	}
	// the defined shapes are answered
	accepted := map[string]int{
		`{"members":["a"]}`:         1,
		`{"members":["a","b","a"]}`: 2,
		`["a","b"]`:                 2,
		`{"selection":[{"row":"build","col":"ready"}]}`:   1,
		`{"selection":[{"row":"build","col":"working"}]}`: 0, // an empty cell is a complete, empty answer
	}
	for scope, want := range accepted {
		rs, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", scope).Slice()
		require.NoError(t, err, "scope %q: %v", scope, trunc(rs))
		require.Len(t, rs, 6, "scope %q: %v", scope, trunc(rs))
		require.Equal(t, "SET", rs[0], "scope %q: %v", scope, trunc(rs))
		got := len(rs[4].([]any))
		assert.Equal(t, want, got, "scope %q: %d members, want %d", scope, got, want)
	}
}

// A manifest names at least one member: an empty one is refused on every path and
// does not advance the table revision.
func TestBatchWithoutMembersIsRefusedAndAdvancesNothing(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	rev := probeRev(ctx, c)
	before := storeImage(t, c)
	raw := manifestWith(rev, "empty", "")
	ans, err := rawApply(ctx, c, raw)
	require.True(t, replyOpens(ans, err, "REFUSED", "MANIFEST"), "the server: %v: %v", trunc(ans), err)
	require.GreaterOrEqual(t, len(ans), 3, "the server: %v", trunc(ans))
	assert.Contains(t, fmt.Sprint(ans[2]), "at least one member", "the server: %v", trunc(ans))
	_, err = ntable.ValidateBatchManifestRaw([]byte(raw))
	assert.ErrorContains(t, err, "at least one member", "the validator")
	_, err = ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "empty"})
	require.ErrorContains(t, err, "at least one member", "ApplyBatch")
	assert.ErrorContains(t, err, "changed=no", "ApplyBatch")
	assert.Equal(t, rev, probeRev(ctx, c), "a refused empty manifest changed the store revision")
	assert.Equal(t, before, storeImage(t, c), "a refused empty manifest changed the store")
}

// A read set's request names no key twice, as a manifest does not: the second
// value would silently replace the first.
func TestReadSetRefusesRepeatedKeys(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	for _, scope := range []string{
		`{"members":["a"],"members":["b"]}`,
		`{"members":["a"],"m\u0065mbers":["b"]}`,
		`{"selection":[{"row":"build","col":"ready","col":"working"}]}`,
		`{"selection":[{"row":"build","col":"ready"},{"row":"test","row":"build","col":"ready"}]}`,
		`{"members":["a"],"selection":[{"row":"build","col":"ready"}],"members":["a"]}`,
	} {
		rs, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", scope).Slice()
		require.True(t, replyOpens(rs, err, "REFUSED", "ARGS"), "%s: %v; want REFUSED ARGS naming a repeated key: %v", scope, trunc(rs), err)
		require.GreaterOrEqual(t, len(rs), 3, "%s: %v; want REFUSED ARGS naming a repeated key", scope, trunc(rs))
		assert.Contains(t, fmt.Sprint(rs[2]), "twice", "%s: %v; want REFUSED ARGS naming a repeated key", scope, trunc(rs))
	}
	// a name that repeats in different objects is not a repeat
	rs, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", `{"selection":[{"row":"build","col":"ready"},{"row":"test","col":"ready"}]}`).Slice()
	require.NoError(t, err, "distinct objects with the same key names: %v", trunc(rs))
	require.Len(t, rs, 6, "distinct objects with the same key names: %v", trunc(rs))
	assert.Equal(t, "SET", rs[0], "distinct objects with the same key names: %v", trunc(rs))
}
