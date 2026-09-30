package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/machine"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// jrRecorder keeps every read a verb sends, to hold what it reads.
type jrRecorder struct {
	C     sprintfn.Client
	mu    sync.Mutex
	reads []*sprintfn.ReadRequest
}

func (r *jrRecorder) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	r.mu.Lock()
	for _, it := range items {
		if it.Read != nil {
			r.reads = append(r.reads, it.Read)
		}
	}
	r.mu.Unlock()
	return r.C.Pipeline(ctx, items)
}

// heartbeat writes the heartbeat's fields as the loop that holds the lease
// writes them (the lease part, 1.4.1): a take of the lease with the fields.
func (w *jrWorld) heartbeat(fields map[string]string, stopped bool) {
	w.t.Helper()
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "tick", Tick: true},
		Lease: &sprintfn.LeasePart{Owner: "loop-token", Name: "the loop", HoldMS: 60_000, Heartbeat: fields, Stopped: stopped}})
}

// ms is a time in milliseconds as a field writes it.
func ms(n int64) string { return strconv.FormatInt(n, 10) }

// groupOf is the group of a type, false when the inbox has none.
func groupOf(v InboxView, typ string) (sprint.Group, bool) {
	for _, g := range v.Groups {
		if g.Type == typ {
			return g, true
		}
	}
	return sprint.Group{}, false
}

// TestInboxListsNotesNotSubjects: a need with 100,000 waiters is 50 notes of
// 2,000 (J cuts a note there, 1.3.4), and the inbox reads the 50 notes, never
// the 100,000 subjects (decision 56: O(notes)): its reads are the page of
// {p}jnotes@e, the heartbeat and the clock, then one line a note; no read
// names a subject. The notes of one cause are one group, counting every
// waiter and listing the first MaxListed. Two round trips.
func TestInboxListsNotesNotSubjects(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	const notes, per = 50, 2000
	for k := 0; k < notes; k++ {
		subjects := make([]string, per)
		for i := range subjects {
			subjects[i] = "w" + strconv.Itoa(k*per+i)
		}
		w.open(typeBlockedMissing, "ghost", subjects...)
	}
	rec := &jrRecorder{C: w.tw}
	cc := &Counting{C: rec}
	env := &Env{C: cc, Names: jrNames, Actor: jrCoord, noWait: true}
	var v InboxView
	res, err := Inbox(context.Background(), env, InboxReq{Out: &v})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trips != 2 || cc.Trips() != 2 || cc.Steps() != 0 {
		t.Fatalf("inbox took %d round trips (counted %d) and %d steps, want 2 and none", res.Trips, cc.Trips(), cc.Steps())
	}
	if len(rec.reads) != 2 {
		t.Fatalf("inbox sent %d reads, want 2", len(rec.reads))
	}
	first, second := rec.reads[0], rec.reads[1]
	for _, q := range first.Sprint {
		if q.Kind != sprintfn.KeyHeartbeat && q.Kind != sprintfn.KeyClock {
			t.Fatalf("the first read asks %s: the inbox reads the heartbeat and the clock of the sprint's keys, and nothing of a subject", q.Kind)
		}
	}
	if len(second.Sprint) != 0 || len(second.Tset) != notes {
		t.Fatalf("the second read asks %d sprint queries and %d of Layer 2, want none and one line a note (%d)", len(second.Sprint), len(second.Tset), notes)
	}
	for _, q := range second.Tset {
		if q.Kind != "lines" || q.Limit != 1 {
			t.Fatalf("the second read asks %+v, want one line by its seq", q)
		}
	}
	g, ok := groupOf(v, typeBlockedMissing)
	if !ok {
		t.Fatalf("no group of %q in %+v", typeBlockedMissing, v.Groups)
	}
	if g.Count != notes*per || len(g.Notes) != notes || len(g.Primaries) != sprint.MaxListed || g.Size != notes*per {
		t.Fatalf("the group counts %d subjects in %d notes, lists %d, size %d; want %d in %d, %d listed", g.Count, len(g.Notes),
			len(g.Primaries), g.Size, notes*per, notes, sprint.MaxListed)
	}
	if len(g.Decisions) != 1 || g.Decisions[0] != "drop w" {
		t.Fatalf("the group prints %v: with no card read, only the decision with no condition (drop w) is printed", g.Decisions)
	}
}

