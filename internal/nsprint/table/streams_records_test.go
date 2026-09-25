package table_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// TestStreamsTableFromRecords (DONE-WHEN of #3530, the streams block): each
// stream row is stream x {waiting, ready, working, merging, landed}, every
// cell the ZCARD of ws:<stream>:<where>, the set of card ids whose record's
// where names it. Ready is its own column: nothing is folded into waiting
// (Glenn 2026-09-25 saw 68 waiting that were 56 waiting + 12 ready). The
// total row is the column sums and the headline's y is every card in the
// five sets. The merging cell is <read>/<unread> (#3900): these cards carry
// no PR, so none is read.
func TestStreamsTableFromRecords(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	streams := []struct {
		name  string
		cells [5]int // waiting, ready, working, merging, landed
	}{
		{"nova-sprint + merge + bus", [5]int{56, 12, 3, 2, 1}},
		{"swarm: cards", [5]int{4, 0, 7, 0, 5}},
		{"idle", [5]int{0, 0, 0, 0, 0}},
		{"only ready", [5]int{0, 9, 0, 0, 0}},
	}
	var cmds [][]string
	created := 1_758_800_000_000
	for i, s := range streams {
		cmds = append(cmds, []string{"ZADD", "ws:order", strconv.Itoa(i + 1), s.name})
		for j, where := range table.WSStates {
			for k := 0; k < s.cells[j]; k++ {
				created++
				id := fmt.Sprintf("c%d-%s-%d", i, where, k)
				at := strconv.Itoa(created)
				cmds = append(cmds,
					[]string{"HSET", "card:" + id, "stream", s.name, "where", where, "created_at", at},
					[]string{"ZADD", "ws:" + s.name + ":" + where, at, id})
			}
		}
	}
	seedCommands(t, client, cmds)
	now := table.SprintFixtureNow()
	snap, err := table.NewSprintReader(client, table.SprintConfig{}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range streams {
		r := snap.Streams[i]
		got := [5]int64{r.Waiting, r.Ready, r.Working, r.Merging, r.Landed}
		for j, where := range table.WSStates {
			n, err := client.ZCard(context.Background(), "ws:"+s.name+":"+where).Result()
			if err != nil || got[j] != n || n != int64(s.cells[j]) {
				t.Fatalf("%s %s: row %d, ZCARD %d (%v), seeded %d", s.name, where, got[j], n, err, s.cells[j])
			}
		}
	}
	out := snap.Render(now)
	rule := "-------------------------------+---------+-------+---------+---------+-------\n"
	want := "SPRINT TABLE\n\n93/99 left, 6% done -> ~5580m\n\n" +
		"stream                         | waiting | ready | working | merging | landed\n" + rule +
		"nova-sprint + merge + bus      |      56 |    12 |       3 |     0/2 |      1\n" +
		"swarm: cards                   |       4 |     0 |       7 |     0/0 |      5\n" +
		"only ready                     |       0 |     9 |       0 |     0/0 |      0\n" + rule +
		"total                          |      60 |    21 |      10 |     0/2 |      6\n\n"
	if !strings.HasPrefix(out, want) {
		t.Fatalf("streams block:\n%s\nwant prefix:\n%s", out, want)
	}
}

// failTransport counts every HTTP request and refuses it.
type failTransport struct{ n atomic.Int64 }

func (f *failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.n.Add(1)
	return nil, errors.New("the table tick made an HTTP call")
}

// TestTableTickMakesNoRestCall (DONE-WHEN of #3530): a tick of the whole
// table is Redis only. With every HTTP request refused and counted, ten
// ticks over the fixture make zero HTTP calls (no GitHub REST, no webhook
// poll), and every command they send is a read of the keyspace (plus the
// writer's lock refresh when a lock is named): no KEYS, no SCAN, no write.
func TestTableTickMakesNoRestCall(t *testing.T) {
	ft := &failTransport{}
	saved := http.DefaultTransport
	http.DefaultTransport = ft
	t.Cleanup(func() { http.DefaultTransport = saved })
	client, _, log := sprintStore(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	cfg := table.SprintFixtureConfig()
	cfg.LockKey, cfg.LockToken = "lock:nova-sprint-table", "t"
	r := table.NewSprintReader(client, cfg)
	for i := 0; i < 10; i++ {
		snap, err := r.Read(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && snap.RoundTrips != 1 {
			t.Fatalf("tick %d took %d round trips, want 1", i, snap.RoundTrips)
		}
		_ = snap.Render(now)
	}
	if n := ft.n.Load(); n != 0 {
		t.Fatalf("ten ticks made %d HTTP calls, want 0", n)
	}
	allowed := map[string]bool{"ZRANGE": true, "ZCARD": true, "SMEMBERS": true, "EXISTS": true, "XRANGE": true, "HGETALL": true, "HGET": true, "HMGET": true, "ZCOUNT": true, "EVAL": true, "EVALSHA": true, "EVAL_RO": true}
	names, _ := log.reset()
	if len(names) == 0 {
		t.Fatal("no commands logged")
	}
	for _, n := range names {
		if !allowed[n] {
			t.Fatalf("the tick sent %s (only keyspace reads and the lock refresh are allowed): %v", n, names)
		}
	}
}
