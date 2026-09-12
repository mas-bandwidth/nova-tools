package records

import "regexp"

// A RawField is one allowlisted source usage field as it was observed. The value is the
// ORIGINAL lexeme, kept as a string: an integer above 2^53 and a decimal with trailing
// digits both survive byte-for-byte because nothing here parses them into a float64.
type RawField struct {
	Presence   string  // present, absent, unavailable
	Value      *string // the original lexeme; nil exactly when presence is not present
	NumberKind string  // integer or decimal
	Unit       string
	Reason     *string // a bounded code; nil exactly when presence is present
}

// Present reports whether the wire carried the field at all. A present zero is present:
// "Raw present zero survives even when measurement semantics are unknown."
func (f RawField) Present() bool { return f.Presence == "present" }

// IsZero reports a present zero by its lexeme, not by arithmetic. "0", "0.0" and "0e0" are
// all zero; the comparison is over the decimal value the lexeme names, and no other lexeme
// is rewritten in the process.
func (f RawField) IsZero() bool {
	if !f.Present() || f.Value == nil {
		return false
	}
	return zeroLexeme.MatchString(*f.Value)
}

var zeroLexeme = regexp.MustCompile(`^0(\.0*)?([eE][-+]?[0-9]+)?$`)

// The three states the scope insists are three: a field the source never carried, a field
// carried as null with a reason, and a field carried as zero. They are distinguished by
// Presence and by IsZero, and nothing in this package collapses them.

// ZeroSemantics is field_rules.zero_semantics from the mapping, as written: measured,
// default_may_mask_absence, or unknown.
type ZeroSemantics string

const (
	ZeroMeasured      ZeroSemantics = "measured"
	ZeroMayMaskAbsent ZeroSemantics = "default_may_mask_absence"
	ZeroUnknown       ZeroSemantics = "unknown"
)

// KnownZeroSemantics reports whether a mapping's declared value is one of the three.
func KnownZeroSemantics(z ZeroSemantics) bool {
	return z == ZeroMeasured || z == ZeroMayMaskAbsent || z == ZeroUnknown
}

// Measurement is what a normaliser may believe about one raw field once the mapping's
// zero_semantics is applied. It is NOT a number: producing one is normalisation, which is
// a separate owner. This is the presence-preserving half, and it is here because getting
// it wrong is how a "not measured" becomes a zero that sums into a month claiming to be
// complete.
type Measurement struct {
	// Measured is true only when the raw evidence supports a measurement.
	Measured bool
	// SubsetComplete is the format's "subset completeness": false whenever a present zero
	// might be a synthesised default rather than an observation.
	SubsetComplete bool
	// Why is a fixed phrase for a diagnostic, never source data.
	Why string
}

// Normalize applies field_rules.zero_semantics exactly as the format writes it:
//
//   - "Missing/unavailable raw fields are never normalized to measured 0." An absent or
//     unavailable field yields no measurement at all, whatever the zero rule says.
//   - "For a present zero under either latter rule, normalized measurement is unknown and
//     subset completeness is false." A present zero is measured only under `measured`.
//   - "a positive value is normalized only when its other counter semantics are supported"
//     -- that second gate belongs to the mapping and is the caller's, so a positive value
//     is reported as measured here and the caller withholds it if its semantics are not.
//
// A mapping that declares no zero rule for the field is treated as `unknown` and said so;
// it is not treated as measured.
func Normalize(f RawField, z ZeroSemantics) Measurement {
	if !f.Present() {
		return Measurement{Why: "the source did not carry the field"}
	}
	if f.IsZero() {
		if z == ZeroMeasured {
			return Measurement{Measured: true, SubsetComplete: true, Why: "a present zero under measured zero semantics"}
		}
		return Measurement{Why: "a present zero whose zero semantics do not prove it was measured"}
	}
	return Measurement{Measured: true, SubsetComplete: true, Why: "a present nonzero value"}
}

// The two lexeme grammars. Integer counters accept only `0` or `[1-9][0-9]*`, so `007`,
// `1.0`, `1e3`, `+1` and an empty string are all refused rather than trimmed. A decimal
// retains its original valid JSON-number lexeme, which is RFC 8259's grammar minus the
// sign this format has no evidence for.
var (
	integerLexeme = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	decimalLexeme = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)
	// A leading minus is checked separately so the refusal can name negative_value rather
	// than a generic lexeme failure: "Integer counters ... reject negatives" and "negative
	// counts ... are explicit failures" are a rule about the number, not about spelling.
	negativeLexeme = regexp.MustCompile(`^-`)
)

// unitLexeme: the format requires a declared unit but names no allowlist of units, since
// units arrive with mappings (tokens, cost ticks of unverified unit, and whatever a new
// harness reports). The shape is checked so a unit is a label and never a path or a
// sentence; membership is the mapping's to state.
var unitLexeme = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// checkValueLexeme refuses a usage lexeme by the rule its declared number_kind states.
func checkValueLexeme(value, numberKind, field string) error {
	if negativeLexeme.MatchString(value) {
		return refuse(RuleNegativeValue, field, "a usage value is never negative")
	}
	switch numberKind {
	case "integer":
		if !integerLexeme.MatchString(value) {
			return refuse(RuleIntegerLexeme, field, "an integer counter is 0 or [1-9][0-9]*")
		}
	case "decimal":
		if !decimalLexeme.MatchString(value) {
			return refuse(RuleDecimalLexeme, field, "a decimal keeps a valid JSON-number lexeme")
		}
	}
	return nil
}
