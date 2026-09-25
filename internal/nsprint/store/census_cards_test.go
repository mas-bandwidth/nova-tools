package store_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

const censusCards = 1042

// seedCards writes censusCards card hashes spread over every card index state,
// the shapes the nova_sprint library writes: cut_at in seconds, the rest in
// milliseconds, bench and attempt only once dealt, route only when cut with one.
func seedCards(t *testing.T, client *redis.Client, sprint string, nowMS int64) map[string]string {
	t.Helper()
	ctx := context.Background()
	want := map[string]string{}
	seed := client.Pipeline()
	for i := 0; i < censusCards; i++ {
		label := fmt.Sprintf("c%04d", i)
		state := store.CardStates[i%len(store.CardStates)]
		fields := []any{"label", label, "state", state, "cut_at", fmt.Sprint(nowMS/1000 - 3600)}
		bench, route, attempt, age := "-", "-", "-", "1h0m0s"
		if state != "queued" {
			bench, attempt = fmt.Sprintf("bench-%d", i%7), fmt.Sprint(1+i%3)
			fields = append(fields, "bench", bench, "attempt", attempt, "dealt_at", fmt.Sprint(nowMS-120_000))
			age = "2m0s"
		}
		if state == "running" {
			fields = append(fields, "launched_at", fmt.Sprint(nowMS-90_000), "beat_at", fmt.Sprint(nowMS-5_000))
			age = "5s"
		}
		if i%2 == 0 {
			route = []string{"pro", "flash", "dsflash"}[i%3]
			fields = append(fields, "route", route)
		}
		seed.HSet(ctx, "s:"+sprint+":card:"+label, fields...)
		seed.SAdd(ctx, "s:"+sprint+":idx:card:"+state, label)
		want[label] = strings.Join([]string{label, state, bench, route, attempt, age}, "\t")
	}
	if _, err := seed.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return want
}

