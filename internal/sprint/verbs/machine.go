package verbs

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The machine's verbs (section 3's table): init, init --coordinator, start,
// stop and goal are each one step through Do, two round trips: its read, then
// its step. clear runs in parts through Parts, n + 1 round trips, one part
// for a sprint of at most 247 members and streams.

// The size of the sprint (section 3, "Size of the sprint"; F1-20): members
// and streams together at most MembersAndStreamsMax, and members + readers +
// 2 x streams at most RowsMax. clear restores every row in its first part,
// inside RowsMax, and creates the control cards in as many parts as they
// need, so any sprint the verbs can build can be cleared (one step at 250 members
// and streams would be 259 entries, over 256).
const (
	MembersAndStreamsMax = sprintfn.SprintMembersMax // 250
	RowsMax              = tset.MaxRowsWithAdvance   // 1,024 rows in a step with advance (L1 6)
)

// EntriesMax is a step's entries (L1 6; 1.0's bounds table).
const EntriesMax = 256

// SizeBounds refuses a sprint of members, readers and streams past either
// bound of section 3, naming the bound. fleet up, reader add and add (IT19,
// IT20) must call SizeBounds before they write, since clear holds a sprint to
// the rows bound alone (clearRows) and so clears only a sprint built within
// these bounds.
func SizeBounds(members, readers, streams int) error {
	if members+streams > MembersAndStreamsMax {
		return fmt.Errorf("members and streams together are %d, over the sprint's %d (members %d, streams %d)",
			members+streams, MembersAndStreamsMax, members, streams)
	}
	if rows := members + readers + 2*streams; rows > RowsMax {
		return fmt.Errorf("members + readers + 2 x streams is %d, over the sprint's %d (members %d, readers %d, streams %d)",
			rows, RowsMax, members, readers, streams)
	}
	return nil
}

// ConfigRows is what the machine's verbs read of nova-config: one row by kind
// and name (config.Store has it).
type ConfigRows interface {
	Get(ctx context.Context, kind, name string) (config.Row, bool, error)
}

// Coordinator is the coordinator nova-config names now: the sprint row's
// coordinator field, a friend (config.KindSprint). The design's verb table
// says "nova-config's fleet row"; in nova-config the fleet row's coordinator
// is a machine and the sprint row's is the friend who coordinates, and a
// verb's actor is the friend, so the sprint row is the one read.
func Coordinator(ctx context.Context, rows ConfigRows) (string, error) {
	if rows == nil {
		return "", fmt.Errorf("no nova-config to read the coordinator from")
	}
	row, found, err := rows.Get(ctx, config.KindSprint, config.KindSprint)
	if err != nil {
		return "", fmt.Errorf("reading nova-config's sprint row: %w", err)
	}
	if !found {
		return "", fmt.Errorf("nova-config has no sprint row")
	}
	return row.Fields["coordinator"], nil
}

// InitReq is init's and init --coordinator's request: the op, and the
// nova-config the coordinator is read from.
type InitReq struct {
	Op     string
	Config ConfigRows
	// Coordinator is the name the command gives (init --coordinator <name>,
	// else its actor). With Config nil it is the coordinator; with Config
	// set, nova-config's is, and a name that is not it is refused (the IT23
	// grammar decisions, 9 to 12).
	Coordinator string
}

// coordinatorOf is the coordinator an init names: nova-config's when the
// request carries it (and the given name, when there is one, must be it),
// else the given name.
func coordinatorOf(ctx context.Context, verb string, req InitReq) (string, error) {
	if req.Config == nil {
		if req.Coordinator == "" {
			return "", refuseLocal(verb, sprintfn.CodeRequest, "names no coordinator: give --coordinator <name>, or nova-config's store (--pg)")
		}
		if !sprint.ValidID(req.Coordinator) {
			return "", refuseLocal(verb, sprintfn.CodeRequest, "--coordinator %q is not a name (letters, digits, _ and -)", req.Coordinator)
		}
		return req.Coordinator, nil
	}
	who, err := Coordinator(ctx, req.Config)
	if err != nil {
		return "", refuseLocal(verb, sprintfn.CodeRequest, "%v", err)
	}
	if who == "" {
		return "", refuseLocal(verb, sprintfn.CodeRequest, "nova-config's sprint row names no coordinator: set it first (nova-config sprint set --coordinator <friend>)")
	}
	if req.Coordinator != "" && req.Coordinator != who {
		return "", refuseLocal(verb, sprintfn.CodeRequest, "nova-config names %s as the coordinator, and --coordinator names %s", who, req.Coordinator)
	}
	return who, nil
}

