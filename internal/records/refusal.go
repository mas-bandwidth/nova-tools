package records

import "fmt"

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

func (r *Refusal) Error() string {
	if r.Note == "" {
		return fmt.Sprintf("refused: %s at %s", r.Rule, r.Field)
	}
	return fmt.Sprintf("refused: %s at %s (%s)", r.Rule, r.Field, r.Note)
}

func refuse(rule, field, note string) *Refusal {
	return &Refusal{Rule: rule, Field: field, Note: note}
}
