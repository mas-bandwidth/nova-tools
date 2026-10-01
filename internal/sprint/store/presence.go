package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A fleet member's beats (sprint/presence.go): one record per member under
// the deployment's prefix, beat:<member>, outside the tables and the fence,
// written only by the member's own beat. A beat is one read of the record and
// one write of it, whatever the machine's state; the tick reads every
// member's record in one exchange.

// keyStrangers is the machines that beat and are no member of the sprint:
// when each was first seen, and whether the coordinator was told.
const keyStrangers = "strangers"

// stranger is one unknown machine that beats.
type stranger struct {
	Seen time.Time `json:"seen"`
	Told bool      `json:"told,omitempty"`
}

func (st *Store) strangers(ctx context.Context) (map[string]stranger, error) {
	out := map[string]stranger{}
	kv, err := st.rootKV()
	if err != nil {
		return out, err
	}
	raw, ok, err := kv.GetKey(ctx, keyStrangers)
	if err != nil || !ok {
		return out, err
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out, nil
}

func (st *Store) putStrangers(ctx context.Context, s map[string]stranger) error {
	kv, err := st.rootKV()
	if err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, keyStrangers, string(b))
}

// noteStranger records a machine that beats and is no member of the sprint,
// once.
func (st *Store) noteStranger(ctx context.Context, member string) error {
	pinned, err := st.pin(ctx)
	if err != nil {
		return err
	}
	shapes, err := pinned.B.Shapes(ctx, []string{pinned.Names.Table(sprint.Fleet)})
	if refusalCode(err) == "NOTABLE" {
		return nil // no sprint yet: nothing to be a stranger to
	}
	if err != nil || len(shapes) == 0 {
		return err
	}
	for _, r := range shapes[0].Rows {
		if r.Key == member {
			return nil
		}
	}
	s, err := st.strangers(ctx)
	if err != nil {
		return err
	}
	if _, known := s[member]; known {
		return nil
	}
	s[member] = stranger{Seen: st.now()}
	return st.putStrangers(ctx, s)
}

// tellStrangers writes one happened notification for each unknown machine
// that beats and was not told of yet: add it with fleet up.
func (st *Store) tellStrangers(ctx context.Context) (Result, error) {
	var res Result
	s, err := st.strangers(ctx)
	if err != nil || len(s) == 0 {
		return res, err
	}
	var names []string
	for m, x := range s {
		if !x.Told {
			names = append(names, m)
		}
	}
	if len(names) == 0 {
		return res, nil
	}
	sort.Strings(names)
	res, err = st.Run(ctx, Step{Verb: "tick strangers", Actor: sprint.MachineActor, Load: tables(sprint.Fleet), Plan: func(snap *sprint.Snapshot) sprint.Plan {
		return sprint.StrangerNotes(snap, names)
	}})
	if err != nil {
		return res, err
	}
	for _, m := range names {
		x := s[m]
		x.Told = true
		s[m] = x
	}
	return res, st.putStrangers(ctx, s)
}

// beatKey is a member's beat record.
func beatKey(member string) string { return "beat:" + member }

// KeysGetter is a store that reads many records in one exchange.
type KeysGetter interface {
	GetKeys(ctx context.Context, names []string) ([]string, []bool, error)
}

// rootKV is the machine records' store: the backend before pinning, as the
// records are the deployment's and not an epoch's.
func (st *Store) rootKV() (KV, error) {
	b := st.root
	if b == nil {
		b = st.B
	}
	kv, ok := b.(KV)
	if !ok {
		return nil, fmt.Errorf("this store keeps no beats")
	}
	return kv, nil
}

