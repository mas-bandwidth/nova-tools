package definition

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// Refusal is one refused input: the card layer's one refusal shape. File is the
// file name (or, for a pin, the repository-relative path), Line is 1-based and
// zero when the cause has no line, Field is the header key when one is involved,
// Found is what was seen (a value from the input is quoted and bounded), Limit is
// the rule or bound it broke and Next is the one action that fixes it.
type Refusal = card.Refusal

// Refusals is every refusal one call found, at most card.MaxRefusals of them,
// and the count of the rest. It is what a call returns beside no result.
type Refusals = card.Refusals

// Cause is the stable name of why something was refused; the vocabulary is the
// card layer's, in internal/card.
type Cause = card.Cause

// The operations a refusal names.
const (
	OpParse    card.Operation = "parse"
	OpValidate card.Operation = "validate"
	OpPin      card.Operation = "pin"
	OpAdmit    card.Operation = "admit"
)

// Limits. Each is named in the refusal that enforces it. Those the card layer
// shares with the request package are the card layer's constants.
const (
	// MaxFiles bounds an array of card files or of paths to pin: the most cards
	// one admission request admits.
	MaxFiles = card.MaxChangedEntries
	// MaxCardBytes bounds one card file and one pinned blob.
	MaxCardBytes = 256 << 10
	// MaxTotalBytes bounds the bytes of one array.
	MaxTotalBytes = 8 << 20
	// MaxPathBytes bounds one repository-relative path to pin.
	MaxPathBytes = card.MaxPathBytes
	// MaxIDBytes bounds a card ID.
	MaxIDBytes = card.MaxIDBytes
	// MaxEntryBytes bounds an ENTRY value.
	MaxEntryBytes = card.MaxEntryBytes
	// MaxTitleBytes bounds a TITLE.
	MaxTitleBytes = card.MaxTitleBytes
	// MaxDoorsBytes bounds a DOORS value.
	MaxDoorsBytes = card.MaxDoorsBytes
	// MaxTestBytes bounds a TEST value.
	MaxTestBytes = card.MaxTestBytes
	// MaxGlobBytes bounds one PATHS glob.
	MaxGlobBytes = card.MaxGlobBytes
	// MaxProseBytes bounds DONE-WHEN and PROBES, which reach the record only
	// through the definition digest.
	MaxProseBytes = card.MaxProseBytes
	// MaxDependsOn bounds the IDs one DEPENDS-ON line names.
	MaxDependsOn = card.MaxDependsOn
	// MaxContractNoteBytes bounds the note after a contract line's ID and sha.
	MaxContractNoteBytes = 512
)

// ref builds a refusal of an operation about a file, line and key.
func ref(op card.Operation, c Cause, file string, line int, key, found, limit, next string) Refusal {
	return Refusal{Operation: op, Index: -1, File: file, Line: line, Field: key, Cause: c, Found: found, Limit: limit, Next: next}
}

// short bounds free explanatory text taken from a validator that quotes the
// caller's value inside it: one line, at most 200 bytes.
func short(s string) string {
	s = strings.NewReplacer("\r", `\r`, "\n", `\n`, "\x00", `\0`).Replace(s)
	if len(s) > 200 {
		cut := 200
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}

// just is a refusal set of a single refusal.
func just(r Refusal) *Refusals { return &Refusals{List: []Refusal{r}} }

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
