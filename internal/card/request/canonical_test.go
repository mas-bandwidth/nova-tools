package request

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalRoundTripProperty(t *testing.T) {
	t.Parallel()
	for _, seed := range seeds {
		seed := seed
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			for _, op := range Operations {
				for i := 0; i < 25; i++ {
					x := genRequest(rng, op)
					if err := Validate(x); err != nil {
						t.Fatalf("seed %d %s #%d: generator built an invalid request: %v\n%s", seed, op, i, err, Canonical(x))
					}
					b := Canonical(x)
					y, err := Parse(b)
					if err != nil {
						t.Fatalf("seed %d %s #%d: parse of canonical: %v\n%s", seed, op, i, err, b)
					}
					if !reflect.DeepEqual(x, y) {
						t.Fatalf("seed %d %s #%d: decode(canonical(x)) != x\n%s", seed, op, i, b)
					}
					if b2 := Canonical(y); !bytes.Equal(b, b2) {
						t.Fatalf("seed %d %s #%d: canonical not stable\n%s\n%s", seed, op, i, b, b2)
					}
					if Hash(x) != Hash(y) || !Hash(x).Valid() {
						t.Fatalf("seed %d %s #%d: hash", seed, op, i)
					}
				}
			}
		})
	}
}

func TestCanonicalIgnoresKeyOrderWhitespaceAndEscapes(t *testing.T) {
	t.Parallel()
	for _, seed := range seeds {
		seed := seed
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			for _, op := range Operations {
				x := genRequest(rng, op)
				want := Canonical(x)
				dec := json.NewDecoder(bytes.NewReader(want))
				dec.UseNumber()
				var generic any
				if err := dec.Decode(&generic); err != nil {
					t.Fatal(err)
				}
				for v := 0; v < 5; v++ {
					var sb strings.Builder
					jsonWriter(rng, &sb, generic)
					y, err := Parse([]byte(sb.String()))
					if err != nil {
						t.Fatalf("seed %d %s: variant does not parse: %v\n%s", seed, op, err, sb.String())
					}
					if got := Canonical(y); !bytes.Equal(got, want) {
						t.Fatalf("seed %d %s: variant changed the canonical bytes\n%s\n%s", seed, op, got, want)
					}
					if Hash(y) != Hash(x) {
						t.Fatalf("seed %d %s: variant changed the hash", seed, op)
					}
				}
			}
		})
	}
}

func TestCanonicalFormIsCompactSortedAndFloatFree(t *testing.T) {
	t.Parallel()
	r := validRequest(OpResolve)
	got := string(Canonical(r))
	want := `{"actor":"coordinator","epoch":"3","expected_table_revision":"12","operation":"resolve","operation_id":"op-17","schema":1,"scope":{"bound":100,"col":"waiting","row":"build"},"table":"work"}`
	if got != want {
		t.Fatalf("canonical\n got  %s\n want %s", got, want)
	}
	if strings.ContainsAny(got, " \n\t") || strings.Contains(got, ".") {
		t.Fatalf("canonical has whitespace or a decimal point: %s", got)
	}
	if b := Canonical(nil); string(b) != "null" {
		t.Fatalf("nil canonical %q", b)
	}
}

// mutateLeaf changes the n-th string or int of r to a different value and
// returns a function that puts it back, or nil when there is no n-th value.
func mutateLeaf(r *Request, n int) (restore func()) {
	i := 0
	leaves(reflect.ValueOf(r), func(v reflect.Value) {
		if i == n {
			switch v.Kind() {
			case reflect.String:
				old := v.String()
				v.SetString(old + "x")
				restore = func() { v.SetString(old) }
			case reflect.Int:
				old := v.Int()
				v.SetInt(old + 1)
				restore = func() { v.SetInt(old) }
			}
		}
		i++
	})
	return restore
}

func TestAnySingleValueMutationChangesTheHash(t *testing.T) {
	t.Parallel()
	for _, seed := range seeds {
		seed := seed
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			for _, op := range Operations {
				base := genRequest(rng, op)
				baseCanon := Canonical(base)
				baseHash := Hash(base)
				n := 0
				for ; ; n++ {
					restore := mutateLeaf(base, n)
					if restore == nil {
						break
					}
					if Hash(base) == baseHash || bytes.Equal(Canonical(base), baseCanon) {
						t.Fatalf("seed %d %s: mutating value #%d left the hash unchanged\n%s", seed, op, n, baseCanon)
					}
					restore()
				}
				if !bytes.Equal(Canonical(base), baseCanon) {
					t.Fatalf("seed %d %s: restoring the values did not restore the request", seed, op)
				}
				if n == 0 {
					t.Fatalf("seed %d %s: no values to mutate", seed, op)
				}
			}
		})
	}
}

