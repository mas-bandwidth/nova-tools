package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// The one-count fixture: one open sprint, three streams, a card in every
// state (a parked and a cancelled card among them, neither counted) and the
// three stream sentinels waiting, which are the streams' stops, not work,
// and counted nowhere (the coordinator's ruling on Glenn's "zeros everywhere
// after sprint clear"). Totals: 11 cards in the six sets, 3 landed.
const oneCountSprint = "one-count"

type oneCountCard struct{ id, stream, where string }

var oneCountCards = []oneCountCard{
	{"a1", "alpha", ws.Waiting}, {"a2", "alpha", ws.Ready}, {"a3", "alpha", ws.Working},
	{"a4", "alpha", ws.Review}, {"a5", "alpha", ws.Merging}, {"a6", "alpha", ws.Landed},
	{"a7", "alpha", ws.Parked}, {"a8", "alpha", ws.Done}, // parked, and cancelled (done/fail)
	{"b1", "beta: two", ws.Waiting}, {"b2", "beta: two", ws.Landed}, {"b3", "beta: two", ws.Landed},
	{"g1", "gamma", ws.Ready}, {"g2", "gamma", ws.Working},
}

var oneCountStreams = []string{"alpha", "beta: two", "gamma"}

// oneCountNumbers are the numbers one printout carries.
type oneCountNumbers struct {
	done, total, pct, left int
	eta                    string
}

var (
	headerRE  = regexp.MustCompile(`(\d+)/(\d+) done (\d+)%, left (\d+), eta ([0-9:]+ ET(?: \+\d+d)?|\?|-|done)`)
	receiptRE = regexp.MustCompile(`done=(\d+)/(\d+) pct=(\d+) left=(\d+) eta="([^"]*)"`)
	totalRE   = regexp.MustCompile(`(?m)^total +\| +(\d+) \| +(\d+) \| +(\d+) \| +(\d+) \| +(\d+) \| +(\d+)$`)
)

func parseNumbers(t *testing.T, what string, re *regexp.Regexp, text string) oneCountNumbers {
	t.Helper()
	m := re.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("%s: no progress numbers in %q", what, text)
	}
	n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
	return oneCountNumbers{done: n(1), total: n(2), pct: n(3), left: n(4), eta: m[5]}
}

// oneCountPrintouts renders the four printouts of the store at now through
// the functions their verbs call, and returns their numbers: sprint status
// (sprint.StatusLines), the table's headline (tableOnce, the one-shot
// `table --layout live`), the live loop's headline (a primed reader's
// steady tick, the path loopTable publishes) and ws counts (wsCountsLine);
// plus the stream block's total row, whose cells must be ws counts' cells.
func oneCountPrintouts(t *testing.T, client *redis.Client, now time.Time) (map[string]oneCountNumbers, [6]int) {
	t.Helper()
	ctx := context.Background()
	got := map[string]oneCountNumbers{}

	lines, err := sprint.StatusLines(ctx, store.New(client), "", now)
	if err != nil || len(lines) != 1 || !strings.HasPrefix(lines[0], oneCountSprint+" open ") {
		t.Fatalf("sprint status: %q %v", lines, err)
	}
	got["sprint status"] = parseNumbers(t, "sprint status", headerRE, lines[0])

	tableOut, err := tableOnce(ctx, client, table.SprintConfig{}, now)
	if err != nil {
		t.Fatalf("table: %v", err)
	}
	got["table header"] = parseNumbers(t, "table header", headerRE, strings.SplitN(tableOut, "\n", 4)[2])
	m := totalRE.FindStringSubmatch(tableOut)
	if m == nil {
		t.Fatalf("table: no stream total row:\n%s", tableOut)
	}
	var cells [6]int
	sum := 0
	for i := range cells {
		cells[i], _ = strconv.Atoi(m[i+1])
		sum += cells[i]
	}
	got["table total row"] = oneCountNumbers{done: cells[5], total: sum, pct: cells[5] * 100 / sum, left: sum - cells[5], eta: got["table header"].eta}

	r := table.NewSprintReader(client, table.SprintConfig{})
	if _, err := r.Read(ctx, now); err != nil {
		t.Fatal(err)
	}
	snap, err := r.Read(ctx, now) // the loop's steady tick
	if err != nil || snap.RoundTrips != 1 {
		t.Fatalf("live tick: %v, %d round trips", err, snap.RoundTrips)
	}
	got["live header"] = parseNumbers(t, "live header", headerRE, snap.Render(now))

	out, err := wsCountsLine(ctx, client, now)
	if err != nil {
		t.Fatalf("ws counts: %v", err)
	}
	got["ws counts"] = parseNumbers(t, "ws counts", receiptRE, out)
	var wsCells [6]int
	for i, state := range ws.Stream {
		f := regexp.MustCompile(` ` + state + `=(\d+) `).FindStringSubmatch(out)
		if f == nil {
			t.Fatalf("ws counts: no %s= in %q", state, out)
		}
		wsCells[i], _ = strconv.Atoi(f[1])
	}
	if wsCells != cells {
		t.Fatalf("ws counts cells %v, table total row %v", wsCells, cells)
	}
	return got, cells
}

// assertOneCount: every printout carries want.
func assertOneCount(t *testing.T, got map[string]oneCountNumbers, want oneCountNumbers) {
	t.Helper()
	for what, n := range got {
		if n != want {
			t.Errorf("%s printed %+v; want %+v (every printout the same numbers)", what, n, want)
		}
	}
}

