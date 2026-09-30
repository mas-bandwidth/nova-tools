package sprint

import (
	"fmt"
	"testing"
)

// resolveWorld is a sprint of n primaries in each of three streams: s1's
// ready, s2's and s3's all waiting on s1's last, with open judgments on other
// cards beside them: what the tick's resolve walks every tick.
func resolveWorld(t testing.TB, n, open int) *Snapshot {
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: n}))
	last := fmt.Sprintf("s1-%d", n)
	w.must(Add(w.s, AddReq{Stream: "s2", Count: n, Needs: []string{last}}))
	w.must(Add(w.s, AddReq{Stream: "s3", Count: n, Needs: []string{last}}))
	for i := 0; i < open; i++ {
		id := fmt.Sprintf("s1-%d", i%n+1)
		o := judgment(NBlocked, "s1", t0, 0, id)
		o.ID = fmt.Sprintf("j%d", i)
		w.s.Open = append(w.s.Open, Open{Key: OpenKey(o.ID, id), Note: o})
	}
	return w.s
}

// The tick's resolve over many waiting primaries and many open judgments
// moves none that waits, and finds each waiting card's stop by the line's
// index of its sentinels: a sentinel added in front of s2's line is the stop
// of every card behind it, and of none before it.
func TestTheTicksResolveOverAManyCardSprint(t *testing.T) {
	t.Parallel()
	s := resolveWorld(t, 300, 300)
	if p, _ := TickResolve(s, TickReq{}); len(p.Units) != 0 {
		t.Fatalf("resolve moved %d: every waiting primary waits on s1-300", len(p.Units))
	}
	line, stops := s.Work.lineStops("s2")
	if len(line) != 300 || stops[len(stops)-1] != -1 {
		t.Fatalf("s2's line: %d cards, last stop %d; want 300 and none", len(line), stops[len(stops)-1])
	}
	gate := &Card{ID: "s2-gate", Row: "s2", Col: string(Waiting), Score: (line[99].Score + line[100].Score) / 2, Fields: map[string]string{"kind": Sentinel}}
	s.Work.Put(gate)
	for i, c := range []*Card{line[99], line[100], line[299]} {
		st := StopBefore(s, "s2", c.Score, nil)
		if want := i > 0; (st != nil) != want || want && st.ID != gate.ID {
			t.Fatalf("%s: stop %v, want the gate %v", c.ID, st, want)
		}
	}
	if st := StopBefore(s, "s2", line[299].Score, map[string]bool{gate.ID: true}); st != nil {
		t.Fatalf("a gate being landed is no stop: %v", st)
	}
}

// BenchmarkTickResolve is the tick's resolve at two sizes: go test -bench
// TickResolve ./internal/sprint. Linear: four times the cards and judgments
// take about four times as long (the walks it replaced took sixteen).
func BenchmarkTickResolve(b *testing.B) {
	for _, n := range []int{250, 1000} {
		b.Run(fmt.Sprintf("waiting=%d", 2*n), func(b *testing.B) {
			s := resolveWorld(b, n, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				TickResolve(s, TickReq{})
			}
		})
	}
}
