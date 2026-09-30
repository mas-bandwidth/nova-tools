package verbs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// refusedCode is the code of a verb's refusal, and whether it was the verb's
// own (from its read, before any write).
func refusedCode(t *testing.T, err error) (string, bool) {
	t.Helper()
	var rf *Refused
	if !errors.As(err, &rf) {
		t.Fatalf("err %v, want a refusal", err)
	}
	return rf.Code(), rf.Local
}

// lineWith says an epoch's log holds a line naming text.
func lineWith(w *world, epoch tset.Decimal, text string) bool {
	for _, l := range w.log.Lines(testPrefix, epoch) {
		if strings.Contains(string(l), text) {
			return true
		}
	}
	return false
}

// atMostTwo holds a machine verb to its two round trips (1.5.3).
func atMostTwo(t *testing.T, w *world, verb string) {
	t.Helper()
	if n := w.cc.Trips(); n > 2 {
		t.Fatalf("%s made %d round trips, want at most 2", verb, n)
	}
	w.cc.Reset()
}

// TestStartStopMachineState: start runs the machine and stop stops it, each
// in two round trips with its notice; a start while running and a stop while
// STOPPED are refused MACHINESTATE from the read, sending nothing, and at
// apply when the machine moved between the read and the step (A2), with no
// retry, since MACHINESTATE is not a race (1.3.5).
func TestStartStopMachineState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.initSprint()
	w.cc.Reset()
	if c, has := w.clock(0); !has || running(c) {
		t.Fatalf("after init: clock %v, running %v; want a STOPPED clock", has, running(c))
	}

	if _, err := Start(ctx, w.env, ClockReq{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	atMostTwo(t, w, "start")
	if c, _ := w.clock(0); !running(c) {
		t.Fatal("start: the machine does not run")
	}
	if !lineWith(w, "0", noticeStarted) {
		t.Fatal("start wrote no notice")
	}

	_, err := Start(ctx, w.env, ClockReq{})
	if code, local := refusedCode(t, err); code != sprintfn.CodeMachineState || !local {
		t.Fatalf("a second start: %s (local %v), want MACHINESTATE from the read", code, local)
	}
	if w.cc.Steps() != 0 {
		t.Fatalf("a refused start sent %d steps", w.cc.Steps())
	}
	w.cc.Reset()

	w.tick(time.Minute)
	if _, err := Stop(ctx, w.env, ClockReq{}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	atMostTwo(t, w, "stop")
	c, _ := w.clock(0)
	if running(c) || !lineWith(w, "0", noticeStopped) {
		t.Fatal("stop: the machine runs, or no notice")
	}
	since := *c.Clock.StoppedSinceMS

	w.tick(time.Minute)
	_, err = Stop(ctx, w.env, ClockReq{})
	if code, _ := refusedCode(t, err); code != sprintfn.CodeMachineState {
		t.Fatalf("a second stop: %s, want MACHINESTATE", code)
	}
	if c, _ := w.clock(0); *c.Clock.StoppedSinceMS != since {
		t.Fatal("a second stop moved stopped_since_ms (A2)")
	}

	// Between this start's read and its step another start applies: the
	// write path refuses the step MACHINESTATE, and the verb does not retry.
	other := &Env{C: w.tw, Names: testNames, Actor: "coord", noWait: true}
	f := &faulty{c: w.tw, before: func(n int) {
		if n == 1 {
			if _, err := Start(ctx, other, ClockReq{}); err != nil {
				t.Errorf("the other start: %v", err)
			}
		}
	}}
	w.env.C = f
	_, err = Start(ctx, w.env, ClockReq{})
	if code, local := refusedCode(t, err); code != sprintfn.CodeMachineState || local {
		t.Fatalf("a start raced by a start: %s (local %v), want MACHINESTATE from the write path", code, local)
	}
	if f.steps != 1 {
		t.Fatalf("the raced start sent %d steps, want 1: MACHINESTATE is not retried", f.steps)
	}
}

// memberAndStream adds members, readers and streams at epoch 0 as the
// fleet, reader and add verbs would: their rows, and the members' and the
// streams' control cards.
func memberAndStream(w *world, members, readers, streams []string) {
	w.t.Helper()
	var entries []tset.Entry
	if len(members) != 0 {
		entries = append(entries, tset.Entry{Kind: "rows", Table: sprint.Fleet, Add: members})
	}
	if len(readers) != 0 {
		entries = append(entries, tset.Entry{Kind: "rows", Table: sprint.Readers, Add: readers})
	}
	if len(streams) != 0 {
		entries = append(entries, tset.Entry{Kind: "rows", Table: sprint.Work, Add: streams},
			tset.Entry{Kind: "rows", Table: sprint.Merge, Add: streams})
	}
	for _, m := range members {
		id := sprint.CtlID(m)
		entries = append(entries, tset.Entry{Kind: "create", Table: sprint.Fleet, To: m + ":ctl", IDs: []string{id}, About: []string{id},
			Scores: []string{"0"}, Set: map[string]string{"kind": "member", "status": sprint.Up}})
	}
	for _, s := range streams {
		id := sprint.CtlID(s)
		entries = append(entries, tset.Entry{Kind: "create", Table: sprint.Merge, To: s + ":ctl", IDs: []string{id}, About: []string{id},
			Scores: []string{"0"}, Set: map[string]string{"kind": "stream", "state": sprint.StreamMerging}})
	}
	w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "setup", Actor: "coord"}, Body: sprintfn.Body{Entries: entries}})
}

