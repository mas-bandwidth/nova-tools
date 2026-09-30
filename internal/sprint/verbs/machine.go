package verbs

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The machine's verbs (section 3's table): init, init --coordinator, start,
// stop, clear and goal. Each is one step through Do, two round trips: its
// read, then its step.

// The size of the sprint (section 3, "Size of the sprint"; F1-20): members
// and streams together at most MembersAndStreamsMax, and members + readers +
// 2 x streams at most RowsMax, so that clear, which cannot be split, restores
// every row and creates every control card in one step.
const (
	MembersAndStreamsMax = sprintfn.SprintMembersMax // 250
	RowsMax              = tset.MaxRowsWithAdvance  // 1,024 rows in a step with advance (L1 6)
)

// EntriesMax is a step's entries (L1 6; 1.0's bounds table).
const EntriesMax = 256

// clearFixedEntries are the entries of a clear beside the control cards: a
// rowset guard for each of the four tables (AL3), the advance, and a rows
// entry for each of the four tables.
const clearFixedEntries = 4 + 1 + 4

// SizeBounds refuses a sprint of members, readers and streams past either
// bound of section 3, naming the bound. fleet up, reader add and an add that
// creates a stream refuse with it (items IT19, IT20); clear refuses a sprint
// already past it, which only a write outside the verbs can make.
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
	who, err := Coordinator(ctx, req.Config)
	if err != nil {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "%v", err)
	}
	if who == "" {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "nova-config's sprint row names no coordinator: set it first (nova-config sprint set --coordinator <friend>)")
	}
	res, err := e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: map[string]any{"coordinator": who},
		Read: clockRead,
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			_, has, err := clockOf(rd, 0)
			if err != nil {
				return nil, err
			}
			if has {
				return nil, refuseLocal(verb, sprintfn.CodeMachineState, "the sprint is already initialised")
			}
			return &sprintfn.Request{Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockInit},
				Sprint: &sprintfn.SprintPart{Coordinator: who}}, nil
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
	who, err := Coordinator(ctx, req.Config)
	if err != nil {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "%v", err)
	}
	if who == "" {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "nova-config's sprint row names no coordinator")
	}
	if e.Actor != who {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeNotCoord, "nova-config names %s as the coordinator, and %q is not %s", who, e.Actor, who)
	}
	res, err := e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: map[string]any{"coordinator": who},
		Read: clockRead,
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			if _, has, err := clockOf(rd, 0); err != nil || !has {
				if err != nil {
					return nil, err
				}
				return nil, refuseLocal(verb, sprintfn.CodeRequest, "there is no sprint: run init")
			}
			return &sprintfn.Request{Meta: sprintfn.Meta{Verb: "init"}, Sprint: &sprintfn.SprintPart{Coordinator: who}}, nil
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
	noticeStarted = "the machine started"
	noticeStopped = "the machine stopped"
	noticeCleared = "the sprint was cleared, and is STOPPED"
	sprintSubject = "sprint"
)

// know is a KNOW note on the sprint (2.5).
func know(typ, cause string) sprintfn.NoteReq {
	return sprintfn.NoteReq{Op: sprintfn.JOpKnow, Type: typ, Cause: cause, Subjects: []string{sprintSubject}}
}

// Start runs the machine (section 3, start; 1.2): R moves again from where it
// stood. Refused MACHINESTATE when it runs already, from the read, and by the
// clock part at apply (A2). KNOW "the machine started". The design's start
// also enters each goal's remind at R; the write path carries no goal yet
// (IT16's sprint part refuses goals), so this start enters none.
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
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			c, has, err := clockOf(rd, 0)
			if err != nil {
				return nil, err
			}
			if !has {
				return nil, refuseLocal(verb, sprintfn.CodeRequest, "there is no sprint: run init")
			}
			if running(c) == wantRunning {
				if wantRunning {
					return nil, refuseLocal(verb, sprintfn.CodeMachineState, "the machine is already running")
				}
				return nil, refuseLocal(verb, sprintfn.CodeMachineState, "the machine is already stopped, since %s", *c.Clock.StoppedSinceMS)
			}
			return &sprintfn.Request{Clock: &sprintfn.ClockPart{Verb: clockVerb},
				Body: sprintfn.Body{Notes: []sprintfn.NoteReq{know(notice, verb)}}}, nil
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
// deployment's prefix. A clear with --op keeps its receipt at the epoch it
// ran at (L1 5: the receipt across advances), so a repeat runs at that epoch
// (Env.Epoch as it was) to find it.
type ClearReq struct {
	Op      string
	Confirm string
}

// The tables whose rows clear restores, in the order it guards and adds them.
var clearTables = []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}