// TestInboxComputedGroups: inbox computes the machine's groups from the
// heartbeat and the clock at read time, with no loop running (1.4.1): no run
// loop is looking (STOPPED, looked_at older than 15 s, or never); the machine
// is not ticking (RUNNING, tick_at older than 15 s); the tick keeps failing
// (failures at least 3, showing the error). A loop that ticks on time shows
// none.
func TestInboxComputedGroups(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	inbox := func() InboxView {
		t.Helper()
		var v InboxView
		if _, err := Inbox(context.Background(), w.env, InboxReq{Out: &v}); err != nil {
			t.Fatal(err)
		}
		return v
	}
	types := func(v InboxView) string {
		var out []string
		for _, g := range v.Groups {
			switch g.Type { // IT17's groups: the id is the type
			case machine.GroupNotTicking, machine.GroupNoLoop, machine.GroupFailing:
				out = append(out, g.Type)
			}
		}
		return strings.Join(out, ", ")
	}

	// STOPPED, and no loop has ever looked.
	if got := types(inbox()); got != machine.GroupNoLoop {
		t.Fatalf("a STOPPED sprint no loop looks at shows %q, want %q", got, machine.GroupNoLoop)
	}
	// A loop looks while STOPPED: nothing, until 15 s pass without a look.
	w.heartbeat(nil, true)
	if got := types(inbox()); got != "" {
		t.Fatalf("a STOPPED sprint a loop looked at now shows %q", got)
	}
	w.tick(16 * time.Second)
	if got := types(inbox()); got != machine.GroupNoLoop {
		t.Fatalf("16 s after the last look: %q, want %q", got, machine.GroupNoLoop)
	}

	// RUNNING and ticking: nothing; 16 s without a tick: not ticking.
	w.start()
	w.heartbeat(map[string]string{"tick_at": ms(w.wall()), "ticks": "1"}, false)
	if got := types(inbox()); got != "" {
		t.Fatalf("a machine that just ticked shows %q", got)
	}
	w.tick(16 * time.Second)
	v := inbox()
	if got := types(v); got != machine.GroupNotTicking {
		t.Fatalf("16 s after the last tick: %q, want %q", got, machine.GroupNotTicking)
	}
	if g, _ := groupOf(v, machine.GroupNotTicking); len(g.Commands) != 2 || g.Commands[0].Lines[0] != "nova-sprint run" || g.Commands[1].Lines[0] != "nova-sprint stop" {
		t.Fatalf("the not ticking group's commands are %+v, want run and stop", g.Commands)
	}

	// Ticking, and failing three times in a row.
	w.heartbeat(map[string]string{"tick_at": ms(w.wall()), "failures": "3", "error": "LIMIT on deal"}, false)
	v = inbox()
	if got := types(v); got != machine.GroupFailing {
		t.Fatalf("three failed ticks: %q, want %q", got, machine.GroupFailing)
	}
	if g, _ := groupOf(v, machine.GroupFailing); !strings.Contains(g.What, "LIMIT on deal") {
		t.Fatalf("the failing group does not show the error: %q", g.What)
	}
	if v.Machine != "running" {
		t.Fatalf("the machine line %q: a machine ticking on time is running, and the failing group, not the line, names the error", v.Machine)
	}
}

// TestInboxReadRefused: inbox --read is refused until the write path carries
// the coordinator's cursor, and nothing is read.
func TestInboxReadRefused(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.cc.Reset()
	if _, err := Inbox(context.Background(), w.env, InboxReq{Read: true}); codeOf(err) != sprintfn.CodeRequest || w.cc.Trips() != 0 {
		t.Fatalf("inbox --read: %v after %d round trips, want a local REQUEST", err, w.cc.Trips())
	}
}

