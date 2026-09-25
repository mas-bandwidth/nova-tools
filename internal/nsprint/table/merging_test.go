package table_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

const mergeStream = "nova-sprint + merge + bus"

// mergingSeed is one stream with 12 cards the lander would take and 7 it
// would not, each for a different reason, plus cfg:land min_score 8.
func mergingSeed(now time.Time) [][]string {
	cmds := [][]string{
		{"ZADD", "ws:order", "1", mergeStream},
		{"HSET", "cfg:land", "min_score", "8"},
	}
	at := now.Add(-time.Hour).UnixMilli()
	card := func(id, pr string) {
		at++
		cmds = append(cmds, []string{"ZADD", "ws:" + mergeStream + ":merging", strconv.FormatInt(at, 10), id})
		if pr != "" {
			cmds = append(cmds, []string{"HSET", "task:" + id, "stream", mergeStream, "state", "merging", "pr", pr})
		} else {
			cmds = append(cmds, []string{"HSET", "task:" + id, "stream", mergeStream, "state", "merging"})
		}
	}
	record := func(n int, head, state, stream, reads string) {
		cmds = append(cmds, []string{"HSET", fmt.Sprintf("pr:nova-tools:%d", n), "repo", "mas-bandwidth/nova-tools", "n", strconv.Itoa(n),
			"head", head, "state", state, "stream", stream, "reads", reads})
	}
	head := func(n int) string { return fmt.Sprintf("%07x0000000000000000000000000000000000", n)[:40] }
	score := func(who string, n, s int) string {
		return fmt.Sprintf("SCORE who=%s head=%s score=%d/10 gates=ci:ok,base:ok,scope:ok", who, head(n)[:8], s)
	}
	// Twelve read: a SCORE at head >= 8 and no hold, in every spelling of
	// the card's pr field the lander reads.
	for n := 1; n <= 12; n++ {
		pr := strconv.Itoa(3000 + n)
		switch n % 4 {
		case 1:
			pr = "#" + pr
		case 2:
			pr = "nova-tools#" + pr
		case 3:
			pr = "https://forge.invalid/mas-bandwidth/nova-tools/pull/" + pr // a pulls URL: the path names the PR
		}
		card(fmt.Sprintf("read-%d", n), pr)
		record(3000+n, head(3000+n), "open", mergeStream, score("emma", 3000+n, 8+n%3))
	}
	// Seven unread, one reason each.
	card("no-pr", "")
	card("no-record", "3101")
	card("no-read", "3102")
	record(3102, head(3102), "open", mergeStream, "")
	card("low-score", "3103")
	record(3103, head(3103), "open", mergeStream, score("emma", 3103, 7))
	card("stale-head", "3104")
	record(3104, head(3104), "open", mergeStream, fmt.Sprintf("SCORE who=emma head=abcdef12 score=10/10 gates=ci:ok,base:ok,scope:ok"))
	card("held", "3105")
	record(3105, head(3105), "open", mergeStream, score("emma", 3105, 10)+"\n"+fmt.Sprintf("HOLD who=stella head=%s", head(3105)[:8]))
	card("jev-only", "3106")
	record(3106, head(3106), "open", mergeStream, score("jev", 3106, 10))
	return cmds
}

// landingSeed is one open landing of 12 members, 6 minutes old, whose stream
// PR's ci is pending, and a merged one that prints nothing.
func landingSeed(now time.Time) [][]string {
	const repo = "mas-bandwidth/nova-tools"
	var members []string
	for n := 1; n <= 12; n++ {
		members = append(members, fmt.Sprintf("%d@%040x", 3000+n, n))
	}
	at := strconv.FormatInt(now.Add(-6*time.Minute).UnixMilli(), 10)
	return [][]string{
		{"SADD", "land:" + repo + ":streams", "nova-sprint-merge-bus", "old"},
		{"HSET", "land:" + repo + ":nova-sprint-merge-bus", "slug", "nova-sprint-merge-bus", "streams", mergeStream,
			"state", "open", "head", "0123456789abcdef0123456789abcdef01234567", "members", strings.Join(members, " "),
			"pr", "3990", "at", at},
		{"HSET", "pr:nova-tools:3990", "head", "0123456789abcdef0123456789abcdef01234567", "state", "open", "ci", "pending"},
		{"HSET", "land:" + repo + ":old", "slug", "old", "streams", "old", "state", "merged", "head", "ffffffff", "members", "1@ff", "pr", "3900", "at", at},
	}
}

