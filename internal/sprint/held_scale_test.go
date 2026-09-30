package sprint

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

func benchSnapshot100k() (*Snapshot, string) {
	s := &Snapshot{
		Now:     time.Now(),
		Work:    NewTable(Work),
		Fleet:   NewTable(Fleet),
		Merge:   NewTable(Merge),
		Readers: NewTable(Readers),
	}
	streams := make([]string, 10)
	for i := range 10 {
		streams[i] = "s" + strconv.Itoa(i)
	}
	s.Work.SetRows(streams)
	s.Fleet.SetRows([]string{"m1", "m2"})
	s.Fleet.Put(&Card{ID: "m1", Row: "m1", Col: Up, Fields: map[string]string{"kind": "member"}})
	s.Fleet.Put(&Card{ID: "m2", Row: "m2", Col: Up, Fields: map[string]string{"kind": "member"}})

	var targetID string
	for i := range 100000 {
		st := streams[i%10]
		id := fmt.Sprintf("%s-%d", st, i/10)
		if i == 50000 {
			targetID = id
		}
		s.Work.Put(&Card{
			ID:     id,
			Row:    st,
			Col:    Ready,
			Score:  float64(i / 10),
			Fields: map[string]string{"kind": "primary"},
		})
	}
	return s, targetID
}

func BenchmarkDealTurn100k(b *testing.B) {
	s, targetID := benchSnapshot100k()
	h := HeldState{Snap: s, Running: true}
	c := newHeld(h, s.Now)
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = c.dealTurn(targetID)
	}
}

func BenchmarkHolder100k(b *testing.B) {
	s, targetID := benchSnapshot100k()
	h := HeldState{Snap: s, Running: true}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_ = Holder(h, s.Now, targetID)
	}
}

func BenchmarkTickDeal100k(b *testing.B) {
	s, _ := benchSnapshot100k()
	req := TickReq{Who: MachineActor}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, _ = TickDeal(s, req)
	}
}

