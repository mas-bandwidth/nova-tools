package table

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The #3530 fixture: a whole-table keyspace of 10 streams, 6 benches and 4
// friends, every stamp relative to SprintFixtureNow so the golden holds at
// that instant. It exercises every rule the render applies: two all-zero
// streams hidden, ready, review and reading in their own columns, landed
// moves in and out of the ETA's hour, cards in review past cfg:review
// max_age (a REVIEW line), a bench outside the benches SET (studio) not
// shown, a friend with a down flag, a friend with no beat, and the ONE
// consumer table (#4071): friends then benches, every cell a ZCARD of
// <kind>:<name>:cards:<set> with no sprint window (a copy that ended before
// s:fix opened counts). The old bash bench-row hash bench:<b> and friend row
// hash friend:<f> beside them disagree on every cell and are never read.

//go:embed testdata/sprint-table-3530.golden
var golden3530 string

// Golden3530 is the whole table rendered from SprintFixture at SprintFixtureNow.
func Golden3530() string { return golden3530 }

// SprintFixtureNow is the fixture's instant.
func SprintFixtureNow() time.Time { return time.Date(2026, 9, 25, 0, 30, 0, 0, time.UTC) }

// SprintFixtureConfig is the roster in Glenn's order and the fixture sprint.
func SprintFixtureConfig() SprintConfig {
	return SprintConfig{Sprint: "fix", Friends: []string{"rowan", "johnny", "emma", "stella"}}
}

// SprintFixtureStreams are the fixture's streams in rank order with their
// waiting, ready, working, review, reading, merging and landed counts.
var SprintFixtureStreams = []FixtureStream{
	{"swarm: cards", 150, 5, 6, 3, 0, 0, 0},
	{"nova-sprint + merge + bus", 310, 6, 0, 0, 2, 1, 2},
	{"nova sprint migration", 14, 0, 0, 0, 0, 0, 0},
	{"fleet, ci, secrets, jev", 0, 0, 2, 1, 1, 0, 1},
	{"redis: store + bus", 81, 0, 0, 0, 0, 0, 0},
	{"nova-work", 0, 0, 0, 0, 0, 0, 0},
	{"landing: streams + lander", 3, 0, 0, 0, 0, 2, 0},
	{"docs", 0, 0, 0, 0, 0, 0, 0},
	{"rowan-tools", 1, 0, 0, 0, 0, 0, 3},
	{"harvest", 0, 4, 0, 0, 0, 0, 0},
}

// FixtureStream is one fixture stream's seven set sizes, in WSStates order.
type FixtureStream struct {
	Name                                                      string
	Waiting, Ready, Working, Review, Reading, Merging, Landed int64
}

// fixtureReviewAges are how long each fixture card in review has waited,
// by id: past cfg:review max_age (2h) are the swarm's first two.
var fixtureReviewAges = map[string]time.Duration{
	"t1-review-0": 3 * time.Hour, "t1-review-1": 150 * time.Minute, "t1-review-2": 10 * time.Minute,
	"t4-review-0": 30 * time.Minute,
}

