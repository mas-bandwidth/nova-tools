//go:build functional

package fn

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

// The probe loads the production validator fragment without table storage or
// registered writers. It compares the wire decision with the target commands
// on the same isolated Redis 8.10.2 server.
func tsetValidatorProbe(t *testing.T) (*redis.Client, context.Context) {
	t.Helper()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testredis.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	info, err := c.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info, "redis_version:8.10.2") {
		t.Fatalf("tset score differential requires Redis 8.10.2, got %q", info)
	}
	fragment, err := sources.ReadFile("lua/table_set_validate.lua")
	if err != nil {
		t.Fatal(err)
	}
	source := "#!lua name=tset_validator_probe\n" +
		"local NS={tset_profile=true,tset={}}\n" +
		"function NS.tset.refuse(code,detail) return {code=code,detail=detail or {}} end\n" +
		string(fragment) + "\n" +
		"redis.register_function('ns_tset_validator_probe',function(keys,args) " +
		"local S=NS.tset; " +
		"if args[1]=='score' then return S.score(args[2]) and 1 or 0 end; " +
		"if args[1]=='bound' then return S.score_bound(args[2]) and 1 or 0 end; " +
		"if args[1]=='uint' then return S.uint(args[2]) and 1 or 0 end; " +
		"if args[1]=='next' then return S.next(args[2]) or '-' end; " +
		"if args[1]=='cmp' then return S.cmp(args[2],args[3]) end; " +
		"local value,err=S.validate('tset/1',args[2],args[1]); " +
		"if err then return err.code end; return S.json.encode(value) end)\n"
	if err := c.FunctionLoad(ctx, source).Err(); err != nil {
		t.Fatalf("load validator probe: %v", err)
	}
	return c, ctx
}

func validatorBool(t *testing.T, ctx context.Context, c *redis.Client, kind, lexeme string) bool {
	t.Helper()
	value, err := c.FCall(ctx, "ns_tset_validator_probe", nil, kind, lexeme).Int64()
	if err != nil {
		t.Fatal(err)
	}
	if value != 0 && value != 1 {
		t.Fatalf("validator %s(%q) returned %d", kind, lexeme, value)
	}
	return value == 1
}

func TestScoreParserMatchesTargetZADD(t *testing.T) {
	t.Parallel()
	c, ctx := tsetValidatorProbe(t)
	cases := []string{
		"0", "-0", "+0", "1", "+1", "-1", ".5", "1.", "1.0", "1e3", "1E-3",
		"5e-324", "4e-324", "2.2250738585072014e-308", "1.7976931348623157e308",
		"1.7976931348623158e308", "1.7976931348623159e308",
		"1.79769313486231590e308", "1.79769313486231595e308", "1.797693134862316e308",
		"-1.7976931348623159e308", "-1.797693134862316e308",
		"1e-324", "2e-324", "1e-330", "1e-340", "1e-342", "2.4703282292062327e-324",
		"1e-343", "2e-343", "9e-344", "1e-350", "1e-400", "-1e-400", "1e309", "0e400",
		"0x10", "0X1p3", " 1", "1 ", "\t1", "1e", "1e+", "nan", "1\x00junk", "",
	}
	longUnderflow := "0." + strings.Repeat("0", 330) + "1"
	cases = append(cases, longUnderflow)
	// Redis 8.10.2 accepts these nonzero decimals but stores a zero score.
	// The tset contract excludes every nonzero-to-zero score spelling.
	narrowedUnderflows := map[string]bool{
		"1e-324": true, "2e-324": true, "1e-330": true,
		"1e-340": true, "1e-342": true, "2.4703282292062327e-324": true,
		longUnderflow: true,
	}
	narrowedHex := map[string]float64{"0x10": 16, "0X1p3": 8}
	power := new(big.Int).Lsh(big.NewInt(1), 1024)
	cases = append(cases, new(big.Int).Sub(new(big.Int).Set(power), big.NewInt(1)).String(),
		power.String(), new(big.Int).Add(new(big.Int).Set(power), big.NewInt(1)).String())
	for i, lexeme := range cases {
		key := fmt.Sprintf("zadd-score-%d", i)
		_, targetErr := c.Do(ctx, "ZADD", key, lexeme, "member").Result()
		got := validatorBool(t, ctx, c, "score", lexeme)
		stored, finite := any(nil), false
		if targetErr == nil {
			var scoreErr error
			stored, scoreErr = c.Do(ctx, "ZSCORE", key, "member").Result()
			if scoreErr != nil {
				t.Fatalf("accepted ZADD %q has unreadable ZSCORE: %v", lexeme, scoreErr)
			}
			finite = finiteStoredScore(t, stored)
		}
		if narrowedUnderflows[lexeme] {
			if targetErr != nil || !finite || storedScoreNumber(t, stored) != 0 || got {
				t.Errorf("contract-narrowed underflow %q: Lua accepted=%v, Redis ZADD error=%v, stored=(%T)%v finite=%v; want Redis zero and Lua refusal", lexeme, got, targetErr, stored, stored, finite)
			}
			continue
		}
		if expected, narrow := narrowedHex[lexeme]; narrow {
			if targetErr != nil || !finite || storedScoreNumber(t, stored) != expected || got {
				t.Errorf("contract-narrowed hex %q: Lua accepted=%v, Redis ZADD error=%v, stored=(%T)%v finite=%v; want Redis score %v and Lua refusal", lexeme, got, targetErr, stored, stored, finite, expected)
			}
			continue
		}
		if strings.Contains(lexeme, "315") || len(lexeme) > 300 {
			t.Logf("edge score lexeme=%q Lua=%v RedisErr=%v ZSCORE=(%T)%v finite=%v", lexeme, got, targetErr, stored, stored, finite)
		}
		if got != finite {
			t.Errorf("score %q: Lua accepted=%v, Redis ZADD error=%v, stored=(%T)%v finite=%v", lexeme, got, targetErr, stored, stored, finite)
		}
	}
	// The protocol deliberately narrows ZADD's domain to finite decimal scores.
	for _, lexeme := range []string{"inf", "-inf", "+Infinity", "NaN", "0x1p2"} {
		if validatorBool(t, ctx, c, "score", lexeme) {
			t.Errorf("excluded score lexeme %q accepted by tset", lexeme)
		}
	}
}