// TestClearLeavesStopped: clear runs only while STOPPED (errata 1): on a
// running machine it is refused naming stop and writes nothing; stopped, it
// advances the epoch and leaves the machine STOPPED there, and start runs it
// at the new epoch. A repeat of the same op at the epoch it ran at returns its
// receipt across the advance (L1 5) and clears nothing again.
func TestClearLeavesStopped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.initSprint()
	memberAndStream(w, []string{"m1"}, nil, []string{"s1"})
	if _, err := Start(ctx, w.env, ClockReq{}); err != nil {
		t.Fatal(err)
	}
	w.cc.Reset()

	_, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix})
	if code, local := refusedCode(t, err); code != sprintfn.CodeMachineState || !local {
		t.Fatalf("clear of a running machine: %s (local %v), want MACHINESTATE", code, local)
	}
	if w.env.Epoch != 0 || w.cc.Steps() != 0 {
		t.Fatalf("a refused clear moved the epoch to %d or sent %d steps", w.env.Epoch, w.cc.Steps())
	}
	_, err = Clear(ctx, w.env, ClearReq{Confirm: "other:"})
	if code, _ := refusedCode(t, err); code != sprintfn.CodeRequest {
		t.Fatalf("clear --confirm of another prefix: %s, want REQUEST", code)
	}

	if _, err := Stop(ctx, w.env, ClockReq{}); err != nil {
		t.Fatal(err)
	}
	w.cc.Reset()
	res, err := Clear(ctx, w.env, ClearReq{Op: "clear-1", Confirm: testPrefix})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	atMostTwo(t, w, "clear")
	if res.EpochAfter != 1 || w.env.Epoch != 1 {
		t.Fatalf("clear: epoch after %d, env at %d; want 1", res.EpochAfter, w.env.Epoch)
	}
	c, has := w.clock(1)
	if !has || running(c) {
		t.Fatal("after clear the machine is not STOPPED")
	}

	// The same op, run again at the epoch it ran at, is its receipt.
	again := &Env{C: w.cc, Names: testNames, Actor: "coord", Epoch: 0, noWait: true}
	rep, err := Clear(ctx, again, ClearReq{Op: "clear-1", Confirm: testPrefix})
	if err != nil || !rep.Replay || rep.EpochAfter != 1 {
		t.Fatalf("clear repeated: %+v, %v; want the receipt, epoch after 1", rep, err)
	}
	if w.cc.Steps() != 0 {
		t.Fatal("the repeated clear sent a step")
	}
	if rd := w.read(clockRead("1")); rd.ActiveEpoch != "1" {
		t.Fatalf("the active epoch is %s after the repeat, want 1: cleared once", rd.ActiveEpoch)
	}
	w.cc.Reset()

	if _, err := Start(ctx, w.env, ClockReq{}); err != nil {
		t.Fatalf("start after clear: %v", err)
	}
	if c, _ := w.clock(1); !running(c) {
		t.Fatal("start after clear: the machine does not run")
	}
}

