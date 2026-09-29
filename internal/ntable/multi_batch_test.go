package ntable

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

type multiReplyClient struct {
	redis.Cmdable
	reply []any
}

func (c multiReplyClient) FCall(ctx context.Context, _ string, _ []string, _ ...interface{}) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	cmd.SetVal(c.reply)
	return cmd
}

func multiValidReply(t *testing.T, m MultiBatchManifest) []any {
	t.Helper()
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(body)
	score := "1"
	delta := MultiBatchDelta{Schema: 2, Scope: m.Scope, OperationID: m.OperationID, Digest: fmt.Sprintf("%x", sum), Actor: m.Actor, Outcome: "changed",
		Tables:        []MultiBatchTableDelta{{Name: "a", Epoch: "0", RevBefore: "1", RevAfter: "2"}, {Name: "b", Epoch: "0", RevBefore: "1", RevAfter: "2"}},
		SelectedCount: 1, GuardCount: 0, ChangedCount: 1, Members: []MultiBatchMemberDelta{{RecordTable: "a", ID: "m", BeforeRev: "0", AfterRev: "1",
			FieldsSet: map[string]string{}, FieldsUnset: []string{}, Fields: map[string]FieldChange{},
			Placements: []MultiBatchPlacementDelta{{Table: "a", BeforePlace: "", AfterPlace: "work:ready", AfterScoreText: &score}}}}}
	raw, err := json.Marshal(delta)
	if err != nil {
		t.Fatal(err)
	}
	return []any{"OK", []any{"MULTI_RECEIPT", "1-0", string(raw)}}
}

func multiFixture() MultiBatchManifest {
	return MultiBatchManifest{Schema: 2, Scope: "release", OperationID: "op-1",
		Tables:  []MultiBatchTable{{Name: "a", Epoch: "0", ExpectedTableRevision: "1"}, {Name: "b", Epoch: "0", ExpectedTableRevision: "1"}},
		Members: []MultiBatchMember{{RecordTable: "a", ID: "m", Expect: &MultiMemberExpect{Absent: true, Places: []MultiPlaceExpect{{Table: "a", Absent: true}}}, Placements: []MultiPlacement{{Table: "a", Add: &MemberCreateOp{Row: "work", Col: "ready", Score: 1}}}}}}
}

func TestMultiManifestRoundTripAndNilClient(t *testing.T) {
	t.Parallel()
	m := multiFixture()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ValidateMultiBatchManifestRaw(b)
	if err != nil || got.Members[0].Placements[0].Add.Score != 1 {
		t.Fatalf("validated %#v: %v", got, err)
	}
	_, err = ApplyMultiBatch(context.Background(), nil, m)
	if err == nil || !strings.Contains(err.Error(), "nil Redis client") {
		t.Fatalf("nil client: %v", err)
	}
}