// clockRead is a read of the clock alone (1.2).
func clockRead(epoch tset.Decimal) *sprintfn.ReadRequest {
	return &sprintfn.ReadRequest{Epoch: epoch, Sprint: []sprintfn.SprintQuery{keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyClock})}}
}

// Init makes a sprint (section 3, init): the running clock, STOPPED, and the
// coordinator nova-config names, in one step. It reads the clock first and is
// refused MACHINESTATE when the sprint has one (the clock part refuses the
// same at apply, IT16). The four tables, their rows and the epoch's engine are
// Layer 1's definitions, written by its lifecycle (L1 9) before the first
// step: ns_sprint_step writes no definition, so init's step is the clock and
// the coordinator.
func Init(ctx context.Context, e *Env, req InitReq) (Result, error) {
	const verb = "init"
	who, err := coordinatorOf(ctx, verb, req)
	if err != nil {
		return Result{Verb: verb}, err
	}
	res, err := e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: map[string]any{"coordinator": who},
		Read: clockRead,
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			_, has, err := clockOf(rd, 0)
			if err != nil {
				return Part{}, err
			}
			if has {
				return Part{}, refuseLocal(verb, sprintfn.CodeMachineState, "the sprint is already initialised")
			}
			return Part{Req: &sprintfn.Request{Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockInit},
				Sprint: &sprintfn.SprintPart{Coordinator: who}}}, nil
		}})
	if err == nil && !res.Replay {
		res.Said = fmt.Sprintf("init: the sprint is made, STOPPED, with coordinator %s", who)
	}
	return res, err
}

// InitCoordinator sets the coordinator of an existing sprint to the one
// nova-config names now (section 3, init --coordinator). The actor must be
// that coordinator, checked here in Go against nova-config; X checks the
// store's copy on every coordinator's verb after it (NOTCOORD, 1.5.3).
func InitCoordinator(ctx context.Context, e *Env, req InitReq) (Result, error) {
	const verb = "init --coordinator"
	who, err := coordinatorOf(ctx, verb, req)
	if err != nil {
		return Result{Verb: verb}, err
	}
	if e.Actor != who {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeNotCoord, "the coordinator is %s, and %q is not %s", who, e.Actor, who)
	}
	res, err := e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: map[string]any{"coordinator": who},
		Read: clockRead,
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			if _, has, err := clockOf(rd, 0); err != nil || !has {
				if err != nil {
					return Part{}, err
				}
				return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "there is no sprint: run init")
			}
			// Meta.Verb is the verb's own name, so the log says
			// "init --coordinator", not init.
			return Part{Req: &sprintfn.Request{Sprint: &sprintfn.SprintPart{Coordinator: who}}}, nil
		}})
	if err == nil && !res.Replay {
		res.Said = "init --coordinator: the coordinator is " + who
	}
	return res, err
}

// ClockReq is start's and stop's request.
type ClockReq struct{ Op string }

// The notices of start, stop and clear (2.5), each on the sprint.
const (
	noticeStarted = sprint.NMachineStarted
	noticeStopped = "the machine stopped"
	noticeCleared = "the sprint was cleared, and is STOPPED"
	sprintSubject = "sprint"
	// stoppedCause is the cause R17 opens "the machine is STOPPED and moves
	// are due" with (internal/sprint's StoppedLook), which start closes.
	stoppedCause = "stopped"
)

// know is a KNOW note on the sprint (2.5).
func know(typ, cause string) sprintfn.NoteReq {
	return sprintfn.NoteReq{Op: sprintfn.JOpKnow, Type: typ, Cause: cause, Subjects: []string{sprintSubject}}
}

// Start runs the machine (section 3, start; 1.2): R moves again from where it
// stood. Refused MACHINESTATE when it runs already, from the read, and by the
// clock part at apply (A2). As the model's start does (tla/SprintEvents.tla,
// VEff "start", applied by VerbApply), it closes the judgment "the machine is
// STOPPED and moves are due" on the sprint, and its KNOW "the machine
// started" is the line whose ingest queues deal, askwait, level and done
// (2.1, "machine started"; internal/sprint's lineRows): askwait asks again for
// each primary a "cannot ask" judgment holds, the model's ask keys. A requeue
// of deal in the step itself is refused by X, since deal is not queued and
// names no line. Owed: each goal's remind at R, which waits for the write path
// to carry goals (IT16's sprint part refuses them).
func Start(ctx context.Context, e *Env, req ClockReq) (Result, error) {
	return clockVerb(ctx, e, req, "start", sprintfn.ClockStart, noticeStarted)
}

