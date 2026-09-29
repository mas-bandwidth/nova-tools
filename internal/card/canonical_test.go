package card

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestEncodeSortsKeysAndWritesNoWhitespace(t *testing.T) {
	t.Parallel()
	got := string(Encode(Obj{"b": 2, "a": "x", "c": []any{"z", "y", uint64(18446744073709551615)}, "d": Obj{"y": true, "x": nil}}))
	want := `{"a":"x","b":2,"c":["z","y",18446744073709551615],"d":{"x":null,"y":true}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// No float: an unsupported value is null, never a number with a point.
	if got := string(Encode(Obj{"f": 1.5})); got != `{"f":null}` {
		t.Fatalf("float encoded as %s", got)
	}
}

// One string, one encoding: the same escape rule for every control character,
// in every package that uses the encoder.
func TestEncodeEscapeRule(t *testing.T) {
	t.Parallel()
	for c := rune(0); c < 0x20; c++ {
		want := fmt.Sprintf(`"\u%04x"`, c)
		if got := string(Encode(string(c))); got != want {
			t.Errorf("control %#x encodes %s, want %s", c, got, want)
		}
	}
	for s, want := range map[string]string{
		"\x7f":       `"\u007f"`,
		"\u2028":     `"\u2028"`,
		"\u2029":     `"\u2029"`,
		`"`:          `"\""`,
		`\`:          `"\\"`,
		"<>&":        `"<>&"`,
		"\n":         `"\u000a"`,
		"\t":         `"\u0009"`,
		"caf\u00e9":  "\"caf\u00e9\"",
		"\U0001f600": "\"\U0001f600\"",
		"a\xffb":     `"a\ufffdb"`,
		"\u0085":     "\"\u0085\"",
		"\u202e":     "\"\u202e\"",
		"\ufeff":     "\"\ufeff\"",
	} {
		if got := string(Encode(s)); got != want {
			t.Errorf("Encode(%q) = %s, want %s", s, got, want)
		}
	}
	// A key is escaped by the same rule.
	if got := string(Encode(Obj{"a\nb": "x"})); got != `{"a\u000ab":"x"}` {
		t.Errorf("key escape: %s", got)
	}
}

func TestSetIsSortedByTheEncoder(t *testing.T) {
	t.Parallel()
	entry := func(id string) Obj { return Obj{"id": id, "z": "1"} }
	a := Set{Key: "id", Items: []any{entry("c"), entry("a"), entry("b")}}
	b := Set{Key: "id", Items: []any{entry("b"), entry("c"), entry("a")}}
	if string(Encode(a)) != string(Encode(b)) {
		t.Fatal("two orders of one set encode differently")
	}
	if got := string(Encode(a)); got != `[{"id":"a","z":"1"},{"id":"b","z":"1"},{"id":"c","z":"1"}]` {
		t.Fatalf("sorted by id: %s", got)
	}
	// An ordered array keeps its order.
	if got := string(Encode([]any{"b", "a"})); got != `["b","a"]` {
		t.Fatalf("ordered array reordered: %s", got)
	}
	if got := string(Encode(Strings([]string{"b", "a", "c"}))); got != `["a","b","c"]` {
		t.Fatalf("strings set: %s", got)
	}
	// Equal keys tie-break on the bytes, so the encoding is total.
	x := Set{Key: "id", Items: []any{Obj{"id": "a", "v": "2"}, Obj{"id": "a", "v": "1"}}}
	y := Set{Key: "id", Items: []any{Obj{"id": "a", "v": "1"}, Obj{"id": "a", "v": "2"}}}
	if string(Encode(x)) != string(Encode(y)) {
		t.Fatal("ties are not deterministic")
	}
}

// An empty optional field and an absent one are the same bytes.
func TestEmptyOptionalIsAbsent(t *testing.T) {
	t.Parallel()
	a, b := Obj{"id": "x"}, Obj{"id": "x"}
	b.Str("head", "")
	b.OptSet("ids", Set{})
	if string(Encode(a)) != string(Encode(b)) {
		t.Fatalf("empty %s vs absent %s", Encode(b), Encode(a))
	}
	b.Str("head", "h")
	b.OptSet("ids", Strings([]string{"q"}))
	if got := string(Encode(b)); got != `{"head":"h","id":"x","ids":["q"]}` {
		t.Fatalf("set optional: %s", got)
	}
}

// Any permutation of an order-free array, however it was built, is one encoding.
func TestSetPermutationInvariantProperty(t *testing.T) {
	t.Parallel()
	for _, seed := range []int64{1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 233, 377, 610, 987, 2026} {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(20)
		var items []any
		for i := 0; i < n; i++ {
			items = append(items, Obj{"id": fmt.Sprintf("c%d", rng.Intn(8)), "v": strings.Repeat("x", rng.Intn(4)), "q": "a\"b\n"})
		}
		want := Encode(Set{Key: "id", Items: items})
		for k := 0; k < 5; k++ {
			rng.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
			if got := Encode(Set{Key: "id", Items: items}); string(got) != string(want) {
				t.Fatalf("seed %d: a permutation changed the bytes", seed)
			}
		}
	}
}