// fleetRows adds members' rows to the fleet table, 100 rows a step (the
// builder's rows bound), each member with a control card when ctl is set.
func (w *jrWorld) fleetRows(n int, ctl bool) []string {
	w.t.Helper()
	var names []string
	for i := 0; i < n; i++ {
		names = append(names, "m"+strconv.Itoa(i))
	}
	for from := 0; from < n; from += 100 {
		chunk := names[from:min(from+100, n)]
		req := &sprintfn.Request{Meta: sprintfn.Meta{Verb: "fleet up"}, Body: sprintfn.Body{Entries: []tset.Entry{{Kind: "rows", Table: sprint.Fleet, Add: chunk}}}}
		w.step(req)
		if ctl {
			req := &sprintfn.Request{Meta: sprintfn.Meta{Verb: "fleet up"}}
			for _, m := range chunk {
				id := sprint.CtlID(m)
				req.Body.Entries = append(req.Body.Entries, tset.Entry{Kind: "create", Table: sprint.Fleet, To: m + ":" + sprint.Ctl,
					IDs: []string{id}, About: []string{id}, Scores: []string{"0"}, Set: map[string]string{"kind": "member", "status": "up"}})
			}
			w.step(req)
		}
	}
	return names
}

// streamRow adds a stream's rows to the work and merge tables, with its
// control card.
func (w *jrWorld) streamRow(s string) {
	w.t.Helper()
	id := sprint.CtlID(s)
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "add"}, Body: sprintfn.Body{Entries: []tset.Entry{
		{Kind: "rows", Table: sprint.Work, Add: []string{s}}, {Kind: "rows", Table: sprint.Merge, Add: []string{s}},
		{Kind: "create", Table: sprint.Merge, To: s + ":" + sprint.Ctl, IDs: []string{id}, About: []string{id}, Scores: []string{"0"},
			Set: map[string]string{"kind": "stream", "state": sprint.StreamWaiting}},
	}}})
}

// where runs where with the streams given, and returns its view and result.
func (w *jrWorld) where(rows WhereRows) (WhereView, Result) {
	w.t.Helper()
	var v WhereView
	res, err := Where(context.Background(), w.env, WhereReq{Rows: rows, Out: &v})
	if err != nil {
		w.t.Fatal(err)
	}
	return v, res
}

// TestWhereViewOnlyTables: the view draws the four tables and one line and
// nothing else (1.4.1): no heartbeat field, counter, lease, backlog or
// dropping mark is in it (they are in where --json); a table with no rows is
// hidden, and so is a stream with no cards; the line is STOPPED while the
// machine is.
func TestWhereViewOnlyTables(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.admit(jrCard{id: "p1", col: "ready"}, jrCard{id: "p2", col: "landed"})
	w.streamRow("idle")
	w.fleetRows(2, true)
	w.heartbeat(map[string]string{"tick_at": ms(w.wall()), "ticks": "9", "backlog": "4", "agenda": "7", "rules": "deal=3"}, true)
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "drop"}, Sprint: &sprintfn.SprintPart{Dropping: map[string]string{"idle": "op-x"}}})
	v, _ := w.where(WhereRows{})
	lines := strings.Split(v.Frame, "\n")
	if len(lines) < 5 || lines[2] != "SPRINT TABLE" || lines[4] != "STOPPED" {
		t.Fatalf("the view's head is %q, want the title and the line STOPPED", lines[:min(5, len(lines))])
	}
	for _, word := range []string{"heartbeat", "tick_at", "lease", "loop-token", "the loop", "backlog", "deal=3", "op-x", "gen", "catching"} {
		if strings.Contains(v.Frame, word) {
			t.Fatalf("the view shows %q:\n%s", word, v.Frame)
		}
	}
	if !strings.Contains(v.Frame, "work") || !strings.Contains(v.Frame, "fleet") {
		t.Fatalf("the view lacks the work or fleet table:\n%s", v.Frame)
	}
	if strings.Contains(v.Frame, "readers") {
		t.Fatalf("the readers table has no rows and is shown:\n%s", v.Frame)
	}
	if strings.Contains(v.Frame, "idle") || strings.Contains(v.Frame, "merge") {
		t.Fatalf("a stream with no cards, or the merge table with no card counted, is shown:\n%s", v.Frame)
	}
	if v.Landed != 1 || v.All != 2 || v.Summary != "1/2 50.0% -> ETA" {
		t.Fatalf("the summary is %q (%d/%d), want 1/2 50.0%%", v.Summary, v.Landed, v.All)
	}
	if got := v.Tables[sprint.Work]["s1"]["ready"]; got != "1" {
		t.Fatalf("the work table's s1:ready is %q, want 1", got)
	}
}

