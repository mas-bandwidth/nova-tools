// The who-reads machinery at the CALL BOUNDARY (Stella, 2026-09-19, r2 of the
// #1925 hold).
//
// readers.go derived the rules and constrained an answer against them, and
// nothing in production called it: MandatoryReader, RequiredReads,
// ConstrainRead and ReadState had no reference outside that file and its
// tests. The verb loaded the pair, called the provider and printed the answer
// verbatim, so a state carrying a settled security designation, two design
// defaults, moved normative text and an unresolved hold accepted
// `child-review` at confidence 1.00 -- one call, exit 0. A rule nothing calls
// is prose with a test beside it.
//
// This file is the wiring, and it is three small things:
//
//   - a question file DECLARES the machinery it is answered under
//     (`"machinery": "who-reads"`), so the binding is in the versioned pair
//     rather than in a name match inside the verb;
//   - ReadStateOf turns the state's own typed fields -- the ones Payload has
//     already validated -- into the ReadState the rules are derived from, and
//     refuses a holder no registry configures, because no answer creates an
//     owner;
//   - ReadLine prints the whole decision: who reads first, every read the
//     evidence obliges beside it, which of the two settled it, and
//     lifts_hold=false as a FIELD rather than as the absence of a sentence.
//
// The order is the point. A settled designation is taken before a client is
// constructed, so it costs zero calls and no key; an advisory answer is
// constrained before it is printed and before it is recorded.
package decide

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// MachineryWhoReads is the one machinery a question file may declare today: a
// question file naming it is answered under readers.go's rules.
const MachineryWhoReads = "who-reads"

// ReadQuestion is the question name the who-reads machinery constrains. A
// question file declaring the machinery and not asking it is a refusal.
const ReadQuestion = "reader"

// The state fields the who-reads rules are derived from. They are the same
// names the shipped question declares as typed state fields, so the facts the
// asker computes first are the facts the rules turn on.
const (
	fieldSecurityShaped = "security_shaped_package"
	fieldDesignDefaults = "design_defaults_taken"
	fieldNormativeMoved = "normative_spec_moved"
	fieldHolder         = "holder_of_the_area"
	fieldHoldIsOpen     = "hold_is_open"
	fieldHelpAnswer     = "help_answer"
)

// noHolder are the spellings of "nobody holds this area". Anything else must
// be a configured role.
var noHolder = map[string]bool{"": true, "none": true, "-": true}

// ReadStateOf builds the typed ReadState from a state text's own fields. The
// state has already been validated against the question's declared types by
// Payload; what is added here is the one check a type cannot make -- a holder
// is a CONFIGURED ROLE or it is a refusal, before any call is made.
func ReadStateOf(state string) (ReadState, error) {
	fields := parseStateFields(state)
	s := ReadState{
		SecurityShapedPackage: stateBool(fields[fieldSecurityShaped]),
		DesignDefaultsTaken:   stateInt(fields[fieldDesignDefaults]),
		NormativeSpecMoved:    stateBool(fields[fieldNormativeMoved]),
		HoldIsOpen:            stateBool(fields[fieldHoldIsOpen]),
		HelpAnswer:            strings.TrimSpace(fields[fieldHelpAnswer]),
	}
	holder := strings.ToLower(strings.TrimSpace(firstWord(fields[fieldHolder])))
	if !noHolder[holder] {
		if !ConfiguredRole(Role(holder)) {
			return ReadState{}, fmt.Errorf("decide: state field %s is %q, which no registry configures; it is one of %s, or none",
				fieldHolder, holder, roleList())
		}
		s.HolderOfTheArea = Role(holder)
	}
	return s, nil
}

// Receipts a who-reads line may carry: where the decision's durable row went,
// or why there is none. A decision printed with no word about its row is a
// decision whose receipt a reader has to assume (Stella, 2026-09-19, expanded
// Go read of #1925).
const (
	// ReceiptRecorded: the configured decisions table took the row.
	ReceiptRecorded = "recorded"
	// ReceiptNotConfigured: no table was configured, so there is no row and
	// the line says so rather than saying nothing.
	ReceiptNotConfigured = "not-configured"
)

// ReadLine renders one constrained who-reads decision. Every field a caller
// would otherwise have to read out of prose is a field: the required set, the
// holder, whether its hold is open, that nothing lifted it, and where the row
// went.
//
// hasConfidence is false for a decision the MACHINERY settled. The provider
// was never asked, so there is no confidence to print and the column is a
// dash: a display constant printed there becomes, one copy later, calibration
// evidence about a call nobody made.
func ReadLine(prefix string, d ReadDecision, s ReadState, confidence float64, hasConfidence bool, floor float64, receipt string) string {
	required := "-"
	if len(d.Required) > 0 {
		names := make([]string, 0, len(d.Required))
		for _, r := range d.Required {
			names = append(names, string(r))
		}
		required = strings.Join(names, ",")
	}
	holder := "-"
	if s.HolderOfTheArea != "" {
		holder = string(s.HolderOfTheArea)
	}
	hold := "-"
	if s.HoldIsOpen {
		hold = "open"
	}
	below := "-"
	if hasConfidence && d.Source == SourceProvider && confidence < floor {
		below = ReadQuestion
	}
	conf := noProviderConfidence
	if hasConfidence {
		conf = fmt.Sprintf("%.2f", confidence)
	}
	if strings.TrimSpace(receipt) == "" {
		receipt = ReceiptNotConfigured
	}
	return fmt.Sprintf("%s reader=%s conf=%s floor=%.2f below=%s source=%s required=%s holder=%s hold=%s lifts_hold=%t receipt=%s reason=%s",
		oneline.Field(prefix), oneline.Field(string(d.First)), conf, floor,
		oneline.Field(below), oneline.Field(d.Source), oneline.Field(required),
		oneline.Field(holder), oneline.Field(hold), d.LiftsHold,
		oneline.Field(receipt), oneline.Quote(oneline.Escape(d.Reason)))
}

// stateBool reads a state field's own spelling of a bool. Payload has already
// refused anything that is not one; an absent optional field is false.
func stateBool(raw string) bool {
	switch strings.ToLower(firstWord(strings.TrimSpace(raw))) {
	case "yes", "true":
		return true
	}
	return false
}

// stateInt reads a state field's count; anything that is not one is no
// evidence, which the rules already read as none.
func stateInt(raw string) int {
	n, err := strconv.Atoi(firstWord(strings.TrimSpace(raw)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
