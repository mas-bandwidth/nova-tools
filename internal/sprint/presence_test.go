package sprint

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
)

var p0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

// TestABeatKeepsTheHighestLoadOfItsWindow: the load is the highest of the
// beats within LoadWindow; a beat in the same second replaces that second's;
// the same beat twice in one second is the same record.
func TestABeatKeepsTheHighestLoadOfItsWindow(t *testing.T) {
	t.Parallel()
	var b Beat
	for i, pct := range []float64{10, 30.04, 20} {
		b = NextBeat(b, p0.Add(time.Duration(i)*time.Second+300*time.Millisecond), pct, hostload.HowCPU, hostload.State{})
	}
	if !b.At.Equal(p0.Add(2*time.Second)) || b.Load != 30 || len(b.Samples) != 3 {
		t.Fatalf("after three beats: %+v, want at the second, load 30.0 of three samples", b)
	}
	again := NextBeat(b, p0.Add(2*time.Second+900*time.Millisecond), 20, hostload.HowCPU, hostload.State{})
	if len(again.Samples) != 3 || again.Load != b.Load || !again.At.Equal(b.At) {
		t.Fatalf("the same beat in the same second: %+v, want the record unchanged", again)
	}
	late := NextBeat(b, p0.Add(LoadWindow+time.Second), 5, hostload.HowCPU, hostload.State{})
	if late.Load != 20 || len(late.Samples) != 2 {
		t.Fatalf("a beat past the window of the first two: %+v, want load 20 of two samples", late)
	}
	later := NextBeat(late, p0.Add(3*LoadWindow), 5, hostload.HowLoad1, hostload.State{})
	if later.Load != 5 || len(later.Samples) != 1 || later.How != hostload.HowLoad1 {
		t.Fatalf("a beat after a silence: %+v, want its own load alone", later)
	}
}

// TestStatusIsDerivedFromTheBeat: never beaten is down with no load; a beat
// is up until BeatDeadline has passed and down after; a hold is held
// whatever the beat.
func TestStatusIsDerivedFromTheBeat(t *testing.T) {
	t.Parallel()
	ctl := &Card{Fields: map[string]string{"status": Up}}
	if s, l := MemberStatus(ctl, Beat{}, p0), LoadText(Beat{}, p0); s != Down || l != "" {
		t.Fatalf("never beaten: %s %q, want down and no load", s, l)
	}
	b := NextBeat(Beat{}, p0, 12.34, hostload.HowCPU, hostload.State{})
	if s, l := MemberStatus(ctl, b, p0.Add(BeatDeadline)), LoadText(b, p0.Add(BeatDeadline)); s != Up || l != "12.3%" {
		t.Fatalf("at the deadline: %s %q, want up 12.3%%", s, l)
	}
	if s, l := MemberStatus(ctl, b, p0.Add(BeatDeadline+time.Second)), LoadText(b, p0.Add(BeatDeadline+time.Second)); s != Down || l != "" {
		t.Fatalf("past the deadline: %s %q, want down and no load", s, l)
	}
	held := &Card{Fields: map[string]string{"status": Down, "held": "2030-01-02T03:04:05Z"}}
	if s := MemberStatus(held, b, p0); s != Held {
		t.Fatalf("held while beating: %s, want held", s)
	}
}

// TestPresenceWithoutBeatsDoesNothing: a tick given no beats (a caller that
// read none) moves no member.
func TestPresenceWithoutBeatsDoesNothing(t *testing.T) {
	t.Parallel()
	if p, _ := TickPresence(&Snapshot{}, TickReq{}); !p.Empty() {
		t.Fatalf("no beats: %+v", p)
	}
}