// Beat writes one beat of member at the store's clock: the load given, or
// else measured from src; the record's measuring state carries from beat to
// beat. It works while the machine is RUNNING or STOPPED and touches no table.
func (st *Store) Beat(ctx context.Context, member string, given *float64, src hostload.Source) (sprint.Beat, error) {
	if !sprint.ValidID(member) {
		return sprint.Beat{}, fmt.Errorf("a member name wants letters, digits, _ and -: %s", member)
	}
	kv, err := st.rootKV()
	if err != nil {
		return sprint.Beat{}, err
	}
	var prev sprint.Beat
	raw, ok, err := kv.GetKey(ctx, beatKey(member))
	if err != nil {
		return prev, err
	}
	if ok {
		// An unreadable record is a first beat: the next write replaces it.
		// ignored: an unreadable record is a first beat, which the next write replaces (see the comment above)
		_ = json.Unmarshal([]byte(raw), &prev)
	}
	now := st.now()
	pct, how, meter := 0.0, sprint.HowGiven, prev.Meter
	if given != nil {
		if *given < 0 || *given > hostload.MaxPercent {
			return prev, fmt.Errorf("a load is a percent from 0 to %v, found %v", hostload.MaxPercent, *given)
		}
		pct = *given
	} else {
		var measured bool
		pct, how, meter, measured = hostload.Measure(src, prev.Meter, now)
		if !measured {
			return prev, fmt.Errorf("this machine's load cannot be measured here: give it with --load <percent>")
		}
	}
	b := sprint.NextBeat(prev, now, pct, how, meter)
	out, err := json.Marshal(b)
	if err != nil {
		return b, err
	}
	if err := kv.SetKey(ctx, beatKey(member), string(out)); err != nil {
		return b, err
	}
	return b, st.noteStranger(ctx, member)
}

// Beats is the beat records of the members, in one exchange where the store
// can; a member that has never beaten has none.
func (st *Store) Beats(ctx context.Context, members []string) (map[string]sprint.Beat, error) {
	out := map[string]sprint.Beat{}
	if len(members) == 0 {
		return out, nil
	}
	kv, err := st.rootKV()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = beatKey(m)
	}
	var vals []string
	var oks []bool
	if kg, ok := kv.(KeysGetter); ok {
		if vals, oks, err = kg.GetKeys(ctx, names); err != nil {
			return nil, err
		}
	} else {
		for _, n := range names {
			v, ok, err := kv.GetKey(ctx, n)
			if err != nil {
				return nil, err
			}
			vals, oks = append(vals, v), append(oks, ok)
		}
	}
	for i, m := range members {
		var b sprint.Beat
		if i < len(oks) && oks[i] && json.Unmarshal([]byte(vals[i]), &b) == nil {
			out[m] = b
		}
	}
	return out, nil
}

// SyncFleet brings every display cell of the fleet up to date, reading the
// control cards: each member's status (derived: held, up or down), width, load (the
// highest measured load of the last LoadWindow while its beat is fresh); done
// and ok% are the table's own formulas. A store that keeps no beats shows the
// control card's status and no load. It is what a step's mirrors run. It says whether it wrote.
func (st *Store) SyncFleet(ctx context.Context) (bool, error) {
	pinned, err := st.pin(ctx)
	if err != nil {
		return false, err
	}
	shapes, err := pinned.B.Shapes(ctx, []string{pinned.Names.Table(sprint.Fleet)})
	if err != nil || len(shapes) == 0 {
		return false, err
	}
	shape := shapes[0]
	var ids, members []string
	for _, r := range shape.Rows {
		ids = append(ids, pinned.sid(sprint.CtlID(r.Key)))
		members = append(members, r.Key)
	}
	if len(ids) == 0 {
		return false, nil
	}
	rs, err := pinned.readSet(ctx, shape.Name, ids)
	if err != nil {
		return false, err
	}
	var beats map[string]sprint.Beat
	if _, kerr := st.rootKV(); kerr == nil {
		if beats, err = st.Beats(ctx, members); err != nil {
			return false, err
		}
	}
	now := st.now()
	diffs := map[string]map[string]string{}
	for _, row := range shape.Rows {
		m, _ := rs.Member(pinned.sid(sprint.CtlID(row.Key)))
		ctl := &sprint.Card{Fields: m.Fields}
		want := map[string]string{sprint.Status: dash(ctl.F("status")), sprint.Load: "", sprint.FieldWidth: sprint.WidthText(ctl)}
		if beats != nil {
			b := beats[row.Key]
			want[sprint.Status], want[sprint.Load] = sprint.MemberStatus(ctl, b, now), sprint.LoadText(b, now)
		}
		if d := rowDiff(row, want); len(d) > 0 {
			diffs[row.Key] = d
		}
	}
	return len(diffs) > 0, pinned.setRows(ctx, shape.Name, diffs)
}