// TestWhereLineNotTicking: a RUNNING machine that has not ticked for 7 s reads
// NOT TICKING 7s on the view's line: its state is RUNNING, so the line never
// says STOPPED for it (1.4.1). Within 5 s the line is the summary.
func TestWhereLineNotTicking(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.admit(jrCard{id: "p1", col: "ready"})
	w.start()
	w.heartbeat(map[string]string{"tick_at": ms(w.wall())}, false)
	w.tick(2 * time.Second)
	v, _ := w.where(WhereRows{})
	if line := strings.Split(v.Frame, "\n")[4]; line != v.Summary {
		t.Fatalf("2 s after a tick the line is %q, want the summary %q", line, v.Summary)
	}
	w.tick(5 * time.Second)
	v, _ = w.where(v.Rows)
	line := strings.Split(v.Frame, "\n")[4]
	if line != "NOT TICKING 7s" || strings.Contains(v.Frame, "STOPPED") {
		t.Fatalf("7 s after a tick the line is %q, want NOT TICKING 7s", line)
	}
	if !v.MachineDetail.Running || v.Machine != "NOT TICKING 7s" {
		t.Fatalf("the machine is %q, running %v", v.Machine, v.MachineDetail.Running)
	}
}

// TestWhereJSONCarriesMachine: where --json carries what the view leaves out
// (1.4.1): the machine's line, and beside it the heartbeat's fields, the
// clock, the lease, the cursor and the backlog, the due count, the parked
// keys and the dropping marks; the command's keys are all there.
func TestWhereJSONCarriesMachine(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.admit(jrCard{id: "p1", col: "ready"})
	w.heartbeat(map[string]string{"tick_at": ms(w.wall()), "ticks": "12", "rules": "deal=3"}, true)
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "drop"}, Sprint: &sprintfn.SprintPart{Dropping: map[string]string{"s1": "op-x"},
		Park: []sprintfn.ParkedKey{{Key: "deal", Rule: "deal", Code: "LIMIT"}}}})
	v, _ := w.where(WhereRows{Work: []string{"s1"}})
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"at", "landed", "all", "summary", "tables", "streams", "epoch", "machine", "machine_detail"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("where --json lacks %q: %s", k, b)
		}
	}
	m := v.MachineDetail
	if v.Machine != "STOPPED" || m.Running || m.Clock["stopped_since_ms"] == "" {
		t.Fatalf("the machine is %q, running %v, clock %v", v.Machine, m.Running, m.Clock)
	}
	if m.Heartbeat["ticks"] != "12" || m.Heartbeat["rules"] != "deal=3" || m.Lease["name"] != "the loop" || m.Lease["gen"] != "1" {
		t.Fatalf("the heartbeat %v or the lease %v is not carried", m.Heartbeat, m.Lease)
	}
	if m.Dropping["s1"] != "op-x" || m.DropCount != 1 || m.Parked != 1 {
		t.Fatalf("dropping %v (%d), parked %d: want s1 by op-x and one parked key", m.Dropping, m.DropCount, m.Parked)
	}
	if m.Backlog == "" || m.R == "" {
		t.Fatalf("the backlog %q or R %q is not carried", m.Backlog, m.R)
	}
}

