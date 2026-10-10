//go:build functional

package store

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// The read paths of where, card and the tick on a 2,000-card sprint, with the
// server's own CPU seconds per operation (INFO cpu): what a verb costs the
// store, not only the caller. Run with
//
//	go test -tags functional -run XXX -bench ReadCost -benchtime 5x ./internal/sprint/store/
func liveStoreB(b *testing.B) (*Store, *redis.Client) {
	b.Helper()
	addr := testredis.Start(b)
	c := redis.NewClient(&redis.Options{Addr: addr})
	b.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	require.NoError(b, fn.Load(ctx, c))
	names := sprint.Names{Prefix: "f-"}
	st := &Store{B: &Redis{C: c, Names: names, Now: time.Now}, Names: names, Actor: "bench"}
	require.NoError(b, st.Init(ctx))
	require.NoError(b, st.B.RowsAdd(ctx, names.Table(sprint.Readers), []string{"reader-a", "reader-b", "reader-c"}))
	return st, c
}

func benchSprint(b *testing.B, streams, perStream int) (*Store, *redis.Client) {
	st, c := liveStoreB(b)
	ctx := context.Background()
	require.NoError(b, st.BeatReaders(ctx))
	for _, m := range []string{"m1", "m2"} {
		res, err := st.Run(ctx, FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: 64}))
		require.NoError(b, err)
		require.Empty(b, res.Refused)
	}
	for i := 0; i < streams; i++ {
		res, err := st.Run(ctx, AddStep(sprint.AddReq{Brief: benchBrief(), Stream: fmt.Sprintf("s%d", i+1), Count: perStream, Held: os.Getenv("BENCH_HELD") == "1"}))
		require.NoError(b, err)
		require.Empty(b, res.Refused)
	}
	return st, c
}

func cpuSeconds(b *testing.B, c *redis.Client) float64 {
	info, err := c.Info(context.Background(), "cpu").Result()
	require.NoError(b, err)
	total := 0.0
	for _, line := range strings.Split(info, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && (k == "used_cpu_sys" || k == "used_cpu_user") {
			f, _ := strconv.ParseFloat(v, 64)
			total += f
		}
	}
	return total
}

func benchOp(b *testing.B, c *redis.Client, name string, op func()) {
	b.Run(name, func(b *testing.B) {
		before := cpuSeconds(b, c)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			op()
		}
		b.StopTimer()
		b.ReportMetric((cpuSeconds(b, c)-before)/float64(b.N)*1000, "server-ms/op")
	})
}

func BenchmarkReadCost(b *testing.B) {
	streams, _ := strconv.Atoi(os.Getenv("BENCH_STREAMS"))
	if streams <= 0 {
		streams = 10
	}
	st, c := benchSprint(b, streams, 2000/streams)
	ctx := context.Background()
	must := func(err error) { require.NoError(b, err) }
	names := make([]string, len(sprint.ViewOrder))
	for i, t := range sprint.ViewOrder {
		names[i] = st.Names.Table(t)
	}
	benchOp(b, c, "shapes5", func() { _, err := st.B.Shapes(ctx, names); must(err) })
	benchOp(b, c, "readset1", func() { _, err := st.readSet(ctx, st.Names.Table(sprint.Work), []string{st.sid("s1-1")}); must(err) })
	benchOp(b, c, "loadWork", func() { _, err := st.Load(ctx, []string{sprint.Work}, nil); must(err) })
	benchOp(b, c, "loadAll", func() { _, err := st.Load(ctx, All, nil); must(err) })
	benchOp(b, c, "heldBack", func() { _, err := st.HeldBack(ctx); must(err) })
	benchOp(b, c, "landedAt", func() { _, err := st.LandedAt(ctx); must(err) })
	benchOp(b, c, "whereFacts-norecord", func() { _, err := st.WhereFacts(ctx, 0); must(err) })
	benchOp(b, c, "streamClocks", func() { _, err := st.StreamClocks(ctx); must(err) })
	benchOp(b, c, "cardOf", func() { _, err := st.CardOf(ctx, "s1-1"); must(err) })
	benchOp(b, c, "held", func() { _, err := st.Held(ctx, "s1-1"); must(err) })
	benchOp(b, c, "log", func() { _, err := st.Log(ctx); must(err) })
	pinned, err := st.pin(ctx)
	must(err)
	tw := pinned.twin()
	benchOp(b, c, "twinRead-cold", func() {
		tw.drop("bench")
		_, _, err := pinned.twinRead(ctx, tw, All, tickExtras, nil)
		must(err)
	})
	benchOp(b, c, "twinRead-warm", func() {
		_, _, err := pinned.twinRead(ctx, tw, All, tickExtras, nil)
		must(err)
	})
}