// TestMergingCellShowsReadUnreadAndLandLines (DONE-WHEN of #3900): a stream
// with 12 read and 7 unread cards in merging prints 12/7, and an open landing
// prints LAND stream=<s> members=12 head=<sha8> ci=pending age=6m; a merged
// landing prints nothing. The steady tick is still one pipeline with no KEYS
// and no SCAN.
func TestMergingCellShowsReadUnreadAndLandLines(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	now := table.SprintFixtureNow()
	seedCommands(t, client, append(mergingSeed(now), landingSeed(now)...))
	log := &cmdLog{}
	client.AddHook(log)
	r := table.NewSprintReader(client, table.SprintConfig{Friends: []string{"rowan"}})
	if _, err := r.Read(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	log.reset()
	snap, err := r.Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	names, trips := log.reset()
	if snap.RoundTrips != 1 || trips != 1 {
		t.Fatalf("steady tick: RoundTrips=%d, client round trips=%d; want 1 and 1", snap.RoundTrips, trips)
	}
	for _, n := range names {
		if n == "KEYS" || n == "SCAN" {
			t.Fatalf("the tick sent %s: %v", n, names)
		}
	}
	if read, unread, ok := snap.ReadSplit(snap.Streams[0]); !ok || read != 12 || unread != 7 {
		t.Fatalf("ReadSplit = %d/%d ok=%v, want 12/7", read, unread, ok)
	}
	out := snap.Render(now)
	row := fmt.Sprintf("%-30s | %7d | %5d | %7d | %6d | %7d | %7s | %6d\n", mergeStream, 0, 0, 0, 0, 0, "12/7", 0)
	if !strings.Contains(out, row) {
		t.Fatalf("no merging row %q in\n%s", row, out)
	}
	if !strings.Contains(out, "\ntotal                          |       0 |     0 |       0 |      0 |       0 |    12/7 |      0\n") {
		t.Fatalf("total row does not carry 12/7:\n%s", out)
	}
	land := `LAND stream="nova-sprint + merge + bus" members=12 head=01234567 ci=pending age=6m` + "\n"
	if !strings.Contains(out, land) {
		t.Fatalf("no line %q in\n%s", land, out)
	}
	if n := strings.Count(out, "LAND "); n != 1 {
		t.Fatalf("%d LAND lines, want 1 (the merged landing prints nothing):\n%s", n, out)
	}
	// The reading column is always there (#3929); with no reading set it is
	// 0 and the split still comes from the records.
	if snap.ReadSource != table.ReadFromRecords {
		t.Fatalf("no reading set yet, but ReadSource = %q, want %q", snap.ReadSource, table.ReadFromRecords)
	}
}

// TestReadingSetIsTheSameSplitsOtherSource (#3900 x #3929): once a
// ws:<s>:reading set holds cards, ReadSplit answers from the sets (reading =
// unread, merging = read, merging no longer <read>/<unread>), and keeps that
// source on a later tick when the set is empty again.
func TestReadingSetIsTheSameSplitsOtherSource(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	now := table.SprintFixtureNow()
	seedCommands(t, client, mergingSeed(now))
	seedCommands(t, client, [][]string{
		{"ZADD", "ws:" + mergeStream + ":reading", "1", "r1", "2", "r2", "3", "r3"},
		{"ZADD", "ws:" + mergeStream + ":waiting", "1", "w1"},
	})
	r := table.NewSprintReader(client, table.SprintConfig{Friends: []string{"rowan"}})
	snap, err := r.Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ReadSource != table.ReadFromSet {
		t.Fatalf("ReadSource = %q, want %q", snap.ReadSource, table.ReadFromSet)
	}
	if read, unread, ok := snap.ReadSplit(snap.Streams[0]); !ok || read != 19 || unread != 3 {
		t.Fatalf("ReadSplit = %d/%d ok=%v, want 19 read (merging) / 3 unread (reading)", read, unread, ok)
	}
	out := snap.Render(now)
	for _, want := range []string{
		"stream                         | waiting | ready | working | review | reading | merging | landed\n",
		fmt.Sprintf("%-30s | %7d | %5d | %7d | %6d | %7d | %7d | %6d\n", mergeStream, 1, 0, 0, 0, 3, 19, 0),
		"23/23 left, 0% done",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %q in\n%s", want, out)
		}
	}
	mr.Del("ws:" + mergeStream + ":reading")
	snap, err = r.Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if out := snap.Render(now); !strings.Contains(out, "| reading | merging |") || snap.ReadSource != table.ReadFromSet {
		t.Fatalf("the split went back to the records with an empty set (ReadSource %q):\n%s", snap.ReadSource, out)
	}
}

// TestLandLineShapes: no stream PR yet prints ci=-, a stopped landing names
// its state, and the age reads in seconds, minutes, then hours.
func TestLandLineShapes(t *testing.T) {
	now := table.SprintFixtureNow()
	for _, c := range []struct {
		row  table.LandRow
		want string
	}{
		{table.LandRow{Streams: "harvest", State: "conflict", Head: "abc", Members: 3, At: now.Add(-45 * time.Second).UnixMilli()},
			"LAND stream=harvest members=3 head=abc ci=- age=45s state=conflict"},
		{table.LandRow{Streams: "a,b", State: "open", Head: "0123456789", PR: "7", CI: "red", Members: 2, At: now.Add(-125 * time.Minute).UnixMilli()},
			"LAND stream=a,b members=2 head=01234567 ci=red age=2h05m"},
		{table.LandRow{Slug: "s", State: "pushed", Members: 1},
			"LAND stream=s members=1 head=- ci=- age=- state=pushed"},
	} {
		if got := c.row.LandLine(now); got != c.want {
			t.Fatalf("LandLine = %q, want %q", got, c.want)
		}
	}
}
