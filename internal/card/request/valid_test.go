package request

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// A Valid is the only thing later layers are handed, and it satisfies one
// interface for every operation.
func TestValidSatisfiesOneInterfaceForEveryOperation(t *testing.T) {
	t.Parallel()
	for _, op := range Operations() {
		var c Checked = mustValid(t, validRequest(op))
		if c.Kind() != op || c.Table() != "work" || !c.Hash().Valid() || !c.HashWithoutOperationID().Valid() || len(c.Canonical()) == 0 {
			t.Errorf("%s: %+v", op, c)
		}
		if op.Mutating() {
			if c.Epoch() != "3" || c.ObservedRevision() != "12" || c.Actor() != "coordinator" || c.OperationID() != "op-17" {
				t.Errorf("%s: envelope %s %s %s %s", op, c.Epoch(), c.ObservedRevision(), c.Actor(), c.OperationID())
			}
		} else if c.Epoch() != "" || c.ObservedRevision() != "" || c.Actor() != "" || c.OperationID() != "" {
			t.Errorf("%s: a read has an envelope: %+v", op, c)
		}
		if op == OpCheck && len(c.Cards()) != 0 {
			t.Errorf("a check names cards: %v", c.Cards())
		}
	}
}

// Validate returns a copy: whoever built the Request cannot change what was
// validated, and the canonical bytes are a copy too.
func TestValidHoldsACopy(t *testing.T) {
	t.Parallel()
	r := validRequest(OpAdmit)
	v := mustValid(t, r)
	want := v.Canonical()
	r.Admissions[0].Title = "changed after validation"
	r.Admissions[0].DependsOn = append(r.Admissions[0].DependsOn, "x")
	r.Table = "other"
	if !bytes.Equal(v.Canonical(), want) || v.Table() != "work" {
		t.Fatal("the validated request changed with its source")
	}
	c := v.Canonical()
	c[0] = 'X'
	if !bytes.Equal(v.Canonical(), want) {
		t.Fatal("Canonical returns the internal bytes")
	}
	q := v.Request()
	q.Admissions[0].Title = "x"
	if !bytes.Equal(v.Canonical(), want) || v.Request().Admissions[0].Title == "x" {
		t.Fatal("Request returns the internal value")
	}
	// A Valid cannot be built from a refused request.
	r2 := validRequest(OpAdmit)
	r2.Table = ""
	if v, err := Validate(r2); v != nil || err == nil {
		t.Fatal("a refused request produced a Valid")
	}
}

// An absent operation ID is derived from the hash without it, so the same
// request asked again is the same operation; a different one is not.
func TestOperationIDIsOptionalAndDerived(t *testing.T) {
	t.Parallel()
	a, b := validRequest(OpApplyEvents), validRequest(OpApplyEvents)
	a.OperationID, b.OperationID = "", ""
	va, vb := mustValid(t, a), mustValid(t, b)
	if va.OperationID() == "" || va.OperationID() != vb.OperationID() || !strings.HasPrefix(va.OperationID(), "op-") || len(va.OperationID()) != 19 {
		t.Fatalf("derived IDs: %q %q", va.OperationID(), vb.OperationID())
	}
	if va.OperationID() != DefaultOperationID(va.HashWithoutOperationID()) {
		t.Fatal("the derived ID is not DefaultOperationID of the hash")
	}
	b.Inputs[0].Head = ""
	b.Inputs[1].Head = g64
	if mustValid(t, b).OperationID() == va.OperationID() {
		t.Fatal("a different request has the same derived ID")
	}
	c := validRequest(OpApplyEvents) // gives op-17
	if vc := mustValid(t, c); vc.OperationID() != "op-17" || vc.HashWithoutOperationID() != va.HashWithoutOperationID() || vc.Hash() == va.Hash() {
		t.Fatalf("given ID: %q", vc.OperationID())
	}
	// The identity a replay is looked up by uses the effective ID.
	if id := va.Identity(); id.OperationID != va.OperationID() || id.Table != "work" || id.Epoch != "3" {
		t.Fatalf("identity %+v", id)
	}
	// A read has none, given or derived.
	if v := mustValid(t, validRequest(OpInspect)); v.OperationID() != "" {
		t.Fatal("an inspect has an operation ID")
	}
}

