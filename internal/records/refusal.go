package records

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The rule names. Every refusal this package can produce names exactly one of them, and
// every one of them has a fixture under testdata/refused that fails only that rule. The
// names are stable: a refusal is reported to a person and quoted back in a note.
const (
	RuleInvalidUTF8         = "invalid_utf8"
	RuleLoneSurrogate       = "lone_surrogate"
	RuleNotJSON             = "not_json"
	RuleDuplicateKey        = "duplicate_key"
	RuleRawJSONNumber       = "raw_json_number"
	RuleEnvelopeShape       = "envelope_shape"
	RuleEnvelopeIDSyntax    = "envelope_id_syntax"
	RuleEnvelopeIDMismatch  = "envelope_id_mismatch"
	RuleUnknownSchema       = "unknown_schema"
	RuleUnknownField        = "unknown_field"
	RuleMissingField        = "missing_field"
	RuleWrongType           = "wrong_type"
	RuleUnknownSourceKind   = "unknown_source_kind"
	RuleUnknownEnum         = "unknown_enum"
	RuleEmptyString         = "empty_string"
	RuleLabelSyntax         = "label_syntax"
	RuleNamespaceSyntax     = "namespace_syntax"
	RuleContentIDSyntax     = "content_id_syntax"
	RuleEmptyArray          = "empty_array"
	RuleNotSorted           = "not_sorted"
	RuleDuplicateElement    = "duplicate_element"
	RuleTimestampSyntax     = "timestamp_syntax"
	RuleTimestampNoOffset   = "timestamp_no_offset"
	RuleIntervalIncomplete  = "interval_incomplete"
	RuleIntervalOrder       = "interval_order"
	RuleUnknownPresence     = "unknown_presence"
	RuleUnknownNumberKind   = "unknown_number_kind"
	RuleUnitSyntax          = "unit_syntax"
	RulePresentValueNull    = "present_value_null"
	RulePresentWithReason   = "present_with_reason"
	RuleAbsentValueNotNull  = "absent_value_not_null"
	RuleAbsentReasonMissing = "absent_reason_missing"
	RuleUnknownReasonCode   = "unknown_reason_code"
	RuleIntegerLexeme       = "integer_lexeme"
	RuleDecimalLexeme       = "decimal_lexeme"
	RuleNegativeValue       = "negative_value"
	RuleFieldNotAllowlisted = "field_not_allowlisted"
	RuleReceiptNotAllowed   = "receipt_field_not_allowlisted"
)

// AllRules is every rule name, for the test that proves each one has a fixture and is
// load-bearing. A rule added without a fixture fails that test rather than shipping
// unexercised.
var AllRules = []string{
	RuleInvalidUTF8, RuleLoneSurrogate, RuleNotJSON, RuleDuplicateKey, RuleRawJSONNumber,
	RuleEnvelopeShape, RuleEnvelopeIDSyntax, RuleEnvelopeIDMismatch, RuleUnknownSchema,
	RuleUnknownField, RuleMissingField, RuleWrongType, RuleUnknownSourceKind, RuleUnknownEnum,
	RuleEmptyString, RuleLabelSyntax, RuleNamespaceSyntax, RuleContentIDSyntax, RuleEmptyArray,
	RuleNotSorted, RuleDuplicateElement, RuleTimestampSyntax, RuleTimestampNoOffset,
	RuleIntervalIncomplete, RuleIntervalOrder, RuleUnknownPresence, RuleUnknownNumberKind,
	RuleUnitSyntax, RulePresentValueNull, RulePresentWithReason, RuleAbsentValueNotNull,
	RuleAbsentReasonMissing, RuleUnknownReasonCode, RuleIntegerLexeme, RuleDecimalLexeme,
	RuleNegativeValue, RuleFieldNotAllowlisted, RuleReceiptNotAllowed,
}

// A Refusal is the only failure this package produces. It names the rule and the field,
// and it deliberately does NOT carry the offending value: the format says to report the
// field name and location without echoing its value, because a prompt or a private path
// can sit in an unsupported source field and a diagnostic is a shared file too.
type Refusal struct {
	Rule  string // one of the Rule* constants
	Field string // dotted path into the envelope, e.g. body.raw_usage.input.value
	Note  string // a fixed phrase, never source data
}

// The widths a refusal renders within. A field is a dotted path and a note is a fixed
// phrase, so both are short by design; the caps are here for the case where one of them is
// not, because an unbounded diagnostic is a way to make a log unreadable and a bounded one
// costs nothing. (An adversarial read of the construction API, #146: a 200x-repeated member
// name rendered a 4682-byte refusal.)
const (
	refusalFieldMax = 160
	refusalNoteMax  = 200
)

// clean is what may reach a rendered refusal: no C0 or C1 control, no DEL, no U+2028 or
// U+2029, and not more than max bytes.
//
// The controls are the whole point. A refusal is ONE line, and a line that can carry a
// newline, a carriage return or a Unicode line separator is a line that can render as two --
// the second of which an attacker chooses, and which can be spelled to look exactly like a
// refusal this package produced. Stripping rather than replacing keeps the line honest about
// its own shape: what is dropped was never part of a path.
func clean(s string, max int) string {
	var b strings.Builder
	truncated := false
	for _, r := range s {
		switch {
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r == 0x2028, r == 0x2029:
			continue
		}
		if b.Len()+utf8.RuneLen(r) > max {
			truncated = true
			break
		}
		b.WriteRune(r)
	}
	if truncated {
		return b.String() + "..."
	}
	return b.String()
}

// Error renders the one line. It cleans again rather than trusting the fields, because
// Refusal is an exported struct: a caller can build one with a newline in it, and Error is
// the render point that cannot be bypassed.
func (r *Refusal) Error() string {
	f := clean(r.Field, refusalFieldMax)
	n := clean(r.Note, refusalNoteMax)
	if n == "" {
		return fmt.Sprintf("refused: %s at %s", clean(r.Rule, 64), f)
	}
	return fmt.Sprintf("refused: %s at %s (%s)", clean(r.Rule, 64), f, n)
}

// refuse cleans at construction as well, so a Refusal's Field is safe to read
// programmatically and not only safe to print.
func refuse(rule, field, note string) *Refusal {
	return &Refusal{
		Rule:  rule,
		Field: clean(field, refusalFieldMax),
		Note:  clean(note, refusalNoteMax),
	}
}
