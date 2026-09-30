package verbs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// status is a member's control card's status.
func (w *wf) status(m string) string {
	w.t.Helper()
	return fieldOf(w.rec(sprint.Fleet, sprint.CtlID(m)), wkfStatus)
}

// beat is one fleet beat of members, which must apply.
func (w *wf) beat(members ...string) Result {
	w.t.Helper()
	res, err := FleetBeat(context.Background(), w.env, FleetBeatReq{Members: members})
	if err != nil {
		w.t.Fatalf("beat %v: %v", members, err)
	}
	return res
}

// TestFleetBeatSetOfMembers: one beat for a set of members is one step and one
// round trip, with no read (1.4.4): each member's beat record, beat:<m> at R +
// 15 s, seen:<m> for a member that is down and for one with no fleet row (a
// stranger, noted once), and nothing for one that is up or held; a beat writes
// no line and changes no card.
func TestFleetBeatSetOfMembers(t *testing.T) {
	t.Parallel()
	w := newWF(t)
	w.member("up1", sprint.Up)
	w.member("down1", sprint.Down)
	w.member("held1", sprint.Held)
	lines := len(w.log.Lines(wfPrefix, "0"))
	rev := w.rec(sprint.Fleet, sprint.CtlID("down1")).Revision
	r := w.r()
	w.cc.Reset()
	res, err := FleetBeat(context.Background(), w.env, FleetBeatReq{Members: []string{"up1", "down1", "held1", "stranger1"},
		Loads: map[string]string{"up1": "0.5"}})
	if err != nil {
		t.Fatalf("beat: %v", err)
	}
	if w.cc.Trips() != 1 || w.cc.Steps() != 1 || res.Trips != 1 {
		t.Fatalf("%d round trips, %d steps; want one step in one round trip", w.cc.Trips(), w.cc.Steps())
	}
	for _, m := range []string{"up1", "down1", "held1", "stranger1"} {
		if s, ok := w.zscore("due", "beat:"+m); !ok || int64(s) != r+15000 {
			t.Fatalf("beat:%s at %v %v, want R + 15 s = %d", m, s, ok, r+15000)
		}
		if kv, ok := w.sprintKey("beat:" + m); !ok || kv.Hash["at_ms"] == "" {
			t.Fatalf("%s has no beat record", m)
		}
	}
	if kv, _ := w.sprintKey("beat:up1"); kv.Hash["load"] != "0.5" {
		t.Fatalf("up1's load %q", kv.Hash["load"])
	}
	for m, want := range map[string]bool{"up1": false, "down1": true, "held1": false, "stranger1": true} {
		if _, ok := w.zscore("due", "seen:"+m); ok != want {
			t.Fatalf("seen:%s entered %v, want %v", m, ok, want)
		}
	}
	if kv, ok := w.sprintKey("strangers"); !ok || kv.Hash["stranger1"] == "" {
		t.Fatal("the stranger was not noted")
	}
	if !strings.Contains(res.Said, "seen: down1, stranger1") || !strings.Contains(res.Said, "no fleet row: stranger1") {
		t.Fatalf("said %q", res.Said)
	}
	if got := len(w.log.Lines(wfPrefix, "0")); got != lines {
		t.Fatalf("a beat wrote %d lines", got-lines)
	}
	if w.rec(sprint.Fleet, sprint.CtlID("down1")).Revision != rev {
		t.Fatal("a beat changed a card")
	}
	t.Run("a beat again moves the entry to the new R + 15 s", func(t *testing.T) {
		w.advance(4 * time.Second)
		w.beat("up1")
		if s, _ := w.zscore("due", "beat:up1"); int64(s) != w.r()+15000 {
			t.Fatalf("beat:up1 at %v, want %d", s, w.r()+15000)
		}
	})
	t.Run("a member named twice, or a bad name, is refused before anything is sent", func(t *testing.T) {
		w.cc.Reset()
		for _, ms := range [][]string{{"up1", "up1"}, {"a b"}, {}} {
			if _, err := FleetBeat(context.Background(), w.env, FleetBeatReq{Members: ms}); err == nil {
				t.Fatalf("%v beat", ms)
			}
		}
		if w.cc.Trips() != 0 {
			t.Fatal("a refused beat was sent")
		}
	})
}