func TestCardsEnumeratesEveryCardWithItsRole(t *testing.T) {
	t.Parallel()
	ids := func(cs []CardRef) []string {
		var out []string
		for _, c := range cs {
			out = append(out, string(c.ID)+"/"+string(c.Role))
		}
		return out
	}
	// admit: the admissions are changed with no expectation; the prerequisites
	// outside the request are dependencies, once each; one inside is not.
	r := validRequest(OpAdmit)
	r.Admissions[0].DependsOn = []ID{"x2", "x1", "c2"}
	r.Admissions[1].DependsOn = []ID{"x1", "x3"}
	cs := mustValid(t, r).Cards()
	want := []string{"c1/changed", "c2/changed", "x1/dependency", "x2/dependency", "x3/dependency"}
	if !reflect.DeepEqual(ids(cs), want) {
		t.Fatalf("admit cards = %v, want %v", ids(cs), want)
	}
	for _, c := range cs {
		if c.Expect != nil {
			t.Errorf("an admission or a dependency has an expectation: %+v", c)
		}
	}
	// inputs: changed with the expected revision and place; a dependency-failed
	// input names its failed prerequisite.
	r = validRequest(OpApplyEvents)
	r.Inputs = append(r.Inputs, input(InDependencyFailed, "c9"))
	cs = mustValid(t, r).Cards()
	want = []string{"c1/changed", "c2/changed", "c3/changed", "c9/changed", "dep-c9/dependency"}
	if !reflect.DeepEqual(ids(cs), want) {
		t.Fatalf("input cards = %v, want %v", ids(cs), want)
	}
	if cs[0].Expect == nil || *cs[0].Expect != expect("2", "build", Ready) || cs[4].Expect != nil {
		t.Fatalf("expectations: %+v %+v", cs[0], cs[4])
	}
	// evidence: changed, with the place and the revision where given.
	r = validRequest(OpRecordEvidence)
	r.Evidence[0].Expect.Revision = ""
	cs = mustValid(t, r).Cards()
	if len(cs) != 1 || cs[0].Role != RoleChanged || cs[0].Expect == nil || cs[0].Expect.Revision != "" || cs[0].Expect.Place.Col != Review {
		t.Fatalf("evidence cards = %+v", cs)
	}
	// replace: the old side with its expectation, the new side without, the new
	// side's outside prerequisites as dependencies.
	r = validRequest(OpReplace)
	r.Replacements[0].New.DependsOn = []ID{"x1"}
	cs = mustValid(t, r).Cards()
	want = []string{"new1/new", "old1/old", "x1/dependency"}
	if !reflect.DeepEqual(ids(cs), want) || cs[1].Expect == nil || cs[1].Expect.Revision != "2" || cs[0].Expect != nil {
		t.Fatalf("replace cards = %v %+v", ids(cs), cs)
	}
	// scope: IDs are guard-only; rows and the whole table name none.
	if cs := mustValid(t, validRequest(OpInspect)).Cards(); !reflect.DeepEqual(ids(cs), []string{"c1/guard-only", "c2/guard-only"}) {
		t.Fatalf("inspect cards = %v", ids(cs))
	}
	if cs := mustValid(t, validRequest(OpResolve)).Cards(); len(cs) != 0 {
		t.Fatalf("a row scope names cards: %v", cs)
	}
	// a copy: changing what was returned changes nothing
	v := mustValid(t, validRequest(OpApplyEvents))
	c1 := v.Cards()
	c1[0].Expect.Revision = "99"
	if v.Cards()[0].Expect.Revision != "2" {
		t.Fatal("Cards returns the internal expectations")
	}
	// order-free: the same request in another order enumerates the same cards
	a, b := validRequest(OpApplyEvents), validRequest(OpApplyEvents)
	b.Inputs[0], b.Inputs[2] = b.Inputs[2], b.Inputs[0]
	if !reflect.DeepEqual(mustValid(t, a).Cards(), mustValid(t, b).Cards()) {
		t.Fatal("Cards depends on the order of the entries")
	}
}