// TestWhereRoundTrips pins where's round trips on the twin: one read when the
// rows of the work and merge tables are the ones the request names (the view
// before's), two when they are not (the first finds the rows, the second
// reads their counts in one snapshot); at 250 members, one read, the fleet
// query counting every member's cells.
func TestWhereRoundTrips(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.admit(jrCard{id: "p1", col: "ready"})
	members := w.fleetRows(sprintfn.SprintMembersMax-1, true)
	w.cc.Reset()
	v, res := w.where(WhereRows{})
	if res.Trips != 2 || w.cc.Trips() != 2 {
		t.Fatalf("a first where took %d round trips (counted %d), want 2", res.Trips, w.cc.Trips())
	}
	w.cc.Reset()
	v, res = w.where(v.Rows)
	if res.Trips != 1 || w.cc.Trips() != 1 || w.cc.Steps() != 0 {
		t.Fatalf("where with the rows it saw took %d round trips (counted %d), want 1", res.Trips, w.cc.Trips())
	}
	if len(v.Tables[sprint.Fleet]) != len(members) {
		t.Fatalf("the fleet table has %d rows, want %d", len(v.Tables[sprint.Fleet]), len(members))
	}
	// A new stream since: the rows moved, and where reads again.
	w.streamRow("s2")
	w.cc.Reset()
	_, res = w.where(v.Rows)
	if res.Trips != 2 {
		t.Fatalf("where after a new stream took %d round trips, want 2", res.Trips)
	}
}

