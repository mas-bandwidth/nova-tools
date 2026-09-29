package request

import (
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// RecordVersion is the version token of the record line.
const RecordVersion = "v1"

// Record is one evidence record: an observation about a card, bound to the
// card's definition digest (Def) and the code head it was observed at (Head),
// with who issued it (Issuer), which verifier checked it against its source
// (Verifier) and the source artifact identity (Artifact). It has one canonical
// text form, Line, and one identity, ID, derived from that text.
type Record struct {
	Kind        EvidenceKind
	Issuer      string
	Disposition Disposition
	Head        string
	Def         Digest
	Verifier    string
	Artifact    string
}

// Line is the record's canonical text: seven blank-separated tokens, none empty,
// none holding a blank or a control character:
//
//	v1 <kind> <issuer> <disposition> <head> sha256:<def> <verifier> <artifact>
//
// Every token is drawn from letters, digits and `. : / @ + _ -`, so a record
// needs no escaping inside a JSON string and its encoded size is its length.
func (r Record) Line() string {
	return strings.Join([]string{RecordVersion, string(r.Kind), r.Issuer, string(r.Disposition), r.Head, r.Def.Tagged(), r.Verifier, r.Artifact}, " ")
}

// ID is the record's identity: the first 16 lower-case hexadecimal characters of
// the SHA-256 of its Line. An identical observation has the same ID, so a
// resubmission is recognised, and two different observations do not share one
// (up to the 64 bits that name them; a store guards the full line).
func (r Record) ID() string { return string(card.Sum([]byte(r.Line())))[:16] }

// Negative reports whether the record is an observation against the card.
func (r Record) Negative() bool { return r.Disposition.Negative() }

// MaxRecordBytes is the longest record line: the version, the kind and the
// disposition at their longest, the issuer, the head and the definition digest at
// their bounds, the verifier and the artifact. Every record fits it.
const MaxRecordBytes = 2 + 8 + MaxIdentityBytes + 8 + MaxHeadBytes + 71 + MaxVerifierBytes + MaxRefBytes + 7

// recordFault reports the first fault of a record as a field name, a cause and
// what it wants, or field "" when the record is well formed.
func recordFault(r Record, submitted bool) (field string, cause Cause, want string) {
	if !EvidenceKind(r.Kind).valid() {
		return "kind", CauseInvalidValue, "one of read, ci, sweep, landing"
	}
	if submitted && r.Kind == KindQueue {
		return "kind", CauseNotApplicable, "queue is written by the queue-rejected input, never submitted"
	}
	okDisp := false
	for _, d := range DispositionsOf(r.Kind) {
		if d == r.Disposition {
			okDisp = true
		}
	}
	if !okDisp {
		return "disposition", CauseInvalidValue, "a disposition of the kind"
	}
	switch {
	case !card.ValidToken(r.Issuer, MaxIdentityBytes):
		return "issuer", tokenCause(r.Issuer, MaxIdentityBytes), "a token of at most 64 bytes: " + card.TokenChars
	case !card.ValidHead(r.Head):
		return "head", CauseInvalidValue, "a git object id (40 or 64 lower-case hex) or a sha256: digest"
	case !r.Def.Valid():
		return "digest", CauseInvalidValue, "64 lower-case hexadecimal characters (SHA-256)"
	case !card.ValidToken(r.Verifier, MaxVerifierBytes):
		return "verifier", tokenCause(r.Verifier, MaxVerifierBytes), "a token of at most 32 bytes: " + card.TokenChars
	case !card.ValidToken(r.Artifact, MaxRefBytes):
		return "artifact", tokenCause(r.Artifact, MaxRefBytes), "a token of at most 128 bytes: " + card.TokenChars
	}
	return "", "", ""
}

func (k EvidenceKind) valid() bool {
	for _, v := range allEvidenceKinds {
		if k == v {
			return true
		}
	}
	return false
}

// tokenCause names why s is not a token of at most max bytes.
func tokenCause(s string, max int) Cause {
	switch {
	case s == "":
		return CauseRequired
	case len(s) > max:
		return CauseTooLong
	case card.TextFault(s, len(s)) != "":
		return CauseControlChar
	}
	return CauseInvalidValue
}

// ParseRecord reads a record line: exactly the tokens of Line, in order, the
// version v1, and every token valid. It returns the record and no error, or a
// zero record and a refusal naming the field that failed. Line(ParseRecord(x))
// is x for every x it accepts.
func ParseRecord(line string) (Record, error) {
	fail := func(field string, cause Cause, found, limit string) (Record, error) {
		r := card.NewRefusal("parse_record", cause)
		r.Field, r.Found, r.Limit, r.Next = field, found, limit, "write the record as `v1 <kind> <issuer> <disposition> <head> sha256:<def> <verifier> <artifact>`"
		return Record{}, &card.Refusals{List: []card.Refusal{r}}
	}
	if len(line) > MaxRecordBytes {
		return fail("", CauseTooLong, card.Value(line), strconv.Itoa(MaxRecordBytes)+" bytes")
	}
	f := strings.Split(line, " ")
	if len(f) != 8 {
		return fail("", CauseInvalidValue, card.Value(line), "eight blank-separated tokens")
	}
	if f[0] != RecordVersion {
		return fail("version", CauseInvalidValue, card.Value(f[0]), RecordVersion)
	}
	def, ok := card.ParseTagged(f[5])
	if !ok {
		return fail("digest", CauseInvalidValue, card.Value(f[5]), "sha256:<64 lower-case hex>")
	}
	r := Record{Kind: EvidenceKind(f[1]), Issuer: f[2], Disposition: Disposition(f[3]), Head: f[4], Def: def, Verifier: f[6], Artifact: f[7]}
	if field, cause, want := recordFault(r, false); field != "" {
		return fail(field, cause, card.Value(map[string]string{"kind": f[1], "issuer": f[2], "disposition": f[3], "head": f[4], "digest": f[5], "verifier": f[6], "artifact": f[7]}[field]), want)
	}
	return r, nil
}
