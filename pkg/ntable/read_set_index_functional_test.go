//go:build functional

package ntable_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// cellReads is how many times the server has listed or scored a cell
// (ZRANGE and ZMSCORE calls, INFO commandstats), the commands the place
// index issues: the store's own count, so the test holds no clock.
func cellReads(t *testing.T, c *redis.Client) int64 {
	t.Helper()
	info, err := c.Info(context.Background(), "commandstats").Result()
	require.NoError(t, err)
	var n int64
	for _, line := range strings.Split(info, "\n") {
		name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || (name != "cmdstat_zrange" && name != "cmdstat_zmscore") {
			continue
		}
		for _, kv := range strings.Split(rest, ",") {
			if v, ok := strings.CutPrefix(kv, "calls="); ok {
				calls, err := strconv.ParseInt(v, 10, 64)
				require.NoError(t, err)
				n += calls
			}
		}
	}
	return n
}

// A read set of a whole table reads each cell once: the place index that
// checks every member's placement against every cell costs the table's cells,
// not its cells times its members in thousands (2,000 cards over 60 streams
// held the sprint store's core, 2026-10-04: where at 2 to 6 s, a child's card
// loop timing out). A read set smaller than the cells scores its ids in each
// cell, once per cell as well.
func TestAWholeTableReadSetReadsEachCellOnce(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	const rows, perRow = 3, ntable.LimitReadSetMembers // 3,072 members; a cell of 1,024, a read set of the bound
	require.NoError(t, ntable.Create(ctx, c, demo(), time.Now()))
	var ids []string
	for r := 0; r < rows; r++ {
		row := fmt.Sprintf("r%d", r)
		_, err := ntable.RowAdd(ctx, c, "demo", row, ntable.RowSpec{})
		require.NoError(t, err)
		for i := 0; i < perRow; i++ {
			id := fmt.Sprintf("%s-%d", row, i)
			ids = append(ids, id)
			_, err := ntable.CellAdd(ctx, c, "demo", row, "ready", id, float64(i))
			require.NoError(t, err)
		}
	}
	// the index lists the rows (one ZRANGE), then looks at every column of every row
	cells := int64(1 + rows*len(demo().Columns))
	for _, n := range []int{ntable.LimitReadSetMembers, 1, 2000} {
		want := ids
		if n < len(ids) {
			want = ids[:n]
		}
		for start := 0; start < len(want); start += ntable.LimitReadSetMembers {
			end := min(start+ntable.LimitReadSetMembers, len(want))
			before := cellReads(t, c)
			rs, err := ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{Members: want[start:end]})
			require.NoError(t, err)
			require.Len(t, rs.Members, end-start)
			require.Empty(t, rs.Missing)
			got := cellReads(t, c) - before
			require.LessOrEqual(t, got, cells, "a read set of %d members read the cells %d times; want at most once each, with the row list (%d)", end-start, got, cells)
		}
	}
}