// TestCardShowsQuarantine: card of a quarantined card says so (1.3.5): every
// read leaves it out, so its record is not read; a card that is not
// quarantined shows its record and what related follows from it. One round
// trip each.
func TestCardShowsQuarantine(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.admit(jrCard{id: "p1", col: "ready"}, jrCard{id: "p2", col: "ready"})
	q := sprintfn.Quarantined{ID: "p1", Stream: "s1", Code: "DRIFT", Rule: "deal", Cells: []string{"s1:ready"}}
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "tick"}, Sprint: &sprintfn.SprintPart{Quarantine: []sprintfn.Quarantined{q}},
		Body: sprintfn.Body{Quarantine: []sprintfn.Quarantined{q}}})
	w.cc.Reset()
	var v CardView
	res, err := Card(context.Background(), w.env, CardReq{ID: "p1", Out: &v})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trips != 1 || !v.Quarantined || v.Record != nil || !strings.Contains(res.Said, "quarantined") {
		t.Fatalf("card of a quarantined card: %d round trips, %+v, %q", res.Trips, v, res.Said)
	}
	// What the design's row shows of the quarantine record (code, rule, cells)
	// has no read yet: the card says what it cannot show, and why.
	for _, want := range []string{"code, rule and cells are not shown", "no read of the quarantine record", "IT30"} {
		if !strings.Contains(res.Said, want) || !strings.Contains(v.NotShown, want) {
			t.Fatalf("card of a quarantined card does not say %q: %q, not shown %q", want, res.Said, v.NotShown)
		}
	}
	var v2 CardView
	res, err = Card(context.Background(), w.env, CardReq{ID: "p2", Out: &v2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trips != 1 || v2.Quarantined || v2.Record == nil || !v2.Record.Exists || v2.Record.Place.Col != "ready" || v2.Follows == nil {
		t.Fatalf("card of p2: %d round trips, %+v", res.Trips, v2)
	}
	if _, err := Card(context.Background(), w.env, CardReq{ID: "not a card"}); codeOf(err) != sprintfn.CodeRequest {
		t.Fatalf("card of a bad id: %v, want REQUEST", err)
	}
}

// TestLogPagesAndStream: log --since reads a page of lines after the seq, one
// round trip a page, and says where the next begins; --stream keeps the
// stream's lines.
func TestLogPagesAndStream(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.admit(jrCard{id: "p1", col: "ready"})
	w.streamRow("s2")
	w.open(typeStepRefused, "LIMIT", "deal")
	w.cc.Reset()
	var v LogView
	res, err := Log(context.Background(), w.env, LogReq{Since: 0, Limit: 2, Out: &v})
	if err != nil {
		t.Fatal(err)
	}
	if res.Trips != 1 || len(v.Lines) != 2 || v.Next != 2 || v.Exhausted {
		t.Fatalf("a page of 2: %d round trips, %d lines, next %d, exhausted %v", res.Trips, len(v.Lines), v.Next, v.Exhausted)
	}
	var all LogView
	if _, err := Log(context.Background(), w.env, LogReq{Since: v.Next, Out: &all}); err != nil {
		t.Fatal(err)
	}
	if !all.Exhausted || len(all.Lines) == 0 {
		t.Fatalf("the rest: %d lines, exhausted %v", len(all.Lines), all.Exhausted)
	}
	var s2 LogView
	if _, err := Log(context.Background(), w.env, LogReq{Stream: "s2", Out: &s2}); err != nil {
		t.Fatal(err)
	}
	for _, l := range s2.Lines {
		if !strings.Contains(string(l), "s2") {
			t.Fatalf("log --stream s2 kept a line of another stream: %s", l)
		}
	}
	if len(s2.Lines) == 0 {
		t.Fatalf("log --stream s2 kept no line")
	}
	if _, err := Log(context.Background(), w.env, LogReq{Card: "p1", Stream: "s1"}); codeOf(err) != sprintfn.CodeRequest {
		t.Fatalf("log --card and --stream: %v, want REQUEST", err)
	}
}

// TestInboxPagesAtAHundredFullNotes: the inbox's second read reads one line a
// note, and a note's line names up to 2,000 subjects, so a page is at most 100
// notes: 200,000 ids, Layer 2's bound on one read (L2 4), which an atomic
// read over it refuses BUDGET whole (the twin does not charge it). With 101
// full notes open the page is 100 and More says the rest lie past it.
func TestInboxPagesAtAHundredFullNotes(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	const notes, per = InboxPage + 1, 2000
	for k := 0; k < notes; k++ {
		subjects := make([]string, per)
		for i := range subjects {
			subjects[i] = "w" + strconv.Itoa(k*per+i)
		}
		w.open(typeBlockedMissing, "ghost", subjects...)
	}
	rec := &jrRecorder{C: w.tw}
	env := &Env{C: rec, Names: jrNames, Actor: jrCoord, noWait: true}
	var v InboxView
	if _, err := Inbox(context.Background(), env, InboxReq{Limit: 1000, Out: &v}); err != nil {
		t.Fatal(err)
	}
	if len(rec.reads) != 2 || len(rec.reads[1].Tset) != InboxPage {
		t.Fatalf("inbox read %d times, the second with %d lines queries; want 2 and %d", len(rec.reads), len(rec.reads[1].Tset), InboxPage)
	}
	g, _ := groupOf(v, typeBlockedMissing)
	if !v.More || len(g.Notes) != InboxPage || g.Count != InboxPage*per || g.Count > 200_000 {
		t.Fatalf("a page of %d notes naming %d subjects, more %v; want %d notes, at most 200,000 ids, more", len(g.Notes), g.Count, v.More, InboxPage)
	}
}

// TestInboxNoticesMore: the lines after the cursor are read a page at a time
// (1,000 lines); with more after them the inbox says so, and Last is the last
// line read, so the next inbox from it reads the rest.
func TestInboxNoticesMore(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	first := w.lastSeq()
	for step := 0; step < 11; step++ {
		var ns []sprintfn.NoteReq
		for i := 0; i < 100; i++ {
			k := strconv.Itoa(step*100 + i)
			ns = append(ns, sprintfn.NoteReq{Op: sprintfn.JOpOpen, Type: typeStepRefused, Cause: "c" + k, Subjects: []string{"k" + k}, Text: "raised by the test"})
		}
		w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "test", Rule: "test"}, Body: sprintfn.Body{Notes: ns}})
	}
	var v InboxView
	res, err := Inbox(context.Background(), w.env, InboxReq{After: first, Out: &v})
	if err != nil {
		t.Fatal(err)
	}
	if !v.NoticesMore || v.Last != strconv.FormatUint(first+inboxNoticeLines, 10) || !strings.Contains(res.Said, "more=notices") {
		t.Fatalf("1,100 lines after the cursor: notices more %v, last %s (want %d), said %q", v.NoticesMore, v.Last, first+inboxNoticeLines, res.Said)
	}
	var rest InboxView
	if _, err := Inbox(context.Background(), w.env, InboxReq{After: first + inboxNoticeLines, Out: &rest}); err != nil {
		t.Fatal(err)
	}
	if rest.NoticesMore || rest.Last != strconv.FormatUint(first+1100, 10) {
		t.Fatalf("the rest: notices more %v, last %s, want none past %d", rest.NoticesMore, rest.Last, first+1100)
	}
}