// benchBrief is a pro brief of BENCH_BRIEF_KB kilobytes (a real brief is
// kilobytes of prose; the read set returns every field of every member).
func benchBrief() string {
	kb, _ := strconv.Atoi(os.Getenv("BENCH_BRIEF_KB"))
	if kb <= 0 {
		return proBrief
	}
	return proBrief + "\n" + strings.Repeat("the brief's prose, one line of it, as a real brief carries it\n", kb*1024/62)
}

// BenchmarkCardRead is one card read, the store reads of `card <id> --json`
// (card-read-speed.w2, card-read-speedb.w1): its records, its needs, its place
// in line and its hold from one read of the tables (CardHeld), and its lines of
// the log from the card log index (CardLog), each alone and the two together
// (card, the whole of what the verb reads), beside Held alone and the whole log
// (the read before the index), with the store round trips (trips/op) and the
// whole-table reads (tables/op) each makes. The twin is the size of the
// 2026-10-04 11:36 PM store: 1,980 cards in ten streams, and a log of 240,119
// lines of about 1,350 bytes, one card (s1-2) with 9,756 of them as that
// store's busiest card had, the rest spread over the cards (the median card had
// 4, the 99th percentile 104). The log is indexed as the ticks index it before
// the reads. Run with
//
//	go test -tags functional -run XXX -bench CardRead -benchtime 20x ./internal/sprint/store/
func BenchmarkCardRead(b *testing.B) {
	st, c := benchSprint(b, 10, 198)
	ctx := context.Background()
	must := func(err error) { require.NoError(b, err) }
	pinned, err := st.pin(ctx)
	must(err)
	have, err := pinned.Log(ctx)
	must(err)
	words := strings.Repeat("the report's words, as a finish carries them whole; ", 24)
	const lines, hot = 240119, 9756
	var pad []sprint.Line
	flush := func() {
		if len(pad) == 0 {
			return
		}
		f, err := pinned.B.ReadFence(ctx)
		must(err)
		op := OpRecord{ID: fmt.Sprintf("pad-%d", len(pad)), Verb: "pad", At: time.Now(), Log: pad}
		ok, err := pinned.B.Acquire(ctx, f.Gen, op)
		must(err)
		require.True(b, ok)
		must(pinned.B.Release(ctx, op, true))
		pad = nil
	}
	for i := len(have); i < lines; i++ {
		id := "s1-2"
		if i >= hot {
			id = fmt.Sprintf("s%d-%d", i%10+1, i/10%198+1)
		}
		pad = append(pad, sprint.Line{Kind: sprint.LineMove, At: time.Now(), Op: "pad", Card: id + ".w1", Primary: id,
			Table: sprint.Fleet, From: "m1:working", To: "m1:working", Text: map[string]string{"report": words}})
		if len(pad) == 5000 {
			flush()
		}
	}
	flush()
	for {
		before, err := pinned.B.(logIndex).logIndexed(ctx)
		must(err)
		must(pinned.keepWhere(ctx, Machine{}))
		after, err := pinned.B.(logIndex).logIndexed(ctx)
		must(err)
		if after == before {
			break
		}
	}
	r := pinned.B.(*Redis)
	r.CountTrips()
	for _, id := range []string{"s1-1", "s1-2"} {
		read := func(name string, op func()) {
			b.Run(id+"/"+name, func(b *testing.B) {
				trips, tables := r.Trips(), st.stats().reads.Load()
				for b.Loop() {
					op()
				}
				b.ReportMetric(float64(r.Trips()-trips)/float64(b.N), "trips/op")
				b.ReportMetric(float64(st.stats().reads.Load()-tables)/float64(b.N), "tables/op")
			})
		}
		read("cardheld", func() { _, err := st.CardHeld(ctx, id); must(err) })
		read("log", func() { _, err := st.CardLog(ctx, id); must(err) })
		read("held", func() { _, err := st.Held(ctx, id); must(err) })
		read("card", func() {
			_, err := st.CardHeld(ctx, id)
			must(err)
			_, err = st.CardLog(ctx, id)
			must(err)
		})
		read("wholelog", func() { _, err := st.Log(ctx); must(err) })
	}
	_ = c
}