// SprintFixture is the keyspace as Redis commands.
func SprintFixture() [][]string {
	now := SprintFixtureNow()
	at := func(d time.Duration) string { return now.Add(d).UTC().Format("2006-01-02T15:04:05Z") }
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	var cmds [][]string
	for i, s := range SprintFixtureStreams {
		cmds = append(cmds, []string{"ZADD", "ws:order", strconv.Itoa(i + 1), s.Name})
		counts := []int64{s.Waiting, s.Ready, s.Working, s.Review, s.Reading, s.Merging, s.Landed}
		for j, state := range WSStates {
			for k := int64(0); k < counts[j]; k++ {
				id := fmt.Sprintf("t%d-%s-%d", i+1, state, k)
				cmds = append(cmds, []string{"ZADD", "ws:" + s.Name + ":" + state, strconv.FormatInt(k+1, 10), id})
				task := []string{"HSET", "task:" + id, "stream", s.Name, "state", state, "title", "task " + id}
				if age, ok := fixtureReviewAges[id]; ok {
					task = append(task, "review_at", ms(-age))
				}
				cmds = append(cmds, task)
			}
		}
	}
	cmds = append(cmds, []string{"HSET", "cfg:review", "max_age", "7200"})
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
	// in it. Each bench's ready, working, ok and fail are its card sets
	// (every bench also has one ok copy that ended before s:fix opened: it
	// counts, there is no sprint window), its load its beat; the bash hash
	// bench:<b> says 9/9/9.99 and must not show.
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
		cmds = append(cmds, ConsumerCards("bench:"+b.name, [4]int{b.ready, b.working, b.ok, b.fail})...)
		cmds = append(cmds, []string{"ZADD", "bench:" + b.name + ":cards:ok", ms(-4 * time.Hour), b.name + "-before-open~1"})
	}
	// Friends: rowan up, johnny's beat fresh but a down flag, emma up,
	// stella with no beat (its 5 s TTL lapsed); the friends SET also names
	// ghost, who is not in the roster. The consumers SET names two members
	// already on the table (each shows once). The friend:<f> row hashes
	// disagree with every cell and are never read.
	cmds = append(cmds,
		[]string{"SADD", "friends", "rowan", "johnny", "emma", "stella", "ghost"},
		[]string{"SADD", "consumers", "friend:rowan", "bench:hetzner"},
		[]string{"HSET", "friend:rowan:beat", "at", ms(-1 * time.Second), "session", "r"},
		[]string{"HSET", "friend:johnny:beat", "at", ms(-1 * time.Second), "session", "j"},
		[]string{"SET", "friend:johnny:down", "out-of-credits@2026-09-24T23:00Z"},
		[]string{"HSET", "friend:emma:beat", "at", ms(-2 * time.Second), "session", "e"},
		[]string{"HSET", "friend:rowan", "at", at(-1 * time.Second), "up", "1", "done", "99"},
		[]string{"HSET", "friend:stella", "at", at(-1 * time.Second), "up", "1"},
		[]string{"HSET", DoneBaseKey, "rowan", "10"},
		// emma's one ok copy from before s:fix opened counts too
		[]string{"ZADD", "friend:emma:cards:ok", ms(-4 * time.Hour), "e-before-open~1"},
	)
	for _, f := range []struct {
		name   string
		counts [4]int
	}{
		{"rowan", [4]int{0, 12, 10, 4}},
		{"johnny", [4]int{2, 0, 3, 0}},
		{"emma", [4]int{1, 4, 3, 1}},
		{"stella", [4]int{0, 1, 5, 0}},
	} {
		cmds = append(cmds, ConsumerCards("friend:"+f.name, f.counts)...)
	}
	// The working column counts live children only (#3892): each working
	// card's record and beat. rowan's 12 are 2 beating, 4 with a beat 10
	// minutes old, 3 never beaten and 3 finished cards left in the set, so
	// the row prints working 2 and stale=10; emma's and stella's all beat.
	for _, f := range []struct {
		name string
		n    int
	}{{"rowan", 12}, {"emma", 4}, {"stella", 1}} {
		for k := 0; k < f.n; k++ {
			id := fmt.Sprintf("task:%s-working-%d", f.name, k)
			rec := []string{"HSET", id, "where", "working", "friend", f.name, "owner", f.name}
			switch {
			case f.name != "rowan" || k < 2:
				rec = append(rec, "beat_at", ms(-10*time.Second))
			case k < 6:
				rec = append(rec, "beat_at", ms(-10*time.Minute))
			case k < 9:
			default:
				rec = append(rec, "beat_at", ms(-10*time.Minute))
				rec[3] = "done"
			}
			cmds = append(cmds, rec)
		}
	}
	return cmds
}

// ConsumerCards is n[i] copy ids in <consumer>:cards:<ConsumerSets[i]>, as
// ZADD commands scored by age: created_at from two hours before the
// fixture's now.
func ConsumerCards(consumer string, n [4]int) [][]string {
	var cmds [][]string
	start := SprintFixtureNow().Add(-2 * time.Hour)
	name := consumer[strings.Index(consumer, ":")+1:]
	for i, set := range ConsumerSets {
		for k := 0; k < n[i]; k++ {
			score := strconv.FormatInt(start.Add(time.Duration(k+1)*time.Second).UnixMilli(), 10)
			cmds = append(cmds, []string{"ZADD", consumer + ":cards:" + set, score, fmt.Sprintf("%s-%s~%d", name, set, k)})
		}
	}
	return cmds
}