// TestFleetUpFreshFromDueSet: fleet up reads a member's freshness from
// beat:<m> in the due set, in running time (1.4.4): a member whose beat lies
// above R comes up, with KNOW "fleet member up" and "no fleet member is up"
// closed; one whose entry is at or below R (or absent) is down until it beats,
// even while its beat record's wall stamp is recent; a member with no fleet
// row gets its row and control card; the member count is held to the sprint's
// size bound (section 3).
func TestFleetUpFreshFromDueSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWF(t)
	w.member("m1", sprint.Held)
	w.member("m2", sprint.Held)
	w.raw() // an empty step: the judgment below is J's, asked by a note request
	open := &sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "seed", Actor: "test"}, Body: sprintfn.Body{Notes: []sprintfn.NoteReq{
		{Op: sprintfn.JOpOpen, Type: sprint.NNoMember, Cause: sprint.CauseNoMember, Subjects: []string{sprint.SubjectSprint}, Text: "no member is up"}}}}
	if res, err := sprintfn.Step(ctx, w.tw, open); err != nil || res.Refusal != nil || res.Err != nil {
		t.Fatalf("open: %v %v %v", err, res.Refusal, res.Err)
	}
	w.beat("m1", "m2")
	// m2's beat:m2 lies at R + 15 s of its beat; 20 s of running time later it
	// is stale by the due set, though its beat record's wall stamp is recent
	// (no tick has popped it). m1 beat 10 s later: still fresh.
	w.advance(10 * time.Second)
	w.beat("m1")
	w.advance(10 * time.Second)
	w.cc.Reset()
	res, err := FleetUp(ctx, w.env, FleetReq{Members: []string{"m2", "m1", "m3"}})
	if err != nil {
		t.Fatalf("fleet up: %v", err)
	}
	if w.cc.Trips() != 2 || res.Step == nil {
		t.Fatalf("%d round trips; want a step in 2", w.cc.Trips())
	}
	if w.status("m1") != sprint.Up || w.status("m2") != sprint.Down || w.status("m3") != sprint.Down {
		t.Fatalf("statuses %s %s %s, want up (fresh), down (stale by the due set), down (never beat)", w.status("m1"), w.status("m2"), w.status("m3"))
	}
	w.at(sprint.Fleet, sprint.CtlID("m3"), "m3:ctl") // the new member's row and control card
	if n := w.notes(sprint.NMemberUp); len(n) != 1 || strings.Join(n[0].About, ",") != "m1" {
		t.Fatalf("fleet member up notes %+v, want one naming m1", n)
	}
	if n := w.notes(sprint.NNoMember); len(n) != 2 || n[0].Op != sprintfn.JOpOpen || n[1].Op != sprintfn.JOpClose {
		t.Fatalf("no fleet member is up: %+v, want its open and then its close", n)
	}
	if !strings.Contains(res.Said, "up: m1") || !strings.Contains(res.Said, "down until they beat: m2, m3") {
		t.Fatalf("said %q", res.Said)
	}
	t.Run("a released member comes up at its next beat, and fleet up again changes nothing", func(t *testing.T) {
		w.beat("m2") // down: the beat enters seen:m2 for R1
		if _, ok := w.zscore("due", "seen:m2"); !ok {
			t.Fatal("a beat of a released member did not enter seen")
		}
		w.cc.Reset()
		res, err := FleetUp(ctx, w.env, FleetReq{Members: []string{"m1"}})
		if err != nil || res.Step != nil || w.cc.Trips() != 1 {
			t.Fatalf("err %v step %v trips %d: an up member is left as it is", err, res.Step != nil, w.cc.Trips())
		}
	})
	t.Run("a beat that raced the read refuses the down, and the verb plans again", func(t *testing.T) {
		w.member("m4", sprint.Held)
		f := &wfFaultyBeat{c: w.tw, w: w, member: "m4"}
		w.env.C = f
		defer func() { w.env.C = w.cc }()
		res, err := FleetUp(ctx, w.env, FleetReq{Members: []string{"m4"}})
		if err != nil {
			t.Fatalf("fleet up: %v", err)
		}
		if res.Retries != 1 || w.status("m4") != sprint.Up {
			t.Fatalf("retries %d, status %s; want 1 (XGUARD beatstale) and up", res.Retries, w.status("m4"))
		}
	})
	t.Run("past the size bound is refused, naming it", func(t *testing.T) {
		// Four members already (m1 to m4); 100 and 100 more fit 250, then 50 more do not.
		var many []string
		for i := 0; i < MembersAndStreamsMax; i++ {
			many = append(many, fmt.Sprintf("nx%03d", i))
		}
		for _, set := range [][]string{many[:RowsAddMax], many[RowsAddMax : 2*RowsAddMax]} {
			if _, err := FleetUp(ctx, w.env, FleetReq{Members: set}); err != nil {
				t.Fatalf("%d members: %v", len(set), err)
			}
		}
		w.cc.Reset()
		_, err := FleetUp(ctx, w.env, FleetReq{Members: many[2*RowsAddMax:]})
		wfRefusedWith(t, err, sprintfn.CodeLimit, "members and streams together are 254, over the sprint's 250")
		if w.cc.Steps() != 0 {
			t.Fatal("a step past the bound was sent")
		}
	})
}