// Stop stops the machine (section 3, stop; 1.2): R stands still until start.
// Refused MACHINESTATE when it is STOPPED already, from the read and at apply
// (A2: a second stop can never move stopped_since_ms). KNOW "the machine
// stopped".
func Stop(ctx context.Context, e *Env, req ClockReq) (Result, error) {
	return clockVerb(ctx, e, req, "stop", sprintfn.ClockStop, noticeStopped)
}

func clockVerb(ctx context.Context, e *Env, req ClockReq, verb, clockVerb, notice string) (Result, error) {
	wantRunning := clockVerb == sprintfn.ClockStart
	res, err := e.Do(ctx, Planned{Verb: verb, Op: req.Op,
		Read: clockRead,
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			c, has, err := clockOf(rd, 0)
			if err != nil {
				return Part{}, err
			}
			if !has {
				return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "there is no sprint: run init")
			}
			if running(c) == wantRunning {
				if wantRunning {
					return Part{}, refuseLocal(verb, sprintfn.CodeMachineState, "the machine is already running")
				}
				return Part{}, refuseLocal(verb, sprintfn.CodeMachineState, "the machine is already stopped, since %s", *c.Clock.StoppedSinceMS)
			}
			req := &sprintfn.Request{Clock: &sprintfn.ClockPart{Verb: clockVerb},
				Body: sprintfn.Body{Notes: []sprintfn.NoteReq{know(notice, verb)}}}
			if wantRunning {
				req.Body.Notes = append(req.Body.Notes, sprintfn.NoteReq{Op: sprintfn.JOpClose, Type: sprint.NStoppedWithDue,
					Cause: stoppedCause, Subjects: []string{sprintSubject}})
			}
			return Part{Req: req}, nil
		}})
	if err == nil && !res.Replay {
		if wantRunning {
			res.Said = "start: the machine runs"
		} else {
			res.Said = "stop: the machine is STOPPED"
		}
	}
	return res, err
}

// ClearReq is clear's request: the op, and --confirm, which must name the
// deployment's prefix. A clear with --op keeps its parts' receipts at the
// epoch it ran at and the one after (L1 5: the receipt across advances), so a
// repeat finds them from either (Parts, resumeRead).
type ClearReq struct {
	Op      string
	Confirm string
}

// The tables whose rows clear restores, in the order it guards and adds them.
var clearTables = []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}

// Clear clears the sprint (section 3, clear; errata 1) in parts under one op
// (1.5.4), the caller's or one it makes, since Layer 1 refuses an advance
// without one (L1 5). Part 1, at the old epoch: the rows of the four tables
// guarded as read (AL3's rowset), the advance to the next epoch, the rows
// added again there, the clock STOPPED, and as many control cards as fit in
// the step's 256 entries: each stream's, then each member's with status down
// (so its next beat brings it up, 1.4.4, R1). Each part after it, at the new
// epoch, creates the next control cards, up to 256 a part. The last part
// writes KNOW "the sprint was cleared, and is STOPPED" at the new epoch. A
// sprint of at most 247 members and streams clears in one part, two round
// trips. It runs only while the machine is STOPPED (errata 1; the clock part
// refuses a running one, IT16): the design's table reads "the clock STOPPED
// (when RUNNING)", and the stack reads it the narrower way, so a running
// machine is refused here naming stop. The design's table puts clear in one
// step; it goes in parts so that any sprint the verbs can build can be
// cleared; a finding for the design names it.
//
// No model action covers clear: tla/SprintEvents.tla leaves clear, epochs
// and remove out (its header's list of what is not modelled), so this cites
// the design's section, the errata and L1 5 alone.
func Clear(ctx context.Context, e *Env, req ClearReq) (Result, error) {
	const verb = "clear"
	if req.Confirm != e.Names.Prefix {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "--confirm %q does not name this sprint's prefix %q", req.Confirm, e.Names.Prefix)
	}
	res, err := e.Parts(ctx, req.Op, PartsPlan{Verb: verb, Args: map[string]any{"confirm": req.Confirm},
		Read: func(epoch tset.Decimal, _ string, _ int) *sprintfn.ReadRequest {
			rr := clockRead(epoch)
			for _, t := range clearTables {
				rr.Tset = append(rr.Tset, tset.ReadQuery{Kind: "rows", Table: t})
			}
			return rr
		},
		Plan: func(rd *sprintfn.ReadReply, cont string, chunk int) (Part, error) {
			if cont == "" {
				return clearFirst(rd, chunk)
			}
			return clearRest(rd, cont, chunk)
		}})
	if err == nil && !res.Replay {
		res.Said = fmt.Sprintf("clear: the sprint is at epoch %d, STOPPED", res.EpochAfter)
	}
	return res, err
}

