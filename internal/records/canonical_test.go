package records

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) Value {
	t.Helper()
	v, err := parseStrict([]byte(s), nil, "body")
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

// The digest is over canonical bytes, so key order and whitespace are irrelevant to
// identity and a changed value is not.
func TestCanonicalIdentityIgnoresOrderAndSpace(t *testing.T) {
	for _, tc := range []struct {
		name, a, b string
		same       bool
	}{
		{"reordered keys", `{"b":"2","a":"1"}`, `{"a":"1","b":"2"}`, true},
		{"whitespace", "{\n  \"a\": \"1\"\n}", `{"a":"1"}`, true},
		{"nested reorder", `{"x":{"q":"1","p":"2"}}`, `{"x":{"p":"2","q":"1"}}`, true},
		{"escaped vs literal non-ASCII", `{"a":"é"}`, "{\"a\":\"é\"}", true},
		{"changed value", `{"a":"1"}`, `{"a":"2"}`, false},
		{"array order", `{"a":["1","2"]}`, `{"a":["2","1"]}`, false},
		{"non-ASCII label", "{\"friend\":\"rôwan\"}", `{"friend":"rowan"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ida, err := ContentID(mustParse(t, tc.a))
			if err != nil {
				t.Fatal(err)
			}
			idb, err := ContentID(mustParse(t, tc.b))
			if err != nil {
				t.Fatal(err)
			}
			if (ida == idb) != tc.same {
				t.Fatalf("same=%v, want %v (%s vs %s)", ida == idb, tc.same, ida, idb)
			}
		})
	}
}

func TestCanonicalBytes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"b":"2","a":"1"}`, `{"a":"1","b":"2"}`},
		// a non-ASCII character is written literally as UTF-8, never re-escaped
		{`{"a":"é"}`, "{\"a\":\"é\"}"},
		// the five short escapes stay short
		{`{"a":"x\ty"}`, `{"a":"x\ty"}`},
		{`{"a":"x\ny"}`, `{"a":"x\ny"}`},
		// every other control character is \u00xx, lowercase hex
		{`{"a":"x\u001fy"}`, `{"a":"x\u001fy"}`},
		{`{"a":"\u0000\u0007"}`, `{"a":"\u0000\u0007"}`},
		// the quote and the backslash
		{`{"a":"\"\\"}`, `{"a":"\"\\"}`},
		{`{"a":["1",{"z":"9","y":"8"}],"A":null}`, `{"A":null,"a":["1",{"y":"8","z":"9"}]}`},
		{`{"t":true,"f":false,"n":null}`, `{"f":false,"n":null,"t":true}`},
	} {
		got, err := Canonicalize(mustParse(t, tc.in))
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if string(got) != tc.want {
			t.Fatalf("%s\n got %s\nwant %s", tc.in, got, tc.want)
		}
		if strings.HasSuffix(string(got), "\n") {
			t.Fatalf("canonical bytes carry no trailing newline")
		}
	}
}

// RFC 8785 sorts member names by UTF-16 code units, which differs from Go's byte order
// exactly where a supplementary character meets one in U+E000..U+FFFF.
func TestUTF16KeyOrder(t *testing.T) {
	// U+10000 encodes as the surrogates D800 DC00, so it sorts BELOW U+E000 in UTF-16 and
	// above it in UTF-8. A canonicaliser that used Go's string order would swap these two.
	const astral, private = "\U00010000", "\uE000"
	if !lessUTF16(astral, private) {
		t.Fatalf("UTF-16 order not applied to member names")
	}
	if astral < private {
		t.Fatalf("the test's premise is wrong: Go byte order would agree")
	}
	got, err := Canonicalize(mustParse(t, "{\"\uE000\":\"1\",\"\U00010000\":\"2\"}"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"" + astral + "\":\"2\",\"" + private + "\":\"1\"}"; string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestParseRefusals(t *testing.T) {
	for _, tc := range []struct{ name, in, rule string }{
		{"duplicate key", `{"a":"1","a":"2"}`, RuleDuplicateKey},
		{"nested duplicate", `{"x":{"a":"1","a":"2"}}`, RuleDuplicateKey},
		{"raw number", `{"a":1}`, RuleRawJSONNumber},
		{"raw number in array", `{"a":["1",2]}`, RuleRawJSONNumber},
		{"lone high surrogate", `{"a":"\ud800"}`, RuleLoneSurrogate},
		{"lone low surrogate", `{"a":"\udc00"}`, RuleLoneSurrogate},
		{"high surrogate then plain", `{"a":"\ud800x"}`, RuleLoneSurrogate},
		{"trailing content", `{"a":"1"} {}`, RuleNotJSON},
		{"truncated", `{"a":`, RuleNotJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStrict([]byte(tc.in), nil, "body")
			r, ok := err.(*Refusal)
			if !ok {
				t.Fatalf("want a refusal, got %v", err)
			}
			if r.Rule != tc.rule {
				t.Fatalf("refused by %s, want %s", r.Rule, tc.rule)
			}
		})
	}
	// A paired surrogate is a real character and is not refused.
	if _, err := parseStrict([]byte(`{"a":"😀"}`), nil, "body"); err != nil {
		t.Fatalf("a valid surrogate pair was refused: %v", err)
	}
	if _, err := parseStrict([]byte{'{', '"', 'a', '"', ':', '"', 0xff, '"', '}'}, nil, "body"); err == nil {
		t.Fatalf("invalid UTF-8 was accepted")
	}
}

// Seal writes the envelope the format specifies and nothing else: two members, the ID
// derived from the body alone, and no trailing newline.
func TestSealShape(t *testing.T) {
	body := mustParse(t, `{"b":"2","a":"1"}`)
	out, id, err := Seal(body)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"` + id + `","body":{"a":"1","b":"2"}}`
	if string(out) != want {
		t.Fatalf("got %s want %s", out, want)
	}
	// The envelope ID is excluded from its own digest: only the body is hashed, so
	// re-sealing the sealed body reproduces the same ID.
	again, id2, err := Seal(mustParse(t, string(out[len(`{"id":"`)+len(id)+len(`","body":`):len(out)-1])))
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id {
		t.Fatalf("the ID is not a function of the body alone: %s then %s", id, id2)
	}
	if string(again) != want {
		t.Fatalf("sealing is not idempotent")
	}
}