func TestOmittedOptionalFieldsChangeTheHashWhenSet(t *testing.T) {
	t.Parallel()
	// A field that is empty and omitted differs from the same field set.
	base := validRequest(OpApplyEvents)
	h := Hash(base)
	c := clone(t, base)
	c.Events[0].Reason = "r"
	if Hash(c) == h {
		t.Fatal("setting an omitted field left the hash unchanged")
	}
}

func TestArrayOrderAndMembershipChangeTheHash(t *testing.T) {
	t.Parallel()
	base := validRequest(OpAdmit)
	h := Hash(base)
	swapped := clone(t, base)
	swapped.Admissions[0], swapped.Admissions[1] = swapped.Admissions[1], swapped.Admissions[0]
	if Hash(swapped) == h {
		t.Fatal("array order is part of the request and the hash ignored it")
	}
	dropped := clone(t, base)
	dropped.Admissions = dropped.Admissions[:1]
	if Hash(dropped) == h {
		t.Fatal("dropping an entry left the hash unchanged")
	}
	added := clone(t, base)
	added.Admissions = append(added.Admissions, adm("c9"))
	if Hash(added) == h {
		t.Fatal("adding an entry left the hash unchanged")
	}
	// An empty array and an absent one differ; both are refused, neither is the same request.
	empty := clone(t, validRequest(OpInspect))
	empty.Scope.IDs = []ID{}
	if bytes.Equal(Canonical(empty), Canonical(validRequest(OpInspect))) {
		t.Fatal("empty and non-empty arrays share canonical bytes")
	}
}

func TestSameRequestComparesCanonicalBytes(t *testing.T) {
	t.Parallel()
	a := validRequest(OpApplyEvents)
	recorded := Canonical(a)
	// The same request, reached through a differently ordered and spaced document.
	reordered := `{ "events": ` + string(mustJSON(t, a)["events"]) + `, "actor":"coordinator","table":"work","schema":1,"operation":"apply_events","operation_id":"op-17","expected_table_revision":"12","epoch":"3" }`
	b := mustParse(t, reordered)
	if !SameRequest(recorded, Canonical(b)) {
		t.Fatal("the same request through a different document is not the same request")
	}
	c := clone(t, a)
	c.Events[1].Source = "other/artifact"
	if SameRequest(recorded, Canonical(c)) {
		t.Fatal("a changed value is the same request")
	}
	c = clone(t, a)
	c.OperationID = "op-18"
	if SameRequest(recorded, Canonical(c)) {
		t.Fatal("a changed operation ID is the same request")
	}
	if SameRequest(nil, nil) || SameRequest(recorded, nil) || SameRequest(nil, recorded) || SameRequest([]byte{}, []byte{}) {
		t.Fatal("empty bytes are never the same request")
	}
	// Non-canonical bytes are not normalised: the caller canonicalises first.
	if SameRequest(recorded, []byte(string(recorded)+" ")) {
		t.Fatal("SameRequest normalised its input")
	}
	if a.Identity() != c.Identity() && a.Identity().OperationID == c.Identity().OperationID {
		t.Fatal("identity mismatch")
	}
}

func mustJSON(t *testing.T, r *Request) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(Canonical(r), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseNeverPanicsOnRandomInput(t *testing.T) {
	t.Parallel()
	for _, seed := range seeds {
		seed := seed
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			check := func(in []byte) {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("seed %d: panic %v on input %q", seed, p, in)
					}
				}()
				r, err := Parse(in)
				if err == nil {
					if verr := Validate(r); verr != nil {
						t.Fatalf("seed %d: Parse accepted what Validate refuses: %v\n%q", seed, verr, in)
					}
					y, err := Parse(Canonical(r))
					if err != nil || !reflect.DeepEqual(r, y) {
						t.Fatalf("seed %d: accepted input does not round trip: %v\n%q", seed, err, in)
					}
				} else if r != nil {
					t.Fatalf("seed %d: refused input returned a request", seed)
				}
			}
			// random bytes
			for i := 0; i < 200; i++ {
				b := make([]byte, rng.Intn(300))
				rng.Read(b)
				check(b)
			}
			// random JSON-ish text from the request's own vocabulary
			for i := 0; i < 200; i++ {
				check([]byte(randomJSON(rng, 0)))
			}
			// mutated valid documents
			for _, op := range Operations {
				valid := Canonical(genRequest(rng, op))
				for i := 0; i < 60; i++ {
					check(mutateBytes(rng, valid))
				}
			}
		})
	}
}

