package records

import (
	"path/filepath"
	"strings"
	"testing"
)

// The construction side of the boundary. Until this file there was no way for a caller
// OUTSIDE this package to build a body at all: Object's keys and vals are private and its
// only setter is unexported, Canonicalize and Seal take a Value that in practice can only
// come from parseStrict, and parseStrict is unexported too. So an adapter had exactly two
// choices, and both are wrong. It could assemble the JSON bytes by hand -- the format's
// "Never serialize an arbitrary source object" turned into string concatenation in every
// adapter, each with its own escaping bug. Or it could move inside this package to reach
// the private fields, which is the one thing the record contract's owner asked not to
// happen ("Do not put provider adapters inside records merely to reach private fields").
//
// These tests pin the third way: a typed Observation goes back to the wire through the
// inverse of the function that read it, and the wire it produces is byte-identical to the
// wire it came from. That identity is the whole property -- it is what makes the encoder
// safe to hand to five adapters at once, because a round trip that changes one byte
// changes the record's ID, and an ID that moves is a second identity for one spend event.

// TestEveryValidFixtureRoundTripsThroughTheTypedEncoder is the load-bearing one. For every
// accepted fixture: validate it, throw the parse tree away, encode the TYPED observation
// back to a body, and require the same canonical bytes and the same ID the sidecar states.
//
// It is deliberately driven by the fixtures rather than by a hand-written observation: a
// fixture per source kind already exists, they carry the corners (an integer above 2^53, a
// present zero, an interval aggregate, reordered keys, a model split), and a round trip
// that holds over all seven is a statement about the schema rather than about one example.
func TestEveryValidFixtureRoundTripsThroughTheTypedEncoder(t *testing.T) {
	checked := 0
	for _, path := range fixtures(t, "valid") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, want := readFixture(t, path)
			v := validatorFor(t, raw)
			env, err := v.ValidateEnvelope(raw)
			if err != nil {
				t.Fatalf("the fixture no longer validates: %v", err)
			}
			// The bytes the fixture's own body seals to: the baseline both sides must meet.
			wantBytes, wantID, err := Seal(env.Body)
			if err != nil {
				t.Fatalf("sealing the fixture's own body: %v", err)
			}
			if wantID != want.ID {
				t.Fatalf("the fixture's body seals to %s, its sidecar says %s", wantID, want.ID)
			}

			// And now from the TYPED observation alone. env.Body is not consulted.
			gotBytes, gotID, err := v.SealObservation(*env.Observation)
			if err != nil {
				t.Fatalf("SealObservation refused an observation this package had just accepted: %v", err)
			}
			if gotID != wantID {
				t.Errorf("the encoder produced ID %s, the wire says %s; a round trip that moves the ID makes a second identity for one spend event", gotID, wantID)
			}
			if string(gotBytes) != string(wantBytes) {
				t.Errorf("the encoder's bytes are not the wire's bytes\n got: %s\nwant: %s", gotBytes, wantBytes)
			}
			checked++
		})
	}
	if checked == 0 {
		t.Fatal("no valid fixture round-tripped; this test was looking in the wrong place and would have passed by checking nothing")
	}
}

// The builder's one rule, and the reason it returns an error at all: parseStrict refuses a
// duplicate member on the way IN, so a builder that quietly kept the last of two would let
// a writer produce a body its own reader refuses.
func TestTheBuilderRefusesADuplicateMember(t *testing.T) {
	o := NewObject()
	if err := o.Set("schema", SchemaObservation); err != nil {
		t.Fatalf("the first Set: %v", err)
	}
	err := o.Set("schema", "nova.tokens.observation/3")
	if err == nil {
		t.Fatal("the builder took one member name twice; parseStrict refuses exactly that on the way in")
	}
	var ref *Refusal
	if !asRefusal(err, &ref) || ref.Rule != RuleDuplicateKey {
		t.Errorf("the refusal is %v, want rule %s", err, RuleDuplicateKey)
	}
	// And the first value stands: a refused Set changes nothing.
	if v, _ := o.Get("schema"); v != SchemaObservation {
		t.Errorf("the refused Set overwrote the member: %v", v)
	}
}

