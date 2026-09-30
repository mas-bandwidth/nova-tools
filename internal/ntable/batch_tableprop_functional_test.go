//go:build functional

package ntable_test

// A table's properties (L1 contract amendment, table properties, section 4):
// a batch manifest sets them with its members in one atomic call, guarded on
// the values it expects; the table read carries them back.

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/require"
)

func propBatch(rev, op string, members []ntable.BatchMemberEntry) ntable.BatchManifest {
	if members == nil {
		members = []ntable.BatchMemberEntry{}
	}
	return ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: op, Actor: "p", Members: members}
}

func TestPropWriteAndReadWithTheMembers(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	m := propBatch(probeRev(ctx, c), "deal", []ntable.BatchMemberEntry{
		{ID: "a", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 1}}})
	m.Props, m.PropAbsent = map[string]string{"deal_index": "build"}, []string{"deal_index"}
	rc, err := ntable.ApplyBatch(ctx, c, m)
	require.NoError(t, err)
	if rc.Outcome != "changed" || rc.BatchDelta == nil || !reflect.DeepEqual(rc.BatchDelta.Props, map[string]string{"deal_index": "build"}) {
		t.Fatalf("receipt %+v, delta %+v", rc, rc.BatchDelta)
	}
	tb, err := ntable.Read(ctx, c, "demo")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"deal_index": "build"}, tb.Props, "props read back %v", tb.Props)
	// the same value again, alone: no change
	same := propBatch(probeRev(ctx, c), "again", nil)
	same.Props, same.PropExpect = map[string]string{"deal_index": "build"}, map[string]string{"deal_index": "build"}
	if rc, err := ntable.ApplyBatch(ctx, c, same); err != nil || rc.Outcome != "noop" {
		t.Fatalf("the same value: %+v %v", rc, err)
	}
}

func TestRefusePROPGUARDWritesNothing(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	set := propBatch(probeRev(ctx, c), "set", nil)
	set.Props = map[string]string{"deal_index": "build"}
	_, err := ntable.ApplyBatch(ctx, c, set)
	require.NoError(t, err)
	for name, m := range map[string]ntable.BatchManifest{
		"expect": func() ntable.BatchManifest {
			m := propBatch(probeRev(ctx, c), "x1", []ntable.BatchMemberEntry{
				{ID: "a", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 1}}})
			m.Props, m.PropExpect = map[string]string{"deal_index": "test"}, map[string]string{"deal_index": "other"}
			return m
		}(),
		"absent": func() ntable.BatchManifest {
			m := propBatch(probeRev(ctx, c), "x2", []ntable.BatchMemberEntry{
				{ID: "a", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 1}}})
			m.Props, m.PropAbsent = map[string]string{"deal_index": "test"}, []string{"deal_index"}
			return m
		}(),
	} {
		before := storeImage(t, c)
		_, err := ntable.ApplyBatch(ctx, c, m)
		var r *ntable.Refusal
		if !errors.As(err, &r) || r.Code != "PROPGUARD" || !errors.Is(err, ntable.ErrPropGuard) {
			t.Fatalf("%s: %v", name, err)
		}
		require.Equal(t, before, storeImage(t, c), "%s: a refused batch changed the store", name)
	}
}

func TestPropLimitAndValidation(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	m := propBatch(probeRev(ctx, c), "many", nil)
	m.Props = map[string]string{}
	for i := 0; i < ntable.LimitManifestProps; i++ {
		m.Props["p"+string(rune('a'+i%26))+string(rune('a'+i/26))] = "v"
	}
	if _, err := ntable.ApplyBatch(ctx, c, m); err != nil {
		t.Fatalf("64 properties: %v", err)
	}
	more := propBatch(probeRev(ctx, c), "more", nil)
	more.Props = map[string]string{"one_more": "v"}
	if _, err := ntable.ApplyBatch(ctx, c, more); !errors.Is(err, ntable.ErrLimit) {
		t.Fatalf("a 65th property: %v", err)
	}
	raw := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + probeRev(ctx, c) + `","operation_id":"bad","members":[],"props":{"bad name":"v"}}`
	ans, err := rawApply(ctx, c, raw)
	require.True(t, replyOpens(ans, err, "REFUSED"), "an invalid property name: %v %v", ans, err)
}