func TestMultiManifestStrictRaw(t *testing.T) {
	t.Parallel()
	base := `{"schema":2,"scope":"release","operation_id":"op-1","tables":[{"name":"a","epoch":"0","expected_table_revision":"1"},{"name":"b","epoch":"0","expected_table_revision":"1"}],"members":[{"record_table":"a","id":"m","expect":{"absent":true,"places":[{"table":"a","absent":true}]},"placements":[{"table":"a","add":{"row":"work","col":"ready","score":1}}]}]}`
	cases := []struct{ name, raw string }{
		{"duplicate root", strings.Replace(base, `"scope":"release"`, `"scope":"release","scope":"other"`, 1)},
		{"unknown nested", strings.Replace(base, `"score":1`, `"score":1,"socre":1`, 1)},
		{"coerced schema", strings.Replace(base, `"schema":2`, `"schema":"2"`, 1)},
		{"coerced epoch", strings.Replace(base, `"epoch":"0"`, `"epoch":0`, 1)},
		{"false absent", strings.Replace(base, `"absent":true`, `"absent":false`, 1)},
		{"absent with empty fields", strings.Replace(base, `"absent":true`, `"absent":true,"fields":{}`, 1)},
		{"null field", strings.Replace(base, `"record_table":"a"`, `"record_table":null`, 1)},
		{"duplicate table", strings.Replace(base, `"name":"b"`, `"name":"a"`, 1)},
		{"unknown participant", strings.Replace(base, `"record_table":"a"`, `"record_table":"z"`, 1)},
		{"empty placement", strings.Replace(base, `"placements":[{"table":"a","add":{"row":"work","col":"ready","score":1}}]`, `"placements":[]`, 1)},
		{"missing add score", strings.Replace(base, `,"score":1`, ``, 1)},
		{"missing place guard", strings.Replace(base, `,"places":[{"table":"a","absent":true}]`, ``, 1)},
		{"trailing", base + ` true`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ValidateMultiBatchManifestRaw([]byte(tc.raw)); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}

func TestApplyMultiBatchRejectsMalformedCommittedReceipt(t *testing.T) {
	t.Parallel()
	m := multiFixture()
	valid := multiValidReply(t, m)
	r, err := ApplyMultiBatch(context.Background(), multiReplyClient{reply: valid}, m)
	if err != nil || r.Delta == nil || r.Delta.Digest == "" {
		t.Fatalf("valid receipt: %+v %v", r, err)
	}
	base := valid[1].([]any)[2].(string)
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing digest", func(x map[string]any) { delete(x, "digest") }},
		{"wrong digest", func(x map[string]any) { x["digest"] = "deadbeef" }},
		{"missing outcome", func(x map[string]any) { delete(x, "outcome") }},
		{"wrong outcome", func(x map[string]any) { x["outcome"] = "noop" }},
		{"missing members", func(x map[string]any) { delete(x, "members") }},
		{"empty table objects", func(x map[string]any) { x["tables"] = []any{map[string]any{}, map[string]any{}} }},
		{"wrong table", func(x map[string]any) { x["tables"].([]any)[0].(map[string]any)["name"] = "other" }},
		{"wrong table revision", func(x map[string]any) { x["tables"].([]any)[1].(map[string]any)["rev_after"] = "9" }},
		{"wrong member", func(x map[string]any) { x["members"].([]any)[0].(map[string]any)["id"] = "other" }},
		{"wrong member revision", func(x map[string]any) { x["members"].([]any)[0].(map[string]any)["before_rev"] = "9" }},
		{"effective add claims noop", func(x map[string]any) {
			x["members"].([]any)[0].(map[string]any)["after_rev"] = "0"
			x["changed_count"] = float64(0)
			x["outcome"] = "noop"
		}},
		{"missing member revision", func(x map[string]any) { delete(x["members"].([]any)[0].(map[string]any), "after_rev") }},
		{"wrong before place", func(x map[string]any) {
			x["members"].([]any)[0].(map[string]any)["placements"].([]any)[0].(map[string]any)["before_place"] = "work:old"
		}},
		{"wrong placement score", func(x map[string]any) {
			x["members"].([]any)[0].(map[string]any)["placements"].([]any)[0].(map[string]any)["after_score"] = "3"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var x map[string]any
			if err := json.Unmarshal([]byte(base), &x); err != nil {
				t.Fatal(err)
			}
			tc.mutate(x)
			b, err := json.Marshal(x)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ApplyMultiBatch(context.Background(), multiReplyClient{reply: []any{"OK", []any{"MULTI_RECEIPT", "1-0", string(b)}}}, m)
			if !errors.Is(err, ErrUnknownOutcome) {
				t.Fatalf("malformed receipt %s: %v", tc.name, err)
			}
		})
	}
}

func TestMultiManifestAggregateBounds(t *testing.T) {
	t.Parallel()
	m := multiFixture()
	m.Members = nil
	for i := 0; i < LimitChangedEntries+1; i++ {
		m.Members = append(m.Members, MultiBatchMember{ID: strings.Repeat("x", i%10) + string(rune('A'+i/10)), RecordTable: "a", Expect: &MultiMemberExpect{}, Set: map[string]string{"x": "y"}})
	}
	b, _ := json.Marshal(m)
	_, err := ValidateMultiBatchManifestRaw(b)
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("changed entries bound: %v", err)
	}
}