// TestFleetUpDueGuard: a member brought up on its beat as read is guarded by
// the beat's entry as read (XGUARD): a beat that moves beat:<m> between the read
// and the step refuses the step, and the verb plans again from the entry as it
// is now and brings the member up, once (the model's fleetup VGuard).
func TestFleetUpDueGuard(t *testing.T) {
	t.Parallel()
	w := newWF(t)
	w.member("m1", sprint.Down)
	w.beat("m1")
	w.advance(time.Second)
	w.hooked(func() {
		w.advance(time.Second)
		w.beat("m1") // beat:m1 moves to a later R + 15 s
	})
	res, err := FleetUp(context.Background(), w.env, FleetReq{Members: []string{"m1"}})
	if err != nil || res.Retries != 1 || w.status("m1") != sprint.Up {
		t.Fatalf("err %v retries %d status %s; want the step refused once by the due guard, then up", err, res.Retries, w.status("m1"))
	}
}

// wfFaultyBeat runs a beat of one member just before the first step it passes,
// as a machine's beat racing the verb.
type wfFaultyBeat struct {
	c      sprintfn.Client
	w      *wf
	member string
	done   bool
}

func (f *wfFaultyBeat) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	for _, it := range items {
		if it.Step != nil && !f.done {
			f.done = true
			if _, err := FleetBeat(ctx, &Env{C: f.c, Names: wfNames, Actor: "machine"}, FleetBeatReq{Members: []string{f.member}}); err != nil {
				return nil, err
			}
		}
	}
	return f.c.Pipeline(ctx, items)
}

// TestFleetDownSticky: fleet down holds a member (status held), with KNOW
// "fleet member down"; beats do not undo it (a beat of a held member enters no
// seen:<m>, so R1 never raises it); only fleet up releases it, up at once when
// its beat is fresh. fleet down again changes nothing.
func TestFleetDownSticky(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWF(t)
	w.member("m1", sprint.Up)
	w.beat("m1")
	w.cc.Reset()
	res, err := FleetDown(ctx, w.env, FleetReq{Members: []string{"m1"}})
	if err != nil || res.Step == nil || w.cc.Trips() != 2 {
		t.Fatalf("fleet down: err %v trips %d", err, w.cc.Trips())
	}
	if w.status("m1") != sprint.Held {
		t.Fatalf("status %s, want held", w.status("m1"))
	}
	if n := w.notes(sprint.NMemberDown); len(n) != 1 || n[0].About[0] != "m1" {
		t.Fatalf("notes %+v", n)
	}
	for i := 0; i < 3; i++ {
		w.advance(4 * time.Second)
		w.beat("m1")
		if _, ok := w.zscore("due", "seen:m1"); ok {
			t.Fatal("a beat of a held member entered seen: the hold is not sticky")
		}
		if w.status("m1") != sprint.Held {
			t.Fatal("a beat changed the held status")
		}
	}
	w.cc.Reset()
	if res, err := FleetDown(ctx, w.env, FleetReq{Members: []string{"m1"}}); err != nil || res.Step != nil || w.cc.Trips() != 1 {
		t.Fatalf("fleet down again: err %v step %v trips %d", err, res.Step != nil, w.cc.Trips())
	}
	if _, err := FleetUp(ctx, w.env, FleetReq{Members: []string{"m1"}}); err != nil {
		t.Fatal(err)
	}
	if w.status("m1") != sprint.Up {
		t.Fatalf("after fleet up with a fresh beat: %s, want up", w.status("m1"))
	}
	_, err = FleetDown(ctx, w.env, FleetReq{Members: []string{"ghost"}})
	wfRefusedWith(t, err, sprintfn.CodeRequest, "ghost: no fleet member")
}

