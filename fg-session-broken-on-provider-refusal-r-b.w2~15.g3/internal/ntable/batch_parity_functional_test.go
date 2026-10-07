//go:build functional

package ntable_test

// A second table of malformed manifests, each sent through raw FCALL and the
// Go validator: both refuse, the server without a raw error and without a write.

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchFurtherMalformedManifestsRefusedByServerAndValidator(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	rev := probeRev(ctx, c)
	head := func(extra string) string {
		return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"m",` + extra
	}
	okm := `"members":[{"id":"a","expect":{}}]}`
	crt := func(v string) string {
		return head(`"members":[{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"ready","score":` + v + `}}]}`)
	}
	deep := strings.Repeat(`{"a":`, 200) + `1` + strings.Repeat(`}`, 200)
	cases := map[string]string{
		"dup root key":           head(`"operation_id":"m2",` + okm),
		"dup schema":             `{"schema":1,"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"m",` + okm,
		"dup member key id":      head(`"members":[{"id":"a","id":"b","expect":{}}]}`),
		"dup expect key":         head(`"members":[{"id":"a","expect":{"revision":"1","revision":"1"}}]}`),
		"dup place key":          head(`"members":[{"id":"a","expect":{"place":{"row":"build","row":"build","col":"ready"}}}]}`),
		"dup fields key":         head(`"members":[{"id":"a","expect":{"fields":{"role":{"equals":"x"},"role":{"equals":"x"}}}}]}`),
		"dup guard key":          head(`"members":[{"id":"a","expect":{"fields":{"role":{"equals":"x","equals":"x"}}}}]}`),
		"dup create key":         head(`"members":[{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1,"score":2}}]}`),
		"dup move key":           head(`"members":[{"id":"a","expect":{},"move":{"row":"build","col":"done","col":"working"}}]}`),
		"dup set key":            head(`"members":[{"id":"a","expect":{},"set":{"k":"1","k":"1"}}]}`),
		"escaped dup key":        head(`"members":[{"id":"a","expect":{},"set":{"k":"1","k":"2"}}]}`),
		"escaped dup root":       head(`"operation_id":"m3",` + okm),
		"case key Members":       head(`"Members":[]}`),
		"case key ID":            head(`"members":[{"ID":"a","expect":{}}]}`),
		"case key Expect":        head(`"members":[{"id":"a","Expect":{}}]}`),
		"case key Absent":        head(`"members":[{"id":"n","expect":{"Absent":true}}]}`),
		"case key Equals":        head(`"members":[{"id":"a","expect":{"fields":{"role":{"Equals":"x"}}}}]}`),
		"schema string":          `{"schema":"1","table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"m",` + okm,
		"schema 1.0":             `{"schema":1.0,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"m",` + okm,
		"schema 1e0":             `{"schema":1e0,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"m",` + okm,
		"schema 2":               `{"schema":2,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"m",` + okm,
		"epoch number":           `{"schema":1,"table":"demo","epoch":0,"expected_table_revision":"` + rev + `","operation_id":"m",` + okm,
		"epoch 00":               `{"schema":1,"table":"demo","epoch":"00","expected_table_revision":"` + rev + `","operation_id":"m",` + okm,
		"epoch huge":             `{"schema":1,"table":"demo","epoch":"18446744073709551616","expected_table_revision":"` + rev + `","operation_id":"m",` + okm,
		"epoch -0":               `{"schema":1,"table":"demo","epoch":"-0","expected_table_revision":"` + rev + `","operation_id":"m",` + okm,
		"rev number":             `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":` + rev + `,"operation_id":"m",` + okm,
		"rev +1":                 `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"+` + rev + `","operation_id":"m",` + okm,
		"member rev number":      head(`"members":[{"id":"a","expect":{"revision":1}}]}`),
		"member rev huge":        head(`"members":[{"id":"a","expect":{"revision":"99999999999999999999999"}}]}`),
		"member rev hex":         head(`"members":[{"id":"a","expect":{"revision":"0x1"}}]}`),
		"score huge int":         crt(`123456789012345678901234567890123456789012345678901234567890e300`),
		"score -1e400":           crt(`-1e400`),
		"score string 1":         crt(`"1"`),
		"score leading +":        crt(`+1`),
		"score .5":               crt(`.5`),
		"score 01":               crt(`01`),
		"score NaN literal":      crt(`NaN`),
		"score Infinity literal": crt(`Infinity`),
		"score object":           crt(`{}`),
		"score null":             crt(`null`),
		"id number":              head(`"members":[{"id":5,"expect":{}}]}`),
		"id empty":               head(`"members":[{"id":"","expect":{}}]}`),
		"id NUL":                 head(`"members":[{"id":"a\u0000b","expect":{}}]}`),
		"id newline":             head(`"members":[{"id":"a\nb","expect":{}}]}`),
		"id 257 bytes":           head(`"members":[{"id":"` + strings.Repeat("x", 257) + `","expect":{}}]}`),
		"invalid utf8 in id":     head(`"members":[{"id":"a` + "\xff" + `","expect":{}}]}`),
		"invalid utf8 in value":  head(`"members":[{"id":"a","expect":{},"set":{"k":"` + "\xc3\x28" + `"}}]}`),
		"overlong utf8":          head(`"members":[{"id":"a","expect":{},"set":{"k":"` + "\xc0\xaf" + `"}}]}`),
		"lone surrogate escape":  head(`"members":[{"id":"a","expect":{},"set":{"k":"\ud800"}}]}`),
		"deep nesting in set":    head(`"members":[{"id":"a","expect":{},"set":{"k":` + deep + `}}]}`),
		"deep nesting unknown":   head(`"x":` + deep + `,` + okm),
		"members object":         head(`"members":{}}`),
		"members null":           head(`"members":null}`),
		"member array":           head(`"members":[["a"]]}`),
		"expect array":           head(`"members":[{"id":"a","expect":[]}]}`),
		"set array":              head(`"members":[{"id":"a","expect":{},"set":[]}]}`),
		"unset object":           head(`"members":[{"id":"a","expect":{},"unset":{}}]}`),
		"unset number item":      head(`"members":[{"id":"a","expect":{},"unset":[1]}]}`),
		"unset dup names":        head(`"members":[{"id":"a","expect":{},"unset":["q","q"]}]}`),
		"one_of dup":             head(`"members":[{"id":"a","expect":{"fields":{"role":{"one_of":["x","x"]}}}}]}`),
		"one_of number":          head(`"members":[{"id":"a","expect":{"fields":{"role":{"one_of":[1]}}}}]}`),
		"equals number":          head(`"members":[{"id":"a","expect":{"fields":{"role":{"equals":1}}}}]}`),
		"guard absent false":     head(`"members":[{"id":"a","expect":{"fields":{"role":{"absent":false}}}}]}`),
		"field name empty":       head(`"members":[{"id":"a","expect":{},"set":{"":"v"}}]}`),
		"field name place:x":     head(`"members":[{"id":"a","expect":{},"set":{"place:x":"v"}}]}`),
		"actor number":           head(`"actor":5,` + okm),
		"actor null":             head(`"actor":null,` + okm),
		"trailing comma":         head(`"members":[{"id":"a","expect":{}},]}`),
		"trailing garbage":       head(okm) + `x`,
		"two documents":          head(okm) + `{}`,
		"BOM":                    "\xef\xbb\xbf" + head(okm),
		"comment":                head(`/*c*/` + okm),
		"single quotes":          head(`'members':[]}`),
		"create no row":          head(`"members":[{"id":"n","expect":{"absent":true},"create":{"col":"ready","score":1}}]}`),
		"move empty":             head(`"members":[{"id":"a","expect":{},"move":{}}]}`),
		"place missing col":      head(`"members":[{"id":"a","expect":{"place":{"row":"build"}}}]}`),
		"remove with create":     head(`"members":[{"id":"n","expect":{"absent":true},"remove":true,"create":{"row":"build","col":"ready","score":1}}]}`),
		"absent with fields":     head(`"members":[{"id":"n","expect":{"absent":true,"fields":{"k":{"absent":true}}}}]}`),
		"absent with place":      head(`"members":[{"id":"n","expect":{"absent":true,"place":{"row":"build","col":"ready"}}}]}`),
		"member no expect":       head(`"members":[{"id":"a"}]}`),
	}
	for n, name := range slices.Sorted(maps.Keys(cases)) {
		raw := strings.Replace(cases[name], `"operation_id":"m"`, fmt.Sprintf(`"operation_id":"f%d"`, n), 1)
		before := storeImage(t, c)
		ans, err := rawApply(ctx, c, raw)
		_, verr := ntable.ValidateBatchManifestRaw([]byte(raw))
		require.True(t, replyOpens(ans, err, "REFUSED"), "%s: the server accepts: %v: %v", name, trunc(ans), err)
		require.GreaterOrEqual(t, len(ans), 2, "%s: the server accepts: %v", name, trunc(ans))
		assert.Error(t, verr, "%s: the Go validator accepts", name)
		assert.Equal(t, before, storeImage(t, c), "%s: the store changed", name)
	}
}

