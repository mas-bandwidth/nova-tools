package store

import (
	"context"
	"fmt"
	"sort"
	"strconv"
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
	return st.B.ViewSet(ctx, st.Names.ViewDef())
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
		s, err := st.Load(ctx, All, tickExtras)
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
		held, err := st.heldState(ctx, s, pending)
		if err != nil {
			return rep, nil, err
		}
		rep.Violations = append(rep.Violations, sprint.CheckHeld(held, s.Now)...)
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

// heldState is the no-stall rule's reading of a snapshot: the machine's
// state, its STOPPED spans, its last tick, and the pending operation.
func (st *Store) heldState(ctx context.Context, s *sprint.Snapshot, pending *OpRecord) (sprint.HeldState, error) {
	h := sprint.HeldState{Snap: s, Grace: st.grace()}
	if pending != nil {
		h.Pending = &sprint.PendingOp{ID: pending.ID, Verb: pending.Verb, At: pending.At}
	}
	if _, ok := st.B.(KV); ok {
		m, hb, err := st.Machine(ctx)
		if err != nil {
			return h, err
		}
		h.Running, h.Stopped, h.LastTick = m.Running(), m.StoppedBetween, hb.At
	}
	return h, nil
}

// Held is what holds one primary now: the no-stall rule's answer for it.
func (st *Store) Held(ctx context.Context, id string) (sprint.Hold, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return sprint.Hold{}, err
	}
	f, err := st.B.ReadFence(ctx)
	if err != nil {
		return sprint.Hold{}, err
	}
	if f.Pending != nil {
		return sprint.Hold{ID: id, Place: id, Why: "operation " + f.Pending.ID + " (" + f.Pending.Verb + ") is pending: the tables are a partial state of it; run: nova-sprint repair"}, nil
	}
	s, err := st.Load(ctx, All, func(s *sprint.Snapshot) map[string][]string {
		return map[string][]string{sprint.Work: append(sprint.ResolveExtras(s), id)}
	})
	if err != nil {
		return sprint.Hold{}, err
	}
	h, err := st.heldState(ctx, s, nil)
	if err != nil {
		return sprint.Hold{}, err
	}
	return sprint.Holder(h, s.Now, id), nil
}

// SyncMirrors brings the display cells up to date: a fleet member's status,
// ok% (from the ok and failed counts on its control card) and load (its
// unfinished work cards), and a stream's ci, state and since. They are
// display only: the state is the control cards', written with the moves.
func (st *Store) SyncMirrors(ctx context.Context) error {
	st, err := st.pin(ctx)
	if err != nil {
		return err
	}
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Fleet), st.Names.Table(sprint.Merge)})
	if err != nil {
		return err
	}
	for _, shape := range shapes {
		logical := st.Names.Logical(shape.Name)
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
			want := map[string]string{}
			if logical == sprint.Fleet {
				ok, failed := atoi(ctl.Fields["ok"]), atoi(ctl.Fields["failed"])
				want[sprint.OkPct] = "-"
				if ok+failed > 0 {
					want[sprint.OkPct] = strconv.FormatFloat(100*float64(ok)/float64(ok+failed), 'f', 1, 64) + "%"
				}
				want[sprint.Status] = dash(ctl.Fields["status"])
				want[sprint.Load] = strconv.FormatInt(count(shape, row, sprint.Ready)+count(shape, row, sprint.Working), 10)
			} else {
				want[sprint.CI] = dash(ctl.Fields["ci"])
				want[sprint.StateCol] = dash(ctl.Fields["state"])
				want[sprint.Since] = clock(ctl.Fields["since"])
			}
			diff := map[string]string{}
			for k, v := range want {
				if row.Texts[k] != v {
					diff[k] = v
				}
			}
			if len(diff) > 0 {
				if err := st.B.RowSet(ctx, shape.Name, row.Key, diff); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

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
	if _, ok := st.B.(KV); ok {
		// One clock for overdue: running time, as the tick's deadlines.
		m, _, err := st.Machine(ctx)
		if err != nil {
			return v, err
		}
		req.Stopped = m.StoppedBetween
	}
	v.Groups = sprint.Inbox(req)
	return v, nil
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
