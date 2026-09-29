package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// All is the four tables, for the verbs that read everything.
var All = []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet}

// Init creates the four tables and the view. A table that exists with the
// same definition is left as it is.
func (st *Store) Init(ctx context.Context) error {
	for _, t := range st.Names.Definitions() {
		if err := st.B.Create(ctx, t); err != nil {
			return fmt.Errorf("create %s: %w", t.Name, err)
		}
	}
	if err := st.B.ViewSet(ctx, st.Names.ViewDef()); err != nil {
		return err
	}
	// The machine is STOPPED from the start: the time before the first start
	// is a STOPPED span, and counts toward no deadline.
	if _, ok := st.B.(KV); ok {
		m, _, err := st.Machine(ctx)
		if err != nil {
			return err
		}
		if m.State == "" {
			now := st.now()
			return st.putJSON(ctx, keyMachine, Machine{State: Stopped, Since: now, Who: st.Actor, Spans: []Span{{From: now}}})
		}
	}
	return nil
}

// CheckReport is check's answer.
type CheckReport struct {
	Violations []sprint.Violation `json:"violations"`
	Reads      int                `json:"reads"`
	Pending    string             `json:"pending,omitempty"` // the operation the fence holds
	// InFlight says the pending operation is younger than the grace: its
	// writer is at it, and only the rules that always hold are judged.
	InFlight bool `json:"in_flight,omitempty"`
}

// Check reads the four tables between two reads of the fence and holds them to
// section 9. A read that saw the fence move, or an operation pending, is taken
// again, up to reads times, for a quiet moment. With an operation still
// pending, only the rules that always hold are judged and the operation is
// reported: in flight when it is younger than the grace, cut (a violation)
// when it is older.
func (st *Store) Check(ctx context.Context, reads int) (CheckReport, *sprint.Snapshot, error) {
	ctx = withBudget(ctx)
	st, err := st.pin(ctx)
	if err != nil {
		return CheckReport{}, nil, err
	}
	var rep CheckReport
	quiet := st.retry(ctx)
	for i := 0; i < max(reads, 1); i++ {
		rep.Reads++
		f, err := st.B.ReadFence(ctx)
		if err != nil {
			return rep, nil, err
		}
		s, err := st.Load(ctx, All, nil)
		if err != nil {
			return rep, nil, err
		}
		f2, err := st.B.ReadFence(ctx)
		if err != nil {
			return rep, nil, err
		}
		pending := f.Pending
		if pending == nil {
			pending = f2.Pending
		}
		last := i == max(reads, 1)-1
		switch {
		case pending == nil && f.Gen != f2.Gen, pending != nil && !last:
			quiet.wait() // another writer is at it: look again for a quiet moment
			continue
		}
		var ops *sprint.Pending
		rep.Pending, rep.InFlight = "", false
		if pending != nil {
			rep.Pending = pending.ID
			rep.InFlight = s.Now.Sub(pending.At) < st.grace()
			ops = pendingOf(*pending, st.Names)
		}
		rep.Violations = sprint.Check(s, ops)
		if rep.InFlight {
			var kept []sprint.Violation
			for _, v := range rep.Violations {
				if v.Rule != 10 {
					kept = append(kept, v)
				}
			}
			rep.Violations = kept
		}
		for _, t := range All {
			if err := st.B.CheckTable(ctx, st.Names.Table(t)); err != nil {
				rep.Violations = append(rep.Violations, sprint.Violation{Rule: 1, Detail: err.Error()})
			}
		}
		sort.SliceStable(rep.Violations, func(i, j int) bool { return rep.Violations[i].Rule < rep.Violations[j].Rule })
		return rep, s, nil
	}
	return rep, nil, fmt.Errorf("the sprint kept changing through %d reads; run check again", rep.Reads)
}

// SyncMirrors brings the display cells up to date: the fleet's (SyncFleet:
// a member's derived status and measured load), and a stream's ci, state and
// since. They are display only: the state is the control cards', written with
// the moves. A fleet member's done and ok% are the table's own formulas over
// its finished cells, never written here.
func (st *Store) SyncMirrors(ctx context.Context) error {
	if _, err := st.SyncFleet(ctx); err != nil {
		return err
	}
	st, err := st.pin(ctx)
	if err != nil {
		return err
	}
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Merge)})
	if err != nil {
		return err
	}
	for _, shape := range shapes {
		var ids []string
		for _, r := range shape.Rows {
			ids = append(ids, st.sid(sprint.CtlID(r.Key)))
		}
		if len(ids) == 0 {
			continue
		}
		rs, err := st.B.ReadSet(ctx, shape.Name, ids)
		if err != nil {
			return err
		}
		for _, row := range shape.Rows {
			ctl, _ := rs.Member(st.sid(sprint.CtlID(row.Key)))
			want := map[string]string{
				sprint.CI:       dash(ctl.Fields["ci"]),
				sprint.StateCol: dash(ctl.Fields["state"]),
				sprint.Since:    clock(ctl.Fields["since"]),
			}
			if _, err := syncRow(ctx, st, shape, row, want); err != nil {
				return err
			}
		}
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// clock prints a stamp as the time of day it names, in the local zone.
func clock(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return "-"
	}
	return t.Local().Format("15:04:05")
}

func count(t ntable.Table, row ntable.Row, col string) int64 {
	j := t.Column(col)
	if j < 0 || j >= len(row.Cells) {
		return 0
	}
	return row.Cells[j].Count
}

