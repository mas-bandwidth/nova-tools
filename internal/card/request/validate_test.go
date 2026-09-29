package request

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidRequestsPass(t *testing.T) {
	t.Parallel()
	for _, op := range Operations {
		if err := Validate(validRequest(op)); err != nil {
			t.Errorf("%s: %v", op, err)
		}
	}
}

type refusalCase struct {
	name string
	op   Operation
	edit func(r *Request)
	want []string // index|field|cause, exactly
}

func longString(n int) string { return strings.Repeat("x", n) }

func manyAdmissions(n int) []Admission {
	out := make([]Admission, n)
	for i := range out {
		out[i] = adm(fmt.Sprintf("c%d", i))
	}
	return out
}

func manyReplacements(n int) []Replacement {
	out := make([]Replacement, n)
	for i := range out {
		out[i] = Replacement{Old: Retired{ID: ID(fmt.Sprintf("o%d", i)), Digest: Digest(dig), Expect: expect("1", "build", Ready)}, New: adm(fmt.Sprintf("n%d", i))}
	}
	return out
}

func TestRefusals(t *testing.T) {
	t.Parallel()
	cases := []refusalCase{
		// envelope
		{"schema", OpAdmit, func(r *Request) { r.Schema = 2 }, wantTriples("-1|schema|bad-value")},
		{"schema missing", OpAdmit, func(r *Request) { r.Schema = 0 }, wantTriples("-1|schema|bad-value")},
		{"operation empty", OpAdmit, func(r *Request) { r.Operation = "" }, wantTriples("-1|operation|required")},
		{"operation unknown", OpAdmit, func(r *Request) { r.Operation = "bogus" }, wantTriples("-1|operation|bad-value")},
		{"table empty", OpAdmit, func(r *Request) { r.Table = "" }, wantTriples("-1|table|required")},
		{"table grammar", OpAdmit, func(r *Request) { r.Table = "bad table" }, wantTriples("-1|table|bad-value")},
		{"table too long", OpAdmit, func(r *Request) { r.Table = longString(MaxNameBytes + 1) }, wantTriples("-1|table|too-long")},
		{"table control", OpAdmit, func(r *Request) { r.Table = "wo\x00rk" }, wantTriples("-1|table|control-character")},
		{"table invalid utf8", OpAdmit, func(r *Request) { r.Table = "wo\xffrk" }, wantTriples("-1|table|invalid-utf8")},
		{"table replacement rune", OpAdmit, func(r *Request) { r.Table = "wo�rk" }, wantTriples("-1|table|invalid-utf8")},
		{"epoch empty", OpAdmit, func(r *Request) { r.Epoch = "" }, wantTriples("-1|epoch|required")},
		{"epoch leading zero", OpAdmit, func(r *Request) { r.Epoch = "03" }, wantTriples("-1|epoch|bad-value")},
		{"epoch float", OpAdmit, func(r *Request) { r.Epoch = "1.5" }, wantTriples("-1|epoch|bad-value")},
		{"epoch negative", OpAdmit, func(r *Request) { r.Epoch = "-1" }, wantTriples("-1|epoch|bad-value")},
		{"epoch exponent", OpAdmit, func(r *Request) { r.Epoch = "1e3" }, wantTriples("-1|epoch|bad-value")},
		{"epoch over uint64", OpAdmit, func(r *Request) { r.Epoch = "18446744073709551616" }, wantTriples("-1|epoch|bad-value")},
		{"epoch too many digits", OpAdmit, func(r *Request) { r.Epoch = "100000000000000000000" }, wantTriples("-1|epoch|too-long")},
		{"epoch max uint64 ok", OpAdmit, func(r *Request) { r.Epoch = "18446744073709551615" }, nil},
		{"epoch zero ok", OpAdmit, func(r *Request) { r.Epoch = "0" }, nil},
		{"revision over", OpAdmit, func(r *Request) { r.TableRevision = "18446744073709551616" }, wantTriples("-1|expected_table_revision|bad-value")},
		{"revision empty", OpAdmit, func(r *Request) { r.TableRevision = "" }, wantTriples("-1|expected_table_revision|required")},
		{"operation id newline", OpAdmit, func(r *Request) { r.OperationID = "a\nb" }, wantTriples("-1|operation_id|control-character")},
		{"operation id empty", OpAdmit, func(r *Request) { r.OperationID = "" }, wantTriples("-1|operation_id|required")},
		{"operation id too long", OpAdmit, func(r *Request) { r.OperationID = longString(MaxIdentityBytes + 1) }, wantTriples("-1|operation_id|too-long")},
		{"actor space", OpAdmit, func(r *Request) { r.Actor = "a b" }, wantTriples("-1|actor|bad-value")},
		{"actor empty", OpAdmit, func(r *Request) { r.Actor = "" }, wantTriples("-1|actor|required")},
		{"inspect epoch", OpInspect, func(r *Request) { r.Epoch = "1" }, wantTriples("-1|epoch|not-applicable")},
		{"inspect operation id", OpInspect, func(r *Request) { r.OperationID = "op" }, wantTriples("-1|operation_id|not-applicable")},
		{"inspect actor", OpInspect, func(r *Request) { r.Actor = "me" }, wantTriples("-1|actor|not-applicable")},
		{"inspect revision", OpInspect, func(r *Request) { r.TableRevision = "1" }, wantTriples("-1|expected_table_revision|not-applicable")},

		// payload selection
		{"admit with events", OpAdmit, func(r *Request) { r.Events = []Event{event(EvStart, "c1")} }, wantTriples("-1|events|not-applicable")},
		{"admit without admissions", OpAdmit, func(r *Request) { r.Admissions = nil }, wantTriples("-1|admissions|required")},
		{"events without events", OpApplyEvents, func(r *Request) { r.Events = nil }, wantTriples("-1|events|required")},
		{"resolve without scope", OpResolve, func(r *Request) { r.Scope = nil }, wantTriples("-1|scope|required")},
		{"inspect with admissions", OpInspect, func(r *Request) { r.Admissions = []Admission{adm("c1")} }, wantTriples("-1|admissions|not-applicable")},
		{"replace with scope", OpReplace, func(r *Request) { r.Scope = &Scope{IDs: []ID{"a"}} }, wantTriples("-1|scope|not-applicable")},

		// counts
		{"admissions empty", OpAdmit, func(r *Request) { r.Admissions = []Admission{} }, wantTriples("-1|admissions|empty-array")},
		{"events empty", OpApplyEvents, func(r *Request) { r.Events = []Event{} }, wantTriples("-1|events|empty-array")},
		{"evidence empty", OpRecordEvidence, func(r *Request) { r.Evidence = []Evidence{} }, wantTriples("-1|evidence|empty-array")},
		{"replacements empty", OpReplace, func(r *Request) { r.Replacements = []Replacement{} }, wantTriples("-1|replacements|empty-array")},
		{"scope ids empty", OpInspect, func(r *Request) { r.Scope = &Scope{IDs: []ID{}} }, wantTriples("-1|scope.ids|empty-array")},
		{"admissions at the bound", OpAdmit, func(r *Request) { r.Admissions = manyAdmissions(MaxChangedEntries) }, nil},
		{"admissions over the bound", OpAdmit, func(r *Request) { r.Admissions = manyAdmissions(MaxChangedEntries + 1) }, wantTriples("-1|admissions|too-many")},
		{"replacements at the bound", OpReplace, func(r *Request) { r.Replacements = manyReplacements(MaxReplacementPairs) }, nil},
		{"replacements over the bound", OpReplace, func(r *Request) { r.Replacements = manyReplacements(MaxReplacementPairs + 1) }, wantTriples("-1|replacements|too-many")},
		{"scope ids at the bound", OpInspect, func(r *Request) { r.Scope = &Scope{IDs: idsN(MaxGuardOnlyEntries)} }, nil},
		{"scope ids over the bound", OpInspect, func(r *Request) { r.Scope = &Scope{IDs: idsN(MaxGuardOnlyEntries + 1)} }, wantTriples("-1|scope.ids|too-many")},
		{"records over the bound", OpRecordEvidence, func(r *Request) {
			for len(r.Evidence[0].Records) <= MaxEvidenceRecordsPerCard {
				n := len(r.Evidence[0].Records)
				r.Evidence[0].Records = append(r.Evidence[0].Records, EvidenceRecord{EvidenceID: ID(fmt.Sprintf("e%d", n)), Kind: KindRead, Disposition: DispAccept, Issuer: "r", Source: "s"})
			}
		}, wantTriples("0|records|too-many")},
		{"records empty", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records = []EvidenceRecord{} }, wantTriples("0|records|empty-array")},
		{"records missing", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records = nil }, wantTriples("0|records|required")},

		// admissions
		{"admission id empty", OpAdmit, func(r *Request) { r.Admissions[0].ID = "" }, wantTriples("0|id|required")},
		{"admission id space", OpAdmit, func(r *Request) { r.Admissions[0].ID = "a b" }, wantTriples("0|id|bad-value")},
		{"admission id colon", OpAdmit, func(r *Request) { r.Admissions[0].ID = "card:1" }, wantTriples("0|id|bad-value")},
		{"admission id comma", OpAdmit, func(r *Request) { r.Admissions[0].ID = "a,b" }, wantTriples("0|id|bad-value")},
		{"admission id too long", OpAdmit, func(r *Request) { r.Admissions[0].ID = ID(longString(MaxIDBytes + 1)) }, wantTriples("0|id|too-long")},
		{"admission id at limit", OpAdmit, func(r *Request) { r.Admissions[0].ID = ID(longString(MaxIDBytes)) }, nil},
		{"admission digest upper", OpAdmit, func(r *Request) { r.Admissions[1].Digest = Digest(strings.ToUpper(dig)) }, wantTriples("1|digest|bad-value")},
		{"admission digest short", OpAdmit, func(r *Request) { r.Admissions[1].Digest = "abc" }, wantTriples("1|digest|bad-value")},
		{"admission digest empty", OpAdmit, func(r *Request) { r.Admissions[1].Digest = "" }, wantTriples("1|digest|required")},
		{"admission object id 39", OpAdmit, func(r *Request) { r.Admissions[0].ObjectID = g40[:39] }, wantTriples("0|object_id|bad-value")},
		{"admission commit", OpAdmit, func(r *Request) { r.Admissions[0].Commit = "HEAD" }, wantTriples("0|commit|bad-value")},
		{"admission repository space", OpAdmit, func(r *Request) { r.Admissions[0].Repository = "a b" }, wantTriples("0|repository|bad-value")},
		{"admission repository too long", OpAdmit, func(r *Request) { r.Admissions[0].Repository = longString(MaxRefBytes + 1) }, wantTriples("0|repository|too-long")},
		{"admission path dotdot", OpAdmit, func(r *Request) { r.Admissions[0].Path = "../x.md" }, wantTriples("0|path|bad-value")},
		{"admission path absolute", OpAdmit, func(r *Request) { r.Admissions[0].Path = "/x.md" }, wantTriples("0|path|bad-value")},
		{"admission path empty segment", OpAdmit, func(r *Request) { r.Admissions[0].Path = "a//b" }, wantTriples("0|path|bad-value")},
		{"admission path backslash", OpAdmit, func(r *Request) { r.Admissions[0].Path = `a\b` }, wantTriples("0|path|bad-value")},
		{"admission path dot", OpAdmit, func(r *Request) { r.Admissions[0].Path = "a/./b" }, wantTriples("0|path|bad-value")},
		{"admission path trailing slash", OpAdmit, func(r *Request) { r.Admissions[0].Path = "a/" }, wantTriples("0|path|bad-value")},
		{"admission path too long", OpAdmit, func(r *Request) { r.Admissions[0].Path = longString(MaxPathBytes + 1) }, wantTriples("0|path|too-long")},
		{"admission path control", OpAdmit, func(r *Request) { r.Admissions[0].Path = "a\tb" }, wantTriples("0|path|control-character")},
		{"admission row space", OpAdmit, func(r *Request) { r.Admissions[0].Row = "a b" }, wantTriples("0|row|bad-value")},
		{"admission row empty", OpAdmit, func(r *Request) { r.Admissions[0].Row = "" }, wantTriples("0|row|required")},
		{"admission repeated id", OpAdmit, func(r *Request) { r.Admissions[1].ID = r.Admissions[0].ID }, wantTriples("1|id|repeated-id")},
		{"admission three entries repeated", OpAdmit, func(r *Request) {
			r.Admissions = append(r.Admissions, adm("c1"))
		}, wantTriples("2|id|repeated-id")},

		// events
		{"event id empty", OpApplyEvents, func(r *Request) { r.Events[0].ID = "" }, wantTriples("0|id|required")},
		{"event type empty", OpApplyEvents, func(r *Request) { r.Events[0].Type = "" }, wantTriples("0|type|required")},
		{"event type unknown", OpApplyEvents, func(r *Request) { r.Events[0].Type = "teleport" }, wantTriples("0|type|bad-value")},
		{"event type verdict alone", OpApplyEvents, func(r *Request) { r.Events[0].Type = "verdict" }, wantTriples("0|type|bad-value")},
		{"event revision zero", OpApplyEvents, func(r *Request) { r.Events[0].Expect.Revision = "0" }, wantTriples("0|expect.revision|bad-value")},
		{"event revision empty", OpApplyEvents, func(r *Request) { r.Events[0].Expect.Revision = "" }, wantTriples("0|expect.revision|required")},
		{"event row bad", OpApplyEvents, func(r *Request) { r.Events[0].Expect.Place.Row = "no way" }, wantTriples("0|expect.place.row|bad-value")},
		{"event col unknown", OpApplyEvents, func(r *Request) { r.Events[0].Expect.Place.Col = "limbo" }, wantTriples("0|expect.place.col|bad-value")},
		{"event col empty", OpApplyEvents, func(r *Request) { r.Events[0].Expect.Place.Col = "" }, wantTriples("0|expect.place.col|required")},
		{"event digest", OpApplyEvents, func(r *Request) { r.Events[0].Digest = "zz" }, wantTriples("0|digest|bad-value")},
		{"event issuer empty", OpApplyEvents, func(r *Request) { r.Events[0].Issuer = "" }, wantTriples("0|issuer|required")},
		{"event source empty", OpApplyEvents, func(r *Request) { r.Events[0].Source = "" }, wantTriples("0|source|required")},
		{"event source space", OpApplyEvents, func(r *Request) { r.Events[0].Source = "a b" }, wantTriples("0|source|bad-value")},
		{"start with head", OpApplyEvents, func(r *Request) { r.Events[0].Head = g40 }, wantTriples("0|head|not-applicable")},
		{"start with reason", OpApplyEvents, func(r *Request) { r.Events[0].Reason = "why" }, wantTriples("0|reason|not-applicable")},
		{"start with landing", OpApplyEvents, func(r *Request) { r.Events[0].Landing = g40 }, wantTriples("0|landing|not-applicable")},
		{"start with result", OpApplyEvents, func(r *Request) { r.Events[0].Result = ResultSuccess }, wantTriples("0|result|not-applicable")},
		{"start with dependency", OpApplyEvents, func(r *Request) { r.Events[0].Dependency = "d" }, wantTriples("0|dependency|not-applicable")},
		{"result without result", OpApplyEvents, func(r *Request) { r.Events[1].Result = "" }, wantTriples("1|result|required")},
		{"result unknown", OpApplyEvents, func(r *Request) { r.Events[1].Result = "meh" }, wantTriples("1|result|bad-value")},
		{"result failure ok", OpApplyEvents, func(r *Request) { r.Events[1].Result = ResultFailure }, nil},
		{"result return ok", OpApplyEvents, func(r *Request) { r.Events[1].Result = ResultReturn }, nil},
		{"result with head ok", OpApplyEvents, func(r *Request) { r.Events[1].Head = g64 }, nil},
		{"result bad head", OpApplyEvents, func(r *Request) { r.Events[1].Head = "nothex" }, wantTriples("1|head|bad-value")},
		{"accept without head", OpApplyEvents, func(r *Request) { r.Events[2].Head = "" }, wantTriples("2|head|required")},
		{"accept with reason", OpApplyEvents, func(r *Request) { r.Events[2].Reason = "because" }, wantTriples("2|reason|not-applicable")},
		{"retry without reason", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvVerdictRetry, "c1")}; r.Events[0].Reason = "" }, wantTriples("0|reason|required")},
		{"rework reason leading space", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvVerdictRework, "c1")}; r.Events[0].Reason = " x" }, wantTriples("0|reason|bad-value")},
		{"rework reason too long", OpApplyEvents, func(r *Request) {
			r.Events = []Event{event(EvVerdictRework, "c1")}
			r.Events[0].Reason = longString(MaxReasonBytes + 1)
		}, wantTriples("0|reason|too-long")},
		{"rework reason newline", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvVerdictRework, "c1")}; r.Events[0].Reason = "a\nb" }, wantTriples("0|reason|control-character")},
		{"rework reason html ok", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvVerdictRework, "c1")}; r.Events[0].Reason = "a<b>&c" }, nil},
		{"cancel without reason", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvCancel, "c1")}; r.Events[0].Reason = "" }, wantTriples("0|reason|required")},
		{"landing without landing", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvLanding, "c1")}; r.Events[0].Landing = "" }, wantTriples("0|landing|required")},
		{"landing without head", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvLanding, "c1")}; r.Events[0].Head = "" }, wantTriples("0|head|required")},
		{"external landing without landing", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvExternalLanding, "c1")}; r.Events[0].Landing = "" }, wantTriples("0|landing|required")},
		{"dependency failed without dependency", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvDependencyFailed, "c1")}; r.Events[0].Dependency = "" }, wantTriples("0|dependency|required")},
		{"dependency on itself", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvDependencyFailed, "c1")}; r.Events[0].Dependency = "c1" }, wantTriples("0|dependency|bad-value")},
		{"dependency bad id", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvDependencyFailed, "c1")}; r.Events[0].Dependency = "a b" }, wantTriples("0|dependency|bad-value")},
		{"head event without head", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvHead, "c1")}; r.Events[0].Head = "" }, wantTriples("0|head|required")},
		{"completed with head", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvCompleted, "c1")}; r.Events[0].Head = g40 }, wantTriples("0|head|not-applicable")},

		// the derived destination
		{"start from working", OpApplyEvents, func(r *Request) { r.Events[0].Expect.Place.Col = Working }, wantTriples("0|expect.place.col|no-transition")},
		{"start from waiting", OpApplyEvents, func(r *Request) { r.Events[0].Expect.Place.Col = Waiting }, wantTriples("0|expect.place.col|no-transition")},
		{"result from ready", OpApplyEvents, func(r *Request) { r.Events[1].Expect.Place.Col = Ready }, wantTriples("1|expect.place.col|no-transition")},
		{"accept from merging", OpApplyEvents, func(r *Request) { r.Events[2].Expect.Place.Col = Merging }, wantTriples("2|expect.place.col|no-transition")},
		{"anything from landed", OpApplyEvents, func(r *Request) { r.Events[0].Expect.Place.Col = Landed }, wantTriples("0|expect.place.col|no-transition")},
		{"anything from done", OpApplyEvents, func(r *Request) { r.Events[0].Expect.Place.Col = Done }, wantTriples("0|expect.place.col|no-transition")},
		{"ci green has no transition", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvCIGreen, "c1")}; r.Events[0].Expect.Place.Col = Merging }, wantTriples("0|expect.place.col|no-transition")},
		{"review to landed by landing", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvLanding, "c1")}; r.Events[0].Expect.Place.Col = Review }, wantTriples("0|expect.place.col|no-transition")},
		{"external landing from review", OpApplyEvents, func(r *Request) {
			r.Events = []Event{event(EvExternalLanding, "c1")}
			r.Events[0].Expect.Place.Col = Review
		}, wantTriples("0|expect.place.col|no-transition")},
		{"external landing from merging", OpApplyEvents, func(r *Request) {
			r.Events = []Event{event(EvExternalLanding, "c1")}
			r.Events[0].Expect.Place.Col = Merging
		}, wantTriples("0|expect.place.col|no-transition")},
		{"completed from working", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvCompleted, "c1")}; r.Events[0].Expect.Place.Col = Working }, wantTriples("0|expect.place.col|no-transition")},
		{"dependency failed from ready", OpApplyEvents, func(r *Request) {
			r.Events = []Event{event(EvDependencyFailed, "c1")}
			r.Events[0].Expect.Place.Col = Ready
		}, wantTriples("0|expect.place.col|no-transition")},
		{"cancel from landed", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvCancel, "c1")}; r.Events[0].Expect.Place.Col = Landed }, wantTriples("0|expect.place.col|no-transition")},

		// one event per card per request
		{"identical events repeat", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvStart, "c1"), event(EvStart, "c1")} }, wantTriples("1|id|repeated-id")},
		{"conflicting events", OpApplyEvents, func(r *Request) {
			r.Events = []Event{event(EvCancel, "c1"), event(EvExternalLanding, "c1")}
			r.Events[0].Expect.Place.Col = Ready
		}, wantTriples("1|id|conflicting-events")},
		{"conflicting: same type other head", OpApplyEvents, func(r *Request) {
			r.Events = []Event{event(EvVerdictAccept, "c1"), event(EvVerdictAccept, "c1")}
			r.Events[1].Head = g64
		}, wantTriples("1|id|conflicting-events")},
		{"event chain start then result", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvStart, "c1"), event(EvResult, "c1")} }, wantTriples("1|expect.place.col|event-chain")},
		{"event chain listed in reverse", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvResult, "c1"), event(EvStart, "c1")} }, wantTriples("1|expect.place.col|event-chain")},
		{"event chain result then accept", OpApplyEvents, func(r *Request) { r.Events = []Event{event(EvResult, "c1"), event(EvVerdictAccept, "c1")} }, wantTriples("1|expect.place.col|event-chain")},
		{"same source different cards ok", OpApplyEvents, func(r *Request) {
			r.Events = []Event{event(EvStart, "c1"), event(EvStart, "c2"), event(EvResult, "c3")}
		}, nil},

		// evidence
		{"evidence kind unknown", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Kind = "vibes" }, wantTriples("0|records[0].kind|bad-value")},
		{"evidence read green", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Disposition = DispGreen }, wantTriples("0|records[0].disposition|bad-value")},
		{"evidence ci accept", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Disposition = DispAccept }, wantTriples("0|records[1].disposition|bad-value")},
		{"evidence ci red ok", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Disposition = DispRed }, nil},
		{"evidence read reject ok", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Disposition = DispReject }, nil},
		{"evidence ci without head", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Head = "" }, wantTriples("0|records[1].head|required")},
		{"evidence read without head ok", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Head = "" }, nil},
		{"evidence bad head", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Head = "beef" }, wantTriples("0|records[0].head|bad-value")},
		{"evidence issuer empty", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Issuer = "" }, wantTriples("0|records[0].issuer|required")},
		{"evidence source empty", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Source = "" }, wantTriples("0|records[1].source|required")},
		{"evidence id empty", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].EvidenceID = "" }, wantTriples("0|records[1].evidence_id|required")},
		{"evidence id repeated", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].EvidenceID = r.Evidence[0].Records[0].EvidenceID }, wantTriples("0|records[1].evidence_id|repeated-id")},
		{"evidence id repeated across cards", OpRecordEvidence, func(r *Request) {
			r.Evidence = append(r.Evidence, evidenceEntry("c2"))
			r.Evidence[1].Records[0].EvidenceID = r.Evidence[0].Records[0].EvidenceID
		}, wantTriples("1|records[0].evidence_id|repeated-id")},
		{"evidence card repeated", OpRecordEvidence, func(r *Request) { r.Evidence = append(r.Evidence, r.Evidence[0]) }, wantTriples("1|id|repeated-id", "1|records[0].evidence_id|repeated-id", "1|records[1].evidence_id|repeated-id")},
		{"evidence digest", OpRecordEvidence, func(r *Request) { r.Evidence[0].Digest = "" }, wantTriples("0|digest|required")},
		{"evidence revision", OpRecordEvidence, func(r *Request) { r.Evidence[0].Expect.Revision = "0" }, wantTriples("0|expect.revision|bad-value")},

		// replacements
		{"replace old working", OpReplace, func(r *Request) { r.Replacements[0].Old.Expect.Place.Col = Working }, wantTriples("0|old.expect.place.col|not-eligible")},
		{"replace old review", OpReplace, func(r *Request) { r.Replacements[0].Old.Expect.Place.Col = Review }, wantTriples("0|old.expect.place.col|not-eligible")},
		{"replace old landed", OpReplace, func(r *Request) { r.Replacements[0].Old.Expect.Place.Col = Landed }, wantTriples("0|old.expect.place.col|not-eligible")},
		{"replace old done", OpReplace, func(r *Request) { r.Replacements[0].Old.Expect.Place.Col = Done }, wantTriples("0|old.expect.place.col|not-eligible")},
		{"replace old waiting ok", OpReplace, func(r *Request) { r.Replacements[0].Old.Expect.Place.Col = Waiting }, nil},
		{"replace same id", OpReplace, func(r *Request) { r.Replacements[0].New.ID = r.Replacements[0].Old.ID }, wantTriples("0|new.id|repeated-id")},
		{"replace new id in other pair", OpReplace, func(r *Request) {
			r.Replacements = append(r.Replacements, Replacement{Old: Retired{ID: "old2", Digest: Digest(dig), Expect: expect("1", "build", Ready)}, New: adm("new1")})
		}, wantTriples("1|new.id|repeated-id")},
		{"replace old id twice", OpReplace, func(r *Request) {
			r.Replacements = append(r.Replacements, Replacement{Old: Retired{ID: "old1", Digest: Digest(dig), Expect: expect("1", "build", Ready)}, New: adm("new2")})
		}, wantTriples("1|old.id|repeated-id")},
		{"replace old is another new", OpReplace, func(r *Request) {
			r.Replacements = append(r.Replacements, Replacement{Old: Retired{ID: "new1", Digest: Digest(dig), Expect: expect("1", "build", Ready)}, New: adm("new2")})
		}, wantTriples("1|old.id|repeated-id")},
		{"replace old digest", OpReplace, func(r *Request) { r.Replacements[0].Old.Digest = "" }, wantTriples("0|old.digest|required")},
		{"replace new path", OpReplace, func(r *Request) { r.Replacements[0].New.Path = "../evil" }, wantTriples("0|new.path|bad-value")},
		{"replace new digest", OpReplace, func(r *Request) { r.Replacements[0].New.Digest = "x" }, wantTriples("0|new.digest|bad-value")},

		// scope
		{"scope ids and row", OpInspect, func(r *Request) { r.Scope.Row = "build" }, wantTriples("-1|scope|bad-value")},
		{"scope ids and bound", OpInspect, func(r *Request) { r.Scope.Bound = 5 }, wantTriples("-1|scope|bad-value")},
		{"scope nothing", OpInspect, func(r *Request) { r.Scope = &Scope{} }, wantTriples("-1|scope|required")},
		{"scope id bad", OpInspect, func(r *Request) { r.Scope.IDs[1] = "a b" }, wantTriples("-1|scope.ids[1]|bad-value")},
		{"scope id repeated", OpInspect, func(r *Request) { r.Scope.IDs[1] = r.Scope.IDs[0] }, wantTriples("-1|scope.ids[1]|repeated-id")},
		{"selection row only", OpResolve, func(r *Request) { r.Scope = &Scope{Row: "build", Bound: 10} }, nil},
		{"selection col only", OpResolve, func(r *Request) { r.Scope = &Scope{Col: Waiting, Bound: 10} }, nil},
		{"selection without bound", OpResolve, func(r *Request) { r.Scope.Bound = 0 }, wantTriples("-1|scope.bound|required")},
		{"selection negative bound", OpResolve, func(r *Request) { r.Scope.Bound = -1 }, wantTriples("-1|scope.bound|bad-value")},
		{"selection bound at limit", OpResolve, func(r *Request) { r.Scope.Bound = MaxGuardOnlyEntries }, nil},
		{"selection bound over", OpResolve, func(r *Request) { r.Scope.Bound = MaxGuardOnlyEntries + 1 }, wantTriples("-1|scope.bound|too-many")},
		{"selection bound alone", OpResolve, func(r *Request) { r.Scope = &Scope{Bound: 4} }, wantTriples("-1|scope|required")},
		{"selection bad col", OpResolve, func(r *Request) { r.Scope.Col = "limbo" }, wantTriples("-1|scope.col|bad-value")},
		{"selection bad row", OpResolve, func(r *Request) { r.Scope.Row = "a b" }, wantTriples("-1|scope.row|bad-value")},

		// several at once
		{"several refusals reported together", OpApplyEvents, func(r *Request) {
			r.Table = ""
			r.Events[0].Digest = "x"
			r.Events[1].Expect.Place.Col = Done
			r.Events[2].Head = ""
			r.Events = append(r.Events, event(EvStart, "c1"))
		}, wantTriples("-1|table|required", "0|digest|bad-value", "1|expect.place.col|no-transition", "2|head|required", "3|id|conflicting-events")},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := validRequest(c.op)
			c.edit(r)
			got := triples(Validate(r))
			if !equalStrings(got, c.want) {
				t.Fatalf("refusals\n got  %v\n want %v", got, c.want)
			}
			// A refused request never half-accepts: Parse of its canonical form
			// refuses the same way (or the request was valid and round trips).
			if len(c.want) == 0 {
				if _, err := Parse(Canonical(r)); err != nil {
					t.Fatalf("valid request did not parse: %v", err)
				}
			}
		})
	}
}