// SameRequest takes the incoming request as a validated one and the recorded
// bytes, and refuses recorded bytes that are not their own canonical form.
func TestSameRequestChecksTheRecordedBytesReCanonicalise(t *testing.T) {
	t.Parallel()
	v := mustValid(t, validRequest(OpApplyEvents))
	rec := v.Canonical()
	if !SameRequest(rec, v) {
		t.Fatal("a request differs from its own canonical bytes")
	}
	// the same entries in another order are the same request
	r := validRequest(OpApplyEvents)
	r.Inputs[0], r.Inputs[1] = r.Inputs[1], r.Inputs[0]
	if !SameRequest(rec, mustValid(t, r)) {
		t.Fatal("a reordered request is a different request")
	}
	// another request is not
	r = validRequest(OpApplyEvents)
	r.Inputs[0].Source = "artifact:other"
	if SameRequest(rec, mustValid(t, r)) {
		t.Fatal("a changed request is the same request")
	}
	// a different operation ID is a different request (the full bytes)
	r = validRequest(OpApplyEvents)
	r.OperationID = "op-18"
	if SameRequest(rec, mustValid(t, r)) {
		t.Fatal("another operation ID is the same request")
	}
	// recorded bytes that are not canonical do not stand for the request, though
	// they parse to it: whitespace, key order, an escape
	pretty := []byte(strings.Replace(string(rec), `{"actor"`, "{ \"actor\"", 1))
	if SameRequest(pretty, v) {
		t.Fatal("non-canonical recorded bytes matched")
	}
	shuffled := []byte(strings.Replace(string(rec), `"operation":"apply_events",`, "", 1))
	if SameRequest(shuffled, v) {
		t.Fatal("recorded bytes that are another request matched")
	}
	for name, b := range map[string][]byte{"nil": nil, "empty": {}, "garbage": []byte("x"), "null": []byte("null"), "an object": []byte("{}"),
		"unsorted arrays": []byte(strings.Replace(string(rec), `"id":"c1"`, `"id":"c9"`, 1))} {
		if SameRequest(b, v) {
			t.Errorf("%s matched", name)
		}
	}
	if SameRequest(rec, nil) {
		t.Fatal("a nil incoming request matched")
	}
	// a recorded request whose arrays are out of order re-canonicalises to
	// something else, so it is refused even when it holds the same entries
	unsorted := mustValid(t, validRequest(OpAdmit))
	raw := unsorted.Request()
	raw.Admissions[0], raw.Admissions[1] = raw.Admissions[1], raw.Admissions[0]
	// build the bytes by hand with the entries in the swapped order
	swapped := swapFirstTwo(t, unsorted.Canonical(), `"admissions":[`)
	if SameRequest(swapped, unsorted) {
		t.Fatal("recorded bytes with unsorted arrays matched")
	}
}

// swapFirstTwo swaps the first two top-level objects of the array that follows
// the marker, in canonical bytes.
func swapFirstTwo(t *testing.T, canon []byte, marker string) []byte {
	t.Helper()
	s := string(canon)
	i := strings.Index(s, marker) + len(marker)
	depth, ends := 0, []int{}
	for j := i; j < len(s) && len(ends) < 2; j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				ends = append(ends, j+1)
			}
		}
	}
	first, second := s[i:ends[0]], s[ends[0]+1:ends[1]]
	return []byte(s[:i] + second + "," + first + s[ends[1]:])
}
