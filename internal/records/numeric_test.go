package records

import (
	"strconv"
	"testing"
)

// The reason usage values are strings: a lexeme no float64 can hold survives byte for
// byte, and the loss it avoids is shown rather than asserted.
func TestExactNumericStringsSurvive(t *testing.T) {
	for _, tc := range []struct{ lexeme, kind string }{
		{"9007199254740993", "integer"},               // 2^53 + 1
		{"18446744073709551615", "integer"},           // 2^64 - 1
		{"123456789012345678901234567890", "integer"}, // wider than any machine integer
		{"0", "integer"},                              // a present zero
		{"0.0001250", "decimal"},                      // trailing digits are part of the lexeme
		{"1.25e-4", "decimal"},
	} {
		value := tc.lexeme
		f := RawField{Presence: "present", Value: &value, NumberKind: tc.kind, Unit: "tokens"}
		if err := checkValueLexeme(*f.Value, f.NumberKind, "body.raw_usage.x.value"); err != nil {
			t.Fatalf("%s refused: %v", tc.lexeme, err)
		}
		if *f.Value != tc.lexeme {
			t.Fatalf("the lexeme was rewritten")
		}
	}
	through, _ := strconv.ParseFloat("9007199254740993", 64)
	if strconv.FormatFloat(through, 'f', -1, 64) == "9007199254740993" {
		t.Fatalf("the premise is wrong: a float64 held 2^53+1")
	}
}

func TestLexemeRefusals(t *testing.T) {
	for _, tc := range []struct{ value, kind, rule string }{
		{"007", "integer", RuleIntegerLexeme},
		{"1.0", "integer", RuleIntegerLexeme},
		{"1e3", "integer", RuleIntegerLexeme},
		{"+1", "integer", RuleIntegerLexeme},
		{"", "integer", RuleIntegerLexeme},
		{" 1", "integer", RuleIntegerLexeme},
		{"1 ", "integer", RuleIntegerLexeme},
		{"-1", "integer", RuleNegativeValue},
		{"-0.5", "decimal", RuleNegativeValue},
		{"1..2", "decimal", RuleDecimalLexeme},
		{".5", "decimal", RuleDecimalLexeme},
		{"0x10", "decimal", RuleDecimalLexeme},
		{"Infinity", "decimal", RuleDecimalLexeme},
	} {
		err := checkValueLexeme(tc.value, tc.kind, "body.raw_usage.x.value")
		r, ok := err.(*Refusal)
		if !ok {
			t.Fatalf("%q as %s was accepted", tc.value, tc.kind)
		}
		if r.Rule != tc.rule {
			t.Fatalf("%q refused by %s, want %s", tc.value, r.Rule, tc.rule)
		}
	}
}

// Absent, null and zero are three different states, and zero_semantics decides what a
// present zero means WITHOUT changing what the source said.
func TestZeroSemantics(t *testing.T) {
	zero, ten := "0", "10"
	reason := "not_supported_by_source"
	present0 := RawField{Presence: "present", Value: &zero, NumberKind: "integer", Unit: "tokens"}
	present10 := RawField{Presence: "present", Value: &ten, NumberKind: "integer", Unit: "tokens"}
	absentF := RawField{Presence: "absent", NumberKind: "integer", Unit: "tokens", Reason: &reason}
	unavail := RawField{Presence: "unavailable", NumberKind: "integer", Unit: "tokens", Reason: &reason}

	for _, tc := range []struct {
		name           string
		f              RawField
		z              ZeroSemantics
		measured, subs bool
	}{
		{"present zero, measured", present0, ZeroMeasured, true, true},
		{"present zero, default may mask absence", present0, ZeroMayMaskAbsent, false, false},
		{"present zero, unknown", present0, ZeroUnknown, false, false},
		{"present nonzero, measured", present10, ZeroMeasured, true, true},
		{"present nonzero, may mask absence", present10, ZeroMayMaskAbsent, true, true},
		{"present nonzero, unknown", present10, ZeroUnknown, true, true},
		{"absent under measured", absentF, ZeroMeasured, false, false},
		{"absent under unknown", absentF, ZeroUnknown, false, false},
		{"unavailable under measured", unavail, ZeroMeasured, false, false},
		{"unavailable under may mask absence", unavail, ZeroMayMaskAbsent, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Normalize(tc.f, tc.z)
			if m.Measured != tc.measured || m.SubsetComplete != tc.subs {
				t.Fatalf("measured=%v subset=%v, want %v/%v (%s)", m.Measured, m.SubsetComplete, tc.measured, tc.subs, m.Why)
			}
		})
	}

	// The three states stay three: presence is never inferred from the value, and the
	// value is never inferred from the presence.
	if !present0.Present() || !present0.IsZero() {
		t.Fatalf("a present zero stopped being a present zero")
	}
	if absentF.Present() || absentF.IsZero() {
		t.Fatalf("an absent field reads as a zero")
	}
	if unavail.Present() {
		t.Fatalf("an unavailable field reads as present")
	}
	for _, lex := range []string{"0", "0.0", "0e0", "0.000"} {
		f := RawField{Presence: "present", Value: &lex, NumberKind: "decimal"}
		if !f.IsZero() {
			t.Fatalf("%q is a zero lexeme", lex)
		}
	}
	for _, lex := range []string{"1", "0.1", "10", "0.0001250"} {
		f := RawField{Presence: "present", Value: &lex, NumberKind: "decimal"}
		if f.IsZero() {
			t.Fatalf("%q is not a zero lexeme", lex)
		}
	}
	if KnownZeroSemantics("measured_ish") {
		t.Fatalf("an unknown zero rule was accepted")
	}
	for _, z := range []ZeroSemantics{ZeroMeasured, ZeroMayMaskAbsent, ZeroUnknown} {
		if !KnownZeroSemantics(z) {
			t.Fatalf("%s is one of the three field_rules.zero_semantics values", z)
		}
	}
}

// The accepted record keeps every dimension the format names, so a reader can group by
// them later without the observation having chosen a grouping.
func TestAcceptedRecordKeepsItsDimensions(t *testing.T) {
	raw := mustReadFile(t, "testdata/valid/grok_request_present_zero.json")
	v := validatorFor(t, raw)
	env, err := v.ValidateEnvelope(raw)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	o := env.Observation
	if o.Source.Kind != "grok" || o.Origin.Friend == nil || *o.Origin.Friend != "johnny" {
		t.Fatalf("source and origin did not survive validation")
	}
	out := o.RawUsage["output"]
	if !out.Present() || !out.IsZero() {
		t.Fatalf("the present zero did not survive as a present zero")
	}
	if cw := o.RawUsage["cache_write"]; cw.Present() || cw.Reason == nil {
		t.Fatalf("the absent field did not survive as absent with its reason")
	}
	if cost := o.RawUsage["cost_usd_ticks"]; cost.Value == nil || *cost.Value != "0.0001250" || cost.Unit != "usd_ticks" {
		t.Fatalf("the decimal lexeme or its unit was rewritten")
	}
	// A raw value of unverified unit is retained and is not spend: nothing here converts
	// it, and Normalize reports only whether it was measured.
	if m := Normalize(o.RawUsage["cost_usd_ticks"], ZeroUnknown); !m.Measured {
		t.Fatalf("a present nonzero decimal should read as measured")
	}
}