// TestOneCountEveryPrintoutSameNumbers (#one-count): sprint status, the table
// headline, the live loop's headline, the table's total row and ws counts
// print the same done/total, pct, left and eta from one store at one instant,
// the sentinels, the parked and the cancelled cards not counted (a sentinel's
// landing in ws:log is not in the eta's rate either); changing one card's
// state moves all of them together.
func TestOneCountEveryPrintoutSameNumbers(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 17, 32, 0, 0, time.UTC) // 13:32 EDT

	pipe := client.Pipeline()
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "closed-one"}, redis.Z{Score: 2, Member: oneCountSprint})
	pipe.HSet(ctx, "s:closed-one", "status", "closed")
	pipe.HSet(ctx, "s:"+oneCountSprint, "status", "open")
	for i, s := range oneCountStreams {
		pipe.ZAdd(ctx, "ws:order", redis.Z{Score: float64(i + 1), Member: s})
		pipe.ZAdd(ctx, ws.Key(s, ws.Waiting), redis.Z{Score: 1, Member: ws.SentinelID(s)})
	}
	// a landing two hours ago is outside the ETA's hour; a sentinel's
	// landing inside it is a stop, not a card landed, and not in the rate
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "ws:log", ID: fmt.Sprintf("%d-0", now.Add(-2*time.Hour).UnixMilli()), Values: []any{"id", "old", "to", "landed"}})
	landedAt := 0
	for i, c := range oneCountCards {
		pipe.ZAdd(ctx, ws.Key(c.stream, c.where), redis.Z{Score: float64(100 + i), Member: c.id})
		if c.where == ws.Landed {
			landedAt++
			at := now.Add(-time.Duration(40-10*landedAt) * time.Minute).UnixMilli()
			pipe.XAdd(ctx, &redis.XAddArgs{Stream: "ws:log", ID: fmt.Sprintf("%d-0", at), Values: []any{"id", c.id, "from", "merging", "to", "landed"}})
		}
	}
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "ws:log", ID: fmt.Sprintf("%d-0", now.Add(-5*time.Minute).UnixMilli()),
		Values: []any{"id", ws.SentinelID("delta"), "stream", "delta", "from", "waiting", "to", "landed"}})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	// 11 cards: alpha 6, beta 3, gamma 2, the three sentinels aside; 3
	// landed in the hour (the sentinel's landing aside), so 8 left at 3 an
	// hour is 2h40m after 13:32 EDT.
	got, cells := oneCountPrintouts(t, client, now)
	assertOneCount(t, got, oneCountNumbers{done: 3, total: 11, pct: 27, left: 8, eta: "16:12 ET"})
	if cells != [6]int{2, 2, 2, 1, 1, 3} {
		t.Fatalf("total row %v; want waiting 2 (the 3 sentinels aside) ready 2 working 2 review 1 merging 1 landed 3", cells)
	}

	// Mutation: g2 lands. Every printout moves together.
	pipe = client.Pipeline()
	pipe.ZRem(ctx, ws.Key("gamma", ws.Working), "g2")
	pipe.ZAdd(ctx, ws.Key("gamma", ws.Landed), redis.Z{Score: 112, Member: "g2"})
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "ws:log", ID: fmt.Sprintf("%d-0", now.Add(-time.Minute).UnixMilli()), Values: []any{"id", "g2", "from", "working", "to", "landed"}})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got, cells = oneCountPrintouts(t, client, now)
	// 7 left at 4 an hour: 1h45m after 13:32 EDT.
	assertOneCount(t, got, oneCountNumbers{done: 4, total: 11, pct: 36, left: 7, eta: "15:17 ET"})
	if cells != [6]int{2, 2, 1, 1, 1, 4} {
		t.Fatalf("total row after the move %v", cells)
	}
}

// TestXYFileRetired (#4411): the live layout's progress line is the one
// count, so --xy-file (sprint-xy's SPRINT-XY.txt) is refused with the remedy,
// on --layout live and on --compare alike, before Redis is dialed.
func TestXYFileRetired(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"table", "--layout", "live", "--once", "--redis", "127.0.0.1:1", "--xy-file", "SPRINT-XY.txt"},
		{"table", "--compare", "t.txt", "--redis", "127.0.0.1:1", "--sprint", "s", "--friends", "a", "--xy-file", "SPRINT-XY.txt"},
	} {
		code, _, stderr := runSprint(args...)
		if code != 2 || !strings.Contains(stderr, xyFileRetired) {
			t.Fatalf("%v: exit %d stderr %q; want 2 and %q", args, code, stderr, xyFileRetired)
		}
	}
}

// TestTableNotOpenRefusal (#4411): the live table's refusal of a sprint that
// is not the open one names where the name came from and the remedy that
// drops it (--sprint, or the NOVA_SPRINT environment), and passes any other
// error through untouched.
func TestTableNotOpenRefusal(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		err   error
		named string
		want  string
	}{
		{&ws.NotOpen{Name: "other", Open: "s1"}, "--sprint",
			`REFUSED table --layout live --sprint other: not the open sprint; open=s1 remedy="nova-sprint table --layout live"` + "\n"},
		{fmt.Errorf("read: %w", &ws.NotOpen{Name: "old"}), "NOVA_SPRINT",
			`REFUSED table --layout live NOVA_SPRINT=old: not the open sprint; open=- remedy="unset NOVA_SPRINT"` + "\n"},
	} {
		var out strings.Builder
		if code, ok := tableNotOpen(c.err, c.named, &out); !ok || code != 1 || out.String() != c.want {
			t.Fatalf("%v: %d %v %q; want 1 %q", c.err, code, ok, out.String(), c.want)
		}
	}
	var out strings.Builder
	if _, ok := tableNotOpen(fmt.Errorf("pipeline: EOF"), "--sprint", &out); ok || out.Len() != 0 {
		t.Fatalf("another error refused as not open: %q", out.String())
	}
}
