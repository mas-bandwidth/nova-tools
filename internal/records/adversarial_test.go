package records

import (
	"path/filepath"
	"strings"
	"testing"
)

// The three findings of the adversarial read of this API (#146 comment 5647774812), each
// kept as the reader's own reproduction so the repair cannot rot back. All three were real,
// and two of them contradicted a sentence encode.go itself writes, which is the worst kind:
// a comment that asserts a property the code does not have is worse than no comment, because
// the next reader trusts it.
//
// The sentinel below is the reader's. It is a synthetic string and names nothing real.
const sentinel = "SYNTHETIC_SENTINEL_7f3a"

// FINDING 1. The schema's member list WAS an exported mutable slice, and it IS what the
// validator reads as its closed set -- so `ObservationMembers = append(ObservationMembers,
// sentinel)` made ValidateEnvelope accept a thirteen-member body, for every Validator in the
// process, and `ObservationMembers[:3]` made it refuse the exact bytes SealObservation had
// just returned. The gate and the bypass shared one slice.
//
// The repair keeps the both-sides-one-list property that the list exists for, and takes the
// mutability away: an unexported slice, and an accessor that hands out a copy.
func TestTheSchemasMemberListCannotBeWidenedByACaller(t *testing.T) {
	raw, _ := readFixture(t, filepath.Join("testdata", "valid", "antigravity_request.json"))
	v := validatorFor(t, raw)
	env, err := v.ValidateEnvelope(raw)
	if err != nil {
		t.Fatalf("the fixture no longer validates: %v", err)
	}

	// A thirteenth member on a body this package otherwise accepts.
	body, err := env.Observation.Body()
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	obj, ok := body.(*Object)
	if !ok {
		t.Fatalf("Body returned %T", body)
	}
	if err := obj.Set(sentinel, sentinel); err != nil {
		t.Fatalf("Set: %v", err)
	}
	widened, _, err := Seal(obj)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	mustRefuse := func(when string) {
		t.Helper()
		_, err := v.ValidateEnvelope(widened)
		var ref *Refusal
		if err == nil {
			t.Fatalf("%s: a thirteen-member body was ACCEPTED; the schema's allowlist is not closed", when)
		}
		if !asRefusal(err, &ref) || ref.Rule != RuleUnknownField {
			t.Errorf("%s: the refusal is %v, want %s", when, err, RuleUnknownField)
		}
	}
	mustRefuse("before any attempt to widen the list")

	// The accessor hands out a copy: writing through what it returns changes nothing, and
	// there is no exported slice left to append to or truncate.
	got := ObservationMembers()
	if len(got) != 12 {
		t.Fatalf("ObservationMembers() returned %d names, want 12", len(got))
	}
	got = append(got, sentinel)
	got[0] = sentinel
	_ = got
	if again := ObservationMembers(); len(again) != 12 || again[0] != "schema" {
		t.Errorf("the accessor handed out its own backing array: %v", again)
	}
	mustRefuse("after appending to and overwriting the accessor's result")

	// And the truncation half: the bytes SealObservation returns must still validate.
	sealed, _, err := v.SealObservation(*env.Observation)
	if err != nil {
		t.Fatalf("SealObservation: %v", err)
	}
	shrunk := ObservationMembers()[:3]
	_ = shrunk
	if _, err := v.ValidateEnvelope(sealed); err != nil {
		t.Errorf("after truncating the accessor's result, a sealed record was refused: %v", err)
	}
}

