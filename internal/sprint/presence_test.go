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
