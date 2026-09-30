package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
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
// failures at one world tick per machine tick and the coordinator accepting
// what the readers passed: in every tick, every row of every table that has
// work at the start of the tick moves in it, and every tick's output names
// the four tables. At width 64 the fleet takes the whole sprint in one wave;
// at width 8 the waves overlap, and every table has work in the same ticks.
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
	for tick := 1; tick <= 10; tick++ {
		var before tablesView
		ta.json("where", &before)
		out := ta.ok("tick")
		if !strings.Contains(out, "TABLES rows changed: work=") || !strings.Contains(out, " readers=") || !strings.Contains(out, " merge=") || !strings.Contains(out, " fleet=") {
			t.Fatalf("tick %d does not name every table:\n%s", tick, out)
		}
		play := ta.ok(fmt.Sprintf("play --simulation --fail 0 --broken 0 --stuck 0 --cross 0 --down 0 --seed %d --ticks 1", tick))
		var after tablesView
		ta.json("where", &after)
		had, moved := rowsWithWork(before), rowsMoved(before, after)
		for tb, rows := range had {
			if len(rows) > 0 {
				seen[tb]++
			}
			for _, r := range rows {
				if !slices.Contains(moved[tb], r) {
					t.Errorf("tick %d: %s row %s had work (%v) and did not move: moved %v\n%s", tick, tb, r, before.Tables[tb][r], moved[tb], play)
				}
			}
		}
		t.Logf("tick %d: rows moved %v", tick, moved)
		if strings.Contains(play, "every stream has landed") {
			break
		}
		ta.ok("accept --read-ok")
	}
	for _, tb := range []string{"fleet", "readers", "work", "merge"} {
		if seen[tb] == 0 {
			t.Errorf("the %s table never had work in the ten ticks: %v", tb, seen)
		}
	}
	t.Logf("ticks with work by table: %v", seen)
}