// A member set to a Go value this format has no wire form for is refused at the builder
// rather than at the digest. A map reaches the canonicaliser as "no canonical form", which
// is a true refusal in the wrong place: by then the caller has lost which member it was.
func TestTheBuilderNamesTheMemberWithNoWireForm(t *testing.T) {
	o := NewObject()
	err := o.Set("raw_usage", map[string]string{"input": "1000"})
	if err == nil {
		t.Fatal("the builder took a Go map; a map has no canonical form and no key order to hash")
	}
	if !strings.Contains(err.Error(), "raw_usage") {
		t.Errorf("the refusal does not name the member: %v", err)
	}
}

// The encoder is not a way around the boundary. An adapter that builds an observation whose
// raw_usage names a field its mapping does not allow is refused by the same rule, with the
// same field named, as if it had arrived over the wire -- because it IS validated over the
// wire: SealObservation seals and then reads its own bytes back through ValidateEnvelope.
func TestAnEncodedRecordCannotCarryAFieldItsMappingDoesNotAllow(t *testing.T) {
	v := NewValidator(Allowlists{RawUsageFields: []string{"input"}, ReceiptFields: []string{"turn_id"}})
	obs := minimalObservation()
	obs.RawUsage["cost_usd_ticks"] = RawField{
		Presence: "present", Value: strptr("17"), NumberKind: "integer", Unit: "ticks",
	}
	_, _, err := v.SealObservation(obs)
	if err == nil {
		t.Fatal("an encoded record carried a field outside its mapping's allowlist")
	}
	var ref *Refusal
	if !asRefusal(err, &ref) || ref.Rule != RuleFieldNotAllowlisted {
		t.Fatalf("the refusal is %v, want rule %s", err, RuleFieldNotAllowlisted)
	}
	if !strings.Contains(ref.Field, "raw_usage") {
		t.Errorf("the refusal names %q, which does not name raw_usage", ref.Field)
	}
}

// The same for a shape the schema fixes: an enum outside its closed set is refused by name,
// and nothing is written. An encoder that produced a body only its own writer could read
// would be a second grammar.
func TestAnEncodedRecordWithAnUnknownEnumIsRefusedByName(t *testing.T) {
	v := testValidator(t)
	obs := minimalObservation()
	obs.Kind = "guess"
	_, _, err := v.SealObservation(obs)
	var ref *Refusal
	if err == nil || !asRefusal(err, &ref) || ref.Rule != RuleUnknownEnum {
		t.Fatalf("the refusal is %v, want rule %s", err, RuleUnknownEnum)
	}
	if ref.Field != "body.kind" {
		t.Errorf("the refusal names %q, want body.kind", ref.Field)
	}
}

// The property the whole format exists for, on the way out as well as in: a lexeme is
// carried, never a number. An integer above 2^53 and a decimal both survive the encoder
// byte for byte, because nothing in it parses a value.
func TestALexemeSurvivesTheEncoderByteForByte(t *testing.T) {
	v := NewValidator(Allowlists{
		RawUsageFields: []string{"input", "cost_usd_ticks"},
		ReceiptFields:  []string{"turn_id"},
	})
	const big = "9007199254740993"      // 2^53 + 1: the first integer float64 cannot hold
	const tick = "0.000000000000000017" // a decimal whose digits a float64 would round
	obs := minimalObservation()
	obs.RawUsage["input"] = RawField{Presence: "present", Value: strptr(big), NumberKind: "integer", Unit: "tokens"}
	obs.RawUsage["cost_usd_ticks"] = RawField{Presence: "present", Value: strptr(tick), NumberKind: "decimal", Unit: "ticks"}
	raw, _, err := v.SealObservation(obs)
	if err != nil {
		t.Fatalf("SealObservation: %v", err)
	}
	for _, lexeme := range []string{`"` + big + `"`, `"` + tick + `"`} {
		if !strings.Contains(string(raw), lexeme) {
			t.Errorf("the lexeme %s is not in the sealed bytes:\n%s", lexeme, raw)
		}
	}
	// And it comes back as the same lexeme, not as a re-printed number.
	env, err := v.ValidateEnvelope(raw)
	if err != nil {
		t.Fatalf("the encoder's own bytes were refused: %v", err)
	}
	if got := *env.Observation.RawUsage["input"].Value; got != big {
		t.Errorf("the integer came back as %q, want %q", got, big)
	}
	if got := *env.Observation.RawUsage["cost_usd_ticks"].Value; got != tick {
		t.Errorf("the decimal came back as %q, want %q", got, tick)
	}
}

