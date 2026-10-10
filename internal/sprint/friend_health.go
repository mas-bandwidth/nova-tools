package sprint

import (
	"fmt"
	"time"
)

// A friend's health as the coordinator observes it (docs/SPEC-SPRINT.md
// section 1, "A friend's health"; the model is tla/SeatHealth.tla). The
// coordinator's daemon keeps a keepalive with every friend's daemon and
// writes what it saw with `friend health`: the state word, the time of the
// proof it rests on, and the seat generation it read the seat at. The sprint
// server is the authority and the table: it fences the observation on the
// seat (the holder and the generation, read in the step's own snapshot, so a
// seat that moved refuses the write), refuses a proof older than the one the
// row holds, and derives the table's word from the row at every read; it
// never validates the keepalive's nonces, which are the daemon's.

// FirstSeatGeneration is the generation of a seat no change has moved: the
// first init's coordinator holds it, with no seat record; the first accepted
// change takes 2, and so on. Nothing resets it but teardown.
const FirstSeatGeneration uint64 = 1

// DaemonPong is the observation word for a pong her daemon answered and her
// session did not. It is the daemon's, kept on the row for it, and shown as
// down: the table's words are up, held and down (the owner, 2026-10-04
// 11:42 AM ET: "anything but up is down"; "sleeping = down").
const DaemonPong = "asleep"

// HealthStates are the words an observation carries; up alone shows as up.
var HealthStates = []string{Up, DaemonPong, Down}

// FriendHealth is the coordinator's last accepted observation of a friend,
// as the friend-health record keeps it: the state word, the time of the proof
// it rests on (a session pong for up, a daemon pong for DaemonPong, the judgment
// for down; the daemon's clock), the seat generation it was observed under,
// and what the pong said of her queue, working and width.
type FriendHealth struct {
	State      string    `json:"state"`
	Seen       time.Time `json:"seen"`
	Generation uint64    `json:"generation"`
	Queue      int       `json:"queue,omitempty"`
	Working    int       `json:"working,omitempty"`
	Width      int       `json:"width,omitempty"`
	// Reason and Until are the daemon's word on a friend not up (her model
	// allowance ran out) and when it expects her back; shown on her row.
	Reason string    `json:"reason,omitempty"`
	Until  time.Time `json:"until,omitzero"`
}

// Observed says the coordinator has observed the friend at least once.
func (h FriendHealth) Observed() bool { return h.State != "" }

// HealthReq is one observation: who sends it (the seat's holder), the friend,
// the observation, and the row as it was read before the step (Prev; Known
// says the roster has her).
type HealthReq struct {
	Friend, Who string
	Obs         FriendHealth
	Prev        FriendHealth
	Known       bool
}

// Replays says the observation is the row's again: the same proof under the
// same seat with the same word. It is answered as recorded and writes
// nothing, so a proof never earns a second ten seconds.
func (r HealthReq) Replays() bool {
	return r.Known && r.Prev.Observed() && r.Obs.State == r.Prev.State && r.Obs.Seen.Equal(r.Prev.Seen) && r.Obs.Generation == r.Prev.Generation
}

// NotHealth is why the observation is refused, "" is accepted: the sender is
// the seat's holder, at the seat's generation, of a friend on the table, with
// a proof dated no later than now, the server's clock, and newer than the row's.
// It reads nothing but its arguments, so the step refuses on its own read of
// the seat and its own clock.
func NotHealth(holder string, generation uint64, now time.Time, r HealthReq) string {
	switch {
	case !r.Known:
		return "no friend " + r.Friend + " on the friends table; run: nova-sprint friend sync"
	case holder == "":
		return "the sprint has no coordinator; run: nova-sprint init --coordinator <name>"
	case r.Who != holder:
		return "friend health is the seat's: " + holder + ", not " + orDash(r.Who)
	case r.Obs.Generation != generation:
		return fmt.Sprintf("the seat is %s's at generation %d, and this observation names generation %d: read the seat again (nova-sprint seat)", holder, generation, r.Obs.Generation)
	case r.Obs.Seen.After(now):
		return fmt.Sprintf("the proof is dated %s, after the server's clock, %s: a proof from the future renews nothing", r.Obs.Seen.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	case r.Prev.Observed() && !r.Obs.Seen.After(r.Prev.Seen):
		return fmt.Sprintf("the row holds a proof seen at %s, and this one's is not newer, %s: an older or repeated proof renews nothing", r.Prev.Seen.UTC().Format(time.RFC3339), r.Obs.Seen.UTC().Format(time.RFC3339))
	}
	return ""
}

// ObserveFriend is the observation written: the plan's Health, which the
// step's commit writes as the friend's record, or its refusal.
func ObserveFriend(s *Snapshot, r HealthReq) Plan {
	var p Plan
	if why := NotHealth(s.Coordinator, s.SeatGeneration, s.Now, r); why != "" {
		p.refuse(r.Friend, why)
		return p
	}
	h := r.Obs
	p.Health = &FriendHealthWrite{Friend: r.Friend, Health: h}
	return p
}

// HealthClearReq is the removal of a friend's observation (friend health --clear): who
// sends it (the seat's holder) and the friend (Known says the roster has her).
type HealthClearReq struct {
	Friend, Who string
	Known       bool
}

// ClearFriendHealth is the observation removed: the plan's HealthClear, which the step's
// commit applies by removing her record, so her status is her session's evidence alone
// (FriendStatus), or its refusal: the sender is the seat's holder, of a friend on the
// table. A friend with no observation is cleared all the same (nothing to remove).
func ClearFriendHealth(s *Snapshot, r HealthClearReq) Plan {
	var p Plan
	switch {
	case !r.Known:
		p.refuse(r.Friend, "no friend "+r.Friend+" on the friends table; run: nova-sprint friend sync")
	case s.Coordinator == "":
		p.refuse(r.Friend, "the sprint has no coordinator; run: nova-sprint init --coordinator <name>")
	case r.Who != s.Coordinator:
		p.refuse(r.Friend, "friend health is the seat's: "+s.Coordinator+", not "+orDash(r.Who))
	default:
		p.HealthClear = []string{r.Friend}
	}
	return p
}

// FriendHealthWrite is the record a step's commit writes: the friend's health.
type FriendHealthWrite struct {
	Friend string       `json:"friend"`
	Health FriendHealth `json:"health"`
}

// FriendPresence is everything the friends' rule reads of one friend: the
// coordinator's hold, her daemon's beat (its age, and the session word it carries), the coordinator's
// observation of her, the seat's generation now, and when a card of hers last
// finished (working to done), zero for never.
type FriendPresence struct {
	Held       bool
	Beat       Beat
	Health     FriendHealth
	Generation uint64
	Finished   time.Time
}
