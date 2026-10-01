//go:build functional

package ntable_test

// A table's properties (L1 contract amendment, table properties, section 4):
// a batch manifest sets them with its members in one atomic call, guarded on
// the values it expects; the table read carries them back.

import (
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
	require.Equal(t, "changed", rc.Outcome, "receipt %+v, delta %+v", rc, rc.BatchDelta)
	require.NotNil(t, rc.BatchDelta, "receipt %+v", rc)
	require.Equal(t, map[string]string{"deal_index": "build"}, rc.BatchDelta.Props, "receipt %+v, delta %+v", rc, rc.BatchDelta)
	tb, err := ntable.Read(ctx, c, "demo")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"deal_index": "build"}, tb.Props, "props read back %v", tb.Props)
	// the same value again, alone: no change
	same := propBatch(probeRev(ctx, c), "again", nil)
	same.Props, same.PropExpect = map[string]string{"deal_index": "build"}, map[string]string{"deal_index": "build"}
	rc, err = ntable.ApplyBatch(ctx, c, same)
	require.NoError(t, err, "the same value: %+v", rc)
	require.Equal(t, "noop", rc.Outcome, "the same value: %+v", rc)
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
		require.ErrorAs(t, err, &r, "%s: %v", name, err)
		require.Equal(t, "PROPGUARD", r.Code, "%s: %v", name, err)
		require.ErrorIs(t, err, ntable.ErrPropGuard, "%s: %v", name, err)
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
	_, err := ntable.ApplyBatch(ctx, c, m)
	require.NoError(t, err, "64 properties")
	more := propBatch(probeRev(ctx, c), "more", nil)
	more.Props = map[string]string{"one_more": "v"}
	_, err = ntable.ApplyBatch(ctx, c, more)
	require.ErrorIs(t, err, ntable.ErrLimit, "a 65th property: %v", err)
	raw := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + probeRev(ctx, c) + `","operation_id":"bad","members":[],"props":{"bad name":"v"}}`
	ans, err := rawApply(ctx, c, raw)
	require.True(t, replyOpens(ans, err, "REFUSED"), "an invalid property name: %v %v", ans, err)
}