// FINDING 2. SealObservation's read-back inherited the validator's skip set, so
// `v.Without(RuleUnknownEnum).SealObservation(o)` returned a sealed envelope whose bytes a
// strict validator refuses -- and the leniency is invisible in the bytes. Without's own
// contract says "It is never used by the publisher"; before this API a Validator could only
// READ, so that was structurally true, and SealObservation had quietly turned a
// read-relaxing handle into a write-relaxing one.
//
// The repair makes the gate unconditional by construction: the read-back runs through a
// validator carrying the same allowlists and NO skips, so there is no handle that can seal
// what the boundary refuses.
func TestAWithoutValidatorCannotSealWhatTheStrictBoundaryRefuses(t *testing.T) {
	// The allowlist matches minimalObservation's one raw field ON PURPOSE. testValidator's
	// wider allowlist masks this finding behind `missing_field at body.raw_usage.cache_read`
	// -- every allowlisted field owes the record an entry -- so a test written with it would
	// have gone green against the unrepaired code and proved nothing. That is how the first
	// draft of this test passed, and it is why the reader's own vehicle was a fixture-derived
	// observation rather than this one.
	v := NewValidator(Allowlists{RawUsageFields: []string{"input"}, ReceiptFields: []string{"turn_id"}})
	obs := minimalObservation()
	obs.Kind = sentinel

	if _, _, err := v.SealObservation(obs); err == nil {
		t.Fatal("a strict validator sealed an unknown enum")
	}

	// The finding, verbatim: the relaxed handle must refuse it too.
	raw, id, err := v.Without(RuleUnknownEnum).SealObservation(obs)
	if err == nil {
		t.Fatalf("Without(%s).SealObservation sealed bytes the strict boundary refuses (id %s):\n%s",
			RuleUnknownEnum, id, raw)
	}
	var ref *Refusal
	if !asRefusal(err, &ref) || ref.Rule != RuleUnknownEnum || ref.Field != "body.kind" {
		t.Errorf("the refusal is %v, want %s at body.kind", err, RuleUnknownEnum)
	}
	if raw != nil {
		t.Errorf("a refused seal returned %d bytes; it returns none", len(raw))
	}

	// Every rule, not just the one the reader tried: no skip set buys a seal.
	for _, rule := range AllRules {
		if _, _, err := v.Without(rule).SealObservation(obs); err == nil {
			t.Errorf("Without(%s).SealObservation sealed an unknown enum", rule)
		}
	}

	// And Without still does its one job on the READ side, which is what it exists for.
	strictBytes, _, err := v.SealObservation(minimalObservation())
	if err != nil {
		t.Fatalf("a valid observation does not seal: %v", err)
	}
	if _, err := v.Without(RuleUnknownEnum).ValidateEnvelope(strictBytes); err != nil {
		t.Errorf("Without no longer relaxes a read: %v", err)
	}
}