// TestPresenceBatchesDowns: when multiple members fall silent, all of them
// go down in one plan (R2), each with its happened notification and status
// set to down, rather than one member per tick.
func TestPresenceBatchesDowns(t *testing.T) {
	t.Parallel()
	members := []string{"m1", "m2", "m3", "m4", "m5"}
	s := &Snapshot{Now: p0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Fleet.SetRows(members)
	for _, m := range members {
		s.Fleet.Put(&Card{ID: CtlID(m), Row: m, Col: Ctl, Rev: 1, Fields: map[string]string{"kind": "member", "status": Up}})
	}
	// All members have no beat (or expired beat).
	p, due := TickPresence(s, TickReq{Beats: map[string]Beat{}})
	if due != 0 {
		t.Fatalf("due %d, want 0: all members handled in one plan", due)
	}
	if len(p.Units) != len(members) {
		t.Fatalf("units %d, want %d", len(p.Units), len(members))
	}
	for i, m := range members {
		u := p.Units[i]
		if u.Key != CtlID(m) {
			t.Fatalf("unit %d key %s, want %s", i, u.Key, CtlID(m))
		}
		if len(u.Notes) != 1 || u.Notes[0].Type != NMemberDown {
			t.Fatalf("unit %d notes %v, want 1 %s note", i, u.Notes, NMemberDown)
		}
	}
}

// TestPresenceBatchesDownsBoundedByTickMaxMoves: more than TickMaxMoves
// members falling silent plans TickMaxMoves downs in the first tick and
// leaves the rest due for the next tick.
func TestPresenceBatchesDownsBoundedByTickMaxMoves(t *testing.T) {
	t.Parallel()
	total := TickMaxMoves + 10
	members := make([]string, total)
	for i := range members {
		members[i] = "m" + itoa(i)
	}
	s := &Snapshot{Now: p0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Fleet.SetRows(members)
	for _, m := range members {
		s.Fleet.Put(&Card{ID: CtlID(m), Row: m, Col: Ctl, Rev: 1, Fields: map[string]string{"kind": "member", "status": Up}})
	}
	p, due := TickPresence(s, TickReq{Beats: map[string]Beat{}})
	if due != 10 {
		t.Fatalf("due %d, want 10", due)
	}
	if len(p.Units) != TickMaxMoves {
		t.Fatalf("units %d, want %d", len(p.Units), TickMaxMoves)
	}
}

// TestPresenceBatchesUpsAndLevellingMovesNotCounted: members coming up are
// batched up to TickMaxMoves, and ready queue levelling moves are included in
// the plan without being counted against or bounded by TickMaxMoves.
func TestPresenceBatchesUpsAndLevellingMovesNotCounted(t *testing.T) {
	t.Parallel()
	members := []string{"m1", "m2", "m3"}
	s := &Snapshot{Now: p0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Fleet.SetRows(members)
	// m1 is already up and has 2 ready cards; m2 and m3 are down and will come up
	s.Fleet.Put(&Card{ID: CtlID("m1"), Row: "m1", Col: Ctl, Rev: 1, Fields: map[string]string{"kind": "member", "status": Up}})
	s.Fleet.Put(&Card{ID: CtlID("m2"), Row: "m2", Col: Ctl, Rev: 1, Fields: map[string]string{"kind": "member", "status": Down}})
	s.Fleet.Put(&Card{ID: CtlID("m3"), Row: "m3", Col: Ctl, Rev: 1, Fields: map[string]string{"kind": "member", "status": Down}})
	s.Fleet.Put(&Card{ID: "c1", Row: "m1", Col: Ready, Rev: 1, Fields: map[string]string{"stream": "s1", "gen": "1"}})
	s.Fleet.Put(&Card{ID: "c2", Row: "m1", Col: Ready, Rev: 1, Fields: map[string]string{"stream": "s1", "gen": "1"}})

	beats := map[string]Beat{
		"m1": NextBeat(Beat{}, p0, 10, hostload.HowCPU, hostload.State{}),
		"m2": NextBeat(Beat{}, p0, 10, hostload.HowCPU, hostload.State{}),
		"m3": NextBeat(Beat{}, p0, 10, hostload.HowCPU, hostload.State{}),
	}
	p, due := TickPresence(s, TickReq{Beats: beats})
	if due != 0 {
		t.Fatalf("due %d, want 0", due)
	}
	// m2 and m3 both come up in the same plan
	upsFound := map[string]bool{}
	var levelMoves int
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			if ch.Table == Fleet {
				if ch.Entry.ID == CtlID("m2") || ch.Entry.ID == CtlID("m3") {
					upsFound[ch.Entry.ID] = true
				}
				if ch.Entry.Move != nil {
					levelMoves++
				}
			}
		}
	}
	if !upsFound[CtlID("m2")] || !upsFound[CtlID("m3")] {
		t.Fatalf("m2 and m3 up entries not found: %+v", p.Units)
	}
	if levelMoves == 0 {
		t.Fatalf("expected levelling moves when new members came up: %+v", p.Units)
	}
}