func finiteStoredScore(t *testing.T, value any) bool {
	t.Helper()
	number := storedScoreNumber(t, value)
	return !math.IsInf(number, 0) && !math.IsNaN(number)
}

func storedScoreNumber(t *testing.T, value any) float64 {
	t.Helper()
	var number float64
	switch v := value.(type) {
	case float64:
		number = v
	case int64:
		number = float64(v)
	case string:
		var err error
		number, err = strconv.ParseFloat(v, 64)
		if err != nil && !math.IsInf(number, 0) {
			t.Fatalf("ZSCORE text %q is not a double: %v", v, err)
		}
	case []byte:
		var err error
		number, err = strconv.ParseFloat(string(v), 64)
		if err != nil && !math.IsInf(number, 0) {
			t.Fatalf("ZSCORE bytes %q are not a double: %v", v, err)
		}
	default:
		t.Fatalf("ZSCORE returned unexpected type %T (%v)", value, value)
	}
	return number
}

func TestTSetExactDecimalWireHelpers(t *testing.T) {
	t.Parallel()
	c, ctx := tsetValidatorProbe(t)
	for _, s := range []string{"0", "1", "9007199254740992", "9007199254740993", "18446744073709551615"} {
		if !validatorBool(t, ctx, c, "uint", s) {
			t.Errorf("valid uint64 %q refused", s)
		}
	}
	for _, s := range []string{"", "00", "+1", "-1", "1.0", "18446744073709551616"} {
		if validatorBool(t, ctx, c, "uint", s) {
			t.Errorf("invalid uint64 %q accepted", s)
		}
	}
	for input, want := range map[string]string{
		"0": "1", "9007199254740992": "9007199254740993",
		"9007199254740993":     "9007199254740994",
		"18446744073709551614": "18446744073709551615",
		"18446744073709551615": "-",
	} {
		got, err := c.FCall(ctx, "ns_tset_validator_probe", nil, "next", input).Text()
		if err != nil || got != want {
			t.Errorf("next(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	got, err := c.FCall(ctx, "ns_tset_validator_probe", nil, "cmp", "9007199254740992", "9007199254740993").Int64()
	if err != nil || got != -1 {
		t.Errorf("exact compare across 2^53 = %d, %v; want -1", got, err)
	}
}

func TestScoreBoundsMatchTargetZRANGEAndZCOUNT(t *testing.T) {
	t.Parallel()
	c, ctx := tsetValidatorProbe(t)
	if err := c.Do(ctx, "ZADD", "score-bound-target", "0", "member").Err(); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		"0", "-0", "+1", "1.5", "(1.5", ".5", "1.", "1e-400", "1e309",
		"-inf", "+inf", "inf", "INF", "infinity", "(+inf", "(-inf",
		" 1", "( 1", "1 ", "1e", "(1e", "nan", "(nan", "[1", "(", "",
		"0x1p2", "(0x1p2", "0x1", "1,2",
	}
	for _, lexeme := range cases {
		want := validatorBool(t, ctx, c, "bound", lexeme)
		_, countErr := c.Do(ctx, "ZCOUNT", "score-bound-target", lexeme, "+inf").Result()
		_, rangeErr := c.Do(ctx, "ZRANGE", "score-bound-target", lexeme, "+inf", "BYSCORE").Result()
		if (countErr == nil) != (rangeErr == nil) {
			t.Errorf("target commands disagree for bound %q: ZCOUNT %v, ZRANGE %v", lexeme, countErr, rangeErr)
		}
		if want != (countErr == nil) || want != (rangeErr == nil) {
			t.Errorf("bound %q: Lua accepted=%v, ZCOUNT error=%v, ZRANGE error=%v", lexeme, want, countErr, rangeErr)
		}
	}
	// Redis's range parser checks the first byte after conversion, so an
	// embedded NUL can alias a longer token to a shorter bound. The tset wire
	// forbids this ambiguous spelling before sending either command.
	if validatorBool(t, ctx, c, "bound", "1\x00junk") {
		t.Error("embedded-NUL score bound accepted by tset")
	}
}

func TestTSetStaticRequestShapesBeforeStoreIO(t *testing.T) {
	t.Parallel()
	c, ctx := tsetValidatorProbe(t)
	probe := func(operation, raw string) string {
		t.Helper()
		out, err := c.FCall(ctx, "ns_tset_validator_probe", nil, operation, raw).Text()
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	valid := `{"epoch":"0","space":"s:","op":"shape","intent":"static-shape","entries":[],"notes":[{"line":{"kind":"note","meta":{"empty_object":{},"empty_array":[],"nil":null}},"about":[]}]}`
	out := probe("step", valid)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("valid request rejected: %q: %v", out, err)
	}
	meta := decoded["notes"].([]any)[0].(map[string]any)["line"].(map[string]any)["meta"].(map[string]any)
	if _, ok := meta["empty_object"].(map[string]any); !ok {
		t.Fatalf("empty object lost: %q", out)
	}
	if _, ok := meta["empty_array"].([]any); !ok {
		t.Fatalf("empty array lost: %q", out)
	}
	if meta["nil"] != nil {
		t.Fatalf("null lost: %q", out)
	}
	aboutRequest := func(abouts []string) string {
		t.Helper()
		encoded, err := json.Marshal(abouts)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf(`{"epoch":"0","space":"s:","op":"about-limit","intent":"distinct notes","entries":[],"notes":[{"line":{"kind":"note","meta":{}},"about":%s}]}`, encoded)
	}
	unique := make([]string, 2001)
	for i := range unique {
		unique[i] = fmt.Sprintf("a-%04d", i)
	}
	if out := probe("step", aboutRequest(unique[:2000])); !json.Valid([]byte(out)) {
		t.Fatalf("2000 distinct note abouts rejected: %q", out)
	}
	if code := probe("step", aboutRequest(unique)); code != "LIMIT" {
		t.Fatalf("2001 distinct note abouts => %q, want LIMIT", code)
	}
	repeated := make([]string, 4000)
	for i := range repeated {
		repeated[i] = "same"
	}
	if out := probe("step", aboutRequest(repeated)); !json.Valid([]byte(out)) {
		t.Fatalf("4000 repeated note abouts rejected: %q", out)
	}
	if code := probe("step", aboutRequest(append(repeated, "same"))); code != "LIMIT" {
		t.Fatalf("4001 total note abouts => %q, want LIMIT", code)
	}
	for _, raw := range []string{
		`{"epoch":0,"space":"s:","entries":[]}`,
		`{"epoch":"0","space":"s:","entries":{}}`,
		`{"epoch":"0","space":"s:","entries":null}`,
		`{"epoch":"0","space":"s:","entries":[{"kind":"create","t":"x","to":"r:c","ids":["a"],"scores":["1e-400"]}]}`,
		`{"epoch":"0","space":"s:","op":"shape","intent":"static-shape","entries":[],"notes":[{"line":{"kind":"note","meta":[]},"about":[]}]}`,
		`{"epoch":"0","space":"s:","entries":[],"unknown":true}`,
		`{"epoch":"0","space":"s:","entries":[],"result":"\uD800"}`,
		`{"epoch":"0","space":"s:","entries":[{"kind":"rows","t":"x","add":["a\u0080"]}]}`,
		"{\"epoch\":\"0\",\"space\":\"s:\",\"entries\":[],\"result\":\"\xff\"}",
		`{"epoch":"0","space":"s:","op":"shape","intent":"static-shape","entries":[],"notes":[{"line":{"kind":"note","meta":{"x":[[[[[[[[[[[[[[[[0]]]]]]]]]]]]]]]]}},"about":[]}]}`,
	} {
		if code := probe("step", raw); code != "REQUEST" {
			t.Errorf("static request %q => %q, want REQUEST", raw, code)
		}
	}
	overlap := `{"epoch":"0","space":"s:","entries":[{"kind":"move","t":"x","from":"r:c","ids":["a"],"set":{"f":"x"},"each":[{}],"unset":["f"]}]}`
	if code := probe("step", overlap); code != "FIELDOVERLAP" {
		t.Errorf("effective set/unset overlap => %q, want FIELDOVERLAP", code)
	}
	for _, raw := range []string{
		`{"epoch":"0","space":"s:","entries":[{"kind":"count","t":"x","cells":["r:c","r:c"],"max":[2,1]}]}`,
		`{"epoch":"0","space":"s:","entries":[{"kind":"rcount","t":"x","cells":["r:c","r:c"],"min":"-inf","max":"+inf","atleast":0}]}`,
	} {
		if out := probe("step", raw); !json.Valid([]byte(out)) {
			t.Errorf("duplicate cells rejected statically: %q => %q", raw, out)
		}
	}
	for _, raw := range []string{
		`{"epoch":"0","space":"s:","queries":[{"kind":"range","t":"x","cell":"r:c","min":"1x","max":"+inf","limit":1}]}`,
		`{"epoch":"0","space":"s:","queries":[{"kind":"rcount","t":"x","cells":["r:c"],"min":"(bad","max":"+inf"}]}`,
		`{"epoch":"0","space":"s:","queries":[{"kind":"range","t":"x","cell":"r:c","min":"0","max":"1","limit":1,"fields":[]}]}`,
		`{"epoch":"0","space":"s:","mode":"page","queries":[{"kind":"rows","t":"x"}]}`,
		`{"epoch":"0","space":"s:","mode":"page","queries":[{"kind":"cardlines","abouts":["a"],"limit":1,"cursor":[]}]}`,
		`{"epoch":"0","space":"s:","mode":"page","queries":[{"kind":"lines","after_seq":"0","limit":1,"cursor":{}}]}`,
	} {
		if code := probe("read", raw); code != "REQUEST" {
			t.Errorf("static read %q => %q, want REQUEST", raw, code)
		}
	}
	tooMany := `{"epoch":"0","space":"s:","entries":[` +
		strings.Repeat(`{"kind":"advance","from":"0"},`, 256) + `{"kind":"advance","from":"0"}]}`
	if code := probe("step", tooMany); code != "LIMIT" {
		t.Errorf("257 entries => %q, want LIMIT", code)
	}
	tooManyQueries := `{"epoch":"0","space":"s:","queries":[` +
		strings.Repeat(`{"kind":"rows","t":"x"},`, 1024) + `{"kind":"rows","t":"x"}]}`
	if code := probe("read", tooManyQueries); code != "LIMIT" {
		t.Errorf("1025 read queries => %q, want LIMIT", code)
	}
	tooManyUnset := `{"epoch":"0","space":"s:","entries":[{"kind":"move","t":"x","from":"r:c","ids":["a"],"unset":[` +
		strings.Repeat(`"field",`, 7999) + `"field"]}]}`
	if code := probe("step", tooManyUnset); code != "LIMIT" {
		t.Errorf("8000 unset names => %q, want LIMIT", code)
	}
	tooManyIDs := `{"epoch":"0","space":"s:","entries":[{"kind":"guard","t":"x","from":"r:c","ids":[` +
		strings.Repeat(`"a",`, 2000) + `"a"]}]}`
	if code := probe("step", tooManyIDs); code != "LIMIT" {
		t.Errorf("2001 IDs in an entry => %q, want LIMIT", code)
	}
	if size, err := c.DBSize(ctx).Result(); err != nil || size != 0 {
		t.Fatalf("static validator wrote %d keys: %v", size, err)
	}
}