// fleetBeats is the fleet's shape, from the shapes a tick read or else read
// now, and the beats of its members.
func (st *Store) fleetBeats(ctx context.Context, shapes []ntable.Table) (ntable.Table, map[string]sprint.Beat, error) {
	name := st.Names.Table(sprint.Fleet)
	var shape ntable.Table
	found := false
	for _, sh := range shapes {
		if sh.Name == name {
			shape, found = sh, true
		}
	}
	if !found {
		read, err := st.B.Shapes(ctx, []string{name})
		if err != nil || len(read) == 0 {
			return shape, nil, err
		}
		shape = read[0]
	}
	var members []string
	for _, r := range shape.Rows {
		members = append(members, r.Key)
	}
	beats, err := st.Beats(ctx, members)
	return shape, beats, err
}

// freshOf is the members whose beat is fresh at now, in row order.
func freshOf(shape ntable.Table, beats map[string]sprint.Beat, now time.Time) []string {
	var out []string
	for _, r := range shape.Rows {
		if beats[r.Key].Fresh(now) {
			out = append(out, r.Key)
		}
	}
	return out
}

// showFleet is the tick's own pass over the fleet's display cells, from the
// row texts and the beats alone, reading no control card: the load of each
// member, and its status up or down by its beat. A member shown held stays
// held: a hold is set and released only by a verb, whose step brings every
// cell up to date (SyncFleet). It says whether it wrote.
func (st *Store) showFleet(ctx context.Context, shape ntable.Table, beats map[string]sprint.Beat, now time.Time) (bool, error) {
	diffs := map[string]map[string]string{}
	for _, row := range shape.Rows {
		b := beats[row.Key]
		want := map[string]string{sprint.Load: sprint.LoadText(b, now)}
		if row.Texts[sprint.Status] != sprint.Held {
			want[sprint.Status] = sprint.Down
			if b.Fresh(now) {
				want[sprint.Status] = sprint.Up
			}
		}
		if d := rowDiff(row, want); len(d) > 0 {
			diffs[row.Key] = d
		}
	}
	if len(diffs) == 0 {
		return false, nil
	}
	pinned, err := st.pin(ctx)
	if err != nil {
		return false, err
	}
	return true, pinned.setRows(ctx, shape.Name, diffs)
}

// rowDiff is the display cells of a row that differ from want.
func rowDiff(row ntable.Row, want map[string]string) map[string]string {
	diff := map[string]string{}
	for k, v := range want {
		if row.Texts[k] != v {
			diff[k] = v
		}
	}
	return diff
}

// RowsSetter is a store that writes the display cells of many rows of a
// table in one exchange.
type RowsSetter interface {
	RowsSet(ctx context.Context, table string, rows map[string]map[string]string) error
}

// setRows writes the display cells of every row that differs, in one exchange
// where the store can (the owner's rule: never a row at a time).
func (st *Store) setRows(ctx context.Context, table string, rows map[string]map[string]string) error {
	if len(rows) == 0 {
		return nil
	}
	if rs, ok := st.B.(RowsSetter); ok {
		return rs.RowsSet(ctx, table, rows)
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := st.B.RowSet(ctx, table, k, rows[k]); err != nil {
			return err
		}
	}
	return nil
}
