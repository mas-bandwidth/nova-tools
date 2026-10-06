package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// nova-config's sprint row's read tier of each kind of change (read_tier_prose,
// read_tier_code, read_tier_tla) reaches the snapshot a step that asks plans with, read
// with the routes; none set is each kind's default (sprint.ReadTierByKindDefault).
func TestTheReadTierByKindIsReadWithTheRoutes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	read := func() sprint.ReadTierByKind {
		rs, err := h.st.routes(h.ctx)
		require.NoError(t, err)
		var s sprint.Snapshot
		rs.into(&s)
		return s.ReadTierByKind
	}
	assert.Equal(t, sprint.ReadTierByKind{}, read())
	h.m.SetDecideBars(Bars{ReadProse: "heavy", ReadCode: "pro", ReadTLA: "card"})
	assert.Equal(t, sprint.ReadTierByKind{Prose: "heavy", Code: "pro", TLA: "card"}, read())
}