// clearCont is clear's continuation: of the streams and members part 1
// restored (SN, MN), how many control cards are made (S, M).
type clearCont struct{ S, SN, M, MN int }

func (c clearCont) String() string { return fmt.Sprintf("%d,%d,%d,%d", c.S, c.SN, c.M, c.MN) }

func parseClearCont(s string) (clearCont, error) {
	var c clearCont
	if _, err := fmt.Sscanf(s, "%d,%d,%d,%d", &c.S, &c.SN, &c.M, &c.MN); err != nil ||
		c.S < 0 || c.S > c.SN || c.M < 0 || c.M > c.MN {
		return c, fmt.Errorf("clear: the continuation %q is not one", s)
	}
	return c, nil
}

// clearRowsOf is the rows of each table as read (Tset slots 0..3,
// clearTables' order), by rank, and the members, readers and streams they
// name: a stream is a row of the work table or the merge table.
func clearRowsOf(rd *sprintfn.ReadReply) (rows map[string][]tset.RowRank, members, readers, streams []string, err error) {
	if len(rd.Tset) < len(clearTables) {
		return nil, nil, nil, nil, fmt.Errorf("clear: the read answered %d row queries, asked %d", len(rd.Tset), len(clearTables))
	}
	rows = map[string][]tset.RowRank{}
	for i, t := range clearTables {
		rs := append([]tset.RowRank{}, rd.Tset[i].Rows...)
		sort.SliceStable(rs, func(a, b int) bool { return compareRank(rs[a].Rank, rs[b].Rank) < 0 })
		rows[t] = rs
	}
	names := func(t string) []string {
		out := make([]string, 0, len(rows[t]))
		for _, r := range rows[t] {
			out = append(out, r.Row)
		}
		return out
	}
	members, readers, streams = names(sprint.Fleet), names(sprint.Readers), names(sprint.Work)
	for _, s := range names(sprint.Merge) {
		if !contains(streams, s) {
			streams = append(streams, s)
		}
	}
	return rows, members, readers, streams, nil
}

// clearRows refuses a sprint past the rows bound (section 3): part 1 adds
// every row again beside the advance, at most RowsMax (L1 6).
func clearRows(members, readers, streams int) error {
	if rows := members + readers + 2*streams; rows > RowsMax {
		return fmt.Errorf("members + readers + 2 x streams is %d, over the sprint's %d (members %d, readers %d, streams %d)",
			rows, RowsMax, members, readers, streams)
	}
	return nil
}

// ctlCards appends the control cards of streams[from:to] and then of
// members[from2:to2] at epoch e (their stored ids), at most room entries,
// and returns how many of each it made.
func ctlCards(entries []tset.Entry, moved []ID, e uint64, streams, members []string, c clearCont, room int) ([]tset.Entry, []ID, clearCont) {
	for ; c.S < c.SN && room > 0; c.S, room = c.S+1, room-1 {
		s := streams[c.S]
		id := sprint.StoredID(sprint.CtlID(s), e)
		entries = append(entries, tset.Entry{Kind: "create", Table: sprint.Merge, To: s + ":" + sprint.Ctl,
			IDs: []string{id}, About: []string{id}, Scores: []string{"0"},
			Set: map[string]string{"kind": "stream", "state": sprint.StreamWaiting}})
		moved = append(moved, id)
	}
	for ; c.M < c.MN && room > 0; c.M, room = c.M+1, room-1 {
		m := members[c.M]
		id := sprint.StoredID(sprint.CtlID(m), e)
		entries = append(entries, tset.Entry{Kind: "create", Table: sprint.Fleet, To: m + ":" + sprint.Ctl,
			IDs: []string{id}, About: []string{id}, Scores: []string{"0"},
			Set: map[string]string{"kind": "member", "status": sprint.Down}})
		moved = append(moved, id)
	}
	return entries, moved, c
}

