package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The resources table (sprint/resource.go; docs/SPEC-SPRINT.md section 19; the
// model is tla/Resources.tla): one record under the deployment's prefix,
// resources, outside the view's tables and the fence as the lanes and the
// friends' roster are, holding every resource's capacity, leases and line. A
// verb is one read of the record and one write; the server runs the members'
// verbs one at a time (run --listen), so no two steps of the record interleave.
// The tick's pass (ResourcesTick) is the one place a lease is released without
// its holder asking: at its expiry, or when its holder is down.

// keyResources is the resources record.
const keyResources = "resources"

// resources reads the record; none is an empty table.
func (st *Store) resources(ctx context.Context) (sprint.Resources, KV, error) {
	kv, err := st.rootKV()
	if err != nil {
		return nil, nil, err
	}
	out := sprint.Resources{}
	raw, ok, err := kv.GetKey(ctx, keyResources)
	if err != nil || !ok {
		return out, kv, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, kv, fmt.Errorf("the resources record cannot be read (%v); nothing was changed", err)
	}
	return out, kv, nil
}

func putResources(ctx context.Context, kv KV, rs sprint.Resources) error {
	b, err := json.Marshal(rs)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, keyResources, string(b))
}

// ResourceAdd is the coordinator's resource add: a new row at kind and capacity,
// or, for a row on the table, its capacity set (kind "" keeps the row's). What
// the capacity change granted from the line is returned.
func (st *Store) ResourceAdd(ctx context.Context, name, kind string, capacity int) (sprint.ResourceChange, error) {
	rs, kv, err := st.resources(ctx)
	if err != nil {
		return sprint.ResourceChange{}, err
	}
	var ch sprint.ResourceChange
	if _, ok := rs[name]; ok {
		if kind != "" && rs[name].Kind != kind {
			return ch, fmt.Errorf("resource %s is on the table as a %s; its kind does not change (remove it first)", name, rs[name].Kind)
		}
		rs, ch, err = rs.SetCapacity(name, capacity, st.now().UTC())
	} else {
		rs, err = rs.Add(name, kind, capacity)
		ch.Name = name
	}
	if err != nil {
		return ch, err
	}
	return ch, putResources(ctx, kv, rs)
}

// ResourceRemove is the coordinator's resource remove: refused while the row is
// held or waited for.
func (st *Store) ResourceRemove(ctx context.Context, name string) error {
	rs, kv, err := st.resources(ctx)
	if err != nil {
		return err
	}
	rs, err = rs.Remove(name)
	if err != nil {
		return err
	}
	return putResources(ctx, kv, rs)
}

// ResourceClaim is who's claim of name for a lease of d at the store's clock
// (sprint.Resources.Claim): the answer, with the record written.
func (st *Store) ResourceClaim(ctx context.Context, name, who string, d time.Duration) (sprint.ResourceAnswer, error) {
	rs, kv, err := st.resources(ctx)
	if err != nil {
		return sprint.ResourceAnswer{}, err
	}
	rs, ans, err := rs.Claim(name, who, d, st.now().UTC())
	if err != nil {
		return ans, err
	}
	return ans, putResources(ctx, kv, rs)
}

// ResourceRenew is who's renewal of its lease of name for d from the store's clock.
func (st *Store) ResourceRenew(ctx context.Context, name, who string, d time.Duration) (sprint.ResourceAnswer, error) {
	rs, kv, err := st.resources(ctx)
	if err != nil {
		return sprint.ResourceAnswer{}, err
	}
	rs, ans, err := rs.Renew(name, who, d, st.now().UTC())
	if err != nil {
		return ans, err
	}
	return ans, putResources(ctx, kv, rs)
}

