package sprint

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.True(t, b.At.Equal(p0.Add(2*time.Second)), "after three beats: %+v, want at the second, load 30.0 of three samples", b)
	require.Equal(t, 30.0, b.Load, "after three beats: %+v, want at the second, load 30.0 of three samples", b)
	require.Len(t, b.Samples, 3, "after three beats: %+v, want at the second, load 30.0 of three samples", b)
	again := NextBeat(b, p0.Add(2*time.Second+900*time.Millisecond), 20, hostload.HowCPU, hostload.State{})
	require.Len(t, again.Samples, 3, "the same beat in the same second: %+v, want the record unchanged", again)
	require.Equal(t, b.Load, again.Load, "the same beat in the same second: %+v, want the record unchanged", again)
	require.True(t, again.At.Equal(b.At), "the same beat in the same second: %+v, want the record unchanged", again)
	late := NextBeat(b, p0.Add(LoadWindow+time.Second), 5, hostload.HowCPU, hostload.State{})
	require.Equal(t, 20.0, late.Load, "a beat past the window of the first two: %+v, want load 20 of two samples", late)
	require.Len(t, late.Samples, 2, "a beat past the window of the first two: %+v, want load 20 of two samples", late)
	later := NextBeat(late, p0.Add(3*LoadWindow), 5, hostload.HowLoad1, hostload.State{})
	require.Equal(t, 5.0, later.Load, "a beat after a silence: %+v, want its own load alone", later)
	require.Len(t, later.Samples, 1, "a beat after a silence: %+v, want its own load alone", later)
	require.Equal(t, hostload.HowLoad1, later.How, "a beat after a silence: %+v, want its own load alone", later)
}

// TestStatusIsDerivedFromTheBeat: never beaten is down with no load; a beat
// is up until MissedBeatsDown beat windows have passed and down after; a hold is held
// whatever the beat.
func TestStatusIsDerivedFromTheBeat(t *testing.T) {
	t.Parallel()
	ctl := &Card{Fields: map[string]string{"status": Up}}
	s, l := MemberStatus(ctl, Beat{}, p0), LoadText(Beat{}, p0)
	require.Equal(t, Down, s, "never beaten: %s %q, want down and no load", s, l)
	require.Empty(t, l, "never beaten: %s %q, want down and no load", s, l)
	b := NextBeat(Beat{}, p0, 12.34, hostload.HowCPU, hostload.State{})
	s, l = MemberStatus(ctl, b, p0.Add(BeatDeadline)), LoadText(b, p0.Add(BeatDeadline))
	require.Equal(t, Up, s, "at the deadline: %s %q, want up 12.3%%", s, l)
	require.Equal(t, "12.3%", l, "at the deadline: %s %q, want up 12.3%%", s, l)
	past := p0.Add(BeatDeadline + time.Second)
	assert.Equal(t, [2]string{Up, ""}, [2]string{MemberStatus(ctl, b, past), LoadText(b, past)}, "past the deadline: up with one missed beat, no load")
	gone := p0.Add(MissedBeatsDown*BeatDeadline + time.Second)
	assert.Equal(t, [2]string{Down, ""}, [2]string{MemberStatus(ctl, b, gone), LoadText(b, gone)}, "past the missed beats: down with no load")
	held := &Card{Fields: map[string]string{"status": Down, "held": "2030-01-02T03:04:05Z"}}
	s = MemberStatus(held, b, p0)
	require.Equal(t, Held, s, "held while beating: %s, want held", s)
}

// TestPresenceWithoutBeatsDoesNothing: a tick given no beats (a caller that
// read none) moves no member.
func TestPresenceWithoutBeatsDoesNothing(t *testing.T) {
	t.Parallel()
	p, _ := TickPresence(&Snapshot{}, TickReq{})
	require.True(t, p.Empty(), "no beats: %+v", p)
}
