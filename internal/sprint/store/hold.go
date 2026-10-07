package store

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/nova-tools/internal/sprint"
)

// HoldStep is hold or unhold of members, readers, friends and streams as one step
// (sprint.HoldNames, docs/SPEC-SPRINT.md section 11): it names its targets, so it applies
// all or none, and its notes are the log's lines of the hold.
func HoldStep(r sprint.HoldReq) Step {
	verb := "hold"
	if r.Release {
		verb = "unhold"
	}
	// the arguments a retry under one --op is held to: the names, reason and flag, never
	// the roster or the beats read for it
	args := r
	args.Friends, args.Alive = nil, nil
	return Step{Named: true, Args: ArgsOf(args), Verb: verb, Load: tables(sprint.Fleet, sprint.Work, sprint.Merge, sprint.Readers), Mirrors: true, Readers: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.HoldNames(s, r) }}
}

// Hold runs hold or unhold (docs/SPEC-SPRINT.md section 11): HoldReqOf, HoldStep, and when
// the step commits whole, WriteHoldRecords.
func (st *Store) Hold(ctx context.Context, r sprint.HoldReq) (Result, error) {
	r, err := st.HoldReqOf(ctx, r)
	if err != nil {
		return Result{}, err
	}
	res, err := st.Run(ctx, HoldStep(r))
	if err != nil || len(res.Refused) > 0 {
		return res, err
	}
	return res, st.WriteHoldRecords(ctx, r)
}

// HoldReqOf is the request with what the step reads outside the tables: the friend
// roster's names (the names a friend's hold may name) and, for an unhold, the names whose
// beat is alive now (a member alive comes up at once).
func (st *Store) HoldReqOf(ctx context.Context, r sprint.HoldReq) (sprint.HoldReq, error) {
	roster, _, err := st.roster(ctx)
	if err != nil {
		return r, err
	}
	r.Friends = slices.Sorted(maps.Keys(roster))
	r.Alive = nil
	if r.Release {
		beats, err := st.Beats(ctx, r.Names)
		if err != nil {
			return r, err
		}
		for _, n := range r.Names {
			if beats[n].Alive(st.now()) {
				r.Alive = append(r.Alive, n)
			}
		}
	}
	return r, nil
}

// WriteHoldRecords writes the hold records of the readers and friends a committed hold
// or unhold named, outside the tables as their presence is (readers.go, friends.go), the
// reason in each; a member's and a stream's hold the step wrote on its control card.
func (st *Store) WriteHoldRecords(ctx context.Context, r sprint.HoldReq) error {
	readers, err := st.ReaderRows(ctx)
	if err != nil {
		return err
	}
	for _, n := range r.Names {
		switch {
		case (r.Kind == "" || r.Kind == sprint.HoldReader) && slices.Contains(readers, n):
			err = st.holdReader(ctx, n, r)
		case (r.Kind == "" || r.Kind == sprint.HoldFriend) && slices.Contains(r.Friends, n):
			err = st.setFriendHold(ctx, n, r)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// holdReader writes a reader's hold record: held with the reason, or empty on unhold.
func (st *Store) holdReader(ctx context.Context, reader string, r sprint.HoldReq) error {
	hold := readerHold{}
	if !r.Release {
		hold = readerHold{Away: true, Held: true, Reason: r.Reason, Return: r.Return}
	}
	return st.setReaderHold(ctx, reader, hold, r.Who)
}

// setFriendHold writes a friend's hold in the roster: held with the reason, or released.
func (st *Store) setFriendHold(ctx context.Context, friend string, r sprint.HoldReq) error {
	ros, kv, err := st.roster(ctx)
	if err != nil {
		return err
	}
	e, ok := ros[friend]
	if !ok {
		return noFriend(ros, friend)
	}
	e.Held, e.At, e.By, e.Reason, e.Return = false, time.Time{}, "", "", false
	if !r.Release {
		e.Held, e.At, e.By, e.Reason, e.Return = true, st.now().UTC().Truncate(time.Second), r.Who, r.Reason, r.Return
	}
	ros[friend] = e
	return putRoster(ctx, kv, ros)
}

// Holds is every hold in force (sprint.HoldView), for where --json and handover: each
// member and stream whose control card is held, each reader and friend whose record is,
// with the reason, who and when; members in the fleet table's order, then readers in theirs, then friends and
// streams by name. A store that keeps no records shows the tables' holds only.
func (st *Store) Holds(ctx context.Context) ([]sprint.HoldView, error) {
	pinned, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	var out []sprint.HoldView
	shapes, err := pinned.B.Shapes(ctx, []string{pinned.Names.Table(sprint.Fleet), pinned.Names.Table(sprint.Merge)})
	if refusalCode(err) == "NOTABLE" {
		return nil, nil // no sprint yet: nothing held
	}
	if err != nil {
		return nil, err
	}
	var streams []sprint.HoldView
	for _, shape := range shapes {
		var ids []string
		for _, row := range shape.Rows {
			ids = append(ids, pinned.sid(sprint.CtlID(row.Key)))
		}
		if len(ids) == 0 {
			continue
		}
		rs, err := pinned.readSet(ctx, shape.Name, ids)
		if err != nil {
			return nil, err
		}
		for _, row := range shape.Rows {
			ctl, ok := rs.Member(pinned.sid(sprint.CtlID(row.Key)))
			if !ok || ctl.Fields["held"] == "" {
				continue
			}
			if shape.Name == pinned.Names.Table(sprint.Merge) {
				streams = append(streams, sprint.HoldView{Kind: sprint.HoldStream, Name: row.Key, Reason: ctl.Fields[sprint.FieldHeldReason], At: ctl.Fields["held"]})
				continue
			}
			if sprint.IsFriendRow(row.Key) {
				continue
			}
			out = append(out, sprint.HoldView{Kind: sprint.HoldMember, Name: row.Key, Reason: ctl.Fields[sprint.FieldHeldReason], By: ctl.Fields[sprint.FieldHeldBy],
				At: ctl.Fields["held"], Return: ctl.Fields[sprint.FieldHeldFinish] == ""})
		}
	}
	kv, err := st.rootKV()
	if err != nil {
		return append(out, streams...), nil // a store that keeps no records: no reader's or friend's hold
	}
	readers, err := st.ReaderRows(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(readers))
	for i, r := range readers {
		keys[i] = readerAwayKey(r)
	}
	vals, oks, err := getKeys(ctx, kv, keys)
	if err != nil {
		return nil, err
	}
	for i, r := range readers {
		var h readerHold
		if i < len(oks) && oks[i] {
			// ignored: an unreadable record is no hold, which the next hold or unhold replaces
			_ = json.Unmarshal([]byte(vals[i]), &h)
		}
		if h.Away {
			out = append(out, sprint.HoldView{Kind: sprint.HoldReader, Name: r, Reason: h.Reason, By: h.By, At: stampOf(h.At), Return: h.Return || !h.Held})
		}
	}
	ros, _, err := st.roster(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range slices.Sorted(maps.Keys(ros)) {
		if e := ros[n]; e.Held {
			out = append(out, sprint.HoldView{Kind: sprint.HoldFriend, Name: n, Reason: e.Reason, By: e.By, At: stampOf(e.At), Return: e.Return})
		}
	}
	slices.SortStableFunc(streams, func(a, b sprint.HoldView) int { return cmp.Compare(a.Name, b.Name) })
	return append(out, streams...), nil
}

// stampOf is a time as the control cards stamp it (RFC 3339, UTC), "" for none.
func stampOf(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