// clearFirst is clear's part 1 from its read at the old epoch.
func clearFirst(rd *sprintfn.ReadReply, chunk int) (Part, error) {
	const verb = "clear"
	c, has, err := clockOf(rd, 0)
	if err != nil {
		return Part{}, err
	}
	if !has {
		return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "there is no sprint: run init")
	}
	if running(c) {
		return Part{}, refuseLocal(verb, sprintfn.CodeMachineState, "a clear runs only while the machine is STOPPED: run stop first")
	}
	epoch, ok := undec(rd.Epoch)
	if !ok {
		return Part{}, fmt.Errorf("clear: the read's epoch %q is not a number", rd.Epoch)
	}
	rows, members, readers, streams, err := clearRowsOf(rd)
	if err != nil {
		return Part{}, err
	}
	if err := clearRows(len(members), len(readers), len(streams)); err != nil {
		return Part{}, refuseLocal(verb, sprintfn.CodeLimit, "%v", err)
	}
	var entries []tset.Entry
	for _, t := range clearTables { // AL3: the rows as read, or nothing is written
		entries = append(entries, tset.Entry{Kind: "rowset", Table: t, Rows: rows[t]})
	}
	entries = append(entries, tset.Entry{Kind: "advance", AdvanceFrom: dec(epoch)})
	restore := map[string][]string{sprint.Work: streams, sprint.Merge: streams, sprint.Readers: readers, sprint.Fleet: members}
	for _, t := range clearTables {
		if len(restore[t]) != 0 {
			entries = append(entries, tset.Entry{Kind: "rows", Table: t, Add: restore[t]})
		}
	}
	room := min(EntriesMax-len(entries), chunk)
	entries, moved, cc := ctlCards(entries, nil, epoch+1, streams, members, clearCont{SN: len(streams), MN: len(members)}, room)
	last := cc.S == cc.SN && cc.M == cc.MN
	req := &sprintfn.Request{Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockClear}, Body: sprintfn.Body{Entries: entries}}
	if last {
		req.Body.Notes = []sprintfn.NoteReq{know(noticeCleared, verb)}
	}
	return Part{Req: req, Moved: moved, Next: cc.String(), Last: last}, nil
}

// clearRest is a later part of clear from its read at the new epoch: the next
// control cards of the streams and members part 1 restored, in part 1's
// order (the rows it added, by rank).
//
// A clear runs only while the machine is STOPPED (errata 1), and in parts a
// start can come between them: the later parts carry no clock part, so the
// clock part's own MACHINESTATE (IT16) does not hold them. Each later part
// refuses MACHINESTATE from its read when the clock runs, and carries X's
// clock guard on stopped_since_ms as read (1.3.5, XGUARD; the guard kinds of
// sprintfn/twin_x.go), so a start (or a start and a stop) between the read
// and the step refuses the part at apply; a race, it is read again, and
// refused here. No part is written, and no notice says the sprint is
// STOPPED, on a machine that runs.
func clearRest(rd *sprintfn.ReadReply, cont string, chunk int) (Part, error) {
	const verb = "clear"
	cc, err := parseClearCont(cont)
	if err != nil {
		return Part{}, err
	}
	c, has, err := clockOf(rd, 0)
	if err != nil {
		return Part{}, err
	}
	if !has {
		return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "there is no sprint: run init")
	}
	if running(c) {
		return Part{}, refuseLocal(verb, sprintfn.CodeMachineState, "the machine runs: a start came between clear's parts, and a clear runs only while the machine is STOPPED; run stop first")
	}
	since, err := strconv.ParseInt(*c.Clock.StoppedSinceMS, 10, 64)
	if err != nil {
		return Part{}, fmt.Errorf("clear: the clock's stopped_since_ms %q is not a number", *c.Clock.StoppedSinceMS)
	}
	epoch, ok := undec(rd.Epoch)
	if !ok {
		return Part{}, fmt.Errorf("clear: the read's epoch %q is not a number", rd.Epoch)
	}
	_, members, _, streams, err := clearRowsOf(rd)
	if err != nil {
		return Part{}, err
	}
	if len(streams) < cc.SN || len(members) < cc.MN {
		return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "the rows clear restored changed under it: %d streams and %d members at epoch %d, it restored %d and %d",
			len(streams), len(members), epoch, cc.SN, cc.MN)
	}
	entries, moved, cc := ctlCards(nil, nil, epoch, streams, members, cc, min(EntriesMax, chunk))
	last := cc.S == cc.SN && cc.M == cc.MN
	req := &sprintfn.Request{Body: sprintfn.Body{Entries: entries,
		Guards: []sprintfn.XGuard{{Kind: sprintfn.XGuardClock, Key: "stopped_since_ms", Score: since}}}}
	if last {
		req.Body.Notes = []sprintfn.NoteReq{know(noticeCleared, verb)}
	}
	return Part{Req: req, Moved: moved, Next: cc.String(), Last: last}, nil
}

