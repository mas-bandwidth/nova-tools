package request

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

func TestValidRequestsPass(t *testing.T) {
	t.Parallel()
	for _, op := range Operations() {
		if _, err := Validate(validRequest(op)); err != nil {
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
		{"schema", OpAdmit, func(r *Request) { r.Schema = 2 }, wantTriples("-1|schema|invalid-value")},
		{"schema missing", OpAdmit, func(r *Request) { r.Schema = 0 }, wantTriples("-1|schema|invalid-value")},
		{"operation empty", OpAdmit, func(r *Request) { r.Operation = "" }, wantTriples("-1|operation|required")},
		{"operation unknown", OpAdmit, func(r *Request) { r.Operation = "bogus" }, wantTriples("-1|operation|invalid-value")},
		{"table empty", OpAdmit, func(r *Request) { r.Table = "" }, wantTriples("-1|table|required")},
		{"table grammar", OpAdmit, func(r *Request) { r.Table = "bad table" }, wantTriples("-1|table|invalid-value")},
		{"table too long", OpAdmit, func(r *Request) { r.Table = longString(MaxNameBytes + 1) }, wantTriples("-1|table|too-long")},
		{"table control", OpAdmit, func(r *Request) { r.Table = "wo\x00rk" }, wantTriples("-1|table|control-character")},
		{"table invalid utf8", OpAdmit, func(r *Request) { r.Table = "wo\xffrk" }, wantTriples("-1|table|invalid-utf8")},
		{"table replacement rune", OpAdmit, func(r *Request) { r.Table = "wo�rk" }, wantTriples("-1|table|invalid-utf8")},
		{"epoch empty", OpAdmit, func(r *Request) { r.Epoch = "" }, wantTriples("-1|epoch|required")},
		{"epoch leading zero", OpAdmit, func(r *Request) { r.Epoch = "03" }, wantTriples("-1|epoch|invalid-value")},
		{"epoch float", OpAdmit, func(r *Request) { r.Epoch = "1.5" }, wantTriples("-1|epoch|invalid-value")},
		{"epoch negative", OpAdmit, func(r *Request) { r.Epoch = "-1" }, wantTriples("-1|epoch|invalid-value")},
		{"epoch exponent", OpAdmit, func(r *Request) { r.Epoch = "1e3" }, wantTriples("-1|epoch|invalid-value")},
		{"epoch over uint64", OpAdmit, func(r *Request) { r.Epoch = "18446744073709551616" }, wantTriples("-1|epoch|invalid-value")},
		{"epoch too many digits", OpAdmit, func(r *Request) { r.Epoch = "100000000000000000000" }, wantTriples("-1|epoch|too-long")},
		{"epoch max uint64 ok", OpAdmit, func(r *Request) { r.Epoch = "18446744073709551615" }, nil},
		{"epoch zero ok", OpAdmit, func(r *Request) { r.Epoch = "0" }, nil},
		{"revision over", OpAdmit, func(r *Request) { r.TableRevision = "18446744073709551616" }, wantTriples("-1|expected_table_revision|invalid-value")},
		{"revision empty", OpAdmit, func(r *Request) { r.TableRevision = "" }, wantTriples("-1|expected_table_revision|required")},
		{"operation id newline", OpAdmit, func(r *Request) { r.OperationID = "a\nb" }, wantTriples("-1|operation_id|control-character")},
		{"operation id empty is derived", OpAdmit, func(r *Request) { r.OperationID = "" }, nil},
		{"operation id with a space", OpAdmit, func(r *Request) { r.OperationID = "a b" }, wantTriples("-1|operation_id|invalid-value")},
		{"operation id too long", OpAdmit, func(r *Request) { r.OperationID = longString(MaxIdentityBytes + 1) }, wantTriples("-1|operation_id|too-long")},
		{"actor space", OpAdmit, func(r *Request) { r.Actor = "a b" }, wantTriples("-1|actor|invalid-value")},
		{"actor empty", OpAdmit, func(r *Request) { r.Actor = "" }, wantTriples("-1|actor|required")},
		{"inspect epoch", OpInspect, func(r *Request) { r.Epoch = "1" }, wantTriples("-1|epoch|not-applicable")},
		{"inspect operation id", OpInspect, func(r *Request) { r.OperationID = "op" }, wantTriples("-1|operation_id|not-applicable")},
		{"inspect actor", OpInspect, func(r *Request) { r.Actor = "me" }, wantTriples("-1|actor|not-applicable")},
		{"inspect revision", OpInspect, func(r *Request) { r.TableRevision = "1" }, wantTriples("-1|expected_table_revision|not-applicable")},

		// payload selection
		{"admit with events", OpAdmit, func(r *Request) { r.Inputs = []Input{input(InStart, "c1")} }, wantTriples("-1|inputs|not-applicable")},
		{"admit without admissions", OpAdmit, func(r *Request) { r.Admissions = nil }, wantTriples("-1|admissions|required")},
		{"inputs without inputs", OpApplyEvents, func(r *Request) { r.Inputs = nil }, wantTriples("-1|inputs|required")},
		{"resolve without scope", OpResolve, func(r *Request) { r.Scope = nil }, wantTriples("-1|scope|required")},
		{"inspect with admissions", OpInspect, func(r *Request) { r.Admissions = []Admission{adm("c1")} }, wantTriples("-1|admissions|not-applicable")},
		{"replace with scope", OpReplace, func(r *Request) { r.Scope = &Scope{IDs: []ID{"a"}} }, wantTriples("-1|scope|not-applicable")},

		// counts
		{"admissions empty", OpAdmit, func(r *Request) { r.Admissions = []Admission{} }, wantTriples("-1|admissions|empty-array")},
		{"events empty", OpApplyEvents, func(r *Request) { r.Inputs = []Input{} }, wantTriples("-1|inputs|empty-array")},
		{"evidence empty", OpRecordEvidence, func(r *Request) { r.Evidence = []Evidence{} }, wantTriples("-1|evidence|empty-array")},
		{"replacements empty", OpReplace, func(r *Request) { r.Replacements = []Replacement{} }, wantTriples("-1|replacements|empty-array")},
		{"scope ids empty", OpInspect, func(r *Request) { r.Scope = &Scope{IDs: []ID{}} }, wantTriples("-1|scope.ids|empty-array")},
		{"admissions at the bound", OpAdmit, func(r *Request) { r.Admissions = manyAdmissions(MaxChangedEntries) }, nil},
		{"admissions over the bound", OpAdmit, func(r *Request) { r.Admissions = manyAdmissions(MaxChangedEntries + 1) }, wantTriples("-1|admissions|too-many")},
		{"replacements at the bound", OpReplace, func(r *Request) { r.Replacements = manyReplacements(MaxReplacementPairs) }, nil},
		{"replacements over the bound", OpReplace, func(r *Request) { r.Replacements = manyReplacements(MaxReplacementPairs + 1) }, wantTriples("-1|replacements|too-many")},
		{"scope ids at the bound", OpInspect, func(r *Request) { r.Scope = &Scope{IDs: idsN(MaxScopeCards)} }, nil},

		{"records over the bound", OpRecordEvidence, func(r *Request) {
			r.Evidence[0].Records = evidenceCards(1, MaxEvidenceRecordsPerCard+1)[0].Records
		}, wantTriples("0|records|too-many")},
		{"records at the bound", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records = evidenceCards(1, MaxEvidenceRecordsPerCard)[0].Records }, nil},
		{"records empty", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records = []Record{} }, wantTriples("0|records|empty-array")},
		{"records missing", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records = nil }, wantTriples("0|records|required")},

		// admissions
		{"admission id empty", OpAdmit, func(r *Request) { r.Admissions[0].ID = "" }, wantTriples("0|id|required")},
		{"admission id space", OpAdmit, func(r *Request) { r.Admissions[0].ID = "a b" }, wantTriples("0|id|invalid-value")},
		{"admission id colon", OpAdmit, func(r *Request) { r.Admissions[0].ID = "card:1" }, wantTriples("0|id|invalid-value")},
		{"admission id comma", OpAdmit, func(r *Request) { r.Admissions[0].ID = "a,b" }, wantTriples("0|id|invalid-value")},
		{"admission id too long", OpAdmit, func(r *Request) { r.Admissions[0].ID = ID(longString(MaxIDBytes + 1)) }, wantTriples("0|id|too-long")},
		{"admission id at limit", OpAdmit, func(r *Request) { r.Admissions[0].ID = ID(longString(MaxIDBytes)) }, nil},
		{"admission id none", OpAdmit, func(r *Request) { r.Admissions[0].ID = "none" }, wantTriples("0|id|reserved-word")},
		{"admission id dash", OpAdmit, func(r *Request) { r.Admissions[0].ID = "-" }, wantTriples("0|id|reserved-word")},
		{"admission digest upper", OpAdmit, func(r *Request) { r.Admissions[1].Digest = Digest(strings.ToUpper(dig)) }, wantTriples("1|digest|invalid-value")},
		{"admission digest short", OpAdmit, func(r *Request) { r.Admissions[1].Digest = "abc" }, wantTriples("1|digest|invalid-value")},
		{"admission digest empty", OpAdmit, func(r *Request) { r.Admissions[1].Digest = "" }, wantTriples("1|digest|required")},
		{"admission object id 39", OpAdmit, func(r *Request) { r.Admissions[0].ObjectID = g40[:39] }, wantTriples("0|object_id|invalid-value")},
		{"admission commit", OpAdmit, func(r *Request) { r.Admissions[0].Commit = "HEAD" }, wantTriples("0|commit|invalid-value")},
		{"admission repository space", OpAdmit, func(r *Request) { r.Admissions[0].Repository = "a b" }, wantTriples("0|repository|invalid-repository")},
		{"admission repository too long", OpAdmit, func(r *Request) {
			r.Admissions[0].Repository = card.Repository("h.example/" + longString(card.MaxRepositoryBytes))
		}, wantTriples("0|repository|invalid-repository")},
		{"admission repository is a URL", OpAdmit, func(r *Request) { r.Admissions[0].Repository = "https://tok:secret@example.org/a/b.git" }, wantTriples("0|repository|invalid-repository")},
		{"admission repository upper host", OpAdmit, func(r *Request) { r.Admissions[0].Repository = "Example.org/a/b" }, wantTriples("0|repository|invalid-repository")},
		{"admission repository empty", OpAdmit, func(r *Request) { r.Admissions[0].Repository = "" }, wantTriples("0|repository|required")},
		{"admission path dotdot", OpAdmit, func(r *Request) { r.Admissions[0].Path = "../x.md" }, wantTriples("0|path|path-escapes")},
		{"admission path absolute", OpAdmit, func(r *Request) { r.Admissions[0].Path = "/x.md" }, wantTriples("0|path|path-escapes")},
		{"admission path empty segment", OpAdmit, func(r *Request) { r.Admissions[0].Path = "a//b" }, wantTriples("0|path|invalid-path")},
		{"admission path backslash", OpAdmit, func(r *Request) { r.Admissions[0].Path = `a\b` }, wantTriples("0|path|invalid-path")},
		{"admission path dot", OpAdmit, func(r *Request) { r.Admissions[0].Path = "a/./b" }, wantTriples("0|path|invalid-path")},
		{"admission path trailing slash", OpAdmit, func(r *Request) { r.Admissions[0].Path = "a/" }, wantTriples("0|path|invalid-path")},
		{"admission path too long", OpAdmit, func(r *Request) { r.Admissions[0].Path = longString(card.MaxPathBytes + 1) }, wantTriples("0|path|too-long")},
		{"admission path dot git", OpAdmit, func(r *Request) { r.Admissions[0].Path = "a/.git/config" }, wantTriples("0|path|invalid-path")},
		{"admission path leading dash", OpAdmit, func(r *Request) { r.Admissions[0].Path = "-rf" }, wantTriples("0|path|invalid-path")},
		{"admission path drive", OpAdmit, func(r *Request) { r.Admissions[0].Path = "C:/x.md" }, wantTriples("0|path|path-escapes")},
		{"admission path utf8", OpAdmit, func(r *Request) { r.Admissions[0].Path = "a\xffb" }, wantTriples("0|path|invalid-utf8")},
		{"admission path control", OpAdmit, func(r *Request) { r.Admissions[0].Path = "a\tb" }, wantTriples("0|path|control-character")},
		{"admission row space", OpAdmit, func(r *Request) { r.Admissions[0].Row = "a b" }, wantTriples("0|row|invalid-value")},
		{"admission row empty", OpAdmit, func(r *Request) { r.Admissions[0].Row = "" }, wantTriples("0|row|required")},
		{"admission kind empty", OpAdmit, func(r *Request) { r.Admissions[0].Kind = "" }, wantTriples("0|kind|required")},
		{"admission kind upper", OpAdmit, func(r *Request) { r.Admissions[0].Kind = "Read" }, wantTriples("0|kind|invalid-value")},
		{"admission title empty", OpAdmit, func(r *Request) { r.Admissions[0].Title = "" }, wantTriples("0|title|required")},
		{"admission title too long", OpAdmit, func(r *Request) { r.Admissions[0].Title = longString(MaxTitleBytes + 1) }, wantTriples("0|title|too-long")},
		{"admission title at the bound", OpAdmit, func(r *Request) { r.Admissions[0].Title = longString(MaxTitleBytes) }, nil},
		{"admission title bidi", OpAdmit, func(r *Request) { r.Admissions[0].Title = "a\u202eb" }, wantTriples("0|title|control-character")},
		{"admission title padded", OpAdmit, func(r *Request) { r.Admissions[0].Title = " a" }, wantTriples("0|title|invalid-value")},
		{"admission entry comma", OpAdmit, func(r *Request) { r.Admissions[0].Entry = "a,b" }, wantTriples("0|entry|invalid-value")},
		{"admission entry too long", OpAdmit, func(r *Request) { r.Admissions[0].Entry = longString(MaxEntryBytes + 1) }, wantTriples("0|entry|too-long")},
		{"admission entry ok", OpAdmit, func(r *Request) { r.Admissions[0].Entry = "work/x" }, nil},
		{"admission depends on none", OpAdmit, func(r *Request) { r.Admissions[0].DependsOn = []ID{"none"} }, wantTriples("0|depends_on[0]|reserved-word")},
		{"admission depends on dash", OpAdmit, func(r *Request) { r.Admissions[0].DependsOn = []ID{"-"} }, wantTriples("0|depends_on[0]|reserved-word")},
		{"admission depends on itself", OpAdmit, func(r *Request) { r.Admissions[0].DependsOn = []ID{"c1"} }, wantTriples("0|depends_on[0]|invalid-value")},
		{"admission depends twice", OpAdmit, func(r *Request) { r.Admissions[0].DependsOn = []ID{"x", "x"} }, wantTriples("0|depends_on[1]|repeated-id")},
		{"admission depends bad id", OpAdmit, func(r *Request) { r.Admissions[0].DependsOn = []ID{"a b"} }, wantTriples("0|depends_on[0]|invalid-value")},
		{"admission depends at the bound", OpAdmit, func(r *Request) { r.Admissions[0].DependsOn = depIDs(MaxDependsOn) }, nil},
		{"admission depends over the bound", OpAdmit, func(r *Request) { r.Admissions[0].DependsOn = depIDs(MaxDependsOn + 1) }, wantTriples("0|depends_on|too-many")},
		{"admission depends on the other admission ok", OpAdmit, func(r *Request) { r.Admissions[0].DependsOn = []ID{"c2"} }, nil},
		{"admission policy version zero", OpAdmit, func(r *Request) { r.Admissions[0].PolicyVersion = "0" }, wantTriples("0|policy_version|invalid-value")},
		{"admission policy version empty", OpAdmit, func(r *Request) { r.Admissions[0].PolicyVersion = "" }, wantTriples("0|policy_version|required")},
		{"admission policy digest", OpAdmit, func(r *Request) { r.Admissions[0].PolicyDigest = "sha256:" + Digest(dig) }, wantTriples("0|policy_digest|too-long")},
		{"admission policy digest empty", OpAdmit, func(r *Request) { r.Admissions[0].PolicyDigest = "" }, wantTriples("0|policy_digest|required")},
		{"admission repeated id", OpAdmit, func(r *Request) { r.Admissions[1].ID = r.Admissions[0].ID }, wantTriples("1|id|repeated-id")},
		{"admission three entries repeated", OpAdmit, func(r *Request) {
			r.Admissions = append(r.Admissions, adm("c1"))
		}, wantTriples("2|id|repeated-id")},

		// events
		{"event id empty", OpApplyEvents, func(r *Request) { r.Inputs[0].ID = "" }, wantTriples("0|id|required")},
		{"event type empty", OpApplyEvents, func(r *Request) { r.Inputs[0].Type = "" }, wantTriples("0|type|required")},
		{"event type unknown", OpApplyEvents, func(r *Request) { r.Inputs[0].Type = "teleport" }, wantTriples("0|type|invalid-value")},
		{"event type verdict alone", OpApplyEvents, func(r *Request) { r.Inputs[0].Type = "verdict" }, wantTriples("0|type|invalid-value")},
		{"event revision zero", OpApplyEvents, func(r *Request) { r.Inputs[0].Expect.Revision = "0" }, wantTriples("0|expect.revision|invalid-value")},
		{"event revision empty", OpApplyEvents, func(r *Request) { r.Inputs[0].Expect.Revision = "" }, wantTriples("0|expect.revision|required")},
		{"event row bad", OpApplyEvents, func(r *Request) { r.Inputs[0].Expect.Place.Row = "no way" }, wantTriples("0|expect.place.row|invalid-value")},
		{"event col unknown", OpApplyEvents, func(r *Request) { r.Inputs[0].Expect.Place.Col = "limbo" }, wantTriples("0|expect.place.col|invalid-value")},
		{"event col empty", OpApplyEvents, func(r *Request) { r.Inputs[0].Expect.Place.Col = "" }, wantTriples("0|expect.place.col|required")},
		{"event digest", OpApplyEvents, func(r *Request) { r.Inputs[0].Digest = "zz" }, wantTriples("0|digest|invalid-value")},
		{"event issuer empty", OpApplyEvents, func(r *Request) { r.Inputs[0].Issuer = "" }, wantTriples("0|issuer|required")},
		{"event source empty", OpApplyEvents, func(r *Request) { r.Inputs[0].Source = "" }, wantTriples("0|source|required")},
		{"event source space", OpApplyEvents, func(r *Request) { r.Inputs[0].Source = "a b" }, wantTriples("0|source|invalid-value")},
		{"start with head", OpApplyEvents, func(r *Request) { r.Inputs[0].Head = g40 }, wantTriples("0|head|not-applicable")},
		{"start with reason", OpApplyEvents, func(r *Request) { r.Inputs[0].Reason = "why" }, wantTriples("0|reason|not-applicable")},
		{"start with landing", OpApplyEvents, func(r *Request) { r.Inputs[0].Landing = g40 }, wantTriples("0|landing|not-applicable")},
		{"start with result", OpApplyEvents, func(r *Request) { r.Inputs[0].Result = ResultSuccess }, wantTriples("0|result|not-applicable")},
		{"start with dependency", OpApplyEvents, func(r *Request) { r.Inputs[0].Dependency = "d" }, wantTriples("0|dependency|not-applicable")},
		{"result without result", OpApplyEvents, func(r *Request) { r.Inputs[1].Result = "" }, wantTriples("1|result|required")},
		{"result unknown", OpApplyEvents, func(r *Request) { r.Inputs[1].Result = "meh" }, wantTriples("1|result|invalid-value")},
		{"result failure ok", OpApplyEvents, func(r *Request) { r.Inputs[1].Result = ResultFailure }, nil},
		{"result return ok", OpApplyEvents, func(r *Request) { r.Inputs[1].Result = ResultReturn }, nil},
		{"result with head ok", OpApplyEvents, func(r *Request) { r.Inputs[1].Head = g64 }, nil},
		{"result bad head", OpApplyEvents, func(r *Request) { r.Inputs[1].Head = "nothex" }, wantTriples("1|head|invalid-value")},
		{"accept without head", OpApplyEvents, func(r *Request) { r.Inputs[2].Head = "" }, wantTriples("2|head|required")},
		{"accept with reason", OpApplyEvents, func(r *Request) { r.Inputs[2].Reason = "because" }, wantTriples("2|reason|not-applicable")},
		{"retry without reason", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InVerdictRetry, "c1")}; r.Inputs[0].Reason = "" }, wantTriples("0|reason|required")},
		{"rework reason leading space", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InVerdictRework, "c1")}; r.Inputs[0].Reason = " x" }, wantTriples("0|reason|invalid-value")},
		{"rework reason too long", OpApplyEvents, func(r *Request) {
			r.Inputs = []Input{input(InVerdictRework, "c1")}
			r.Inputs[0].Reason = longString(MaxReasonBytes + 1)
		}, wantTriples("0|reason|too-long")},
		{"rework reason newline", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InVerdictRework, "c1")}; r.Inputs[0].Reason = "a\nb" }, wantTriples("0|reason|control-character")},
		{"rework reason html ok", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InVerdictRework, "c1")}; r.Inputs[0].Reason = "a<b>&c" }, nil},
		{"cancel without reason", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InCancel, "c1")}; r.Inputs[0].Reason = "" }, wantTriples("0|reason|required")},
		{"landing without landing", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InLanding, "c1")}; r.Inputs[0].Landing = "" }, wantTriples("0|landing|required")},
		{"landing without head", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InLanding, "c1")}; r.Inputs[0].Head = "" }, wantTriples("0|head|required")},
		{"external landing without landing", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InExternalLanding, "c1")}; r.Inputs[0].Landing = "" }, wantTriples("0|landing|required")},
		{"dependency failed without dependency", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InDependencyFailed, "c1")}; r.Inputs[0].Dependency = "" }, wantTriples("0|dependency|required")},
		{"dependency on itself", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InDependencyFailed, "c1")}; r.Inputs[0].Dependency = "c1" }, wantTriples("0|dependency|invalid-value")},
		{"dependency bad id", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InDependencyFailed, "c1")}; r.Inputs[0].Dependency = "a b" }, wantTriples("0|dependency|invalid-value")},
		{"head event without head", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InHead, "c1")}; r.Inputs[0].Head = "" }, wantTriples("0|head|required")},
		{"completed with head", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InCompleted, "c1")}; r.Inputs[0].Head = g40 }, wantTriples("0|head|not-applicable")},

		// the derived destination
		{"start from working", OpApplyEvents, func(r *Request) { r.Inputs[0].Expect.Place.Col = Working }, wantTriples("0|expect.place.col|no-transition")},
		{"start from waiting", OpApplyEvents, func(r *Request) { r.Inputs[0].Expect.Place.Col = Waiting }, wantTriples("0|expect.place.col|no-transition")},
		{"result from ready", OpApplyEvents, func(r *Request) { r.Inputs[1].Expect.Place.Col = Ready }, wantTriples("1|expect.place.col|no-transition")},
		{"accept from merging", OpApplyEvents, func(r *Request) { r.Inputs[2].Expect.Place.Col = Merging }, wantTriples("2|expect.place.col|no-transition")},
		{"anything from landed", OpApplyEvents, func(r *Request) { r.Inputs[0].Expect.Place.Col = Landed }, wantTriples("0|expect.place.col|no-transition")},
		{"anything from done", OpApplyEvents, func(r *Request) { r.Inputs[0].Expect.Place.Col = Done }, wantTriples("0|expect.place.col|no-transition")},
		{"ci green is not a type", OpApplyEvents, func(r *Request) { r.Inputs[0].Type = "ci-green" }, wantTriples("0|type|invalid-value")},
		{"ci red is not a type", OpApplyEvents, func(r *Request) { r.Inputs[0].Type = "ci-red" }, wantTriples("0|type|invalid-value")},
		{"review to landed by landing", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InLanding, "c1")}; r.Inputs[0].Expect.Place.Col = Review }, wantTriples("0|expect.place.col|no-transition")},
		{"external landing from review", OpApplyEvents, func(r *Request) {
			r.Inputs = []Input{input(InExternalLanding, "c1")}
			r.Inputs[0].Expect.Place.Col = Review
		}, wantTriples("0|expect.place.col|no-transition")},
		{"external landing from merging", OpApplyEvents, func(r *Request) {
			r.Inputs = []Input{input(InExternalLanding, "c1")}
			r.Inputs[0].Expect.Place.Col = Merging
		}, wantTriples("0|expect.place.col|no-transition")},
		{"completed from working", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InCompleted, "c1")}; r.Inputs[0].Expect.Place.Col = Working }, wantTriples("0|expect.place.col|no-transition")},
		{"dependency failed from ready", OpApplyEvents, func(r *Request) {
			r.Inputs = []Input{input(InDependencyFailed, "c1")}
			r.Inputs[0].Expect.Place.Col = Ready
		}, wantTriples("0|expect.place.col|no-transition")},
		{"cancel from landed", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InCancel, "c1")}; r.Inputs[0].Expect.Place.Col = Landed }, wantTriples("0|expect.place.col|no-transition")},

		// one event per card per request
		{"identical events repeat", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InStart, "c1"), input(InStart, "c1")} }, wantTriples("1|id|repeated-id")},
		{"conflicting events", OpApplyEvents, func(r *Request) {
			r.Inputs = []Input{input(InCancel, "c1"), input(InExternalLanding, "c1")}
			r.Inputs[0].Expect.Place.Col = Ready
		}, wantTriples("1|id|conflicting-inputs")},
		{"conflicting: same type other head", OpApplyEvents, func(r *Request) {
			r.Inputs = []Input{input(InVerdictAccept, "c1"), input(InVerdictAccept, "c1")}
			r.Inputs[1].Head = g64
		}, wantTriples("1|id|conflicting-inputs")},
		{"event chain start then result", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InStart, "c1"), input(InResult, "c1")} }, wantTriples("1|expect.place.col|input-chain")},
		{"event chain listed in reverse", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InResult, "c1"), input(InStart, "c1")} }, wantTriples("1|expect.place.col|input-chain")},
		{"event chain result then accept", OpApplyEvents, func(r *Request) { r.Inputs = []Input{input(InResult, "c1"), input(InVerdictAccept, "c1")} }, wantTriples("1|expect.place.col|input-chain")},
		{"same source different cards ok", OpApplyEvents, func(r *Request) {
			r.Inputs = []Input{input(InStart, "c1"), input(InStart, "c2"), input(InResult, "c3")}
		}, nil},

		// evidence
		{"evidence kind unknown", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Kind = "vibes" }, wantTriples("0|records[0].kind|invalid-value")},
		{"evidence kind queue is not submitted", OpRecordEvidence, func(r *Request) {
			r.Evidence[0].Records[0].Kind = KindQueue
			r.Evidence[0].Records[0].Disposition = DispReject
		}, wantTriples("0|records[0].kind|not-applicable")},
		{"evidence kind empty", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Kind = "" }, wantTriples("0|records[0].kind|required")},
		{"evidence read green", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Disposition = DispGreen }, wantTriples("0|records[0].disposition|invalid-value")},
		{"evidence ci accept", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Disposition = DispAccept }, wantTriples("0|records[1].disposition|invalid-value")},
		{"evidence ci ok is not a disposition", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Disposition = "ok" }, wantTriples("0|records[1].disposition|invalid-value")},
		{"evidence ci red ok", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Disposition = DispRed }, nil},
		{"evidence read reject ok", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Disposition = DispReject }, nil},
		{"evidence sweep clean ok", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0] = rec(KindSweep, DispClean, "sweeper", g40, "obs:1") }, nil},
		{"evidence sweep negative ok", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0] = rec(KindSweep, DispNegative, "sweeper", g40, "obs:1") }, nil},
		{"evidence landing landed ok", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0] = rec(KindLanding, DispLanded, "git", g40, "land:abc") }, nil},
		{"evidence landing green", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0] = rec(KindLanding, DispGreen, "git", g40, "land:abc") }, wantTriples("0|records[0].disposition|invalid-value")},
		{"evidence without head", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Head = "" }, wantTriples("0|records[0].head|required")},
		{"evidence tagged head ok", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Head = Digest(dig).Tagged() }, nil},
		{"evidence bad head", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Head = "beef" }, wantTriples("0|records[0].head|invalid-value")},
		{"evidence issuer empty", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Issuer = "" }, wantTriples("0|records[0].issuer|required")},
		{"evidence issuer space", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Issuer = "a b" }, wantTriples("0|records[0].issuer|invalid-value")},
		{"evidence issuer at the bound", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Issuer = longString(MaxIdentityBytes) }, nil},
		{"evidence issuer over the bound", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Issuer = longString(MaxIdentityBytes + 1) }, wantTriples("0|records[0].issuer|too-long")},
		{"evidence verifier empty", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Verifier = "" }, wantTriples("0|records[0].verifier|required")},
		{"evidence verifier over the bound", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Verifier = longString(MaxVerifierBytes + 1) }, wantTriples("0|records[0].verifier|too-long")},
		{"evidence artifact empty", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Artifact = "" }, wantTriples("0|records[1].artifact|required")},
		{"evidence artifact bidi", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Artifact = "run\u202e9" }, wantTriples("0|records[1].artifact|control-character")},
		{"evidence artifact zero width", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Artifact = "run\u200b9" }, wantTriples("0|records[1].artifact|control-character")},
		{"evidence artifact unicode", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1].Artifact = "run-\u00e9" }, wantTriples("0|records[1].artifact|invalid-value")},
		{"evidence record digest empty", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Def = "" }, wantTriples("0|records[0].digest|required")},
		{"evidence record digest short", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[0].Def = "abc" }, wantTriples("0|records[0].digest|invalid-value")},
		{"evidence record repeated", OpRecordEvidence, func(r *Request) { r.Evidence[0].Records[1] = r.Evidence[0].Records[0] }, wantTriples("0|records[1]|repeated-id")},
		{"evidence records conflict", OpRecordEvidence, func(r *Request) {
			r.Evidence[0].Records[1] = r.Evidence[0].Records[0]
			r.Evidence[0].Records[1].Disposition = DispReject
		}, wantTriples("0|records[1]|conflicting-records")},
		{"evidence one reader at two heads ok", OpRecordEvidence, func(r *Request) {
			r.Evidence[0].Records[1] = r.Evidence[0].Records[0]
			r.Evidence[0].Records[1].Head = g64
		}, nil},
		{"evidence card repeated", OpRecordEvidence, func(r *Request) { r.Evidence = append(r.Evidence, r.Evidence[0]) }, wantTriples("1|id|repeated-id", "1|records[0]|repeated-id", "1|records[1]|repeated-id")},
		{"evidence same record on two cards ok", OpRecordEvidence, func(r *Request) { r.Evidence = append(r.Evidence, evidenceEntry("c2")) }, nil},
		{"evidence revision zero", OpRecordEvidence, func(r *Request) { r.Evidence[0].Expect.Revision = "0" }, wantTriples("0|expect.revision|invalid-value")},
		{"evidence revision may be left out", OpRecordEvidence, func(r *Request) { r.Evidence[0].Expect.Revision = "" }, nil},
		{"evidence place is required", OpRecordEvidence, func(r *Request) { r.Evidence[0].Expect.Place = Place{} }, wantTriples("0|expect.place.row|required", "0|expect.place.col|required")},
		{"evidence records over 512 in all", OpRecordEvidence, func(r *Request) {
			r.Evidence = evidenceCards(MaxEvidenceRecords/MaxEvidenceRecordsPerCard+1, MaxEvidenceRecordsPerCard)
		}, wantTriples("-1|evidence|too-many")},
		{"evidence records at 512 in all", OpRecordEvidence, func(r *Request) {
			r.Evidence = evidenceCards(MaxEvidenceRecords/MaxEvidenceRecordsPerCard, MaxEvidenceRecordsPerCard)
		}, nil},
		{"evidence 127 cards at the bound", OpRecordEvidence, func(r *Request) { r.Evidence = evidenceCards(MaxChangedEntries, 4) }, nil},
		{"evidence 128 cards over the bound", OpRecordEvidence, func(r *Request) { r.Evidence = evidenceCards(MaxChangedEntries+1, 1) }, wantTriples("-1|evidence|too-many")},

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
		{"replace new path", OpReplace, func(r *Request) { r.Replacements[0].New.Path = "../evil" }, wantTriples("0|new.path|path-escapes")},
		{"replace new digest", OpReplace, func(r *Request) { r.Replacements[0].New.Digest = "x" }, wantTriples("0|new.digest|invalid-value")},

		// scope
		{"scope ids and rows", OpInspect, func(r *Request) { r.Scope.Rows = []string{"build"} }, wantTriples("-1|scope|invalid-value")},
		{"scope ids and all", OpInspect, func(r *Request) { r.Scope.All = true }, wantTriples("-1|scope|invalid-value")},
		{"scope nothing", OpInspect, func(r *Request) { r.Scope = &Scope{} }, wantTriples("-1|scope|required")},
		{"scope all ok", OpInspect, func(r *Request) { r.Scope = &Scope{All: true} }, nil},
		{"scope all for resolve ok", OpResolve, func(r *Request) { r.Scope = &Scope{All: true} }, nil},
		{"scope id bad", OpInspect, func(r *Request) { r.Scope.IDs[1] = "a b" }, wantTriples("-1|scope.ids[1]|invalid-value")},
		{"scope id none", OpInspect, func(r *Request) { r.Scope.IDs[1] = "none" }, wantTriples("-1|scope.ids[1]|reserved-word")},
		{"scope id repeated", OpInspect, func(r *Request) { r.Scope.IDs[1] = r.Scope.IDs[0] }, wantTriples("-1|scope.ids[1]|repeated-id")},
		{"scope rows ok", OpResolve, func(r *Request) { r.Scope = &Scope{Rows: []string{"build", "docs"}} }, nil},
		{"scope row bad", OpResolve, func(r *Request) { r.Scope = &Scope{Rows: []string{"a b"}} }, wantTriples("-1|scope.rows[0]|invalid-value")},
		{"scope row repeated", OpResolve, func(r *Request) { r.Scope = &Scope{Rows: []string{"a", "a"}} }, wantTriples("-1|scope.rows[1]|repeated-id")},
		{"scope rows empty", OpResolve, func(r *Request) { r.Scope = &Scope{Rows: []string{}} }, wantTriples("-1|scope.rows|empty-array")},
		{"scope rows over the bound", OpResolve, func(r *Request) { r.Scope = &Scope{Rows: rowNames(MaxScopeRows + 1)} }, wantTriples("-1|scope.rows|too-many")},
		{"scope rows at the bound", OpResolve, func(r *Request) { r.Scope = &Scope{Rows: rowNames(MaxScopeRows)} }, nil},
		{"resolve ids at 113", OpResolve, func(r *Request) { r.Scope = &Scope{IDs: idsN(MaxResolveCards)} }, nil},
		{"resolve ids over 113", OpResolve, func(r *Request) { r.Scope = &Scope{IDs: idsN(MaxResolveCards + 1)} }, wantTriples("-1|scope.ids|too-many")},
		{"inspect ids over 1024", OpInspect, func(r *Request) { r.Scope = &Scope{IDs: idsN(MaxScopeCards + 1)} }, wantTriples("-1|scope.ids|too-many")},
		{"check without scope", OpCheck, func(r *Request) {}, nil},
		{"check with rows", OpCheck, func(r *Request) { r.Scope = &Scope{Rows: []string{"build"}} }, nil},
		{"check with an epoch", OpCheck, func(r *Request) { r.Epoch = "3" }, wantTriples("-1|epoch|not-applicable")},
		{"check with an operation id", OpCheck, func(r *Request) { r.OperationID = "op" }, wantTriples("-1|operation_id|not-applicable")},
		{"check with admissions", OpCheck, func(r *Request) { r.Admissions = []Admission{adm("c1")} }, wantTriples("-1|admissions|not-applicable")},

		// several at once
		{"several refusals reported together", OpApplyEvents, func(r *Request) {
			r.Table = ""
			r.Inputs[0].Digest = "x"
			r.Inputs[1].Expect.Place.Col = Done
			r.Inputs[2].Head = ""
			r.Inputs = append(r.Inputs, input(InStart, "c1"))
		}, wantTriples("-1|table|required", "0|digest|invalid-value", "1|expect.place.col|no-transition", "2|head|required", "3|id|conflicting-inputs")},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := validRequest(c.op)
			c.edit(r)
			_, verr := Validate(r)
			got := triples(verr)
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
	_, e := Validate(r)
	err := e.(*Refusals)
	f := err.List[0]
	if f.Cause != CauseTooMany || f.Limit != "127 entries" || f.Next == "" || f.Found != "128 entries" || f.Operation != "admit" || f.Index != -1 {
		t.Fatalf("refusal = %+v", f)
	}
	line := f.String()
	for _, want := range []string{"refused admit", "field=admissions", "too-many", "found 128 entries", "limit 127 entries", "next:"} {
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
	r.Inputs[1].Expect.Place.Col = Done
	_, e := Validate(r)
	f := e.(*Refusals).List[0]
	if f.Index != 1 || f.ID != "c2" || f.Field != "expect.place.col" || f.Cause != CauseNoTransition || f.Operation != "apply_events" {
		t.Fatalf("refusal = %+v", f)
	}
	if f.Found != `"result@done"` || !strings.Contains(f.Limit, "working") {
		t.Fatalf("found %q limit %q", f.Found, f.Limit)
	}
	if want := `refused apply_events[1] card=c2 field=expect.place.col: no-transition; found "result@done"; limit source state one of working; next: declare a source state the type moves from; the destination is derived, never sent`; f.String() != want {
		t.Fatalf("line\n got  %s\n want %s", f.String(), want)
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
	_, e := Validate(r)
	rs := e.(*Refusals)
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
	_, e := Validate(r)
	rs := e.(*Refusals)
	first := rs.List[0]
	if first.Cause != CauseTooLarge || first.Limit != "1048576 bytes" || first.Next == "" {
		t.Fatalf("first refusal = %+v", first)
	}
	if !rs.Has(0, "path", CauseTooLong) {
		t.Fatal("entry refusals missing")
	}
}

func TestValidateNilAndZero(t *testing.T) {
	t.Parallel()
	if _, err := Validate(nil); err == nil {
		t.Fatal("nil request accepted")
	}
	if _, err := Validate(&Request{}); err == nil {
		t.Fatal("zero request accepted")
	}
}

func TestValidationNeverPartiallyAccepts(t *testing.T) {
	t.Parallel()
	// One bad entry among many good ones refuses the request whole.
	r := validRequest(OpAdmit)
	r.Admissions = manyAdmissions(50)
	r.Admissions[49].Path = "../escape"
	if _, err := Validate(r); err == nil {
		t.Fatal("request with one bad entry accepted")
	}
}

func TestIdentityAndPredicates(t *testing.T) {
	t.Parallel()
	v := mustValid(t, validRequest(OpAdmit))
	if got := v.Identity().String(); got != "work/3/op-17" {
		t.Fatalf("identity %q", got)
	}
	if !OpInspect.Valid() || OpInspect.Mutating() || OpCheck.Mutating() || !OpReplace.Mutating() || Operation("x").Mutating() {
		t.Fatal("Operation predicates drifted")
	}
	if !InCancel.Valid() || InputType("nope").Valid() || !Ready.Valid() || State("x").Valid() {
		t.Fatal("predicates drifted")
	}
}

func depIDs(n int) []ID {
	out := make([]ID, n)
	for i := range out {
		out[i] = ID(fmt.Sprintf("dep%d", i))
	}
	return out
}

func rowNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("row%d", i)
	}
	return out
}

// evidenceCards builds n evidence entries of m distinct valid records each.
func evidenceCards(n, m int) []Evidence {
	out := make([]Evidence, n)
	for i := range out {
		out[i] = Evidence{ID: ID(fmt.Sprintf("c%d", i)), Expect: expect("4", "build", Review)}
		for j := 0; j < m; j++ {
			out[i].Records = append(out[i].Records, rec(KindCI, DispGreen, fmt.Sprintf("ci:check%d", j), g40, fmt.Sprintf("run:%d", j)))
		}
	}
	return out
}
