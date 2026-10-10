package store

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// storeFleet is the fleet table's definition as the live store holds it: the eleven
// columns init created before 2026-10-01 (git show 36aa250fb^:internal/sprint/schema.go),
// pinned here as a literal so a tick that writes a column the store does not have is red.
const storeFleet = "ready,working,width:text:sum,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%,status:text,load:text,withdrawn,ok,failed,ctl:first:none"

// lockedFleet is the fleet table's definition as internal/sprint/TABLES.lock holds it: the
// store's, the hidden defect column after failed (2026-10-05, a brief defect), and the
// hidden refused and provider columns after withdrawn (2026-10-06, ok-percent-is-work-
// actually-done), which only a finish of a launch the member refused or a take the provider
// failed writes, and the store takes before that build is installed (nova-table col add
// fleet refused --after withdrawn; nova-table col add fleet provider --after refused;
// nova-table set fleet --hide defect --hide refused --hide provider).
const lockedFleet = "ready,working,width:text:sum,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%,status:text,load:text,withdrawn,refused,provider,ok,failed,defect,ctl:first:none"

// colChecked is the mem twin refusing a display cell of a column its table does
// not define, as the real store does (ntable: "no such column").
type colChecked struct{ *Mem }

func (c colChecked) RowSet(ctx context.Context, table, row string, texts map[string]string) error {
	c.mu.Lock()
	t, err := c.table(table)
	var cols []string
	if err == nil {
		for _, col := range t.def.Columns {
			cols = append(cols, col.Name)
		}
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	for k := range texts {
		if !slices.Contains(cols, k) {
			return fmt.Errorf("table %q row %q column %q: no such column", table, row, k)
		}
	}
	return c.Mem.RowSet(ctx, table, row, texts)
}

// A store whose fleet table was created with the eleven-column definition is ticked by
// this build, a member up and a card ready, without an error: the tick writes no fleet
// cell that table does not have, and the build's own definition is the locked one.
func TestATickOnAStoreWithTheLockedFleetTableWritesNoColumnItLacks(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns(storeFleet)
	require.NoError(t, err)
	require.Len(t, cols, 11)
	lockedCols, err := ntable.ParseColumns(lockedFleet)
	require.NoError(t, err)
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("flash", "")) // dealt on flash (route.go, tierLadder)
	h.startMachine()
	name := h.st.Names.Table(sprint.Fleet)
	// the twin's fleet table made the live store's: its definition, and no cell of a
	// column outside it (the locked store never had one to write)
	h.m.mu.Lock()
	ft := h.m.tables[name]
	ft.def.Columns = cols
	locked := ft.def
	locked.Columns = lockedCols
	for _, ep := range ft.epochs {
		for _, texts := range ep.texts {
			for k := range texts {
				if !slices.ContainsFunc(cols, func(c ntable.Column) bool { return c.Name == k }) {
					delete(texts, k)
				}
			}
		}
	}
	h.m.mu.Unlock()
	h.st.B = colChecked{h.m}

	_, err = h.st.Tick(h.ctx)
	require.NoError(t, err, "the tick on a store with the locked fleet table")
	assert.Equal(t, sprint.Ready, h.snap().Fleet.Card("s1-1.w1").Col, "the card was dealt")

	for _, d := range h.st.Names.Definitions() {
		if d.Name == name {
			assert.True(t, ntable.SameDefinition(locked, d), "the build's fleet definition is the locked one")
		}
	}
}