// TestClearNoticeAtNewEpoch: clear's notice is written at the epoch it
// advances to, where the new sprint's inbox reads it, and not at the old one.
func TestClearNoticeAtNewEpoch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.initSprint()
	if _, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !lineWith(w, "1", noticeCleared) {
		t.Fatalf("no line at epoch 1 names %q", noticeCleared)
	}
	if lineWith(w, "0", noticeCleared) {
		t.Fatal("the old epoch holds the clear's notice")
	}
	keys := w.tw.SprintKeys()
	if _, ok := keys[testNames.Key("notes")+"@1"]; !ok {
		t.Fatal("the new epoch's notes list is not written")
	}
}

// TestClearCreatesControlCards: clear restores every row at the new epoch and
// creates each member's control card with status down and each stream's
// control card; a member's next beat then enters seen:<m>, so R1 brings it up
// (1.4.4). Readers get their rows back and no control card (F1-20).
func TestClearCreatesControlCards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.initSprint()
	memberAndStream(w, []string{"m1", "m2"}, []string{"r1"}, []string{"s1", "s2"})
	if _, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	rd := w.read(&sprintfn.ReadRequest{Epoch: "1", Tset: []tset.ReadQuery{
		{Kind: "rows", Table: sprint.Work}, {Kind: "rows", Table: sprint.Readers},
		{Kind: "rows", Table: sprint.Merge}, {Kind: "rows", Table: sprint.Fleet},
		{Kind: "ids", Table: sprint.Fleet, IDs: []string{"ctl-m1~1", "ctl-m2~1"}, Fields: []string{"status"}},
		{Kind: "ids", Table: sprint.Merge, IDs: []string{"ctl-s1~1", "ctl-s2~1"}, Fields: []string{"kind", "state"}},
	}})
	want := [][]string{{"s1", "s2"}, {"r1"}, {"s1", "s2"}, {"m1", "m2"}}
	for i, rows := range want {
		var got []string
		for _, r := range rd.Tset[i].Rows {
			got = append(got, r.Row)
		}
		if strings.Join(got, ",") != strings.Join(rows, ",") {
			t.Fatalf("table %s rows at epoch 1: %v, want %v", clearTables[i], got, rows)
		}
	}
	for _, r := range rd.Tset[4].Records {
		if !r.Exists || r.Fields["status"].Value != sprint.Down || r.Place == nil || r.Place.Col != sprint.Ctl {
			t.Fatalf("member control card %s: %+v, want status down in ctl", r.ID, r)
		}
	}
	for _, r := range rd.Tset[5].Records {
		if !r.Exists || r.Fields["kind"].Value != "stream" || r.Fields["state"].Value != sprint.StreamWaiting {
			t.Fatalf("stream control card %s: %+v, want a waiting stream", r.ID, r)
		}
	}
	beat := w.step(&sprintfn.Request{Epoch: "1", Meta: sprintfn.Meta{Verb: "fleet beat"},
		Beat: &sprintfn.BeatPart{Members: []sprintfn.BeatMember{{Member: "m1"}}}})
	if got := string(beat.Parts[sprintfn.PartBeat]); !strings.Contains(got, `"seen":["m1"]`) {
		t.Fatalf("m1's beat after clear: %s; want seen:m1 entered, so R1 brings it up", got)
	}
}