// A surrogate pair is one character on both paths; a lone surrogate is refused
// by both, never stored as U+FFFD.
func TestBatchSurrogateEscapesAreRefusedAlikeByServerAndValidator(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	member := func(v string) string { return `{"id":"a","expect":{},"set":{"k":"` + v + `"}}` }

	pair := manifestWith(probeRev(ctx, c), "pair", member(`😀`))
	m, err := ntable.ValidateBatchManifestRaw([]byte(pair))
	require.NoError(t, err, "a valid pair is refused by the validator")
	assert.Equal(t, "\U0001F600", m.Members[0].Set["k"], "the validator decoded the pair as %q", m.Members[0].Set["k"])
	ans, err := rawApply(ctx, c, pair)
	require.True(t, replyOpens(ans, err, "OK"), "a valid pair is refused by the server: %v: %v", trunc(ans), err)
	got := c.HGet(ctx, ntable.MemberKey("a"), "k").Val()
	assert.Equal(t, "\U0001F600", got, "the pair was stored as %q", got)

	for i, v := range []string{`\ud800`, `\udc00`, `\ud800x`, `\ud800A`, `\udc00\ud800`, `x\ud83d`, `\ud83d\ude0`} {
		raw := manifestWith(probeRev(ctx, c), fmt.Sprintf("lone%d", i), member(v))
		before := storeImage(t, c)
		ans, err := rawApply(ctx, c, raw)
		assert.True(t, replyOpens(ans, err, "REFUSED"), "%s: the server: %v %v", v, trunc(ans), err)
		_, verr := ntable.ValidateBatchManifestRaw([]byte(raw))
		assert.Error(t, verr, "%s: the Go validator accepts it", v)
		assert.Equal(t, before, storeImage(t, c), "%s: the store changed", v)
	}
}
