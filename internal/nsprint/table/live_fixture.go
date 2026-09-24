package table

import (
	_ "embed"
	"fmt"
	"strings"
	"time"
)

// The #2674 control's fixture is a REAL keyspace, not a hand-written one:
// testdata/sprint-table-2674-live.snap is every key the bash of record reads,
// dumped from the fleet Redis in one pipelined read (TestCapture2674LiveSnapshot
// wrote it; friend:<name> is the whole hash exactly as friend-row writes it).
// The goldens are what the bash of record (testdata/sprint-table-redis.bash,
// sha256 pinned in the test) printed when run on that keyspace inside
// TestControl2674SprintLayout, not what Go prints.

//go:embed testdata/sprint-table-2674-live.snap
var liveSnap2674 string

//go:embed testdata/sprint-table-2674.golden
var golden2674 string

//go:embed testdata/sprint-table-2674-degraded.golden
var golden2674Degraded string

//go:embed testdata/sprint-table-2674-noredis.golden
var golden2674NoRedis string

// Golden2674 is the bash of record's SPRINT-TABLE.txt on Fixture2674().
func Golden2674() string { return golden2674 }

// Golden2674Degraded is the bash's table on Fixture2674() plus
// Fixture2674Degraded(): missing, stale and down friend rows, no xy or
// landed key, an empty q:blocked, no bench:pool, a stale bench, a hostless hash, and fresh
// and stale dealer counts.
func Golden2674Degraded() string { return golden2674Degraded }

// Golden2674NoRedis is the bash's first tick when Redis does not answer.
func Golden2674NoRedis() string { return golden2674NoRedis }

// LiveSnapshot2674 is the raw snapshot file.
func LiveSnapshot2674() string { return liveSnap2674 }

// Fixture2674Now is the capture instant of the snapshot (its "captured=" line).
func Fixture2674Now() time.Time {
	for _, line := range strings.Split(liveSnap2674, "\n") {
		if i := strings.Index(line, "captured="); strings.HasPrefix(line, "#") && i >= 0 {
			field := strings.Fields(line[i+len("captured="):])
			if len(field) > 0 {
				if t, ok := parseUTC(field[0]); ok {
					return t
				}
			}
		}
	}
	panic("sprint-table-2674-live.snap has no captured= line")
}

// Fixture2674Config is the bash's roster and sprint (FRIENDS, XYKEY).
func Fixture2674Config() LiveConfig {
	return LiveConfig{Friends: []string{"rowan", "johnny", "emma", "stella"}, Sprint: "fixes-2026-09-22", RowStale: 10 * time.Second}
}

// Fixture2674 is the live snapshot as Redis commands, one per key, in file
// order. Lines are tab-separated; "#" lines are comments.
func Fixture2674() [][]string {
	var cmds [][]string
	for n, line := range strings.Split(liveSnap2674, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cmd := strings.Split(line, "\t")
		if len(cmd) < 3 {
			panic(fmt.Sprintf("sprint-table-2674-live.snap line %d: %q", n+1, line))
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}

// Fixture2674Degraded is applied on top of Fixture2674(): every branch of the
// bash the live snapshot did not happen to take. Every age is at least 15 s
// from its threshold, so a render a few seconds late prints the same cells.
func Fixture2674Degraded() [][]string {
	c := Fixture2674Now()
	at := func(d time.Duration) string { return c.Add(d).UTC().Format("2006-01-02T15:04:05Z") }
	return [][]string{
		{"DEL", "friend:emma", "friend:emma:down"},                              // no row at all: "-" cells, status ?
		{"HSET", "friend:rowan", "at", at(-30 * time.Second), "up", "1"},        // beat 30 s old: stale, last counts kept
		{"HSET", "friend:stella", "at", at(-1 * time.Second), "up", "0"},        // fresh, presence gone, no down flag: down
		{"DEL", "sprint:fixes-2026-09-22:xy", "sprint:fixes-2026-09-22:landed"}, // no xy (fallback + stale line), landed ?
		{"DEL", "q:blocked", "bench:pool"},                                      // blocked: 0
		{"HSET", "bench:zz-stale", "host", "zz-stale", "queue", "4", "working", "2", "done", "9", "ok", "9", "fail", "0", "load1", "1.00", "at", at(-300 * time.Second), "dealer_queue", "1", "dealer_at", at(-2 * time.Second)}, // beat 300 s old: not a row
		{"HSET", "bench:zz-hostless", "dealer_queue", "3", "dealer_at", at(-2 * time.Second)}, // no host field: not a row
		{"HSET", "bench:zz-dealer", "host", "zz-dealer", "queue", "5", "working", "1", "done", "10", "ok", "7", "fail", "3", "load1", "0.50", "at", at(-1 * time.Second), "dealer_queue", "11", "dealer_at", at(-2 * time.Second)}, // fresh dealer_queue wins: 11
		{"HSET", "bench:zz-olddealer", "host", "zz-olddealer", "queue", "6", "working", "0", "done", "0", "ok", "0", "fail", "0", "at", at(-1 * time.Second), "dealer_queue", "12", "dealer_at", at(-120 * time.Second)},           // stale dealer_queue ignored: 6; no load1: -
	}
}
