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

// Teardown drops the four tables, the view and every key of the deployment.
func (st *Store) Teardown(ctx context.Context) (int, error) {
	_ = st.B.ViewDelete(ctx, st.Names.View())
	for _, t := range All {
		if err := st.B.DropTable(ctx, st.Names.Table(t)); err != nil && refusalCode(err) != "NOTABLE" {
			return 0, err
		}
	}
	return st.B.DropKeys(ctx)
}

// CheckReport is check's answer.
type CheckReport struct {
	Violations []sprint.Violation `json:"violations"`
	Reads      int                `json:"reads"`
	Pending    string             `json:"pending,omitempty"` // the operation the fence holds
}

// Check reads the four tables between two reads of the fence and holds them to
// section 9. With an operation pending, only the rules that always hold are
// judged, and the operation is reported. A read that saw the fence move is
// taken again, up to reads times.
func (st *Store) Check(ctx context.Context, reads int) (CheckReport, *sprint.Snapshot, error) {
	var rep CheckReport
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
		var pending []string
		switch {
		case f.Pending != nil:
			rep.Pending, pending = f.Pending.ID, []string{f.Pending.ID}
		case f2.Pending != nil:
			rep.Pending, pending = f2.Pending.ID, []string{f2.Pending.ID}
		case f.Gen != f2.Gen:
			st.backoff(i + 2)
			continue
		}
		rep.Violations = sprint.Check(s, pending)
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

// SyncMirrors brings the display cells up to date: a fleet member's status,
// ok% (from the ok and failed counts on its control card) and load (its
// unfinished work cards), and a stream's ci, state and since. They are
// display only: the state is the control cards', written with the moves.
func (st *Store) SyncMirrors(ctx context.Context) error {
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Fleet), st.Names.Table(sprint.Merge)})
	if err != nil {
		return err
	}
	for _, shape := range shapes {
		logical := st.Names.Logical(shape.Name)
		var ids []string
		for _, r := range shape.Rows {
			ids = append(ids, sprint.CtlID(r.Key))
		}
		if len(ids) == 0 {
			continue
		}
		rs, err := st.B.ReadSet(ctx, shape.Name, ids)
		if err != nil {
			return err
		}
		for _, row := range shape.Rows {
			ctl, _ := rs.Member(sprint.CtlID(row.Key))
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
	var err error
	if v.Open, err = st.B.OpenNotes(ctx); err != nil {
		return v, err
	}
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
	v.Groups = sprint.Inbox(sprint.InboxReq{Now: st.Now(), Open: v.Open, Recent: notes, Streams: clocks, Deadline: deadline, Stale: stale})
	return v, nil
}

// StreamClocks is every stream's state, since and progress.
func (st *Store) StreamClocks(ctx context.Context) ([]sprint.StreamClock, error) {
	name := st.Names.Table(sprint.Merge)
	shapes, err := st.B.Shapes(ctx, []string{name})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, r := range shapes[0].Rows {
		ids = append(ids, sprint.CtlID(r.Key))
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
		ctl, _ := rs.Member(sprint.CtlID(r.Key))
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