// lastSeq is the log's last seq at epoch 0.
func (w *jrWorld) lastSeq() uint64 {
	w.t.Helper()
	rd := w.read(&sprintfn.ReadRequest{Tset: []tset.ReadQuery{{Kind: "last"}}})
	n, _ := strconv.ParseUint(string(rd.Tset[0].LastSeq), 10, 64)
	return n
}

// TestInboxCountsRaisedOn: a judgment's group counts the subjects its notes
// were raised on, read in O(notes); a subject answered since is among them,
// so the inbox prints the count as "raised on", never as open (which subjects
// are still open is each one's jopen, O(subjects): IT30's notes-only jnote).
func TestInboxCountsRaisedOn(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	note := w.open(typeStepRefused, "LIMIT", "k1", "k2", "k3")
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "test", Rule: "test"}, Body: sprintfn.Body{
		Notes: []sprintfn.NoteReq{{Op: sprintfn.JOpClose, Type: typeStepRefused, Cause: "LIMIT", Subjects: []string{"k1", "k2"}, Text: "closed"}}}})
	var v InboxView
	res, err := Inbox(context.Background(), w.env, InboxReq{Out: &v})
	if err != nil {
		t.Fatal(err)
	}
	g, ok := groupOf(v, typeStepRefused)
	if !ok || g.Count != 3 || len(g.Notes) != 1 || g.Notes[0] != note {
		t.Fatalf("the group of %s: %+v", note, g)
	}
	if !strings.Contains(res.Said, "raised on 3") || strings.Contains(res.Said, typeStepRefused+"  x3") {
		t.Fatalf("the inbox prints the count as open: %q", res.Said)
	}
}

// TestLogAfterClearReadsTheActiveEpoch: log on an Env that has not seen a
// clear reads the active epoch's lines, not the cleared epoch's: the page
// names the active epoch, and the page is read again there, one round trip
// more; the Env's epoch moves with it.
func TestLogAfterClearReadsTheActiveEpoch(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	w.admit(jrCard{id: "p1", col: "ready"})
	if _, err := Clear(context.Background(), w.env, ClearReq{Confirm: jrPrefix}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	fresh := &Env{C: w.cc, Names: jrNames, Actor: jrCoord, noWait: true}
	w.cc.Reset()
	var v LogView
	res, err := Log(context.Background(), fresh, LogReq{Out: &v})
	if err != nil {
		t.Fatal(err)
	}
	now := w.read(&sprintfn.ReadRequest{Epoch: "1", Tset: []tset.ReadQuery{{Kind: "lines", AfterSeq: "0", Limit: 5000}}}).Tset[0].Lines
	if v.Epoch != 1 || fresh.Epoch != 1 || res.Epoch != 1 || res.Trips != 2 || len(v.Lines) != len(now) {
		t.Fatalf("log after a clear: epoch %d (env %d), %d round trips, %d lines; want epoch 1's %d lines in 2",
			v.Epoch, fresh.Epoch, res.Trips, len(v.Lines), len(now))
	}
	for i := range now {
		if string(v.Lines[i]) != string(now[i]) {
			t.Fatalf("line %d is %s, want epoch 1's %s", i+1, v.Lines[i], now[i])
		}
	}
	w.cc.Reset()
	if res, err := Log(context.Background(), fresh, LogReq{Out: &v}); err != nil || res.Trips != 1 {
		t.Fatalf("log on a warm Env: %v, %d round trips, want 1", err, res.Trips)
	}
}

// jrCardPager serves Layer 2's cardlines in page mode for one card (L2 4):
// the card's history of n lines, pages of at most 500 across the cursor, which
// carries the high-water the first page took. It refuses LIMIT a page over
// 500, as Layer 1's plan check does, and passes every other item to the twin.
// The write path's client and the twin do not serve cardlines pages yet: this
// stands for the store, to drive log --card's paging.
type jrCardPager struct {
	C     sprintfn.Client
	about string
	n     int
	mu    sync.Mutex
	pages []tset.ReadQuery
}

func (p *jrCardPager) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	if len(items) != 1 || items[0].Page == nil || items[0].Page.Queries[0].Kind != "cardlines" {
		return p.C.Pipeline(ctx, items)
	}
	plan := items[0].Page
	q := plan.Queries[0]
	p.mu.Lock()
	p.pages = append(p.pages, q)
	p.mu.Unlock()
	if err := tset.ValidateReadPlan(*plan); err != nil {
		code := sprintfn.CodeRequest
		var r *tset.Refusal
		if errors.As(err, &r) {
			code = r.Code
		}
		return []sprintfn.Result{{Refusal: &sprintfn.Refusal{Code: code, Message: err.Error()}}}, nil
	}
	from, through := int64(0), int64(p.n-1)
	if q.Cursor != nil {
		from, through = q.Cursor.Positions[0].NextIndex, q.Cursor.Positions[0].ThroughIndex
	}
	rep := tset.ReadReply{Status: "page", Epoch: plan.Epoch, ActiveEpoch: plan.Epoch, Items: []json.RawMessage{}}
	i := from
	for ; i <= through && len(rep.Items) < q.Limit; i++ {
		rep.Items = append(rep.Items, json.RawMessage(`{"seq":"`+strconv.FormatInt(i+1, 10)+`","kind":"note","about":["`+p.about+`"]}`))
	}
	rep.Exhausted = i > through
	if !rep.Exhausted {
		c := tset.CardCursor{Epoch: plan.Epoch, Fields: []string{}, IncludeMeta: q.IncludeMeta,
			Positions: []tset.CardCursorPosition{{About: p.about, NextIndex: i, ThroughIndex: through}}}
		b, _ := json.Marshal(c)
		rep.Next = b
	}
	return []sprintfn.Result{{Page: &rep}}, nil
}

