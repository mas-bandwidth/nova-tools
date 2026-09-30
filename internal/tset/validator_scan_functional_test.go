//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

func validatorCreateRaw(space, id, valueToken, extra string) string {
	return fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"create","t":"work","to":"r:ready","ids":[%q],"scores":["1"],"set":{"brief":%s}%s}]}`,
		space, id, valueToken, extra)
}

func validatorValueToken(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func validatorRequireCreate(t *testing.T, fx *tsetFixture, raw, id, value string) {
	t.Helper()
	var got Reply
	if err := json.Unmarshal(decimalRawStep(t, fx, raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" || got.Changed != 1 || got.Replay {
		t.Fatalf("valid raw create %s: %+v", id, got)
	}
	stored, err := fx.Client.HGet(context.Background(), fixtureRecordKey(fx.Space, "work", id), "brief").Result()
	if err != nil || stored != value {
		t.Fatalf("raw create %s stored %q, want %q: %v", id, stored, value, err)
	}
}

func TestValidatorNativeScanWireSemantics(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "ready")
	fx.AddRow(t, "work", "r", 0)
	fx.Activate(t)

	// Both odd and even backslash runs near a closing quote must leave the
	// raw number scanner aligned with the next JSON token. The stored bytes
	// also prove that cjson decoded exactly the intended escape sequence.
	valid := []struct{ id, value string }{
		{"slash-quote", strings.Repeat("a", 4096) + `\"tail`},
		{"slash-end", strings.Repeat("b", 4096) + `\\`},
		{"utf8-mixed", strings.Repeat("c", 4096) + "é雪🙂"},
		{"utf8-max-field", strings.Repeat("d", 65536-len("é雪🙂")) + "é雪🙂"},
		{"utf8-first-two", "pre\u0080post"},
		{"utf8-last-two", "pre\u07ffpost"},
		{"utf8-first-three", "pre\u0800post"},
		{"utf8-before-surrogates", "pre\ud7ffpost"},
		{"utf8-after-surrogates", "pre\ue000post"},
		{"utf8-last-three", "pre\uffffpost"},
		{"utf8-first-four", "pre\U00010000post"},
		{"utf8-last-four", "pre\U0010ffffpost"},
	}
	for _, tc := range valid {
		raw := validatorCreateRaw(fx.Space, tc.id, validatorValueToken(t, tc.value), "")
		validatorRequireCreate(t, fx, raw, tc.id, tc.value)
	}

	// Invalid bytes sit after a long ASCII run, where the new native search
	// must stop and invoke exactly the old multibyte boundary rules.
	invalid := []struct{ name, suffix string }{
		{"isolated-continuation", string([]byte{0x80})},
		{"overlong-lead", string([]byte{0xc0, 0xaf})},
		{"truncated-three", string([]byte{0xe2, 0x82})},
		{"truncated-four", string([]byte{0xf0, 0x9f, 0x92})},
		{"bad-continuation", string([]byte{0xe2, 0x28, 0xa1})},
		{"surrogate", string([]byte{0xed, 0xa0, 0x80})},
		{"above-maximum", string([]byte{0xf4, 0x90, 0x80, 0x80})},
		{"invalid-five-lead", string([]byte{0xf5, 0x80, 0x80, 0x80})},
		{"invalid-ff-lead", string([]byte{0xff})},
	}
	for _, tc := range invalid {
		token := `"` + strings.Repeat("x", 4096) + tc.suffix + `"`
		decimalRawStepRefusal(t, fx, validatorCreateRaw(fx.Space, tc.name, token, ""), "REQUEST")
	}
	for _, token := range []string{`"bad\q"`, `"bad\"`} {
		decimalRawStepRefusal(t, fx, validatorCreateRaw(fx.Space, "bad-escape", token, ""), "REQUEST")
	}

	// The escaped key must be decoded before the duplicate-key last-wins
	// precision guard and the opaque metadata exemption are applied.
	count := func(maxes string) string {
		return fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"count","t":"work","cells":["r:ready"],%s}]}`,
			fx.Space, maxes)
	}
	decimalRawStepOK(t, fx, count(`"max":[100.00000000000000001],"m\u0061x":[100]`))
	decimalRawStepRefusal(t, fx, count(`"max":[100],"m\u0061x":[100.00000000000000001]`), "REQUEST")
	meta := `,"meta":{"fraction":1.00000000000000001},"\u006deta":{"fraction":2.00000000000000001}`
	validatorRequireCreate(t, fx, validatorCreateRaw(fx.Space, "escaped-meta", `"ok"`, meta), "escaped-meta", "ok")
}

const validatorScanProbeName = "ns_tset_scan_probe"

const validatorScanProbeLua = `
local S=NS.tset
redis.register_function('ns_tset_scan_probe',function(keys,args)
  if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
  local before=NS.__validator_scan_counts()
  local request,err=S.validate(args[1],args[2],'step')
  local after=NS.__validator_scan_counts()
  return S.json.encode({accepted=err==nil,code=err and err.code or nil,
    utf8=after.utf8-before.utf8,quoted=after.quoted-before.quoted})
end)
`

// Count Lua loop iterations, not elapsed time or native string.find bytes.
// The same anchors match the original and candidate scans; the old per-byte
// implementation is therefore a deterministic negative control.
func validatorCountedSource(t *testing.T) string {
	t.Helper()
	source := tsetTestSourceWithProbe(t, fn.TSetStandalone, validatorScanProbeLua, validatorScanProbeName)
	const utfStart, utfEnd = "local function utf8_valid(s)", "S.utf8_valid = utf8_valid"
	const numStart, numEnd = "local function exact_request_numbers(raw)", "local function failure(code, detail)"
	if strings.Count(source, utfStart) != 1 || strings.Count(source, utfEnd) != 1 ||
		strings.Count(source, numStart) != 1 || strings.Count(source, numEnd) != 1 {
		t.Fatal("validator scanner instrumentation anchors changed")
	}
	source = strings.Replace(source, utfStart, "local scan_utf8_visits,scan_quote_visits=0,0\n"+utfStart, 1)
	utfAt, utfDone := strings.Index(source, utfStart), strings.Index(source, utfEnd)
	utfBody := source[utfAt:utfDone]
	if strings.Count(utfBody, "    while i <= n do\n") != 1 {
		t.Fatal("UTF-8 loop anchor changed")
	}
	utfBody = strings.Replace(utfBody, "    while i <= n do\n", "    while i <= n do\n        scan_utf8_visits=scan_utf8_visits+1\n", 1)
	source = source[:utfAt] + utfBody + source[utfDone:]
	source = strings.Replace(source, utfEnd,
		utfEnd+"\nNS.__validator_scan_counts=function() return {utf8=scan_utf8_visits,quoted=scan_quote_visits} end", 1)
	numAt, numDone := strings.Index(source, numStart), strings.Index(source, numEnd)
	numBody := source[numAt:numDone]
	quoteAt := strings.Index(numBody, `if ch == '"' then`)
	if quoteAt < 0 || strings.Count(numBody[quoteAt:], "            while i <= n do\n") != 1 {
		t.Fatal("quoted-string loop anchor changed")
	}
	prefix, quoted := numBody[:quoteAt], numBody[quoteAt:]
	quoted = strings.Replace(quoted, "            while i <= n do\n",
		"            while i <= n do\n                scan_quote_visits=scan_quote_visits+1\n", 1)
	source = source[:numAt] + prefix + quoted + source[numDone:]
	return source
}

type validatorScanCounts struct {
	Accepted bool   `json:"accepted"`
	Code     string `json:"code"`
	UTF8     int    `json:"utf8"`
	Quoted   int    `json:"quoted"`
}

func TestValidatorAsciiScanDoesNotLoopPerByteInLua(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	source := validatorCountedSource(t)
	if err := fx.Client.FunctionLoad(context.Background(), source).Err(); err != nil {
		t.Fatalf("load counted test-only validator: %v", err)
	}
	tsetRequireProbeLoaded(t, fx.Client, validatorScanProbeName)
	call := func(size int) validatorScanCounts {
		t.Helper()
		raw := validatorCreateRaw(fx.Space, "scan", validatorValueToken(t, strings.Repeat("a", size)), "")
		wire, err := fx.Client.FCall(context.Background(), validatorScanProbeName, nil, Version, raw).Text()
		if err != nil {
			t.Fatal(err)
		}
		var got validatorScanCounts
		if err := json.Unmarshal([]byte(wire), &got); err != nil || !got.Accepted || got.Code != "" {
			t.Fatalf("valid counted request: %v %s", err, wire)
		}
		return got
	}
	short, long := call(256), call(32<<10)
	if long.UTF8-short.UTF8 > 64 || long.Quoted-short.Quoted > 64 ||
		short.UTF8 == 0 || short.Quoted == 0 {
		t.Fatalf("Lua scanner loops scaled with ASCII payload: short=%+v long=%+v", short, long)
	}
}