// TestCensusSprintCardsOneRoundTrip is the DONE-WHEN of the card census
// (nova-tools #3037, sprint 2026-09-24): 1,042 cards in a throwaway Redis,
// read by `census --sprint <S>` over the sprint's named card index sets, come
// back as TSV rows with exactly one pipeline flush on the connection (the
// counter's control is TestCensusOnePipeline's three separate HGETs), and the
// CENSUS line prints round_trips=1 and the ms. The one-second budget is the
// logged ms, not an assertion: internal/ci refuses an elapsed-time bound in a
// test (CI-WAITS kind=elapsed); the event asserted is the single flush.
//
// The Redis is testutil's throwaway redis-server, not miniredis: miniredis
// v2.39.0 implements neither SORT nor FCALL, and without one of them a set's
// members and their hash fields cannot come back in one round trip.
func TestCensusSprintCardsOneRoundTrip(t *testing.T) {
	addr := startRedis(t)
	client, flushes := countedClient(t, addr)
	ctx := context.Background()
	now, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	const sprint = "census-1042"
	want := seedCards(t, client, sprint, now.UnixMilli())
	st := store.New(client)

	var out bytes.Buffer
	flushes.reset()
	start := time.Now()
	sum, err := store.RunCardCensus(ctx, st, store.CardCensusRequest{Sprint: sprint}, &out)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if got := flushes.count(); got != 1 {
		t.Fatalf("census --sprint used %d flushes; want exactly 1 for all %d cards", got, censusCards)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if lines[0] != store.CardCensusHeader {
		t.Fatalf("header = %q", lines[0])
	}
	summary := lines[len(lines)-1]
	t.Logf("census --sprint %s: %d cards, %s; %s", sprint, censusCards, elapsed, summary)
	rows := lines[1 : len(lines)-1]
	if len(rows) != censusCards {
		t.Fatalf("census printed %d rows; want %d", len(rows), censusCards)
	}
	lastIndex := -1
	for _, row := range rows {
		label := strings.SplitN(row, "\t", 2)[0]
		if !sameCardRow(row, want[label]) {
			t.Fatalf("row %q; want %q", row, want[label])
		}
		idx := indexOf(store.CardStates, strings.Split(row, "\t")[1])
		if idx < lastIndex {
			t.Fatalf("rows are not in index order at %q", row)
		}
		lastIndex = idx
	}
	wantPrefix := fmt.Sprintf("CENSUS sprint=%s sets=%d cards=%d missing=0 dup=0 drift=0 round_trips=1 ms=", sprint, len(store.CardStates), censusCards)
	if !strings.HasPrefix(summary, wantPrefix) {
		t.Fatalf("summary = %q; want prefix %q", summary, wantPrefix)
	}
	if sum.RoundTrips != 1 || sum.Cards != censusCards {
		t.Fatalf("summary struct = %+v", sum)
	}

	t.Run("missing hash, dup, drift and --keys", func(t *testing.T) {
		pipe := client.Pipeline()
		pipe.SAdd(ctx, "s:"+sprint+":idx:card:running", "zz-gone")                                                   // no hash
		pipe.SAdd(ctx, "s:"+sprint+":idx:card:dealt", "c0003")                                                       // c0003 is running too
		pipe.HSet(ctx, "s:"+sprint+":card:c0015", "state", "ended")                                                  // c0015 sits in running
		pipe.Del(ctx, "s:"+sprint+":card:c0027")                                                                     // indexed, hash deleted
		pipe.HDel(ctx, "s:"+sprint+":card:c0039", "bench", "attempt", "dealt_at", "route", "launched_at", "beat_at") // running, fields gone
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		flushes.reset()
		sum, err := store.RunCardCensus(ctx, st, store.CardCensusRequest{Sprint: sprint, States: []string{"dealt", "running"}}, &out)
		if err != nil {
			t.Fatal(err)
		}
		if got := flushes.count(); got != 1 {
			t.Fatalf("census --keys dealt,running used %d flushes; want 1", got)
		}
		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		byLabel := map[string]string{}
		for _, row := range lines[1 : len(lines)-1] {
			if strings.TrimSpace(row) == "" {
				t.Fatalf("census printed a blank row")
			}
			byLabel[strings.SplitN(row, "\t", 2)[0]] = row
		}
		for label, row := range map[string]string{
			"zz-gone": "zz-gone\tMISSING\t-\t-\t-\t-",
			"c0027":   "c0027\tMISSING\t-\t-\t-\t-",
			"c0003":   want["c0003"], // printed once, under dealt
			"c0015":   strings.Replace(want["c0015"], "\trunning\t", "\tended\t", 1),
			"c0039":   "c0039\trunning\t-\t-\t-\t1h0m0s",
		} {
			if !sameCardRow(byLabel[label], row) {
				t.Fatalf("row for %s = %q; want %q", label, byLabel[label], row)
			}
		}
		// The seeded dealt and running cards, plus zz-gone; c0003 in both prints once (dup).
		seeded := 0
		for i := 0; i < censusCards; i++ {
			if st := store.CardStates[i%len(store.CardStates)]; st == "dealt" || st == "running" {
				seeded++
			}
		}
		wantSum := fmt.Sprintf("CENSUS sprint=%s sets=2 cards=%d missing=2 dup=1 drift=2 round_trips=1 ms=", sprint, seeded+1)
		if summary := lines[len(lines)-1]; !strings.HasPrefix(summary, wantSum) {
			t.Fatalf("summary = %q; want prefix %q (%+v)", summary, wantSum, sum)
		}
	})
}

// sameCardRow compares two TSV rows exactly, except age, which may differ by
// the second the seed's cut_at was truncated to plus the census's own latency.
func sameCardRow(got, want string) bool {
	g, w := strings.Split(got, "\t"), strings.Split(want, "\t")
	if len(g) != 6 || len(w) != 6 || strings.Join(g[:5], "\t") != strings.Join(w[:5], "\t") {
		return false
	}
	if g[5] == "-" || w[5] == "-" {
		return g[5] == w[5]
	}
	ga, err1 := time.ParseDuration(g[5])
	wa, err2 := time.ParseDuration(w[5])
	return err1 == nil && err2 == nil && ga >= wa-time.Second && ga <= wa+2*time.Second
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func TestCardCensusRefusals(t *testing.T) {
	ctx := context.Background()
	st := store.New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}))
	defer st.Close()
	for name, req := range map[string]store.CardCensusRequest{
		"no sprint":      {},
		"sprint pattern": {Sprint: "s*"},
		"key pattern":    {Sprint: "s1", States: []string{"run*"}},
		"blank key":      {Sprint: "s1", States: []string{"queued", ""}},
		"twice":          {Sprint: "s1", States: []string{"queued", "queued"}},
	} {
		var out bytes.Buffer
		if _, err := store.RunCardCensus(ctx, st, req, &out); err == nil {
			t.Errorf("%s: census ran; want a refusal", name)
		}
		if out.Len() != 0 {
			t.Errorf("%s: refusal printed %q", name, out.String())
		}
	}
}