// InboxView is the inbox as read: its groups, and the last stream id read.
type InboxView struct {
	Groups []sprint.Group `json:"groups"`
	Last   string         `json:"last,omitempty"`
	Cursor string         `json:"cursor,omitempty"`
	Recent []sprint.Note  `json:"-"`
	Open   []sprint.Open  `json:"-"`
}

// Inbox reads the open judgments, the notifications since the cursor (at
// most max) and the streams' clocks, and groups them at the clock's reading.
func (st *Store) Inbox(ctx context.Context, deadline, stale time.Duration, max int) (InboxView, error) {
	var v InboxView
	st, err := st.pin(ctx)
	if err != nil {
		return v, err
	}
	if v.Open, err = st.B.OpenNotes(ctx); err != nil {
		return v, err
	}
	v.Open, _ = sprint.SplitOpen(v.Open)
	if v.Cursor, err = st.B.Cursor(ctx); err != nil {
		return v, err
	}
	notes, ids, err := st.B.NotesSince(ctx, v.Cursor, max)
	if err != nil {
		return v, err
	}
	v.Recent = notes
	if len(ids) > 0 {
		v.Last = ids[len(ids)-1]
	}
	clocks, err := st.StreamClocks(ctx)
	if err != nil {
		return v, err
	}
	req := sprint.InboxReq{Now: st.now(), Open: v.Open, Recent: notes, Streams: clocks, Deadline: deadline, Stale: stale, Prefix: st.Names.Prefix}
	var machine []sprint.Group
	if _, ok := st.B.(KV); ok {
		// One clock for overdue: running time, as the tick's deadlines.
		m, hb, err := st.Machine(ctx)
		if err != nil {
			return v, err
		}
		req.Stopped = m.StoppedBetween
		if machine, err = st.machineGroups(ctx, m, hb); err != nil {
			return v, err
		}
	}
	v.Groups = append(machine, sprint.Inbox(req)...)
	return v, nil
}

// machineGroups is what the machine's record says the coordinator must act
// on, computed at read time as the stale line is: a RUNNING machine that is
// not ticking (its run loop died), a tick failing three times in a row, and
// a STOPPED machine with moves due.
func (st *Store) machineGroups(ctx context.Context, m Machine, hb Heartbeat) ([]sprint.Group, error) {
	now := st.now()
	var out []sprint.Group
	group := func(id, typ, what string, cmds ...sprint.Command) sprint.Group {
		return sprint.Group{ID: id, Kind: sprint.Judgment, Type: typ, Count: 1, Marked: true, Oldest: now, Due: now, What: what, Commands: cmds}
	}
	if m.Running() {
		if gap := now.Sub(hb.Alive()); gap > MachineSilence {
			out = append(out, group("machine:silent", sprint.NMachineSilent,
				fmt.Sprintf("the machine is RUNNING and nothing has ticked for %ds: its run loop is not running", int(gap/time.Second)),
				sprint.Command{Decision: "run the loop", Lines: []string{"nova-sprint run"}},
				sprint.Command{Decision: "stop the machine", Lines: []string{"nova-sprint stop"}}))
		}
		if hb.Failures >= 3 && hb.Error != "" {
			out = append(out, group("machine:failing", sprint.NTickFailing,
				fmt.Sprintf("%d ticks in a row failed; the last: %s", hb.Failures, hb.Error),
				sprint.Command{Decision: "look", Lines: []string{"nova-sprint tick", "nova-sprint check"}},
				sprint.Command{Decision: "repair", Lines: []string{"nova-sprint repair"}}))
		}
		return out, nil
	}
	s, err := st.Load(ctx, tables(sprint.Work, sprint.Fleet), nil)
	if err != nil {
		return out, err
	}
	if n := sprint.MovesDue(s); n > 0 {
		out = append(out, group("machine:stopped", sprint.NStoppedWithDue,
			fmt.Sprintf("the machine is STOPPED and %d moves are due", n),
			sprint.Command{Decision: "start", Lines: []string{"nova-sprint start"}}))
	}
	return out, nil
}

// StreamClocks is every stream's state, since and progress.
func (st *Store) StreamClocks(ctx context.Context) ([]sprint.StreamClock, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	name := st.Names.Table(sprint.Merge)
	shapes, err := st.B.Shapes(ctx, []string{name})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, r := range shapes[0].Rows {
		ids = append(ids, st.sid(sprint.CtlID(r.Key)))
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rs, err := st.B.ReadSet(ctx, name, ids)
	if err != nil {
		return nil, err
	}
	progress, err := st.B.Progress(ctx)
	if err != nil {
		return nil, err
	}
	var out []sprint.StreamClock
	for _, r := range shapes[0].Rows {
		ctl, _ := rs.Member(st.sid(sprint.CtlID(r.Key)))
		since := parseStamp(ctl.Fields["since"])
		p := progress[r.Key]
		if since.After(p) {
			p = since
		}
		out = append(out, sprint.StreamClock{Stream: r.Key, State: ctl.Fields["state"], Since: since, Progress: p})
	}
	return out, nil
}

func parseStamp(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// pendingOf is a pending operation as check judges it: a rank's new scores of
// its primaries, read from its work-table manifests.
func pendingOf(op OpRecord, names sprint.Names) *sprint.Pending {
	p := &sprint.Pending{ID: op.ID, Verb: op.Verb, Scores: map[string]float64{}}
	for _, m := range op.Manifests {
		if m.Table != names.Table(sprint.Work) {
			continue
		}
		for _, e := range m.Members {
			if e.Move != nil && e.Move.Score != nil {
				p.Scores[e.ID] = *e.Move.Score
			}
		}
	}
	return p
}