// TestSizeBoundsRefused: members and streams together at most 250, members +
// readers + 2 x streams at most 1,024 (section 3); clear refuses a sprint past
// either, and past a step's 256 entries, before any write.
func TestSizeBoundsRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		members, readers, streams int
		ok                        bool
	}{
		{250, 0, 0, true}, {125, 0, 125, true}, {251, 0, 0, false}, {200, 0, 51, false},
		{100, 774, 75, true}, {100, 775, 75, false}, {0, 1024, 0, true}, {0, 1025, 0, false},
	} {
		err := SizeBounds(c.members, c.readers, c.streams)
		if (err == nil) != c.ok {
			t.Fatalf("SizeBounds(%d, %d, %d) = %v, want ok %v", c.members, c.readers, c.streams, err, c.ok)
		}
	}
	ctx := context.Background()
	names := func(p string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s%03d", p, i)
		}
		return out
	}
	grow := func(w *world, table string, rows []string) {
		for i := 0; i < len(rows); i += 100 {
			w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "setup", Actor: "coord"}, Body: sprintfn.Body{Entries: []tset.Entry{
				{Kind: "rows", Table: table, Add: rows[i:min(i+100, len(rows))]}}}})
		}
	}
	for _, c := range []struct {
		name             string
		members, streams int
	}{{"past 250 members and streams", 200, 51}, {"past a step's entries", 240, 8}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.initSprint()
			grow(w, sprint.Fleet, names("m", c.members))
			grow(w, sprint.Work, names("s", c.streams))
			w.cc.Reset()
			_, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix})
			if code, local := refusedCode(t, err); code != sprintfn.CodeLimit || !local {
				t.Fatalf("clear: %s (local %v), want LIMIT from the read", code, local)
			}
			if w.cc.Steps() != 0 || w.env.Epoch != 0 {
				t.Fatalf("a refused clear sent %d steps, epoch %d", w.cc.Steps(), w.env.Epoch)
			}
		})
	}
	t.Run("at the entries' edge", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.initSprint()
		grow(w, sprint.Fleet, names("m", 240))
		grow(w, sprint.Work, names("s", 7))
		if _, err := Clear(ctx, w.env, ClearReq{Confirm: testPrefix}); err != nil {
			t.Fatalf("a clear of 247 members and streams: %v", err)
		}
	})
}

// TestInitCoordinatorFromConfig: init writes the coordinator nova-config
// names; init --coordinator takes the one nova-config names now, run only by
// that actor (checked in Go), and X then refuses a coordinator's verb from
// anyone else (NOTCOORD, 1.5.3).
func TestInitCoordinatorFromConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.configure("f1")
	w.cc.Reset()
	if _, err := Init(ctx, w.env, InitReq{Config: w.cfg}); err != nil {
		t.Fatalf("init: %v", err)
	}
	atMostTwo(t, w, "init")
	coordKey := testNames.Key("coordinator")
	if got := w.tw.SprintKeys()[coordKey].String; got != "f1" {
		t.Fatalf("init wrote coordinator %q, want f1", got)
	}
	_, err := Init(ctx, w.env, InitReq{Config: w.cfg})
	if code, _ := refusedCode(t, err); code != sprintfn.CodeMachineState {
		t.Fatalf("a second init: %s, want MACHINESTATE", code)
	}

	w.configure("f2")
	w.env.Actor = "f1"
	_, err = InitCoordinator(ctx, w.env, InitReq{Config: w.cfg})
	if code, local := refusedCode(t, err); code != sprintfn.CodeNotCoord || !local {
		t.Fatalf("init --coordinator by f1 when nova-config names f2: %s (local %v), want NOTCOORD", code, local)
	}
	w.env.Actor = "f2"
	w.cc.Reset()
	if _, err := InitCoordinator(ctx, w.env, InitReq{Config: w.cfg}); err != nil {
		t.Fatalf("init --coordinator by f2: %v", err)
	}
	atMostTwo(t, w, "init --coordinator")
	if got := w.tw.SprintKeys()[coordKey].String; got != "f2" {
		t.Fatalf("the coordinator is %q, want f2", got)
	}

	// X checks the store's copy: a coordinator's verb from f1 is refused.
	res, err := sprintfn.Step(ctx, w.tw, &sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "ack", Actor: "f1"}})
	if err != nil || res.Refusal == nil || res.Refusal.Code != sprintfn.CodeNotCoord {
		t.Fatalf("ack by f1: %+v, %v; want NOTCOORD", res.Refusal, err)
	}

	empty := newWorld(t)
	if _, _, err := empty.cfg.Update(ctx, "sprint", "sprint", map[string]string{"coordinator": ""}, "test"); err != nil {
		t.Fatal(err)
	}
	_, err = Init(ctx, empty.env, InitReq{Config: empty.cfg})
	if code, _ := refusedCode(t, err); code != sprintfn.CodeRequest {
		t.Fatalf("init with no coordinator in nova-config: %s, want REQUEST", code)
	}
}