// ResourceRelease is who's release of its lease of name, or of its place in the line.
func (st *Store) ResourceRelease(ctx context.Context, name, who string) (sprint.ResourceAnswer, error) {
	rs, kv, err := st.resources(ctx)
	if err != nil {
		return sprint.ResourceAnswer{}, err
	}
	rs, ans, err := rs.Release(name, who, st.now().UTC())
	if err != nil {
		return ans, err
	}
	return ans, putResources(ctx, kv, rs)
}

// ResourceRows is the table as a reader sees it (resource list): nothing is
// written. A store that keeps no records has no resources.
func (st *Store) ResourceRows(ctx context.Context) ([]sprint.ResourceRow, error) {
	if _, err := st.rootKV(); err != nil {
		return nil, nil // ignored: a store that keeps no records has no resources to show
	}
	rs, _, err := st.resources(ctx)
	if err != nil {
		return nil, err
	}
	return rs.Rows(), nil
}

// ResourceDown is whether a member is down as the resources' tick sees it: a
// friend of the roster whose status is not up (held, down by her beat or her
// observation, out of credit, no session evidence); a fleet member whose status
// is not up (held, or lapsed beats); a reader not up (away, held, retired, never
// beaten). A name no table knows is not down: nothing can observe it, and its
// lease runs to its expiry.
func (st *Store) ResourceDown(ctx context.Context, now time.Time) (func(string) bool, error) {
	down := map[string]bool{}
	friends, err := st.FriendRows(ctx, now)
	if err != nil {
		return nil, err
	}
	for _, f := range friends {
		down[f.Name] = f.Status != sprint.Up
	}
	s, err := st.Load(ctx, tables(sprint.Fleet, sprint.Readers), nil)
	if err != nil {
		if refusalCode(err) == "NOTABLE" {
			return func(who string) bool { return down[who] }, nil
		}
		return nil, err
	}
	members := s.Members()
	beats, err := st.Beats(ctx, members)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		if ctl := s.MemberCtl(m); ctl != nil {
			if _, ok := down[m]; !ok {
				down[m] = sprint.MemberStatus(ctl, beats[m], now) != sprint.Up
			}
		}
	}
	readers, err := st.ReaderRows(ctx)
	if err != nil {
		return nil, err
	}
	if len(readers) > 0 {
		states, err := st.ReaderStates(ctx, readers, now)
		if err != nil {
			return nil, err
		}
		for r, state := range states {
			if _, ok := down[r]; !ok {
				down[r] = state != sprint.ReaderUp
			}
		}
	}
	return func(who string) bool { return down[who] }, nil
}

// ResourcesTick is the tick's pass over the resources table (sprint.Resources.Tick):
// a lease past its expiry or whose holder is down is released and the line is
// served, each release a happened note; a resource with waiters and no room for
// sprint.ResourceStarveBound raises one judgment (NResourceStarved) while it
// stands, closed when room comes. The record is written before the notes, so a
// note is never of a release that was not made. A table with no lease and no
// line reads no table and writes nothing; a pass that changed nothing and found
// no judgment to close is a step that writes nothing.
func (st *Store) ResourcesTick(ctx context.Context) (Result, []sprint.ResourceChange, error) {
	var res Result
	rs, kv, err := st.resources(ctx)
	if kv == nil || err != nil {
		return res, nil, err
	}
	busy := false
	for _, r := range rs {
		if len(r.Holders)+len(r.Waiters) > 0 {
			busy = true
			break
		}
	}
	if !busy {
		return res, nil, nil
	}
	now := st.now().UTC()
	down, err := st.ResourceDown(ctx, now)
	if err != nil {
		return res, nil, err
	}
	rs, changes := rs.Tick(now, down)
	if len(changes) > 0 {
		if err := putResources(ctx, kv, rs); err != nil {
			return res, nil, err
		}
	}
	// a resource whose line has room again (a release by its holder, a wider
	// capacity) closes its judgment, whether or not the pass changed anything
	var served []string
	for _, name := range rs.Names() {
		if len(rs[name].Waiters) == 0 && !rs[name].Judged.IsZero() {
			served = append(served, name)
		}
	}
	if len(changes) == 0 && len(served) == 0 {
		return res, nil, nil
	}
	var raised, closed []string
	res, err = st.Run(ctx, Step{Verb: "tick resources", Actor: sprint.MachineActor, Load: tables(sprint.Fleet), Plan: func(s *sprint.Snapshot) sprint.Plan {
		var p sprint.Plan
		p, raised, closed = resourceNotes(s, rs, changes, served)
		return p
	}})
	if err != nil {
		return res, changes, err
	}
	if len(raised)+len(closed) > 0 {
		for _, name := range raised {
			r := rs[name]
			r.Judged = now
			rs[name] = r
		}
		for _, name := range closed {
			r := rs[name]
			r.Judged = time.Time{}
			rs[name] = r
		}
		if err := putResources(ctx, kv, rs); err != nil {
			return res, changes, err
		}
	}
	return res, changes, nil
}

