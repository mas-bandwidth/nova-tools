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
// streams hidden, ready and reading in their own columns, landed moves in and out of the
// ETA's hour, a bench outside the benches SET (studio) not shown, a friend with a down flag, a friend whose beat is
// stale, a friend with no row, and a done base from a clear. Host rows are
// each bench's own keys (#2389): its card views and its beat; the bash
// bench-row hash beside them disagrees on every cell and is never read.

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
// waiting, ready, working, reading, merging and landed counts.
var SprintFixtureStreams = []FixtureStream{
	{"swarm: cards", 150, 5, 6, 0, 0, 0},
	{"nova-sprint + merge + bus", 310, 6, 0, 2, 1, 2},
	{"nova sprint migration", 14, 0, 0, 0, 0, 0},
	{"fleet, ci, secrets, jev", 0, 0, 2, 1, 0, 1},
	{"redis: store + bus", 81, 0, 0, 0, 0, 0},
	{"nova-work", 0, 0, 0, 0, 0, 0},
	{"landing: streams + lander", 3, 0, 0, 0, 2, 0},
	{"docs", 0, 0, 0, 0, 0, 0},
	{"rowan-tools", 1, 0, 0, 0, 0, 3},
	{"harvest", 0, 4, 0, 0, 0, 0},
}

// FixtureStream is one fixture stream's six set sizes, in WSStates order.
type FixtureStream struct {
	Name                                              string
	Waiting, Ready, Working, Reading, Merging, Landed int64
}

// SprintFixture is the keyspace as Redis commands.
func SprintFixture() [][]string {
	now := SprintFixtureNow()
	at := func(d time.Duration) string { return now.Add(d).UTC().Format("2006-01-02T15:04:05Z") }
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	var cmds [][]string
	for i, s := range SprintFixtureStreams {
		cmds = append(cmds, []string{"ZADD", "ws:order", strconv.Itoa(i + 1), s.Name})
		counts := []int64{s.Waiting, s.Ready, s.Working, s.Reading, s.Merging, s.Landed}
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
	cmds = append(cmds, []string{"SET", "s:fix:pitstop", "fixture"})
	// Benches: six in the SET; studio has a fresh beat and cards but is not
	// in it. Each bench's ready and working are its card views, its ok and
	// fail its ended cards since s:fix opened (#3894; every bench also has
	// one ok card from before the open, which never counts), its load its
	// beat; the bash hash bench:<b> says 9/9/9.99 and must not show.
	cmds = append(cmds, []string{"HSET", "s:fix", "status", "open", "opened_at", ms(-3 * time.Hour)})
	for _, b := range []struct {
		name                     string
		ready, working, ok, fail int
		load                     string
	}{
		{"batman", 0, 0, 0, 0, "0.89"},
		{"hetzner", 2, 3, 3, 1, "0.19"},
		{"hulk", 7, 0, 2, 0, "0.40"},
		{"space", 1, 4, 1, 2, "1.04"},
		{"superman", 0, 1, 0, 1, "1.02"},
		{"vision", 0, 0, 0, 0, "0.72"},
		{"studio", 9, 9, 9, 9, "3.00"},
	} {
		if b.name != "studio" {
			cmds = append(cmds, []string{"SADD", "benches", b.name})
		}
		cmds = append(cmds, []string{"HSET", "bench:" + b.name + ":beat", "host", b.name, "load1", b.load, "live", strconv.Itoa(b.working), "at", ms(-1 * time.Second)})
		cmds = append(cmds, []string{"HSET", "bench:" + b.name, "host", b.name, "queue", "9", "working", "9", "load1", "9.99", "at", at(-1 * time.Second)})
		for k := 0; k < b.ready; k++ {
			cmds = append(cmds, []string{"ZADD", "bench:" + b.name + ":cards:ready", strconv.Itoa(k + 1), fmt.Sprintf("card:%s-r%d", b.name, k)})
		}
		for k := 0; k < b.working; k++ {
			cmds = append(cmds, []string{"ZADD", "bench:" + b.name + ":cards:working", strconv.Itoa(k + 1), fmt.Sprintf("card:%s-w%d", b.name, k)})
		}
		for k := 0; k < b.ok; k++ {
			cmds = append(cmds, []string{"ZADD", "bench:" + b.name + ":cards:ok", ms(-time.Duration(k+1) * time.Minute), fmt.Sprintf("card:%s-ok%d", b.name, k)})
		}
		for k := 0; k < b.fail; k++ {
			cmds = append(cmds, []string{"ZADD", "bench:" + b.name + ":cards:fail", ms(-time.Duration(k+1) * time.Minute), fmt.Sprintf("card:%s-fail%d", b.name, k)})
		}
		cmds = append(cmds, []string{"ZADD", "bench:" + b.name + ":cards:ok", ms(-4 * time.Hour), "card:" + b.name + "-before-open"})
	}
	// Friends: rowan up (done 14, 10 at the last clear), johnny down flag,
	// emma up, stella's beat 30 s old; the friends SET also names ghost,
	// who has no row.
	// The counts are the friend's card sets (friend:<f>:cards:<where>); the
	// hash holds only the beat (at, up).
	cmds = append(cmds,
		[]string{"SADD", "friends", "rowan", "johnny", "emma", "stella", "ghost"},
		[]string{"HSET", "friend:rowan", "at", at(-1 * time.Second), "up", "1"},
		[]string{"HSET", "friend:johnny", "at", at(-1 * time.Second), "up", "1"},
		[]string{"SET", "friend:johnny:down", "out-of-credits@2026-09-24T23:00Z"},
		[]string{"HSET", "friend:emma", "at", at(-2 * time.Second), "up", "1"},
		[]string{"HSET", "friend:stella", "at", at(-30 * time.Second), "up", "1"},
		[]string{"HSET", DoneBaseKey, "rowan", "10"},
	)
	for _, f := range []struct {
		name   string
		counts [3]int
	}{
		{"rowan", [3]int{0, 12, 14}},
		{"johnny", [3]int{2, 0, 3}},
		{"emma", [3]int{1, 4, 0}},
		{"stella", [3]int{0, 1, 5}},
	} {
		cmds = append(cmds, FriendCards(f.name, f.counts)...)
	}
	return cmds
}

// FriendCards is n[i] task ids in friend:<name>:cards:<FriendWheres[i]>, as
// ZADD commands scored by age: created_at from two hours before the fixture's
// now, after s:fix opened (#3894), so the done column counts every one.
func FriendCards(name string, n [3]int) [][]string {
	var cmds [][]string
	start := SprintFixtureNow().Add(-2 * time.Hour)
	for i, w := range FriendWheres {
		for k := 0; k < n[i]; k++ {
			score := strconv.FormatInt(start.Add(time.Duration(k+1)*time.Second).UnixMilli(), 10)
			cmds = append(cmds, []string{"ZADD", FriendCardsKey(name, w), score, fmt.Sprintf("%s-%s-%d", name, w, k)})
		}
	}
	return cmds
}