func TestRefusalsCarryLimitAndRemedyForBounds(t *testing.T) {
	t.Parallel()
	r := validRequest(OpAdmit)
	r.Admissions = manyAdmissions(MaxChangedEntries + 1)
	err := Validate(r).(*Refusals)
	f := err.List[0]
	if f.Cause != CauseTooMany || f.Limit != "128 entries" || f.Remedy == "" || f.Found != "129 entries" || f.Operation != OpAdmit || f.Index != -1 {
		t.Fatalf("refusal = %+v", f)
	}
	line := f.Line()
	for _, want := range []string{"refused admit", "field=admissions", "too-many", "found 129 entries", "limit 128 entries", "remedy:"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "\n") {
		t.Errorf("line breaks: %q", line)
	}
}

func TestRefusalNamesCardIDAndIndex(t *testing.T) {
	t.Parallel()
	r := validRequest(OpApplyEvents)
	r.Events[1].Expect.Place.Col = Done
	f := Validate(r).(*Refusals).List[0]
	if f.Index != 1 || f.ID != "c2" || f.Field != "expect.place.col" || f.Cause != CauseNoTransition || f.Operation != OpApplyEvents {
		t.Fatalf("refusal = %+v", f)
	}
	if f.Found != `"result@done"` || !strings.Contains(f.Limit, "working") {
		t.Fatalf("found %q limit %q", f.Found, f.Limit)
	}
	if want := `refused apply_events[1] card=c2 field=expect.place.col: no-transition; found "result@done"; limit source state one of working; remedy: declare a source state the type moves from; the destination is derived, never sent`; f.Line() != want {
		t.Fatalf("line\n got  %s\n want %s", f.Line(), want)
	}
}