var vocab = []string{"schema", "operation", "table", "epoch", "expected_table_revision", "operation_id", "actor",
	"admissions", "events", "evidence", "replacements", "scope", "id", "type", "expect", "revision", "place", "row", "col",
	"digest", "issuer", "source", "head", "result", "reason", "dependency", "landing", "records", "ids", "bound", "old", "new",
	"admit", "apply_events", "inspect", "start", "ready", "work", "1", "0", "", "x y", "é", "<", "\\u0000"}

func randomJSON(rng *rand.Rand, depth int) string {
	pick := func() string { return vocab[rng.Intn(len(vocab))] }
	switch n := rng.Intn(9); {
	case depth > 6 || n < 3:
		return []string{`"` + strings.ReplaceAll(pick(), `\`, `\\`) + `"`, "1", "1.5", "-2", "1e9", "true", "null", "123456789012345678901234567890"}[rng.Intn(8)]
	case n < 6:
		var parts []string
		for i := rng.Intn(6); i > 0; i-- {
			parts = append(parts, `"`+pick()+`":`+randomJSON(rng, depth+1))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		var parts []string
		for i := rng.Intn(5); i > 0; i-- {
			parts = append(parts, randomJSON(rng, depth+1))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
}

func mutateBytes(rng *rand.Rand, in []byte) []byte {
	b := append([]byte(nil), in...)
	if len(b) == 0 {
		return b
	}
	switch rng.Intn(6) {
	case 0:
		b[rng.Intn(len(b))] = byte(rng.Intn(256))
	case 1:
		b = b[:rng.Intn(len(b))]
	case 2:
		i := rng.Intn(len(b))
		b = append(b[:i], b[i+1:]...)
	case 3:
		i := rng.Intn(len(b))
		b = append(b[:i], append([]byte{byte(rng.Intn(256))}, b[i:]...)...)
	case 4:
		i, j := rng.Intn(len(b)), rng.Intn(len(b))
		if i > j {
			i, j = j, i
		}
		b = append(b[:j], append(append([]byte(nil), b[i:j]...), b[j:]...)...)
	default:
		b = append(b, []byte(vocab[rng.Intn(len(vocab))])...)
	}
	return b
}

var nasty = []string{"", "\x00", "\xff", "é", "a b", strings.Repeat("x", 300), " ", "<>&", `"`, `\`, "0", "18446744073709551616", "�", "\n", "a,b", "..", "a/b"}

func TestValidateAndCanonicalNeverPanicOnGarbageValues(t *testing.T) {
	t.Parallel()
	for _, seed := range seeds {
		seed := seed
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			for _, op := range Operations {
				for i := 0; i < 40; i++ {
					x := genRequest(rng, op)
					// set up to three values to garbage, sometimes drop or empty an array
					for k := rng.Intn(4); k > 0; k-- {
						var n int
						leaves(reflect.ValueOf(x), func(reflect.Value) { n++ })
						if n == 0 {
							break
						}
						target, idx := rng.Intn(n), 0
						leaves(reflect.ValueOf(x), func(v reflect.Value) {
							if idx == target {
								if v.Kind() == reflect.String {
									v.SetString(nasty[rng.Intn(len(nasty))])
								} else {
									v.SetInt(int64(rng.Intn(3000) - 500))
								}
							}
							idx++
						})
					}
					switch rng.Intn(6) {
					case 0:
						x.Events, x.Admissions, x.Evidence, x.Replacements = []Event{}, []Admission{}, []Evidence{}, []Replacement{}
					case 1:
						x.Scope = &Scope{IDs: []ID{}}
					case 2:
						x.Operation = Operation(nasty[rng.Intn(len(nasty))])
					}
					func() {
						defer func() {
							if p := recover(); p != nil {
								t.Fatalf("seed %d: panic %v on %+v", seed, p, x)
							}
						}()
						err := Validate(x)
						b := Canonical(x)
						_ = Hash(x)
						if err == nil {
							y, perr := Parse(b)
							if perr != nil || !reflect.DeepEqual(x, y) {
								t.Fatalf("seed %d: a valid request does not round trip: %v\n%s", seed, perr, b)
							}
						}
					}()
				}
			}
		})
	}
}
