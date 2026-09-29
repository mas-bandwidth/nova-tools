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

func mustValid(t testing.TB, r *Request) *Valid {
	t.Helper()
	v, err := Validate(r)
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, Canonical(r))
	}
	return v
}

func TestCanonicalRoundTripProperty(t *testing.T) {
	t.Parallel()
	for _, seed := range seeds {
		seed := seed
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			for _, op := range Operations() {
				for i := 0; i < 25; i++ {
					x := genRequest(rng, op)
					vx, err := Validate(x)
					if err != nil {
						t.Fatalf("seed %d %s #%d: generator built an invalid request: %v\n%s", seed, op, i, err, Canonical(x))
					}
					b := Canonical(x)
					if !bytes.Equal(vx.Canonical(), b) {
						t.Fatalf("seed %d %s #%d: Valid.Canonical differs from Canonical", seed, op, i)
					}
					vy, err := Parse(b)
					if err != nil {
						t.Fatalf("seed %d %s #%d: parse of canonical: %v\n%s", seed, op, i, err, b)
					}
					if b2 := vy.Canonical(); !bytes.Equal(b, b2) {
						t.Fatalf("seed %d %s #%d: canonical not stable\n%s\n%s", seed, op, i, b, b2)
					}
					if vx.Hash() != vy.Hash() || !vx.Hash().Valid() || Hash(x) != vx.Hash() {
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
			for _, op := range Operations() {
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
					if got := y.Canonical(); !bytes.Equal(got, want) {
						t.Fatalf("seed %d %s: variant changed the canonical bytes\n%s\n%s", seed, op, got, want)
					}
					if y.Hash() != Hash(x) {
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
	want := `{"actor":"coordinator","epoch":"3","expected_table_revision":"12","operation":"resolve","operation_id":"op-17","schema":1,"scope":{"rows":["build"]},"table":"work"}`
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
			for _, op := range Operations() {
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

// Arrays whose order carries no meaning are sorted by the encoder: two requests
// that list the same entries in another order are the same request.
func TestOrderFreeArraysHaveOneEncoding(t *testing.T) {
	t.Parallel()
	swap := func(name string, build func() *Request, reorder func(*Request)) {
		a := build()
		b := build()
		reorder(b)
		if reflect.DeepEqual(a, b) {
			t.Fatalf("%s: the reordering changed nothing", name)
		}
		if !bytes.Equal(Canonical(a), Canonical(b)) || Hash(a) != Hash(b) {
			t.Errorf("%s: the same entries in another order have other bytes\n%s\n%s", name, Canonical(a), Canonical(b))
		}
		// and both are valid requests with one hash
		if mustValid(t, a).Hash() != mustValid(t, b).Hash() {
			t.Errorf("%s: validated hashes differ", name)
		}
	}
	swap("admissions", func() *Request { return validRequest(OpAdmit) }, func(r *Request) { r.Admissions[0], r.Admissions[1] = r.Admissions[1], r.Admissions[0] })
	swap("inputs", func() *Request { return validRequest(OpApplyEvents) }, func(r *Request) { r.Inputs[0], r.Inputs[2] = r.Inputs[2], r.Inputs[0] })
	swap("evidence entries", func() *Request {
		r := validRequest(OpRecordEvidence)
		r.Evidence = append(r.Evidence, evidenceEntry("c2"))
		return r
	}, func(r *Request) { r.Evidence[0], r.Evidence[1] = r.Evidence[1], r.Evidence[0] })
	swap("evidence records", func() *Request { return validRequest(OpRecordEvidence) }, func(r *Request) {
		rs := r.Evidence[0].Records
		rs[0], rs[1] = rs[1], rs[0]
	})
	swap("replacements", func() *Request {
		r := validRequest(OpReplace)
		r.Replacements = append(r.Replacements, Replacement{Old: Retired{ID: "old2", Digest: Digest(dig), Expect: expect("2", "build", Waiting)}, New: adm("new2")})
		return r
	}, func(r *Request) { r.Replacements[0], r.Replacements[1] = r.Replacements[1], r.Replacements[0] })
	swap("scope ids", func() *Request { return validRequest(OpInspect) }, func(r *Request) { r.Scope.IDs[0], r.Scope.IDs[1] = r.Scope.IDs[1], r.Scope.IDs[0] })
	swap("scope rows", func() *Request {
		r := validRequest(OpResolve)
		r.Scope.Rows = []string{"build", "docs"}
		return r
	}, func(r *Request) { r.Scope.Rows[0], r.Scope.Rows[1] = r.Scope.Rows[1], r.Scope.Rows[0] })
	swap("dependencies", func() *Request {
		r := validRequest(OpAdmit)
		r.Admissions[0].DependsOn = []ID{"x1", "x2", "x3"}
		return r
	}, func(r *Request) { d := r.Admissions[0].DependsOn; d[0], d[2] = d[2], d[0] })
	// An array whose order does mean something is not sorted: there is none in a
	// request, so a receipt's notifications are the one place to check.
	rc := &Receipt{Changed: []CardChange{{ID: "a", Notifications: []Notification{{Kind: NoteReady}, {Kind: NoteStarted}}}}}
	rd := &Receipt{Changed: []CardChange{{ID: "a", Notifications: []Notification{{Kind: NoteStarted}, {Kind: NoteReady}}}}}
	if bytes.Equal(CanonicalReceipt(rc), CanonicalReceipt(rd)) {
		t.Error("the order of a card's notifications is the manager's derivation order and is kept")
	}
}

// An empty optional field and an absent one are the same bytes, and the doc says so.
func TestEmptyOptionalFieldIsAbsent(t *testing.T) {
	t.Parallel()
	base := func() *Request {
		r := validRequest(OpApplyEvents)
		r.Inputs[0].Head = ""
		return r
	}
	a := Canonical(base())
	// an explicit empty string in the document: the same bytes as the key left out
	doc := strings.Replace(string(Canonical(base())), `"id":"c1"`, `"id":"c1","head":"","reason":"","landing":"","dependency":"","result":""`, 1)
	v, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("explicit empty optional fields refused: %v", err)
	}
	if !bytes.Equal(v.Canonical(), a) {
		t.Fatalf("explicit empty differs from absent:\n%s\n%s", v.Canonical(), a)
	}
	// operation_id, entry, depends_on, expect.revision of evidence
	adm := validRequest(OpAdmit)
	adm.OperationID = ""
	adm.Admissions[0].Entry = ""
	adm.Admissions[0].DependsOn = []ID{}
	adm2 := validRequest(OpAdmit)
	adm2.OperationID = ""
	adm2.Admissions[0].DependsOn = nil
	if !bytes.Equal(Canonical(adm), Canonical(adm2)) {
		t.Errorf("an empty depends_on and an absent one differ")
	}
	ev := validRequest(OpRecordEvidence)
	ev.Evidence[0].Expect.Revision = ""
	if strings.Contains(string(Canonical(ev)), `"revision"`) {
		t.Errorf("an empty revision is written: %s", Canonical(ev))
	}
	sc := validRequest(OpInspect)
	sc.Scope = &Scope{IDs: []ID{"a"}, Rows: []string{}}
	sc2 := validRequest(OpInspect)
	sc2.Scope = &Scope{IDs: []ID{"a"}}
	if !bytes.Equal(Canonical(sc), Canonical(sc2)) {
		t.Errorf("an empty rows array and an absent one differ")
	}
}

// The hash without the operation ID ignores the ID and nothing else.
func TestHashWithoutOperationID(t *testing.T) {
	t.Parallel()
	a, b := validRequest(OpAdmit), validRequest(OpAdmit)
	b.OperationID = "op-other"
	if Hash(a) == Hash(b) {
		t.Fatal("the full hash ignores the operation ID")
	}
	if HashWithoutOperationID(a) != HashWithoutOperationID(b) {
		t.Fatal("the hash without the operation ID depends on it")
	}
	if bytes.Contains(CanonicalWithoutOperationID(a), []byte("operation_id")) {
		t.Fatal("operation_id in the canonical bytes without it")
	}
	// Absent operation ID: the two hashes are the same bytes.
	c := validRequest(OpAdmit)
	c.OperationID = ""
	if Hash(c) != HashWithoutOperationID(c) {
		t.Fatal("with no operation ID the two hashes are one")
	}
	if HashWithoutOperationID(a) != HashWithoutOperationID(c) {
		t.Fatal("the same ask differs by operation ID")
	}
	d := validRequest(OpAdmit)
	d.Admissions[0].Title = "another"
	if HashWithoutOperationID(a) == HashWithoutOperationID(d) {
		t.Fatal("a different ask has the same hash")
	}
	if HashWithoutOperationID(nil) == "" || string(CanonicalWithoutOperationID(nil)) != "null" {
		t.Fatal("nil")
	}
}
