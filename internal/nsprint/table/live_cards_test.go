package table_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// withCardViews adds, for every bench row the #2674 keyspace holds, the card
// views bench:<b>:cards:<cell> (nova-tools#3692) with as many members as the
// cell the bash of record prints from the row's own hash: its ready (queue,
// or dealer_queue while dealer_at is at most 30 s older than now), working,
// done, ok and fail. The bash never reads these sets and Go reads only them,
// so the goldens (the bash's bytes) still hold and Go prints them from sets.
func withCardViews(cmds [][]string, now time.Time) [][]string {
	rows := map[string]map[string]string{}
	var order []string
	for _, cmd := range cmds {
		switch cmd[0] {
		case "DEL":
			for _, k := range cmd[1:] {
				delete(rows, k)
			}
		case "HSET":
			k := cmd[1]
			if !strings.HasPrefix(k, "bench:") || strings.Contains(k[len("bench:"):], ":") || k == "bench:pool" {
				continue
			}
			if rows[k] == nil {
				rows[k] = map[string]string{}
				order = append(order, k)
			}
			for i := 2; i+1 < len(cmd); i += 2 {
				rows[k][cmd[i]] = cmd[i+1]
			}
		}
	}
	out := append([][]string{}, cmds...)
	for _, k := range order {
		f := rows[k]
		if f == nil {
			continue
		}
		ready := f["queue"]
		if at, err := time.Parse("2006-01-02T15:04:05Z", f["dealer_at"]); err == nil && f["dealer_queue"] != "" {
			if age := now.Unix() - at.Unix(); age >= 0 && age <= 30 {
				ready = f["dealer_queue"]
			}
		}
		for cell, v := range map[string]string{"ready": ready, "working": f["working"], "done": f["done"], "ok": f["ok"], "fail": f["fail"]} {
			n, _ := strconv.Atoi(v)
			if n <= 0 {
				continue
			}
			z := []string{"ZADD", k + ":cards:" + cell}
			for i := 0; i < n; i++ {
				z = append(z, strconv.Itoa(i), fmt.Sprintf("s:fixture:card:%s-%s-%d", k[len("bench:"):], cell, i))
			}
			out = append(out, z)
		}
	}
	return out
}

// TestHostRowReadsCardViews (#3692): the host row's ready, working, done, ok
// and fail are the ZCARDs of bench:<b>:cards:*, never the bash bench-row's
// fields; a bench with no card views prints zeros, not the hash's counts.
func TestHostRowReadsCardViews(t *testing.T) {
	t.Parallel()

	now := table.Fixture2674Now()
	at := now.Add(-1 * time.Second).UTC().Format("2006-01-02T15:04:05Z")
	client := liveStore(t, [][]string{
		{"HSET", "bench:alpha", "host", "alpha", "at", at, "queue", "70", "working", "71", "done", "72", "ok", "73", "fail", "74", "load1", "1.00", "dealer_queue", "9", "dealer_at", at},
		{"ZADD", "bench:alpha:cards:ready", "1", "s:x:card:r1", "2", "s:x:card:r2"},
		{"ZADD", "bench:alpha:cards:working", "1", "s:x:card:w1"},
		{"ZADD", "bench:alpha:cards:done", "1", "s:x:card:d1", "2", "s:x:card:d2", "3", "s:x:card:d3"},
		{"ZADD", "bench:alpha:cards:ok", "1", "s:x:card:d1"},
		{"ZADD", "bench:alpha:cards:fail", "2", "s:x:card:d2", "3", "s:x:card:d3"},
		{"HSET", "bench:beta", "host", "beta", "at", at, "queue", "5", "working", "5", "done", "5", "ok", "5", "fail", "0"},
	})
	snap, err := table.ReadLive(context.Background(), client, table.LiveConfig{Friends: []string{"rowan"}, Sprint: "x"})
	if err != nil {
		t.Fatal(err)
	}
	out := snap.RenderLive(now)
	for _, want := range []string{
		"alpha      |     2 |       1 |     3 |     1 |     2 |  33% |   1.00\n",
		"beta       |     0 |       0 |     0 |     0 |     0 |   0% |      -\n",
		"total      |     2 |       1 |     3 |     1 |     2 |  33% |\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("table lacks %q\n%s", want, out)
		}
	}
}

// TestHostRowPrintsUnreadCountsAsQuestionMarks (#3695 hold 7 item 3): a card
// view whose ZCARD errors (here WRONGTYPE) prints "?" in its cell, its ok%
// and its column total, never a false 0.
func TestHostRowPrintsUnreadCountsAsQuestionMarks(t *testing.T) {
	t.Parallel()

	now := table.Fixture2674Now()
	at := now.Add(-1 * time.Second).UTC().Format("2006-01-02T15:04:05Z")
	client := liveStore(t, [][]string{
		{"HSET", "bench:alpha", "host", "alpha", "at", at, "load1", "1.00"},
		{"ZADD", "bench:alpha:cards:ready", "1", "s:x:card:r1"},
		{"SET", "bench:alpha:cards:working", "not-a-zset"},
		{"ZADD", "bench:alpha:cards:done", "1", "s:x:card:d1"},
		{"ZADD", "bench:alpha:cards:ok", "1", "s:x:card:d1"},
		{"SET", "bench:alpha:cards:fail", "not-a-zset"},
		{"HSET", "bench:beta", "host", "beta", "at", at},
		{"ZADD", "bench:beta:cards:working", "1", "s:x:card:w9"},
	})
	snap, err := table.ReadLive(context.Background(), client, table.LiveConfig{Friends: []string{"rowan"}, Sprint: "x"})
	if err != nil {
		t.Fatal(err)
	}
	out := snap.RenderLive(now)
	for _, want := range []string{
		"alpha      |     1 |       ? |     1 |     1 |     ? | 100% |   1.00\n",
		"beta       |     0 |       1 |     0 |     0 |     0 |   0% |      -\n",
		"total      |     1 |       ? |     1 |     1 |     ? | 100% |\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("table lacks %q\n%s", want, out)
		}
	}
}