// TestFleetVerbsOp: fleet up, fleet down and reader add take --op: a repeat
// with the same op and arguments returns the recorded result and writes
// nothing (L1 5); other arguments are refused.
func TestFleetVerbsOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWF(t)
	w.member("m1", sprint.Up)
	if _, err := FleetDown(ctx, w.env, FleetReq{Members: []string{"m1"}, Op: "op-down-1"}); err != nil {
		t.Fatal(err)
	}
	res, err := FleetDown(ctx, w.env, FleetReq{Members: []string{"m1"}, Op: "op-down-1"})
	if err != nil || !res.Replay {
		t.Fatalf("repeat: err %v replay %v", err, res.Replay)
	}
	_, err = FleetDown(ctx, w.env, FleetReq{Members: []string{"m2"}, Op: "op-down-1"})
	wfRefusedWith(t, err, "OPCONFLICT", "other arguments")
}

// TestReaderAdd: reader add adds the readers' rows in one step, two round
// trips; readers with rows already change nothing; the size bound refuses.
func TestReaderAdd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWF(t)
	w.reader("r1")
	w.cc.Reset()
	res, err := ReaderAdd(ctx, w.env, ReaderAddReq{Readers: []string{"r2", "r1", "r3"}})
	if err != nil || res.Step == nil || w.cc.Trips() != 2 || res.Said != "reader add: r2, r3" {
		t.Fatalf("err %v trips %d said %q", err, w.cc.Trips(), res.Said)
	}
	rows := w.read(&sprintfn.ReadRequest{Epoch: "0", Tset: flRowsQueries()})
	got, _ := flRowsOf(rows)
	if !got.readers["r1"] || !got.readers["r2"] || !got.readers["r3"] {
		t.Fatalf("readers %v", got.readers)
	}
	w.cc.Reset()
	if res, err := ReaderAdd(ctx, w.env, ReaderAddReq{Readers: []string{"r1", "r2"}}); err != nil || res.Step != nil || w.cc.Trips() != 1 {
		t.Fatalf("again: err %v step %v", err, res.Step != nil)
	}
	var many []string
	for i := 0; i < 2*RowsAddMax+RowsAddMax/2; i++ {
		many = append(many, fmt.Sprintf("rd%03d", i))
	}
	if _, err := ReaderAdd(ctx, w.env, ReaderAddReq{Readers: many, Op: "op-1"}); err == nil || !strings.Contains(err.Error(), "without --op") {
		t.Fatalf("a set past one step with an op: %v", err)
	}
	w.cc.Reset()
	if _, err := ReaderAdd(ctx, w.env, ReaderAddReq{Readers: many}); err != nil {
		t.Fatalf("250 readers: %v", err)
	}
	if w.cc.Steps() != 3 {
		t.Fatalf("%d steps for 250 new rows, want 3 of at most 100", w.cc.Steps())
	}
}

// read is one atomic read on the twin.
func (w *wf) read(rr *sprintfn.ReadRequest) *sprintfn.ReadReply {
	w.t.Helper()
	res, err := sprintfn.Read(context.Background(), w.tw, rr)
	if err != nil || res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("read: %v %v %v", err, res.Refusal, res.Err)
	}
	return res.Read
}
