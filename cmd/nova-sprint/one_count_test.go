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
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// The one-count fixture: one open sprint, three streams, a card in every
// state (a parked and a cancelled card among them, neither counted) and the
// three stream sentinels waiting, which are counted like any card. Totals:
// 14 cards in the six sets, 3 landed.
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
	headerRE  = regexp.MustCompile(`(\d+)/(\d+) done (\d+)%, left (\d+), eta ([0-9:]+ ET(?: \+\d+d)?|\?|done)`)
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

// oneCountPrintouts renders the four printouts of the store at now and
// returns their numbers: sprint status, the table's headline (the one-shot
// `table --layout live`), the live loop's headline (a primed reader, the
// path loopTable publishes) and ws counts; plus the stream block's total row
// cells and ws counts' cells.
func oneCountPrintouts(t *testing.T, addr string, client redis.UniversalClient, now time.Time) (map[string]oneCountNumbers, [6]int) {
	t.Helper()
	countsNow = func() time.Time { return now }
	got := map[string]oneCountNumbers{}

	code, out, errOut := runSprint("sprint", "status", "--redis", addr)
	if code != 0 || !strings.HasPrefix(out, oneCountSprint+" open ") || strings.Count(out, "\n") != 1 {
		t.Fatalf("sprint status: exit %d %q %q", code, out, errOut)
	}
	got["sprint status"] = parseNumbers(t, "sprint status", headerRE, out)

	code, tableOut, errOut := runSprint("table", "--layout", "live", "--redis", addr)
	if code != 0 {
		t.Fatalf("table: exit %d %q", code, errOut)
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
	if _, err := r.Read(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	snap, err := r.Read(context.Background(), now) // the loop's steady tick
	if err != nil || snap.RoundTrips != 1 {
		t.Fatalf("live tick: %v, %d round trips", err, snap.RoundTrips)
	}
	got["live header"] = parseNumbers(t, "live header", headerRE, snap.Render(now))

	code, out, errOut = runSprint("ws", "counts", "--redis", addr)
	if code != 0 || strings.Count(out, "\n") != 1 {
		t.Fatalf("ws counts: exit %d %q %q", code, out, errOut)
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
// the sentinels counted and the parked and cancelled cards not; changing one
// card's state moves all of them together.
func TestOneCountEveryPrintoutSameNumbers(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	t.Setenv("NOVA_SPRINT", "")
	saved := countsNow
	t.Cleanup(func() { countsNow = saved })
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
	// a landing two hours ago is outside the ETA's hour
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
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	// 14 cards: alpha 6 + sentinel, beta 3 + sentinel, gamma 2 + sentinel;
	// 3 landed in the hour, so 11 left at 3 an hour is 3h40m after 13:32 EDT.
	got, cells := oneCountPrintouts(t, mr.Addr(), client, now)
	assertOneCount(t, got, oneCountNumbers{done: 3, total: 14, pct: 21, left: 11, eta: "17:12 ET"})
	if cells != [6]int{5, 2, 2, 1, 1, 3} {
		t.Fatalf("total row %v; want waiting 5 (2 cards, 3 sentinels) ready 2 working 2 review 1 merging 1 landed 3", cells)
	}

	// Mutation: g2 lands. Every printout moves together.
	pipe = client.Pipeline()
	pipe.ZRem(ctx, ws.Key("gamma", ws.Working), "g2")
	pipe.ZAdd(ctx, ws.Key("gamma", ws.Landed), redis.Z{Score: 112, Member: "g2"})
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "ws:log", ID: fmt.Sprintf("%d-0", now.Add(-time.Minute).UnixMilli()), Values: []any{"id", "g2", "from", "working", "to", "landed"}})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got, cells = oneCountPrintouts(t, mr.Addr(), client, now)
	// 10 left at 4 an hour: 2h30m after 13:32 EDT.
	assertOneCount(t, got, oneCountNumbers{done: 4, total: 14, pct: 28, left: 10, eta: "16:02 ET"})
	if cells != [6]int{5, 2, 1, 1, 1, 4} {
		t.Fatalf("total row after the move %v", cells)
	}
}