// An absent field is written with its null and its reason, and stays absent. This is the
// distinction the format's whole presence apparatus exists to keep, and an encoder that
// dropped an absent field -- the obvious thing a writer does with a nil value -- would turn
// "the source did not carry it" into "the mapping has no such field" one seal later.
func TestAnAbsentFieldStaysAbsentThroughTheEncoder(t *testing.T) {
	v := NewValidator(Allowlists{RawUsageFields: []string{"input", "reasoning"}, ReceiptFields: []string{"turn_id"}})
	obs := minimalObservation()
	obs.RawUsage["reasoning"] = RawField{
		Presence: "absent", NumberKind: "integer", Unit: "tokens", Reason: strptr("not_supported_by_source"),
	}
	raw, _, err := v.SealObservation(obs)
	if err != nil {
		t.Fatalf("SealObservation: %v", err)
	}
	env, err := v.ValidateEnvelope(raw)
	if err != nil {
		t.Fatalf("the encoder's own bytes were refused: %v", err)
	}
	got, ok := env.Observation.RawUsage["reasoning"]
	if !ok {
		t.Fatal("the absent field vanished; absent and unsupported are not one state")
	}
	if got.Presence != "absent" || got.Value != nil || got.Reason == nil || *got.Reason != "not_supported_by_source" {
		t.Errorf("the absent field came back as %+v", got)
	}
	if got.Present() {
		t.Error("an absent field came back present")
	}
}

// Body is the inverse of the validator's read, so the members it writes are exactly the
// members the validator demands -- no more, so a stray key cannot ride out; no fewer, so a
// required key is never missing. Both directions are checked against the schema's own list,
// which is read out of the validator rather than retyped here.
func TestTheEncodedBodyCarriesExactlyTheSchemasMembers(t *testing.T) {
	body, err := minimalObservation().Body()
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	obj, ok := body.(*Object)
	if !ok {
		t.Fatalf("Body returned %T, want an object", body)
	}
	want := map[string]bool{}
	for _, k := range ObservationMembers {
		want[k] = true
	}
	if len(want) != 12 {
		t.Fatalf("ObservationMembers has %d names; the schema's table has 12", len(want))
	}
	got := map[string]bool{}
	for _, k := range obj.Keys() {
		got[k] = true
		if !want[k] {
			t.Errorf("the encoder wrote %q, which is outside the schema's members", k)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("the encoder omitted %q, which the schema requires present even when null", k)
		}
	}
}

// ------------------------------------------------------------------ the test's own helpers

func strptr(s string) *string { return &s }

// asRefusal is errors.As without the import, kept local so this file says what it means by
// a refusal: this package's own typed one, carrying the rule and the field.
func asRefusal(err error, out **Refusal) bool {
	r, ok := err.(*Refusal)
	if ok {
		*out = r
	}
	return ok
}

// minimalObservation is one accepted observation with every required member filled and
// nothing interesting in it, so a test can change exactly one thing.
func minimalObservation() Observation {
	return Observation{
		Schema: SchemaObservation,
		Source: Source{
			Kind:      "claude_code",
			Namespace: "nova.claude-code",
			SessionID: "sess-1",
			EventKey:  []string{"msg_1"},
		},
		Kind:     "request",
		Revision: Revision{Basis: "none"},
		Time: Times{
			OccurredAt: strptr("2026-09-12T00:00:00Z"),
			Basis:      "response_observation",
		},
		Origin:     Origin{Friend: strptr("rowan"), Bench: strptr("studio"), Basis: "source"},
		Model:      Model{ID: strptr("claude-opus-5"), Basis: "provider_reported"},
		Repository: Repository{ID: strptr("nova-tools"), Basis: "source_binding", Touched: []string{"nova-tools"}},
		RawUsage: map[string]RawField{
			"input": {Presence: "present", Value: strptr("1000"), NumberKind: "integer", Unit: "tokens"},
		},
		MappingID: "sha256:" + strings.Repeat("1b", 32),
		Receipt:   map[string]string{"turn_id": "t-1"},
	}
}
