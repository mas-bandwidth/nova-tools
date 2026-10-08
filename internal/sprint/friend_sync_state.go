package sprint

import (
	"encoding/json"
	"fmt"
	"time"
)

// The friend sync loop's standing failure, told once (docs/FRIENDS.md, "The
// friend sync loop"). On 2026-10-08 the loop refused every pass from 01:05 to
// 08:51 ET ("schema config is at version 35 and this binary carries 36; run:
// nova-config migrate") and for 7h45m no friend card was delivered or
// collected and no width followed nova-config; nobody was told, because the
// loop wrote its one FAILING line to its own log and raised no judgment. Now a
// loop failing past FriendSyncJudgeAfter records its state on the fleet table
// (PropFriendSync, FriendSyncState), the tick's deal raises one judgment of
// type NFriendSyncFailing on it (friendSyncConds, one open at a time: notify),
// and the first ok pass clears the state, which closes the judgment. The loop
// keeps retrying as before; the judgment is the coordinator's to see, with
// the refusal and its remedy in it, and nothing to answer but ack or wait.
const (
	// NFriendSyncFailing is the judgment: friend sync has refused every pass past the bound.
	NFriendSyncFailing = "friend sync keeps refusing"
	// NFriendSyncRefusing and NFriendSyncRecovered are the log's happened notes of the
	// state's record and its clearing.
	NFriendSyncRefusing  = "friend sync refusing"
	NFriendSyncRecovered = "friend sync ok again"
	// PropFriendSync is the fleet table property holding the loop's standing failure
	// (FriendSyncState as JSON); absent or empty while the loop is ok.
	PropFriendSync = "friend_sync"
	// FriendSyncJudgeAfter is how long the loop fails before it records the state the
	// judgment is raised on: one minute, four passes of the 15 s loop.
	FriendSyncJudgeAfter = time.Minute
)

// FriendSyncState is the loop's standing failure as the property holds it.
type FriendSyncState struct {
	Since  time.Time `json:"since"`
	Failed int       `json:"failed"`
	Exit   int       `json:"exit"`
	Said   string    `json:"said"`
}

// FriendSyncStateReq is the loop's record of its state: Failing with the
// failure's facts, or not failing (the first ok pass after a recorded failure).
type FriendSyncStateReq struct {
	Failing bool
	Since   time.Time
	Failed  int
	Exit    int
	Said    string
	Who     string
}

// friendSyncStateOf is the recorded state, and whether one is recorded.
func friendSyncStateOf(s *Snapshot) (FriendSyncState, bool) {
	v, ok := s.Fleet.Prop(PropFriendSync)
	if !ok || v == "" {
		return FriendSyncState{}, false
	}
	var st FriendSyncState
	if json.Unmarshal([]byte(v), &st) != nil {
		return FriendSyncState{}, false
	}
	return st, true
}

// FriendSyncStateStep records the loop's state: one property write guarded on
// the value read, and one happened note when the state is first recorded or
// cleared. A failing record over a recorded one updates the facts (the count,
// what it says) with no note; an ok record with nothing recorded is empty.
func FriendSyncStateStep(s *Snapshot, r FriendSyncStateReq) Plan {
	var p Plan
	was, had := s.Fleet.Prop(PropFriendSync)
	_, recorded := friendSyncStateOf(s)
	if !r.Failing {
		if !recorded {
			return p
		}
		n := happened(NFriendSyncRecovered, "", s.Now)
		n.Who = r.Who
		n.What = fmt.Sprintf("friend sync is ok again after %d failing passes", r.Failed)
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropFriendSync, Value: "", Was: was, WasAbsent: !had})
		p.Notes = append(p.Notes, n)
		return p
	}
	st := FriendSyncState{Since: r.Since.UTC().Truncate(time.Second), Failed: r.Failed, Exit: r.Exit, Said: r.Said}
	b, err := json.Marshal(st)
	if err != nil {
		p.refuse(PropFriendSync, "the state cannot be written: "+err.Error())
		return p
	}
	p.Props = append(p.Props, PropWrite{Table: Fleet, Name: PropFriendSync, Value: string(b), Was: was, WasAbsent: !had})
	if !recorded {
		n := happened(NFriendSyncRefusing, "", s.Now)
		n.Who = r.Who
		n.What = friendSyncWhat(st)
		p.Notes = append(p.Notes, n)
	}
	return p
}

// friendSyncWhat is the judgment's text: since when, how many passes, what the
// loop says (the refusal, which carries its own remedy), and what to do.
func friendSyncWhat(st FriendSyncState) string {
	return fmt.Sprintf("friend sync has refused every pass since %s (%d passes, exit %d), so no friend card is delivered or collected and no width follows nova-config: %s; the loop keeps retrying every pass and this closes when a pass is ok",
		stamp(st.Since), st.Failed, st.Exit, st.Said)
}

// friendSyncConds is the tick's condition on the recorded state: one
// sprint-level judgment while the loop's failure stands, none when it is clear.
func friendSyncConds(s *Snapshot) []cond {
	st, ok := friendSyncStateOf(s)
	if !ok {
		return nil
	}
	return []cond{{typ: NFriendSyncFailing, streamLevel: true, what: friendSyncWhat(st), decisions: []string{"ack", "wait"}}}
}
