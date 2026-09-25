package table

import (
	_ "embed"
	"fmt"
	"strconv"
	"time"
)

// The #3530 fixture: a whole-table keyspace of 10 streams, 6 benches and 4
// friends, every stamp relative to SprintFixtureNow so the golden holds at
// that instant. It exercises every rule the render applies: two all-zero
// streams hidden, ready counted as waiting, landed moves in and out of the
// ETA's hour, a bench outside the benches SET (studio) not shown, a fresh
// dealer count winning, a friend with a down flag, a friend whose beat is
// stale, a friend with no row, and a done base from a clear.

//go:embed testdata/sprint-table-3530.golden
var golden3530 string

// Golden3530 is the whole table rendered from SprintFixture at SprintFixtureNow.
func Golden3530() string { return golden3530 }

// SprintFixtureNow is the fixture's instant.
func SprintFixtureNow() time.Time { return time.Date(2026, 9, 25, 0, 30, 0, 0, time.UTC) }

// SprintFixtureConfig is the roster in Glenn's order and the fixture sprint.
func SprintFixtureConfig() SprintConfig {
	return SprintConfig{Sprint: "fix", Friends: []string{"rowan", "johnny", "emma", "stella"}, RowStale: 10 * time.Second}
}

// SprintFixtureStreams are the fixture's streams in rank order with their
// waiting, ready, working, merging and landed counts.
var SprintFixtureStreams = []StreamRow{
	{"swarm: cards", 150, 5, 6, 0, 0},
	{"nova-sprint + merge + bus", 310, 6, 0, 1, 2},
	{"nova sprint migration", 14, 0, 0, 0, 0},
	{"fleet, ci, secrets, jev", 0, 0, 2, 0, 1},
	{"redis: store + bus", 81, 0, 0, 0, 0},
	{"nova-work", 0, 0, 0, 0, 0},
	{"landing: streams + lander", 3, 0, 0, 2, 0},
	{"docs", 0, 0, 0, 0, 0},
	{"rowan-tools", 1, 0, 0, 0, 3},
	{"harvest", 0, 4, 0, 0, 0},
}

// SprintFixture is the keyspace as Redis commands.
func SprintFixture() [][]string {
	now := SprintFixtureNow()
	at := func(d time.Duration) string { return now.Add(d).UTC().Format("2006-01-02T15:04:05Z") }
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	var cmds [][]string
	for i, s := range SprintFixtureStreams {
		cmds = append(cmds, []string{"ZADD", "ws:order", strconv.Itoa(i + 1), s.Name})
		counts := []int64{s.Waiting, s.Ready, s.Working, s.Merging, s.Landed}
		for j, state := range WSStates {
			for k := int64(0); k < counts[j]; k++ {
				id := fmt.Sprintf("t%d-%s-%d", i+1, state, k)
				cmds = append(cmds, []string{"ZADD", "ws:" + s.Name + ":" + state, strconv.FormatInt(k+1, 10), id})
				cmds = append(cmds, []string{"HSET", "task:" + id, "stream", s.Name, "state", state, "title", "task " + id, "owner", "rowan"})
			}
		}
	}
	// The ETA's hour: three moves to landed inside it, one before it, and
	// moves to other states that never count.
	for _, e := range []struct {
		ago    time.Duration
		id, to string
	}{
		{2 * time.Hour, "t2-landed-0", "landed"},
		{50 * time.Minute, "t2-landed-1", "landed"},
		{40 * time.Minute, "t4-working-0", "working"},
		{30 * time.Minute, "t4-landed-0", "landed"},
		{10 * time.Minute, "t9-landed-0", "landed"},
		{5 * time.Minute, "t7-merging-0", "merging"},
	} {
		cmds = append(cmds, []string{"XADD", "ws:log", ms(-e.ago) + "-0", "id", e.id, "from", "working", "to", e.to, "by", "fixture", "at", ms(-e.ago)})
	}
	cmds = append(cmds, []string{"HSET", "s:fix:pitstop", "reason", "fixture", "at", "1600000000000"})
	// Benches: six in the SET; studio has a fresh row but is not in it.
	for _, b := range []struct {
		name, queue, working, load string
	}{
		{"batman", "0", "0", "0.89"},
		{"hetzner", "2", "3", "0.19"},
		{"hulk", "0", "0", "0.40"},
		{"space", "1", "4", "1.04"},
		{"superman", "0", "1", "1.02"},
		{"vision", "0", "0", "0.72"},
	} {
		cmds = append(cmds, []string{"SADD", "benches", b.name})
		cmds = append(cmds, []string{"HSET", "bench:" + b.name, "host", b.name, "queue", b.queue, "working", b.working, "done", "9", "ok", "2", "fail", "7", "load1", b.load, "at", at(-1 * time.Second)})
	}
	cmds = append(cmds,
		[]string{"HSET", "bench:hulk", "dealer_queue", "7", "dealer_at", at(-2 * time.Second)}, // a fresh dealer count wins: 7
		[]string{"HSET", "bench:studio", "host", "studio", "queue", "9", "working", "9", "load1", "3.00", "at", at(-1 * time.Second)},
	)
	// Friends: rowan up (done 14, 10 at the last clear), johnny down flag,
	// emma up, stella's beat 30 s old; the friends SET also names ghost,
	// who has no row.
	cmds = append(cmds,
		[]string{"SADD", "friends", "rowan", "johnny", "emma", "stella", "ghost"},
		[]string{"HSET", "friend:rowan", "at", at(-1 * time.Second), "up", "1", "ready", "0", "working", "12", "done", "14"},
		[]string{"HSET", "friend:johnny", "at", at(-1 * time.Second), "up", "1", "ready", "2", "working", "0", "done", "3"},
		[]string{"SET", "friend:johnny:down", "out-of-credits@2026-09-24T23:00Z"},
		[]string{"HSET", "friend:emma", "at", at(-2 * time.Second), "up", "1", "ready", "1", "working", "4", "done", "0"},
		[]string{"HSET", "friend:stella", "at", at(-30 * time.Second), "up", "1", "ready", "0", "working", "1", "done", "5"},
		[]string{"HSET", DoneBaseKey, "rowan", "10"},
	)
	return cmds
}