// compareRank orders two exact decimal ranks.
func compareRank(a, b tset.Decimal) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(string(a), string(b))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// GoalReq is a goal verb's request: the person, and for goal set the goal.
type GoalReq struct {
	Op     string
	Person string
	Goal   string
}

// goalBytesMax is the longest goal: one field value (1.0's bounds table).
const goalBytesMax = 64 << 10

// GoalSet sets a person's goal (section 3, goal set; 1.4.1): the goal record
// and remind:<person> at R, in one step of the sprint part. The write path
// does not carry goals yet: IT16's sprint part refuses a request that names
// one (REQUEST, never ignored), so this verb is refused by it until the part
// writes {p}goal:<person> and remind:<person>; the step is the design's, and
// the refusal says so.
func GoalSet(ctx context.Context, e *Env, req GoalReq) (Result, error) {
	if !sprint.ValidID(req.Person) {
		return Result{Verb: "goal set"}, refuseLocal("goal set", sprintfn.CodeRequest, "%q is not a person's name", req.Person)
	}
	if req.Goal == "" || len(req.Goal) > goalBytesMax || !utf8.ValidString(req.Goal) {
		return Result{Verb: "goal set"}, refuseLocal("goal set", sprintfn.CodeRequest, "a goal is UTF-8 text of 1 to %d bytes", goalBytesMax)
	}
	return goalStep(ctx, e, "goal set", req, req.Goal)
}

// GoalDrop drops a person's goal (section 3, goal drop): the goal record and
// remind:<person> removed. Refused by today's write path as GoalSet is.
func GoalDrop(ctx context.Context, e *Env, req GoalReq) (Result, error) {
	if !sprint.ValidID(req.Person) {
		return Result{Verb: "goal drop"}, refuseLocal("goal drop", sprintfn.CodeRequest, "%q is not a person's name", req.Person)
	}
	return goalStep(ctx, e, "goal drop", req, "")
}

func goalStep(ctx context.Context, e *Env, verb string, req GoalReq, goal string) (Result, error) {
	res, err := e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: map[string]any{"person": req.Person, "goal": goal},
		Read: clockRead,
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			if _, has, err := clockOf(rd, 0); err != nil || !has {
				if err != nil {
					return Part{}, err
				}
				return Part{}, refuseLocal(verb, sprintfn.CodeRequest, "there is no sprint: run init")
			}
			return Part{Req: &sprintfn.Request{Sprint: &sprintfn.SprintPart{Goals: map[string]string{req.Person: goal}}}}, nil
		}})
	if rf, ok := err.(*Refused); ok && rf.Code() == sprintfn.CodeRequest && !rf.Local {
		rf.Hint = "the write path does not carry goals yet: the sprint part refuses them (IT16)"
	}
	return res, err
}

// GoalShow is a person's goal (section 3, goal show). The sprint's key reads
// (IT30) have no read of {p}goal:<person>, so it is refused, naming the read
// it needs; nothing is sent.
func GoalShow(ctx context.Context, e *Env, req GoalReq) (Result, error) {
	if !sprint.ValidID(req.Person) {
		return Result{Verb: "goal show"}, refuseLocal("goal show", sprintfn.CodeRequest, "%q is not a person's name", req.Person)
	}
	return Result{Verb: "goal show"}, refuseLocal("goal show", sprintfn.CodeRequest,
		"the sprint's key reads have no read of a person's goal yet (IT30's kinds: clock, lease, tick, heartbeat, dropping, parked, missing, jopen, duecount)")
}