// TestLogCardPages: log --card reads the card's lines through cardlines in
// pages of at most 500 (L1 7; L2 4), each page's cursor the next's, one round
// trip a page, until the page that reaches the high-water. A page asked over
// 500 is cut to 500, never sent to be refused LIMIT.
func TestLogCardPages(t *testing.T) {
	t.Parallel()
	w := newJRWorld(t)
	pager := &jrCardPager{C: w.tw, about: "p1", n: 1_234}
	cc := &Counting{C: pager}
	env := &Env{C: cc, Names: jrNames, Actor: jrCoord, noWait: true}
	var got []json.RawMessage
	var cursor *tset.CardCursor
	pages := 0
	for {
		var v LogView
		res, err := Log(context.Background(), env, LogReq{Card: "p1", CardCursor: cursor, Limit: 5000, Out: &v})
		if err != nil {
			t.Fatalf("page %d: %v", pages+1, err)
		}
		pages++
		if res.Trips != 1 {
			t.Fatalf("page %d took %d round trips, want 1", pages, res.Trips)
		}
		got = append(got, v.Lines...)
		if v.Exhausted {
			if v.CardNext != nil {
				t.Fatalf("the last page carries a cursor")
			}
			break
		}
		if v.CardNext == nil || pages > 10 {
			t.Fatalf("page %d is not the last and carries no cursor", pages)
		}
		cursor = v.CardNext
	}
	if pages != 3 || len(got) != pager.n {
		t.Fatalf("log --card read %d lines in %d pages, want %d in 3", len(got), pages, pager.n)
	}
	for i, q := range pager.pages {
		if q.Limit != LogCardPage || len(q.Abouts) != 1 || q.Abouts[0] != "p1" || (i == 0) != (q.Cursor == nil) {
			t.Fatalf("page %d asked %+v, want cardlines of p1, limit %d, the cursor after the first", i+1, q, LogCardPage)
		}
	}
	if string(got[pager.n-1]) != `{"seq":"1234","kind":"note","about":["p1"]}` {
		t.Fatalf("the last line is %s", got[pager.n-1])
	}
	var v LogView
	if _, err := Log(context.Background(), env, LogReq{Card: "p1", Out: &v}); err != nil || len(v.Lines) != LogCardPage {
		t.Fatalf("log --card with the default page: %v, %d lines, want a page of %d", err, len(v.Lines), LogCardPage)
	}
}