// Clear clears the sprint (section 3, clear; errata 1): in one step, the rows
// of the four tables guarded as read (AL3's rowset), the advance to the next
// epoch, the rows added again there, each member's control card with status
// down (so its next beat brings it up, 1.4.4, R1), each stream's control card,
// and KNOW "the sprint was cleared, and is STOPPED" at the new epoch. It runs
// only while the machine is STOPPED (errata 1; the clock part refuses a
// running one, IT16). The step always has an op, the caller's or one it
// makes, since Layer 1 refuses an advance without one (L1 5). The design's table reads "the clock STOPPED (when
// RUNNING)", and the stack reads it the narrower way, so a running machine is
// refused here naming stop. A sprint past the size bounds is refused before
// any write. The size bound keeps the rows inside 1,024, but four rowset
// guards, the advance and four rows entries beside 250 control cards are 259
// entries, over 256: a clear of more than 247 members and streams is refused
// LIMIT, naming the entries (a finding for the design).
func Clear(ctx context.Context, e *Env, req ClearReq) (Result, error) {
	const verb = "clear"
	if req.Confirm != e.Names.Prefix {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "--confirm %q does not name this sprint's prefix %q", req.Confirm, e.Names.Prefix)
	}
	op := req.Op
	if op == "" {
		op = NewOp() // L1 5: every advance carries an op identity
	}
	res, err := e.Do(ctx, Planned{Verb: verb, Op: op, Args: map[string]any{"confirm": req.Confirm},
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
			rr := clockRead(epoch)
			for _, t := range clearTables {
				rr.Tset = append(rr.Tset, tset.ReadQuery{Kind: "rows", Table: t})
			}
			return rr
		},
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			c, has, err := clockOf(rd, 0)
			if err != nil {
				return nil, err
			}
			if !has {
				return nil, refuseLocal(verb, sprintfn.CodeRequest, "there is no sprint: run init")
			}
			if running(c) {
				return nil, refuseLocal(verb, sprintfn.CodeMachineState, "a clear runs only while the machine is STOPPED: run stop first")
			}
			return clearStep(rd)
		}})
	if err == nil && !res.Replay {
		res.Said = fmt.Sprintf("clear: the sprint is at epoch %d, STOPPED", res.EpochAfter)
	}
	return res, err
}

// clearStep is clear's one step from its read: the rows of each table as read
// (Tset slots 0..3, clearTables' order) and the epoch it read at.
func clearStep(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
	const verb = "clear"
	epoch, ok := undec(rd.Epoch)
	if !ok {
		return nil, fmt.Errorf("clear: the read's epoch %q is not a number", rd.Epoch)
	}
	next := epoch + 1
	if len(rd.Tset) < len(clearTables) {
		return nil, fmt.Errorf("clear: the read answered %d row queries, asked %d", len(rd.Tset), len(clearTables))
	}
	rows := map[string][]tset.RowRank{}
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
	members, readers := names(sprint.Fleet), names(sprint.Readers)
	streams := names(sprint.Work)
	for _, s := range names(sprint.Merge) {
		if !contains(streams, s) {
			streams = append(streams, s)
		}
	}
	if err := SizeBounds(len(members), len(readers), len(streams)); err != nil {
		return nil, refuseLocal(verb, sprintfn.CodeLimit, "%v", err)
	}
	if n := clearFixedEntries + len(members) + len(streams); n > EntriesMax {
		return nil, refuseLocal(verb, sprintfn.CodeLimit, "the clear is %d entries (%d members and streams, and %d fixed), over a step's %d", n, len(members)+len(streams), clearFixedEntries, EntriesMax)
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
	for _, s := range streams {
		id := sprint.StoredID(sprint.CtlID(s), next)
		entries = append(entries, tset.Entry{Kind: "create", Table: sprint.Merge, To: s + ":" + sprint.Ctl,
			IDs: []string{id}, About: []string{id}, Scores: []string{"0"},
			Set: map[string]string{"kind": "stream", "state": sprint.StreamWaiting}})
	}
	for _, m := range members {
		id := sprint.StoredID(sprint.CtlID(m), next)
		entries = append(entries, tset.Entry{Kind: "create", Table: sprint.Fleet, To: m + ":" + sprint.Ctl,
			IDs: []string{id}, About: []string{id}, Scores: []string{"0"},
			Set: map[string]string{"kind": "member", "status": sprint.Down}})
	}
	return &sprintfn.Request{Clock: &sprintfn.ClockPart{Verb: sprintfn.ClockClear},
		Body: sprintfn.Body{Entries: entries, Notes: []sprintfn.NoteReq{know(noticeCleared, verb)}}}, nil
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
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			if _, has, err := clockOf(rd, 0); err != nil || !has {
				if err != nil {
					return nil, err
				}
				return nil, refuseLocal(verb, sprintfn.CodeRequest, "there is no sprint: run init")
			}
			return &sprintfn.Request{Sprint: &sprintfn.SprintPart{Goals: map[string]string{req.Person: goal}}}, nil
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
