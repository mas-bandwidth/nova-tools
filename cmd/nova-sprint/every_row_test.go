package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every row of every table moves every tick, never a row at a time (the
// owner's rule, errata 3 amendment 10: "you should update each row in workers
// in fleet table, per-tick"; "there is NO REASON to ever do a row at a time,
// with 1sec updates"; "each table should be updated per-tick at least once").

// tablesView is the four tables' cells as where --json shows them.
type tablesView struct {
	Tables map[string]map[string]map[string]string `json:"tables"`
}

// busy is the columns of a table's row that say it has work a tick or the
// world moves without waiting on the coordinator, and the columns whose
// change says it moved.
var busy = map[string]struct{ work, moved []string }{
	"fleet":   {[]string{"ready", "working"}, []string{"ready", "working", "done"}},
	"readers": {[]string{"asked", "reading"}, []string{"asked", "reading", "ok", "broken"}},
	"work":    {[]string{"ready", "working"}, []string{"ready", "working", "review"}},
	"merge":   {[]string{"queued"}, []string{"queued", "merging", "landed"}},
}

// rowsWithWork is each table's rows that have work in the view.
func rowsWithWork(v tablesView) map[string][]string {
	out := map[string][]string{}
	for tb, cols := range busy {
		for row, cells := range v.Tables[tb] {
			for _, c := range cols.work {
				if n, _ := strconv.Atoi(cells[c]); n > 0 {
					out[tb] = append(out[tb], row)
					break
				}
			}
		}
		slices.Sort(out[tb])
	}
	return out
}

// rowsMoved is each table's rows whose moved columns differ between two views.
func rowsMoved(a, b tablesView) map[string][]string {
	out := map[string][]string{}
	for tb, cols := range busy {
		for row, cells := range b.Tables[tb] {
			for _, c := range cols.moved {
				if a.Tables[tb][row][c] != cells[c] {
					out[tb] = append(out[tb], row)
					break
				}
			}
		}
		slices.Sort(out[tb])
	}
	return out
}

// Eight machines, three streams of 50, four readers, the world with no
// failures at one world tick per machine tick: in every tick, every row of the
// readers', merge and fleet tables that has work at the start of the tick moves
// in it, and every tick's output names the four tables. The work table is the
// pump's, advanced once a tick from what the other tables' updates queued for
// it (the owner, 2026-09-30: "nothing advances the work stream table EXCEPT on
// the next tick"): what is queued is not in the view, so its rows are not
// required to move in a tick that has nothing queued, and the test holds that
// it moves in the ticks the queue fills. The machine accepts what the readers
// passed. At width 64 the fleet takes the whole sprint in one wave; at width 8
// the waves overlap, and every table has work in the same ticks.
func TestEveryRowWithWorkMovesEveryTick(t *testing.T) {
	t.Parallel()
	for _, width := range []int{64, 8} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			t.Parallel()
			everyRowMoves(t, width)
		})
	}
}

func everyRowMoves(t *testing.T, width int) {
	members := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	ta := newTestApp(t)
	ta.live = members
	var ms []string
	for _, m := range members {
		ms = append(ms, m+":"+strconv.Itoa(width))
	}
	ta.ok("init --readers reader-a,reader-b,reader-c,reader-d --members " + strings.Join(ms, ","))
	for _, s := range []string{"s1", "s2", "s3"} {
		ta.ok("add --stream " + s + " --count 50")
	}
	ta.ok("start")
	ta.live = nil            // from here the driver's machines beat
	seen := map[string]int{} // ticks in which each table had work
	workMoved := 0           // ticks in which the work table moved
	for tick := 1; tick <= 12; tick++ {
		var before tablesView
		ta.json("where", &before)
		out := ta.ok("tick")
		if !strings.Contains(out, "TABLES rows changed: work=") || !strings.Contains(out, " readers=") || !strings.Contains(out, " merge=") || !strings.Contains(out, " fleet=") {
			t.Fatalf("tick %d does not name every table:\n%s", tick, out)
		}
		done := strings.Contains(out, "the sprint is done")
		// the merge's work is what the tick's pump accepted into its queue, which
		// the merger lands in the world's turn of the same round: it is read
		// between the two
		var mid tablesView
		ta.json("where", &mid)
		play := ""
		if !done {
			play = ta.ok(fmt.Sprintf("play --simulation --fail 0 --broken 0 --stuck 0 --cross 0 --down 0 --seed %d --ticks 1", tick))
		}
		var after tablesView
		ta.json("where", &after)
		had, moved := rowsWithWork(before), rowsMoved(before, after)
		had["merge"], moved["merge"] = rowsWithWork(mid)["merge"], rowsMoved(mid, after)["merge"]
		for tb, rows := range had {
			if len(rows) > 0 {
				seen[tb]++
			}
			for _, r := range rows {
				if tb != "work" && !slices.Contains(moved[tb], r) {
					t.Errorf("tick %d: %s row %s had work (%v) and did not move: moved %v\n%s", tick, tb, r, before.Tables[tb][r], moved[tb], play)
				}
			}
		}
		if len(moved["work"]) > 0 {
			workMoved++
		}
		t.Logf("tick %d: rows moved %v", tick, moved)
		if done || strings.Contains(play, "every stream has landed") {
			break
		}
	}
	for _, tb := range []string{"fleet", "readers", "work", "merge"} {
		if seen[tb] == 0 {
			t.Errorf("the %s table never had work in the ticks: %v", tb, seen)
		}
	}
	assert.GreaterOrEqual(t, workMoved, 3, "the work table moved in %d ticks, fewer than 3: the pump did not advance it from what the other tables queued", workMoved)
	t.Logf("ticks with work by table: %v; the work table moved in %d", seen, workMoved)
}