func TestMultiManifestParticipantBound(t *testing.T) {
	t.Parallel()
	m := multiFixture()
	m.Tables = nil
	for i := 0; i < LimitMultiTables+1; i++ {
		m.Tables = append(m.Tables, MultiBatchTable{Name: "table" + string(rune('A'+i)), Epoch: "0", ExpectedTableRevision: "0"})
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateMultiBatchManifestRaw(b); !errors.Is(err, ErrLimit) {
		t.Fatalf("table bound: %v", err)
	}
}

func TestParseMultiBatchReply(t *testing.T) {
	t.Parallel()
	delta := `{"schema":2,"scope":"release","operation_id":"op-1","digest":"abc","actor":"bot","outcome":"changed","tables":[{"name":"a","epoch":"0","rev_before":"1","rev_after":"2"}],"selected_count":1,"guard_count":0,"changed_count":1,"members":[{"record_table":"a","id":"m","before_rev":"0","after_rev":"1","fields_set":[],"fields_unset":{},"fields":[],"placements":[{"table":"a","before_place":"","after_place":"work:ready","before_score":null,"after_score":"1"}]}]}`
	reply := []any{"OK", []any{"MULTI_RECEIPT", "123-0", delta}, "REPLAY"}
	r, err := parseMultiBatchReply(reply)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Replay || r.ID != "123-0" || r.Delta.Members[0].Placements[0].AfterScore == nil || *r.Delta.Members[0].Placements[0].AfterScore != 1 {
		t.Fatalf("receipt: %+v", r)
	}
	if len(r.Delta.Members[0].FieldsSet) != 0 || len(r.Delta.Members[0].FieldsUnset) != 0 {
		t.Fatalf("empty cjson containers: %+v", r.Delta.Members[0])
	}
	for _, bad := range [][]any{{"OK"}, {"OK", []any{"RECEIPT", "123-0", delta}}, {"OK", []any{"MULTI_RECEIPT", "123-0", `{}`}}} {
		if _, err := parseMultiBatchReply(bad); err == nil {
			t.Fatalf("accepted malformed reply: %#v", bad)
		}
	}
}

func TestApplyMultiBatchSchema2RefusalText(t *testing.T) {
	t.Parallel()
	m := multiFixture()
	for _, tc := range []struct {
		name, location, sentence, next string
		wire                           []any
		cause                          error
	}{
		{"missing participant", `table "b" batch "op-1"`, "no such table", "nova-table list", []any{"REFUSED", "NOTABLE", "b"}, ErrNoTable},
		{"stale participant", `table "b" batch "op-1"`, "requested 0, active 2", "nova-table show 'b'", []any{"REFUSED", "STALE", "b", "0", "2"}, ErrStale},
		{"epoch ahead", `table "b" batch "op-1"`, "requested epoch 5, active epoch 0", "nova-table show 'b'", []any{"REFUSED", "EPOCHAHEAD", "b", "5", "0"}, ErrEpochAhead},
		{"revision mismatch", `table "b" batch "op-1"`, "expected 0, observed 2", "nova-table show 'b'", []any{"REFUSED", "REVISION", "b", "0", "2"}, ErrRevisionMismatch},
		{"revision overflow", `table "b" batch "op-1"`, "the table revision 18446744073709551615 is at its maximum", "nova-table show 'b'", []any{"REFUSED", "REVISION", "b", "18446744073709551615"}, ErrCounterOverflow},
		{"place guard", `table "a" batch "op-1" member "m"`, `member "m": expected place work:done, observed work:ready`, "nova-table member read 'a' 'm'", []any{"REFUSED", "PLACEGUARD", "m", "a", "work:done", "work:ready"}, ErrPlaceGuard},
		{"record exists", `table "a" batch "op-1" member "m"`, "expected absent, observed member record", "nova-table member read 'a' 'm'", []any{"REFUSED", "MEMBEREXISTS", "m", "a"}, ErrMemberExists},
		{"already placed", `table "b" batch "op-1" member "m"`, "expected absent, observed work:ready", "nova-table member read 'b' 'm'", []any{"REFUSED", "MEMBEREXISTS", "m", "b", "work:ready"}, ErrMemberExists},
		{"unplaced move", `table "b" batch "op-1" member "m"`, "expected a placed member to move, observed unplaced", "nova-table member read 'b' 'm'", []any{"REFUSED", "NOTMEMBER", "m", "b", "a placed member to move"}, ErrNotMember},
		{"duplicate physical member", `table "b" batch "op-1" member "m"`, `duplicate manifest member: member "m" appears more than once`, "nova-table show 'b'", []any{"REFUSED", "TWICE", "m", "b"}, ErrDuplicateMember},
		{"stored score", `table "b" batch "op-1" member "m"`, "expected a finite JSON number, observed stored score is not finite", "nova-table show 'b'", []any{"REFUSED", "SCORE", "m", "b", "stored score is not finite"}, ErrInvalidScore},
		{"scope operation conflict", `multi batch scope "release" operation "op-1"`, `operation "op-1" already holds a different request`, "nova-table list", []any{"REFUSED", "OPCONFLICT", "op-1"}, ErrOpConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ApplyMultiBatch(context.Background(), multiReplyClient{reply: tc.wire}, m)
			var refusal *Refusal
			if !errors.As(err, &refusal) || !errors.Is(err, tc.cause) {
				t.Fatalf("reply %#v: refusal %v", tc.wire, err)
			}
			if refusal.Location != tc.location || !strings.Contains(refusal.Sentence, tc.sentence) || refusal.Next != tc.next || !refusal.Guarded {
				t.Fatalf("reply %#v: %+v", tc.wire, refusal)
			}
			if strings.Contains(refusal.Error(), "nova-table show 'release'") || strings.Contains(refusal.Error(), "nova-table member read 'release'") {
				t.Fatalf("scope incorrectly used as table: %v", refusal)
			}
		})
	}

	_, err := ApplyMultiBatch(context.Background(), multiReplyClient{reply: []any{"REFUSED", "REVISION", "b"}}, m)
	if !errors.Is(err, ErrUnknownOutcome) || IsRefusal(err) || !strings.Contains(err.Error(), "send the identical manifest") {
		t.Fatalf("malformed refusal is not a confirmed no: %v", err)
	}
}

