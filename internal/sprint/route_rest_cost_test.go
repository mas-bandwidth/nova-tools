package sprint

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// restWorld is the cold read's scale (PR 5179): one member of width 8, two flash routes,
// 1000 flash primaries added (16 dealt, 984 ready the tick does not deal) and 3000 work
// cards done on the routes beside them: every tick's held rule asks noRoute of each
// ready primary.
func restWorld(t testing.TB) *Snapshot {
	w := newWorld(t, "reader-a", "reader-b")
	for _, name := range []string{"flash-a", "flash-b"} {
		w.s.Routes = append(w.s.Routes, Route{Name: name, Tier: "flash", Provider: "p", Model: name, Enabled: true})
	}
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 8}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 1000, Brief: "c: the work tier: flash\nThe task.\n"}))
	p, _ := TickDeal(w.s, TickReq{})
	w.do(p)
	for i := range 3000 {
		w.s.Fleet.Put(&Card{ID: fmt.Sprintf("old-%d.w1", i), Row: "m1", Col: DoneOK, Fields: map[string]string{
			"ok": "yes", FieldRoute: []string{"flash-a", "flash-b"}[i%2], "finished": stamp(t0)}})
	}
	return w.s
}

// The tick's cost at that scale is a number in the gate (the sub-second tick): every part
// of a tick that draws routes or asks why a card is not dealt settles the resting routes
// with one scan of the fleet table, and each draw after it reads a map. The scans are
// counted, with no clock: the branch the cold read measured scanned per ready primary
// (1,969 scans, 3.82 s a TickCheck); settled, the check part scans once, the deal once.
func TestTheTicksCheckSettlesTheRestsOnceAtScale(t *testing.T) {
	t.Parallel()
	s := restWorld(t)
	require.Len(t, s.Work.Column(Ready), 984, "984 ready primaries the tick does not deal")
	const scansPerPart = 1
	for _, part := range []struct {
		name string
		fn   TickPartFn
	}{{"check", TickCheck}, {"deal", TickDeal}} {
		n := 0
		s.restScans = &n
		part.fn(s, TickReq{})
		t.Logf("TICK-COST part=%s ready=984 fleet=3016 rest_scans=%d gate=%d", part.name, n, scansPerPart)
		require.LessOrEqual(t, n, scansPerPart, "the %s part scans the fleet table for rests per card", part.name)
		require.Positive(t, n, "the %s part settles the rests it reads", part.name)
	}
}

// BenchmarkTickCheckWithRests is the check part's wall at that scale: go test -bench
// TickCheckWithRests ./internal/sprint (about 10 ms settled once; seconds with a scan per card).
func BenchmarkTickCheckWithRests(b *testing.B) {
	s := restWorld(b)
	b.ResetTimer()
	for range b.N {
		TickCheck(s, TickReq{})
	}
}