// FINDING 3. Object.Set rendered the caller's member name verbatim and unbounded into
// Refusal.Field, and Error() folded only "\n" -- so a bare CR survived, an attacker-chosen
// "refused: ..." substring sat inside a real refusal line, and a repeated name gave a
// multi-kilobyte diagnostic. This was the only place in the package where a caller string
// reached a rendered refusal; every validator path already reports an ordinal instead.
//
// The repair is in two places on purpose: Set reports the member's ORDINAL and never its
// name, the way exactKeys and rawUsage do; and refuse() plus Error() strip C0/C1, U+2028 and
// U+2029 and bound the width, so a Refusal built anywhere -- including by a caller through
// the exported struct -- cannot carry a control character or an unbounded string.
func TestABuilderRefusalNeverEchoesTheCallersMemberName(t *testing.T) {
	name := sentinel + "\r/private/path/prompt.txt\nrefused: envelope_shape at envelope"
	o := NewObject()
	if err := o.Set(name, "x"); err != nil {
		t.Fatalf("the first Set: %v", err)
	}
	err := o.Set(name, "y")
	if err == nil {
		t.Fatal("the duplicate member was accepted")
	}
	got := err.Error()
	for _, must := range []string{sentinel, "/private/path/prompt.txt"} {
		if strings.Contains(got, must) {
			t.Errorf("the diagnostic echoes the caller's member name (%q):\n%s", must, got)
		}
	}
	// "\r", "\n", U+2028 LINE SEPARATOR and U+2029 PARAGRAPH SEPARATOR. The last two are
	// written as themselves and render as blanks in most editors; they are line breaks to a
	// reader that follows Unicode, which is what makes them worth naming here.
	for _, bad := range []string{"\r", "\n", " ", " "} {
		if strings.Contains(got, bad) {
			t.Errorf("the diagnostic carries %q, so one refusal can render as two lines:\n%q", bad, got)
		}
	}
	if n := strings.Count(got, "refused:"); n != 1 {
		t.Errorf("the diagnostic carries %d `refused:` tokens; a caller's string can forge one:\n%s", n, got)
	}
	if !strings.Contains(got, RuleDuplicateKey) {
		t.Errorf("the diagnostic does not name its rule:\n%s", got)
	}
	// The ordinal, which is what every other path in this package reports.
	if !strings.Contains(got, "member[0]") {
		t.Errorf("the diagnostic does not name the member's position:\n%s", got)
	}

	// Bounded, whatever the caller passed: the reader's 200x-repeated name gave 4682 bytes.
	long := NewObject()
	huge := strings.Repeat(sentinel, 200)
	if err := long.Set(huge, "x"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	e2 := long.Set(huge, "y")
	if e2 == nil {
		t.Fatal("the duplicate member was accepted")
	}
	if len(e2.Error()) > 300 {
		t.Errorf("the diagnostic is %d bytes; a refusal is one bounded line", len(e2.Error()))
	}

	// A Refusal built by hand, bypassing refuse(), renders safely too: Error() is the render
	// point and it cannot be bypassed.
	hand := &Refusal{Rule: RuleWrongType, Field: "body\r\n" + strings.Repeat("x", 9000), Note: "note\nsplit"}
	if s := hand.Error(); strings.ContainsAny(s, "\r\n") || len(s) > 600 {
		t.Errorf("a hand-built Refusal renders %d bytes with control characters: %q", len(s), s)
	}
}

// The fourth consequence the reader folded into finding 3: Set was reachable on a body that
// ValidateEnvelope had already accepted, after which env.ID disagreed with the body's own
// ContentID -- an envelope whose ID is not the digest of its body, made with two exported
// calls and no refusal.
//
// The repair: every object the strict parser builds is sealed at birth, so the exported Set
// refuses it -- deeply, since the parser builds every nested object the same way. NewObject's
// are the caller's own and stay writable. There is no new rule name for this, deliberately:
// it is an API misuse rather than a defect in a record's bytes, and every Rule* constant owes
// testdata a fixture that fails only that rule.
func TestAValidatedBodyCannotBeMutated(t *testing.T) {
	raw, _ := readFixture(t, filepath.Join("testdata", "valid", "claude_code_request.json"))
	v := validatorFor(t, raw)
	env, err := v.ValidateEnvelope(raw)
	if err != nil {
		t.Fatalf("the fixture no longer validates: %v", err)
	}
	before, err := ContentID(env.Body)
	if err != nil {
		t.Fatal(err)
	}
	if before != env.ID {
		t.Fatalf("the fixture's ID is not its body's digest: %s vs %s", env.ID, before)
	}

	if err := env.Body.Set(sentinel, "x"); err == nil {
		t.Error("a validated body accepted a new member; env.ID now disagrees with its body")
	} else if !strings.Contains(err.Error(), "sealed") {
		t.Errorf("the refusal does not say the body is sealed: %v", err)
	}
	// Deeply: a nested object the parser built is sealed too.
	nested, ok := env.Body.Get("time")
	if !ok {
		t.Fatal("the fixture has no time member")
	}
	if err := nested.(*Object).Set(sentinel, "x"); err == nil {
		t.Error("a nested validated object accepted a new member")
	}

	after, err := ContentID(env.Body)
	if err != nil {
		t.Fatal(err)
	}
	if after != env.ID {
		t.Errorf("the body moved under its own ID: %s is now %s", env.ID, after)
	}

	// And the caller's own objects are still writable, or the encoder could not work.
	own := NewObject()
	if err := own.Set("schema", SchemaObservation); err != nil {
		t.Errorf("NewObject's own object refused a member: %v", err)
	}
}

// Finding 1's sibling, one level down, closed in the same hand: a Validator used to keep the
// caller's own backing arrays, so appending to the slice you handed to NewValidator changed
// what the boundary enforced -- including between a SealObservation's seal and its strict
// read-back. The allowlist is a declaration made once, not a live handle.
func TestAValidatorsAllowlistIsNotALiveHandle(t *testing.T) {
	fields := []string{"input"}
	receipts := []string{"turn_id"}
	v := NewValidator(Allowlists{RawUsageFields: fields, ReceiptFields: receipts})

	obs := minimalObservation()
	extra := "17"
	obs.RawUsage["cost_usd_ticks"] = RawField{Presence: "present", Value: &extra, NumberKind: "integer", Unit: "ticks"}
	if _, _, err := v.SealObservation(obs); err == nil {
		t.Fatal("a field outside the mapping's allowlist sealed")
	}

	// The vector that BITES is writing through the array the caller still holds. Measured
	// both ways before this test was trusted: with the copy removed, this assertion fails
	// with `receipt_field_not_allowlisted at body.receipt[0]`.
	receipts[0] = sentinel
	if _, _, err := v.SealObservation(minimalObservation()); err != nil {
		t.Errorf("overwriting the caller's receipt slice changed the validator: %v", err)
	}
	fields[0] = sentinel
	if _, _, err := v.SealObservation(minimalObservation()); err != nil {
		t.Errorf("overwriting the caller's raw-usage slice changed the validator: %v", err)
	}

	// Appending is NOT a vector and this test does not pretend it is: a stored slice header
	// keeps its own length, so a caller's append -- reallocating or not -- is invisible to the
	// validator with or without the copy. It is asserted here only so the next reader does not
	// add it as a check that cannot fail.
	fields = append(fields, "cost_usd_ticks")
	_ = fields
	if _, _, err := v.SealObservation(obs); err == nil {
		t.Error("a field outside the allowlist sealed after the caller appended")
	}
}
