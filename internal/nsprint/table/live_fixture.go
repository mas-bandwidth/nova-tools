package table

import (
	_ "embed"
	"time"
)

// Golden2674 is a real SPRINT-TABLE.txt, written by the bash of record
// (rowan-tools bin/sprint-table-redis) on the Studio at 2026-09-23T19:26:17Z.
//
//go:embed testdata/sprint-table-2674.golden
var golden2674 string

func Golden2674() string { return golden2674 }

// Fixture2674Now is the render instant of Golden2674.
func Fixture2674Now() time.Time { return time.Date(2026, 9, 23, 19, 26, 17, 0, time.UTC) }

// Fixture2674Config is the bash's roster and sprint of that moment.
func Fixture2674Config() LiveConfig {
	return LiveConfig{Friends: []string{"rowan", "johnny", "emma", "stella"}, Sprint: "fixes-2026-09-22", RowStale: 10 * time.Second}
}

// Fixture2674 is the keyspace behind Golden2674, plus the keys the bash
// must leave out: a stale bench (beat 5 min old), a dealer-only hash with
// no host field, bench:stuck_done, and a bench whose dealer_queue is fresh
// and overrides its own queue field.
func Fixture2674() [][]string {
	bench := func(host, queue, working, done, ok, fail, load, at string) []string {
		return []string{"HSET", "bench:" + host, "host", host, "queue", queue, "working", working, "done", done, "ok", ok, "fail", fail, "load1", load, "ncpu", "16", "at", at, "dealer_queue", "0", "dealer_at", "2026-09-23T19:26:10Z"}
	}
	cmds := [][]string{
		bench("batman", "0", "0", "76", "75", "1", "13.61", "2026-09-23T19:26:16Z"),
		bench("hetzner", "0", "0", "318", "311", "7", "1.62", "2026-09-23T19:26:15Z"),
		bench("hulk", "3", "0", "12", "5", "7", "2.16", "2026-09-23T19:26:16Z"), // fresh dealer_queue=0 wins
		bench("spacegame", "0", "0", "16", "4", "12", "0.61", "2026-09-23T19:26:17Z"),
		bench("superman", "0", "0", "103", "97", "6", "16.20", "2026-09-23T19:26:16Z"),
		bench("vision", "0", "0", "15", "6", "9", "2.44", "2026-09-23T19:26:14Z"),
		bench("captainamerica", "0", "2", "9", "9", "0", "0.10", "2026-09-23T19:21:17Z"),  // beat 300 s old: not a row
		{"HSET", "bench:space", "dealer_queue", "0", "dealer_at", "2026-09-23T19:26:10Z"}, // no host: not a row
		{"HSET", "bench:stuck_done", "total", "261", "at", "2026-09-23T19:25:07Z"},
		{"HSET", "bench:pool", "queue", "0", "dealer_at", "2026-09-23T19:26:10Z"},
		{"HSET", "friend:rowan", "at", "2026-09-23T19:26:16Z", "up", "1", "ready", "13", "queue", "13", "working", "26", "width", "26", "done", "2328", "slots", "32"},
		{"HSET", "friend:johnny", "at", "2026-09-23T19:26:16Z", "up", "0", "ready", "0", "queue", "0", "working", "0", "width", "0", "done", "446", "slots", "16"},
		{"SET", "friend:johnny:down", "out-of-credits-week@2026-09-23T21:30Z"},
		{"HSET", "friend:emma", "at", "2026-09-23T19:26:16Z", "up", "0", "ready", "0", "queue", "0", "working", "0", "width", "0", "done", "283", "slots", "8"},
		{"SET", "friend:emma:down", "out-of-credits@2026-09-23T12:15Z"},
		{"HSET", "friend:stella", "at", "2026-09-23T19:26:17Z", "up", "1", "ready", "87", "queue", "87", "working", "21", "width", "21", "done", "1553", "slots", "32"},
		{"SET", "sprint:fixes-2026-09-22:xy", "4644/4918 94% -> ~61m"},
		{"SET", "sprint:fixes-2026-09-22:landed", "193/666 landed 28% (nova-tools 141/403, rowan-tools 29/69, schema 23/194, message-bus 0/0) eta=~21h at=2026-09-23T19:25:17Z"},
	}
	blocked := []string{"ZADD", "q:blocked"}
	for i := 0; i < 99; i++ {
		blocked = append(blocked, "0", "blocked-task-"+string(rune('a'+i/26))+string(rune('a'+i%26)))
	}
	return append(cmds, blocked)
}
