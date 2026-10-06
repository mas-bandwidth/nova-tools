package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The seat (seat.go): NotSeat's authority rule and MoveSeat's plan. Both read
// only their arguments and a Snapshot's Coordinator and Now, so these tests
// hand MoveSeat a Snapshot built by hand: no store, no clock, no subprocess.

func TestSeatCoverNotSeat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		holder string
		r      SeatReq
		want   string
	}{
		{"the holder gives it", "coord", SeatReq{To: "someone", Who: "coord", Reason: "handover at the hour", Owner: "owner"}, ""},
		{"the owner gives it", "coord", SeatReq{To: "someone", Who: "owner", Reason: "the holder is away", Owner: "owner"}, ""},
		{"a take with the owner's name", "coord", SeatReq{To: "someone", Who: "someone", Reason: "the holder is out of credits", Take: true, ApprovedBy: "owner", Owner: "owner"}, ""},
		{"the name is no id", "coord", SeatReq{To: "some one", Who: "coord", Reason: "x"}, "a name wants letters, digits, _ and -: some one"},
		{"an empty name shows as a dash", "coord", SeatReq{To: "", Who: "coord", Reason: "x"}, "a name wants letters, digits, _ and -: -"},
		{"a reason is wanted", "coord", SeatReq{To: "someone", Who: "coord", Reason: "   "}, "a seat change wants --reason <text>: it is recorded in the log"},
		{"no coordinator to move", "", SeatReq{To: "someone", Who: "someone", Reason: "x"}, "the sprint has no coordinator; run: nova-sprint init --coordinator <name>"},
		{"the holder already holds it", "coord", SeatReq{To: "coord", Who: "coord", Reason: "x"}, "coord holds the seat already; nothing was changed"},
		{"approved-by without take", "coord", SeatReq{To: "someone", Who: "coord", Reason: "x", ApprovedBy: "owner"}, "--approved-by goes with --take; the holder or the owner gives the seat without it"},
		{"a stranger gives, no owner named", "coord", SeatReq{To: "someone", Who: "someone", Reason: "x"}, "the seat is the holder's to give: coord, not someone; to take it: nova-sprint coordinator someone --take --approved-by <owner> --reason <text>; nothing was changed"},
		{"a stranger gives, the owner named", "coord", SeatReq{To: "someone", Who: "other", Reason: "x", Owner: "owner"}, "the seat is the holder's to give: coord, not other (or the owner's: owner); to take it: nova-sprint coordinator someone --take --approved-by <owner> --reason <text>; nothing was changed"},
		{"a take names no approver", "coord", SeatReq{To: "someone", Who: "someone", Reason: "x", Take: true, Owner: "owner"}, "--take wants --approved-by <owner>: a seat is taken only with the owner's name in the record; nothing was changed"},
		{"a take where the sprint names no owner", "coord", SeatReq{To: "someone", Who: "someone", Reason: "x", Take: true, ApprovedBy: "owner"}, "the sprint names no owner, whose name a take carries: init --owner <name>, or NOVA_SPRINT_OWNER; nothing was changed"},
		{"a take approved by not the owner", "coord", SeatReq{To: "someone", Who: "someone", Reason: "x", Take: true, ApprovedBy: "other", Owner: "owner"}, "the sprint's owner is owner, not other; nothing was changed"},
		{"a take by not the one taking it", "coord", SeatReq{To: "someone", Who: "other", Reason: "x", Take: true, ApprovedBy: "owner", Owner: "owner"}, "a seat is taken by the one taking it: someone, not other; nothing was changed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, NotSeat(c.holder, c.r))
		})
	}
}

func TestSeatCoverMoveSeat(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		holder string
		r      SeatReq
		notes  []Note
		seat   *SeatChange
		want   []Refusal
	}{
		{"a given seat: a happened note to the new holder, the change planned, the reason trimmed",
			"coord", SeatReq{To: "someone", Who: "coord", Reason: " handover at the hour "},
			[]Note{{Kind: Happened, Type: NSeat, At: now, Who: "coord", To: "someone",
				What: "coord -> someone: handover at the hour, by coord"}},
			&SeatChange{Holder: "someone", Generation: 2, From: "coord", At: now, By: "coord", Reason: "handover at the hour"},
			nil},
		{"a taken seat: the note is addressed to the holder it was taken from",
			"coord", SeatReq{To: "someone", Who: "someone", Reason: "the holder is out of credits", Take: true, ApprovedBy: "owner", Owner: "owner"},
			[]Note{{Kind: Happened, Type: NSeatTaken, At: now, Who: "someone", To: "coord",
				What: "coord -> someone, approved by owner: the holder is out of credits",
				Hint: "the seat is someone's now; your coordinator verbs are refused"}},
			&SeatChange{Holder: "someone", Generation: 2, From: "coord", At: now, By: "someone", Taken: true, ApprovedBy: "owner", Reason: "the holder is out of credits"},
			nil},
		{"the refusal moves nothing: the reason refused, no note, no change",
			"coord", SeatReq{To: "coord", Who: "coord", Reason: "x"},
			nil, nil,
			[]Refusal{{Key: "coord", Why: "coord holds the seat already; nothing was changed"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := MoveSeat(&Snapshot{Now: now, Coordinator: c.holder}, c.r)
			assert.Equal(t, c.notes, p.Notes)
			assert.Equal(t, c.seat, p.Seat)
			assert.Equal(t, c.want, p.Refused)
		})
	}
}

// A move decided at another generation than the seat's is stale: refused by
// StaleSeat, and by MoveSeat on the generation its step read, with no change
// planned; a move that names none, or the seat's, is not
// (handover-is-a-restart-checkpoint.w4).
func TestSeatCoverStaleSeat(t *testing.T) {
	t.Parallel()
	give := SeatReq{To: "someone", Who: "owner", Reason: "x", Owner: "owner"}
	assert.Empty(t, StaleSeat(3, give), "a move that names no generation")
	give.Generation = 3
	assert.Empty(t, StaleSeat(3, give), "a move decided at the seat's generation")
	give.Generation = 1
	assert.Empty(t, StaleSeat(0, give), "a seat with no record is at the first generation")
	give.Generation = 2
	want := "the seat is at generation 3, not 2: it moved after this was decided; read nova-sprint handover again, and decide at generation 3; nothing was changed"
	assert.Equal(t, want, StaleSeat(3, give))
	p := MoveSeat(&Snapshot{Coordinator: "coord", SeatGeneration: 3, Now: time.Unix(0, 0)}, give)
	assert.Nil(t, p.Seat, "a stale move planned a change")
	assert.Equal(t, []Refusal{{Key: "someone", Why: want}}, p.Refused)
}
