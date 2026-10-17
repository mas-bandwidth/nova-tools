package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store/storetest"
	"github.com/stretchr/testify/require"
)

func TestProbeLoadCacheAfterRollback(t *testing.T) {
	for _, extra := range []int{0, 1, 2, 3, 4, 5, 6} {
		a := newHarness(t)
		a.setup(20)
		a.startMachine()
		a.machine()
		a.work("m1")
		a.machine()
		lc := NewLoadCache()
		ca := a.st.clone()
		ca.lc = lc
		sa, err := ca.Load(a.ctx, []string{sprint.Fleet}, nil)
		require.NoError(t, err)
		revA := sa.Fleet.Revision
		b := newHarness(t)
		b.setup(20)
		b.startMachine()
		b.machine()
		b.work("m2")
		for i := 0; i < extra; i++ {
			b.machine()
		}
		cb := b.st.clone()
		cb.lc = lc
		got, err := cb.Load(b.ctx, []string{sprint.Fleet}, nil)
		require.NoError(t, err)
		fb := b.st.clone()
		fb.lc = nil
		fresh, err := fb.Load(b.ctx, []string{sprint.Fleet}, nil)
		require.NoError(t, err)
		d := storetest.TwinDiff(got, fresh)
		t.Logf("extra=%d revA=%d revB=%d diffempty=%v", extra, revA, fresh.Fleet.Revision, len(d) == 0)
		require.Empty(t, d)
	}
}
