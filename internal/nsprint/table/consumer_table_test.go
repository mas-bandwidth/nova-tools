package table_test

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// consumerStore is an empty in-process store with a command log.
func consumerStore(t *testing.T, cmds [][]string) (*redis.Client, *cmdLog) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	seedCommands(t, client, cmds)
	log := &cmdLog{}
	client.AddHook(log)
	return client, log
}

// consumerBlock is the rendered table from its consumer header to the end.
func consumerBlock(t *testing.T, got string) string {
	t.Helper()
	i := strings.Index(got, "\nconsumer ")
	if i < 0 {
		t.Fatalf("no consumer table:\n%s", got)
	}
	return got[i+1:]
}

// TestControl4071OneConsumerTable is the #4071 DONE-WHEN fixture: one friend
// and one bench, each holding one ok and one fail copy, render ONE consumer
// table (no friend block, no host block) with two rows, kind shown, ok% 50
// for both; done = ok + fail from the sets alone (a sprint opened after the
// cards ended changes nothing: no opened_at window), status from the beat
// and the down key, load from the beat (- for a friend, whose beat has none).
func TestControl4071OneConsumerTable(t *testing.T) {
	t.Parallel()
	now := table.SprintFixtureNow()
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	client, _ := consumerStore(t, [][]string{
		{"SADD", "friends", "emma"},
		{"SADD", "benches", "hetzner"},
		{"HSET", "friend:emma:beat", "at", ms(-time.Second), "session", "s1"},
		{"HSET", "bench:hetzner:beat", "load1", "0.19", "at", ms(-time.Second)},
		{"ZADD", "friend:emma:cards:ok", ms(-3 * time.Hour), "p1~1"},
		{"ZADD", "friend:emma:cards:fail", ms(-3 * time.Hour), "p2~1"},
		{"ZADD", "bench:hetzner:cards:ok", ms(-3 * time.Hour), "p3~1"},
		{"ZADD", "bench:hetzner:cards:fail", ms(-3 * time.Hour), "p4~1"},
		// a sprint opened after every card ended: no window, they all count
		{"ZADD", "sprint:order", "1", "S1"},
		{"HSET", "s:S1", "status", "open", "opened_at", ms(-time.Hour)},
	})
	snap, err := table.NewSprintReader(client, table.SprintConfig{Sprint: "S1"}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Render(now)
	if strings.Contains(got, "\nfriend     |") || strings.Contains(got, "\nhost       |") {
		t.Fatalf("a separate friend or host block is still printed:\n%s", got)
	}
	want := "consumer             | ready | working |  done |    ok |  fail |  ok% | status | load\n" +
		"---------------------+-------+---------+-------+-------+-------+------+--------+------\n" +
		"emma                 |     0 |       0 |     2 |     1 |     1 |  50% | up     | -\n" +
		"hetzner              |     0 |       0 |     2 |     1 |     1 |  50% | up     | 0.19\n" +
		"---------------------+-------+---------+-------+-------+-------+------+--------+------\n" +
		"total                |     0 |       0 |     4 |     2 |     2 |  50% |\n"
	if block := consumerBlock(t, got); block != want {
		t.Fatalf("consumer table:\n%s\nwant:\n%s", block, want)
	}
}

// TestConsumerTableStatusAndOrder: friends first (the roster order), then
// benches, then any other enrolled consumer, each once; a consumer with a
// down key, or whose beat is gone or older than a minute, is down and still
// shows its cells (cards never disappear).
func TestConsumerTableStatusAndOrder(t *testing.T) {
	t.Parallel()
	now := table.SprintFixtureNow()
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	client, _ := consumerStore(t, [][]string{
		{"SADD", "friends", "rowan", "stella"},
		{"SADD", "benches", "hulk", "space"},
		{"SADD", "consumers", "bench:hulk", "friend:rowan", "bench:extra"},
		{"HSET", "friend:rowan:beat", "at", ms(-time.Second)},
		{"SET", "friend:stella:down", "out-of-credits"},
		{"HSET", "friend:stella:beat", "at", ms(-time.Second)},
		{"HSET", "bench:hulk:beat", "load1", "0.40", "at", ms(-2 * time.Minute)},
		{"HSET", "bench:space:beat", "load1", "1.04", "at", ms(-time.Second)},
		{"ZADD", "bench:hulk:cards:ready", "1", "a~1", "2", "b~1"},
		{"ZADD", "friend:stella:cards:working", "1", "c~1"},
		{"ZADD", "bench:extra:cards:ok", "1", "d~1"},
		// the old rows disagree with every cell and are never read
		{"HSET", "friend:rowan", "at", "2026-09-25T00:29:59Z", "up", "1", "done", "99"},
		{"HSET", "bench:space", "queue", "99", "working", "99", "load1", "9.99"},
	})
	snap, err := table.NewSprintReader(client, table.SprintConfig{Friends: []string{"stella", "rowan"}}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	block := consumerBlock(t, snap.Render(now))
	want := []string{
		"stella               |     0 |       1 |     0 |     0 |     0 |    - | down   | -",
		"rowan                |     0 |       0 |     0 |     0 |     0 |    - | up     | -",
		"hulk                 |     2 |       0 |     0 |     0 |     0 |    - | down   | 0.40",
		"space                |     0 |       0 |     0 |     0 |     0 |    - | up     | 1.04",
		"extra                |     0 |       0 |     1 |     1 |     0 | 100% | down   | -",
	}
	rows := strings.Split(block, "\n")[2:7]
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
	if strings.Contains(block, "99") || strings.Contains(block, "9.99") {
		t.Fatalf("an old row hash reached the table:\n%s", block)
	}
}

// TestConsumerTableKeyAllowlist: a steady tick reads, per consumer, only
// the ZCARDs of its four sets, its beat (HMGET) and its down key (EXISTS);
// never the old friend:<f> or bench:<b> row hash, ws:done0, a sprint's
// opened_at or its cards, and never a ZCOUNT window.
func TestConsumerTableKeyAllowlist(t *testing.T) {
	t.Parallel()
	now := table.SprintFixtureNow()
	client, log := consumerStore(t, [][]string{
		{"SADD", "friends", "emma"},
		{"SADD", "benches", "hetzner"},
		{"SADD", "consumers", "friend:emma"},
		{"ZADD", "sprint:order", "1", "S1"},
		{"HSET", "s:S1", "opened_at", "1"},
	})
	r := table.NewSprintReader(client, table.SprintConfig{Sprint: "S1"})
	if _, err := r.Read(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	log.reset()
	if _, err := r.Read(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var consumerKeys []string
	for k := range log.keysRead() {
		name, key, _ := strings.Cut(k, " ")
		if name == "ZCOUNT" || key == "ws:done0" || key == "s:S1" || strings.HasPrefix(key, "sprint:S1:") || key == "sprint:order" {
			t.Errorf("the tick read %s", k)
		}
		if strings.HasPrefix(key, "friend:") || strings.HasPrefix(key, "bench:") {
			consumerKeys = append(consumerKeys, k)
		}
	}
	sort.Strings(consumerKeys)
	want := []string{
		"EXISTS bench:hetzner:down", "EXISTS friend:emma:down",
		"HMGET bench:hetzner:beat",
		"HMGET bench:studio:beat", // every friend lives on the Studio today: its load (hardcoded, see readOnce)
		"HMGET friend:emma:beat",
		"ZCARD bench:hetzner:cards:fail", "ZCARD bench:hetzner:cards:ok", "ZCARD bench:hetzner:cards:ready", "ZCARD bench:hetzner:cards:working",
		"ZCARD friend:emma:cards:fail", "ZCARD friend:emma:cards:ok", "ZCARD friend:emma:cards:ready", "ZCARD friend:emma:cards:working",
	}
	if strings.Join(consumerKeys, "\n") != strings.Join(want, "\n") {
		t.Fatalf("consumer keys read:\n%s\nwant exactly:\n%s", strings.Join(consumerKeys, "\n"), strings.Join(want, "\n"))
	}
}

// TestStreamTableReviewColumn (#4072): review is its own column between
// working and reading, counted in y and never done (left includes it), and
// a card in review longer than cfg:review max_age is a REVIEW bound line.
func TestStreamTableReviewColumn(t *testing.T) {
	t.Parallel()
	now := table.SprintFixtureNow()
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	client, _ := consumerStore(t, [][]string{
		{"ZADD", "ws:order", "1", "swarm: cards"},
		{"ZADD", "ws:swarm: cards:working", "1", "w1"},
		{"ZADD", "ws:swarm: cards:review", "2", "r-old", "3", "r-new"},
		{"HSET", "task:r-old", "where", "review", "review_at", ms(-2 * time.Hour)},
		{"HSET", "task:r-new", "where", "review", "review_at", ms(-5 * time.Minute)},
		{"ZADD", "ws:swarm: cards:landed", "4", "l1"},
		{"HSET", "cfg:review", "max_age", "3600"},
	})
	snap, err := table.NewSprintReader(client, table.SprintConfig{}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Render(now)
	want := "3/4 left, 25% done -> ~180m\n\n" +
		"stream                         | waiting | ready | working | review | reading | merging | landed\n" +
		"-------------------------------+---------+-------+---------+--------+---------+---------+-------\n" +
		"swarm: cards                   |       0 |     0 |       1 |      2 |       0 |     0/0 |      1\n" +
		"-------------------------------+---------+-------+---------+--------+---------+---------+-------\n" +
		"total                          |       0 |     0 |       1 |      2 |       0 |     0/0 |      1\n" +
		"REVIEW stream=\"swarm: cards\" over=1 oldest=r-old age=2h00m max=1h00m\n\n"
	if !strings.Contains(got, want) {
		t.Fatalf("stream table:\n%s\nwant:\n%s", got, want)
	}
}
