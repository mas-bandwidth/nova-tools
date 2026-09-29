package definition

import (
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Limits. Each is named in the refusal that enforces it.
const (
	// MaxFiles bounds an array of card files or of paths to pin.
	MaxFiles = 128
	// MaxCardBytes bounds one card file and one pinned blob.
	MaxCardBytes = 256 << 10
	// MaxTotalBytes bounds the bytes of one array.
	MaxTotalBytes = 8 << 20
	// MaxPathBytes bounds one repository-relative path to pin.
	MaxPathBytes = 1024
	// MaxIDBytes bounds a card ID.
	MaxIDBytes = 128
	// MaxEntryBytes bounds an ENTRY value.
	MaxEntryBytes = 512
	// MaxValueBytes bounds a single-line prose value (TITLE, DONE-WHEN, DOORS, PROBES).
	MaxValueBytes = 2048
	// MaxDependsOn bounds the IDs one DEPENDS-ON line names.
	MaxDependsOn = 64
)

// Cause is the stable name of why something was refused.
type Cause string

// The operations a refusal names.
const (
	OpParse    = "parse"
	OpValidate = "validate"
	OpPin      = "pin"
	OpAdmit    = "admit"
)

// Causes, by operation.
const (
	CauseEmptyArray        Cause = "empty-array"
	CauseTooManyFiles      Cause = "too-many-files"
	CauseTotalTooLarge     Cause = "total-too-large"
	CauseDuplicateFile     Cause = "duplicate-file"
	CauseFileName          Cause = "file-name"
	CauseFileTooLarge      Cause = "file-too-large"
	CauseEmptyFile         Cause = "empty-file"
	CauseBOM               Cause = "byte-order-mark"
	CauseInvalidUTF8       Cause = "invalid-utf8"
	CauseCarriageReturn    Cause = "carriage-return"
	CauseNUL               Cause = "nul-byte"
	CauseContractLine      Cause = "contract-line"
	CauseContractSHA       Cause = "contract-sha"
	CauseDuplicateKey      Cause = "duplicate-key"
	CauseUnknownKey        Cause = "unknown-key"
	CauseAmbiguousSpelling Cause = "ambiguous-spelling"
	CauseUnsupportedSchema Cause = "unsupported-schema"
	CauseRequiredMissing   Cause = "required-missing"
	CauseStranded          Cause = "stranded"
	CauseIDMismatch        Cause = "id-mismatch"
	CauseEmptyValue        Cause = "empty-value"
	CauseInvalidValue      Cause = "invalid-value"
	CauseInvalidID         Cause = "invalid-id"
	CauseInvalidEntry      Cause = "invalid-entry"
	CauseInvalidKind       Cause = "invalid-kind"
	CauseUnclassifiedKind  Cause = "unclassified-kind"
	CauseInvalidPaths      Cause = "invalid-paths"
	CauseInvalidDependsOn  Cause = "invalid-depends-on"
	CauseInvalidTier       Cause = "invalid-tier"
	CauseInvalidTest       Cause = "invalid-test"
	CauseNoBrief           Cause = "no-brief"

	CauseDuplicateID   Cause = "duplicate-id"
	CauseSelfDependent Cause = "self-dependency"
	CauseCycle         Cause = "dependency-cycle"

	CauseInvalidCommit   Cause = "invalid-commit"
	CauseInvalidPath     Cause = "invalid-path"
	CausePathEscapes     Cause = "path-escapes"
	CauseDuplicatePath   Cause = "duplicate-path"
	CauseNotRepository   Cause = "not-repository"
	CauseGitUnavailable  Cause = "git-unavailable"
	CauseGitFailed       Cause = "git-failed"
	CauseTimeout         Cause = "timeout"
	CauseUnknownCommit   Cause = "unknown-commit"
	CauseNotCommit       Cause = "not-commit"
	CauseMissingPath     Cause = "missing-path"
	CauseSymlink         Cause = "symlink"
	CauseNotBlob         Cause = "not-blob"
	CauseBlobTooLarge    Cause = "blob-too-large"
	CauseIdentityMissing Cause = "identity-missing"
	CauseIdentityInvalid Cause = "identity-invalid"
	CausePinMismatch     Cause = "pin-mismatch"
)

// Refusal is one refused input, as a value. Operation names the step; File is the
// file name (or, for Pin, the repository-relative path); Line is 1-based and zero
// when the cause has no line; Key is the header key when one is involved; Cause is
// the stable code; Found is what was seen; Next is the one action that fixes it.
// Also names the other files a refusal involves (the second file of a repeated ID,
// the other members of a cycle).
type Refusal struct {
	Operation string
	File      string
	Line      int
	Key       string
	Cause     Cause
	Found     string
	Next      string
	Also      []string
}

// String is the refusal on one line: REFUSED <operation> then the fields that are
// set, then cause, found and next. Free text is quoted, so the line is one line.
func (r Refusal) String() string {
	var b strings.Builder
	b.WriteString("REFUSED ")
	b.WriteString(r.Operation)
	if r.File != "" {
		b.WriteString(" file=" + oneline.Field(r.File))
	}
	if r.Line > 0 {
		b.WriteString(" line=" + strconv.Itoa(r.Line))
	}
	if r.Key != "" {
		b.WriteString(" key=" + oneline.Field(r.Key))
	}
	b.WriteString(" cause=" + string(r.Cause))
	if len(r.Also) > 0 {
		b.WriteString(" also=")
		for i, a := range r.Also {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(oneline.Field(a))
		}
	}
	b.WriteString(" found=" + oneline.Quote(oneline.Cap(r.Found, 200)))
	b.WriteString(" next=" + oneline.Quote(r.Next))
	return b.String()
}

// Lines renders refusals one per line, in order.
func Lines(rs []Refusal) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.String()
	}
	return out
}