// resourceNotes is the tick's plan for the changes: a happened note per release
// the tick made, the starved judgment raised once per resource while it stands,
// and closed once the line has room again (served names the resources with an
// empty line and a judgment standing, whoever emptied the line); raised and
// closed name the resources whose judgment it raised or closed, for the record.
func resourceNotes(s *sprint.Snapshot, rs sprint.Resources, changes []sprint.ResourceChange, served []string) (p sprint.Plan, raised, closed []string) {
	open := map[string]sprint.Open{}
	for _, o := range s.Open {
		if o.Note.Type == sprint.NResourceStarved {
			open[o.Subject()] = o
		}
	}
	for _, name := range served {
		closed = append(closed, name)
		if o, judged := open[sprint.ResourceSubject(name)]; judged {
			p.Closes = append(p.Closes, o)
			delete(open, sprint.ResourceSubject(name))
		}
	}
	for _, ch := range changes {
		if n := len(ch.Expired) + len(ch.Down); n > 0 {
			var what []string
			if len(ch.Expired) > 0 {
				what = append(what, "expired: "+strings.Join(ch.Expired, ", "))
			}
			if len(ch.Down) > 0 {
				what = append(what, "holder down: "+strings.Join(ch.Down, ", "))
			}
			if len(ch.Granted) > 0 {
				what = append(what, "granted: "+strings.Join(ch.Granted, ", "))
			}
			p.Notes = append(p.Notes, sprint.Note{Kind: sprint.Happened, Type: sprint.NResourceReleased, Who: sprint.MachineActor, At: s.Now,
				What: "resource " + ch.Name + ": " + strings.Join(what, "; ")})
		}
		sub := sprint.ResourceSubject(ch.Name)
		o, judged := open[sub]
		switch {
		case ch.Starving && !judged:
			r := rs[ch.Name]
			var holders, waiters []string
			for _, h := range r.Holders {
				holders = append(holders, h.Who+" until "+h.Until.UTC().Format(time.RFC3339))
			}
			for _, w := range r.Waiters {
				waiters = append(waiters, w.Who)
			}
			raised = append(raised, ch.Name)
			p.Notes = append(p.Notes, sprint.Note{Kind: sprint.Judgment, Type: sprint.NResourceStarved, Who: sprint.MachineActor, At: s.Now,
				Primaries: []string{sub}, Count: 1, Marked: true,
				Decisions: append([]string(nil), sprint.ResourceDecisions...),
				What: fmt.Sprintf("resource %s (%s, %d/%d) has had waiters and no room since %s: held by %s; waiting: %s", ch.Name, r.Kind, len(r.Holders), r.Capacity,
					r.Starved.UTC().Format(time.RFC3339), strings.Join(holders, ", "), strings.Join(waiters, ", "))})
		case !ch.Starving && judged && len(rs[ch.Name].Waiters) == 0:
			closed = append(closed, ch.Name)
			p.Closes = append(p.Closes, o)
			delete(open, sub)
		}
	}
	return p, raised, closed
}
