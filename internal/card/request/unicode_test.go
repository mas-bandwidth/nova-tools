package request

import (
	"fmt"
	"testing"
)

// hidden are characters no one-line text may carry: they change how text reads
// without changing what it is, or break a line.
var hidden = map[string]string{
	"right-to-left override": "a\u202eb", "left-to-right override": "a\u202db", "bidi embedding": "a\u202ab",
	"bidi isolate": "a\u2066b", "pop isolate": "a\u2069b", "left-to-right mark": "a\u200eb", "right-to-left mark": "a\u200fb",
	"arabic letter mark": "a\u061cb", "zero-width space": "a\u200bb", "zero-width non-joiner": "a\u200cb", "zero-width joiner": "a\u200db",
	"word joiner": "a\u2060b", "byte-order mark": "a\ufeffb", "soft hyphen": "a\u00adb", "line separator": "a\u2028b",
	"paragraph separator": "a\u2029b", "next line": "a\u0085b", "tag character": "a\U000e0041b", "invisible times": "a\u2062b",
	"newline": "a\nb", "tab": "a\tb", "nul": "a\x00b", "delete": "a\x7fb",
}

// References are printable ASCII: an issuer, an actor, an operation ID, a
// source artifact, a landing identity, a verifier and an artifact of a record.
func TestReferencesArePrintableASCII(t *testing.T) {
	t.Parallel()
	type ref struct {
		name string
		op   Operation
		set  func(r *Request, s string)
		path string
	}
	refs := []ref{
		{"actor", OpAdmit, func(r *Request, s string) { r.Actor = s }, "-1|actor|"},
		{"operation id", OpAdmit, func(r *Request, s string) { r.OperationID = s }, "-1|operation_id|"},
		{"input issuer", OpApplyEvents, func(r *Request, s string) { r.Inputs[0].Issuer = s }, "0|issuer|"},
		{"input source", OpApplyEvents, func(r *Request, s string) { r.Inputs[0].Source = s }, "0|source|"},
		{"input landing", OpApplyEvents, func(r *Request, s string) {
			r.Inputs = []Input{input(InLanding, "c1")}
			r.Inputs[0].Landing = s
		}, "0|landing|"},
		{"record issuer", OpRecordEvidence, func(r *Request, s string) { r.Evidence[0].Records[0].Issuer = s }, "0|records[0].issuer|"},
		{"record verifier", OpRecordEvidence, func(r *Request, s string) { r.Evidence[0].Records[0].Verifier = s }, "0|records[0].verifier|"},
		{"record artifact", OpRecordEvidence, func(r *Request, s string) { r.Evidence[0].Records[0].Artifact = s }, "0|records[0].artifact|"},
	}
	for _, rf := range refs {
		rf := rf
		t.Run(rf.name, func(t *testing.T) {
			t.Parallel()
			for name, s := range hidden {
				r := validRequest(rf.op)
				rf.set(r, s)
				_, err := Validate(r)
				if len(triples(err)) != 1 || triples(err)[0] != rf.path+"control-character" {
					t.Errorf("%s: %q gave %v", name, s, triples(err))
				}
			}
			// printable ASCII outside the token alphabet, and any non-ASCII, are refused too
			for _, s := range []string{"a b", "a,b", `a"b`, "a\\b", "a#b", "café", "世", "\U0001f600"} {
				r := validRequest(rf.op)
				rf.set(r, s)
				if _, err := Validate(r); err == nil {
					t.Errorf("%q accepted as a reference", s)
				}
			}
			r := validRequest(rf.op)
			rf.set(r, "ok:1/a@b+c_d-e.f")
			if _, err := Validate(r); err != nil {
				t.Errorf("a token refused: %v", err)
			}
		})
	}
}

// Reasons and other free text refuse the same characters, and accept ordinary
// Unicode.
func TestFreeTextRefusesHiddenCharacters(t *testing.T) {
	t.Parallel()
	type field struct {
		name string
		op   Operation
		set  func(r *Request, s string)
		path string
	}
	fields := []field{
		{"reason", OpApplyEvents, func(r *Request, s string) {
			r.Inputs = []Input{input(InVerdictRework, "c1")}
			r.Inputs[0].Reason = s
		}, "0|reason|"},
		{"cancel reason", OpApplyEvents, func(r *Request, s string) {
			r.Inputs = []Input{input(InCancel, "c1")}
			r.Inputs[0].Reason = s
		}, "0|reason|"},
		{"title", OpAdmit, func(r *Request, s string) { r.Admissions[0].Title = s }, "0|title|"},
		{"entry", OpAdmit, func(r *Request, s string) { r.Admissions[0].Entry = s }, "0|entry|"},
	}
	for _, f := range fields {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			for name, s := range hidden {
				r := validRequest(f.op)
				f.set(r, s)
				_, err := Validate(r)
				if got := triples(err); len(got) != 1 || got[0] != f.path+"control-character" {
					t.Errorf("%s: %q gave %v", name, s, got)
				}
			}
			for _, s := range []string{"café 世界", "quotes \" and <tags> & more", "emoji \U0001f600", "back\\slash"} {
				r := validRequest(f.op)
				f.set(r, s)
				if _, err := Validate(r); err != nil {
					t.Errorf("ordinary text %q refused: %v", s, err)
				}
			}
		})
	}
}

// The same holds through Parse: an escaped bidi override in a JSON document is
// the same character and refuses.
func TestParseRefusesEscapedHiddenCharacters(t *testing.T) {
	t.Parallel()
	for _, esc := range []string{`\u202e`, `\u200b`, `\u2028`, `\ufeff`, `\u2066`} {
		doc := fmt.Sprintf(`{"schema":1,"operation":"apply_events","table":"work","epoch":"3","expected_table_revision":"12","actor":"coordinator","inputs":[{"id":"c1","type":"cancel","expect":{"revision":"2","place":{"row":"build","col":"ready"}},"digest":"%s","issuer":"i","source":"s","reason":"a%sb"}]}`, dig, esc)
		if got := parseRefusals(t, doc); !equalStrings(got, wantTriples("0|reason|control-character")) {
			t.Errorf("%s: %v", esc, got)
		}
	}
}