func TestRefusalListIsBoundedAndCounted(t *testing.T) {
	t.Parallel()
	r := validRequest(OpAdmit)
	r.Admissions = manyAdmissions(MaxChangedEntries)
	for i := range r.Admissions {
		r.Admissions[i].Digest = "bad"
		r.Admissions[i].Row = "bad row"
	}
	rs := Validate(r).(*Refusals)
	if len(rs.List) != MaxRefusals || rs.Omitted != 2*MaxChangedEntries-MaxRefusals {
		t.Fatalf("list %d omitted %d", len(rs.List), rs.Omitted)
	}
	lines := rs.Lines()
	if len(lines) != MaxRefusals+1 || !strings.Contains(lines[len(lines)-1], "omitted") {
		t.Fatalf("lines: %d, last %q", len(lines), lines[len(lines)-1])
	}
	if !strings.Contains(rs.Error(), fmt.Sprintf("and %d more", 2*MaxChangedEntries-1)) {
		t.Fatalf("error %q", rs.Error())
	}
}

func TestCanonicalSizeBound(t *testing.T) {
	t.Parallel()
	r := validRequest(OpAdmit)
	r.Admissions = manyAdmissions(MaxChangedEntries)
	for i := range r.Admissions {
		r.Admissions[i].Path = longString(9000)
	}
	rs := Validate(r).(*Refusals)
	first := rs.List[0]
	if first.Cause != CauseTooLarge || first.Limit != "1048576 bytes" || first.Remedy == "" {
		t.Fatalf("first refusal = %+v", first)
	}
	if !rs.Has(0, "path", CauseTooLong) {
		t.Fatal("entry refusals missing")
	}
}