func TestApplyMultiBatchPreSendRefusalUsesScopeContext(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*MultiBatchManifest)
	}{
		{"duplicate table", func(m *MultiBatchManifest) { m.Tables[1].Name = "a" }},
		{"malformed expect", func(m *MultiBatchManifest) { m.Members[0].Expect = nil }},
		{"participant limit", func(m *MultiBatchManifest) {
			m.Tables = nil
			for i := 0; i < LimitMultiTables+1; i++ {
				m.Tables = append(m.Tables, MultiBatchTable{Name: fmt.Sprintf("table%d", i), Epoch: "0", ExpectedTableRevision: "0"})
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := multiFixture()
			tc.mutate(&m)
			_, err := ApplyMultiBatch(context.Background(), nil, m)
			if err == nil || !strings.Contains(err.Error(), `multi batch scope "release" operation "op-1"`) ||
				!strings.Contains(err.Error(), CheckedBeforeSending) ||
				!strings.Contains(err.Error(), "changed=no; run: nova-table batch -h") ||
				strings.Contains(err.Error(), `table "release"`) || strings.Contains(err.Error(), "nova-table show 'release'") {
				t.Fatalf("wrong pre-send context or next command: %v", err)
			}
			var refusal *Refusal
			if errors.As(err, &refusal) && (refusal.Location != `multi batch scope "release" operation "op-1"` || refusal.Next != "nova-table batch -h") {
				t.Fatalf("wrong structured refusal: %+v", refusal)
			}
		})
	}
}
