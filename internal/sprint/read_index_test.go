package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadByPrimaryIncludesKeptReadsAndTracksChanges(t *testing.T) {
	t.Parallel()
	tb := NewTable("fleet")
	put := func(id, primary, kind string) {
		tb.Put(&Card{ID: id, Fields: map[string]string{PrimaryField: primary, "kind": kind}})
	}
	put("p.r1.z", "p", "read")
	put("p.w1", "p", "work")
	put("p.r1.a", "p", "read")
	got := tb.ReadByPrimary()["p"]
	require.Len(t, got, 2)
	require.Equal(t, []string{"p.r1.a", "p.r1.z"}, []string{got[0].ID, got[1].ID})
	put("p.r1.b", "p", "read")
	got = tb.ReadByPrimary()["p"]
	require.Len(t, got, 3)
	require.Equal(t, "p.r1.b", got[1].ID)
	tb.Drop("p.r1.a")
	got = tb.ReadByPrimary()["p"]
	require.Len(t, got, 2)
	require.Equal(t, "p.r1.b", got[0].ID)
	got = tb.Frozen().ReadByPrimary()["p"]
	require.Len(t, got, 2)
	require.Equal(t, "p.r1.z", got[1].ID)
}