func TestValidateNilAndZero(t *testing.T) {
	t.Parallel()
	if err := Validate(nil); err == nil {
		t.Fatal("nil request accepted")
	}
	if err := Validate(&Request{}); err == nil {
		t.Fatal("zero request accepted")
	}
}

func TestValidationNeverPartiallyAccepts(t *testing.T) {
	t.Parallel()
	// One bad entry among many good ones refuses the request whole.
	r := validRequest(OpAdmit)
	r.Admissions = manyAdmissions(50)
	r.Admissions[49].Path = "../escape"
	if err := Validate(r); err == nil {
		t.Fatal("request with one bad entry accepted")
	}
}

func TestIdentityAndValidators(t *testing.T) {
	t.Parallel()
	r := validRequest(OpAdmit)
	if got := r.Identity().String(); got != "work/3/op-17" {
		t.Fatalf("identity %q", got)
	}
	if !ValidID("a_B-9") || ValidID("") || ValidID("a.b") || ValidID("é") {
		t.Fatal("ValidID drifted")
	}
	if !Digest(dig).Valid() || Digest("").Valid() {
		t.Fatal("Digest.Validate drifted")
	}
	if !ID("x").Valid() || ID("x y").Valid() {
		t.Fatal("ID.Validate drifted")
	}
	if !OpInspect.Valid() || OpInspect.Mutating() || !OpReplace.Mutating() || Operation("x").Mutating() {
		t.Fatal("Operation predicates drifted")
	}
	if !EvCancel.Valid() || EventType("nope").Valid() || !Ready.Valid() || State("x").Valid() {
		t.Fatal("predicates drifted")
	}
}