// TestGoalVerbsRefusedUntilThePartCarriesGoals: goal set and goal drop send
// the design's step (the sprint part's goals), which IT16's sprint part
// refuses REQUEST until it writes {p}goal:<person>; the refusal is not retried
// and says so. goal show has no read yet and sends nothing. When the part
// carries goals, this test is the one to turn around.
func TestGoalVerbsRefusedUntilThePartCarriesGoals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.initSprint()
	f := &faulty{c: w.tw}
	w.env.C = f
	_, err := GoalSet(ctx, w.env, GoalReq{Person: "p1", Goal: "ship the verbs"})
	var rf *Refused
	if !errors.As(err, &rf) || rf.Code() != sprintfn.CodeRequest || rf.Local || !strings.Contains(rf.Hint, "goals") {
		t.Fatalf("goal set: %v; want the write path's REQUEST, named", err)
	}
	if f.steps != 1 || f.sent[0].Sprint == nil || f.sent[0].Sprint.Goals["p1"] != "ship the verbs" {
		t.Fatalf("goal set sent %d steps, want one carrying the goal", f.steps)
	}
	if _, err := GoalDrop(ctx, w.env, GoalReq{Person: "p1"}); err == nil {
		t.Fatal("goal drop applied on a write path that carries no goal")
	}
	if _, err := GoalShow(ctx, w.env, GoalReq{Person: "p1"}); err == nil || f.steps != 2 {
		t.Fatalf("goal show: %v, steps %d; want a local refusal and no step", err, f.steps)
	}
	if _, err := GoalSet(ctx, w.env, GoalReq{Person: "no one", Goal: "x"}); err == nil {
		t.Fatal("goal set took a name that is not one")
	}
}

// ---- the store tier's skip rule

// missingLogReason is why the store tier skips: the one cause it skips for.
const missingLogReason = "Layer 2's fragment lua/table_set_log.lua not landed"

// missingTSetLogFragment is the store tier's one skip cause, the strict
// single-cause classifier Layer 1's tests use (internal/tset's
// TestMissingTSetLogFragmentClassifier): the composed source's assembly
// failed because lua/table_set_log.lua does not exist, and for no other
// reason. Another missing fragment, an empty one, a permission error or a
// load error stays a failure.
func missingTSetLogFragment(err error) bool {
	var pathErr *fs.PathError
	return errors.As(err, &pathErr) && pathErr.Path == "lua/table_set_log.lua" &&
		errors.Is(pathErr.Err, fs.ErrNotExist)
}

// TestMissingTSetLogFragmentClassifier holds the skip rule to its one cause,
// case for case with Layer 1's.
func TestMissingTSetLogFragmentClassifier(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"present successful source", nil, false},
		{"bare missing error without path", fs.ErrNotExist, false},
		{"wrapped real log absence", fmt.Errorf("read lua/table_set_log.lua: %w",
			&fs.PathError{Op: "open", Path: "lua/table_set_log.lua", Err: fs.ErrNotExist}), true},
		{"another missing fragment", fmt.Errorf("read lua/table_set_read.lua: %w",
			&fs.PathError{Op: "open", Path: "lua/table_set_read.lua", Err: fs.ErrNotExist}), false},
		{"permission error", &fs.PathError{Op: "open", Path: "lua/table_set_log.lua", Err: fs.ErrPermission}, false},
		{"unrelated absence alongside log permission", errors.Join(
			&fs.PathError{Op: "open", Path: "lua/table_set_log.lua", Err: fs.ErrPermission},
			&fs.PathError{Op: "open", Path: "lua/table_set_read.lua", Err: fs.ErrNotExist}), false},
		{"present but empty", errors.New("empty function file lua/table_set_log.lua"), false},
		{"present but malformed", errors.New("ERR Error compiling function: syntax error"), false},
		{"present but load rejected", errors.New("ERR Error registering function: duplicate name"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := missingTSetLogFragment(tc.err); got != tc.want {
				t.Errorf("missingTSetLogFragment(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
