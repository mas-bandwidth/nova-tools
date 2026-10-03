package store

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The friends (sprint.Friends; docs/SPEC-SPRINT.md section 1, the friends
// table): three kinds of record under the deployment's prefix, outside the
// tables, the fence and the epochs, as a reader's beat and hold are. friends
// is the roster, every friend of nova-config's friend rows (friend sync) with
// the coordinator's hold of each (friend down; friend up releases it) and her
// width; friend-jobs:<f> is the friend's job cards, what friend sync last read
// of her working directory (each job of her inbox, ready, working or done, and
// done ok or not); friend-beat:<f> is the friend's last beat (friend beat),
// written by the friend's own machinery. A friend's status is derived when it
// is shown, never stored, by the friends' rule (sprint.FriendStatus): held,
// else up while her last beat is within sprint.FriendDownAfter (15 s), else
// down. Her counts are her job cards'
// (the owner, 2026-10-02: "give friends in the friends table the same ready,
// working, width, done, ok%, status that we have for machines, but no load").

const keyFriends = "friends"

func friendBeatKey(friend string) string { return "friend-beat:" + friend }
func friendJobsKey(friend string) string { return "friend-jobs:" + friend }

// Job states, the friends table's columns a job is counted in.
const (
	JobReady   = "ready"
	JobWorking = "working"
	JobDone    = "done"
)

// friendEntry is a friend's entry in the roster: the coordinator's hold,
// empty while released, and her width, how many jobs she works at once.
type friendEntry struct {
	Held  bool      `json:"held,omitempty"`
	At    time.Time `json:"at,omitempty"`
	By    string    `json:"by,omitempty"`
	Width int       `json:"width,omitempty"`
}

// FriendJob is one job card: a job of the friend's inbox by its directory's
// name, ready, working or done, and when done whether it came back ok.
type FriendJob struct {
	ID    string `json:"id"`
	State string `json:"state"`
	OK    bool   `json:"ok,omitempty"`
}

// FriendSpec is what friend sync knows of one friend: her name (a friend row
// of nova-config), her width, and her jobs as her working directory has them.
type FriendSpec struct {
	Name  string
	Width int
	Jobs  []FriendJob
}

// FriendRow is one row of the friends table as where draws it: the counts of
// her job cards, her width and her status.
type FriendRow struct {
	Name    string `json:"name"`
	Ready   int    `json:"ready"`
	Working int    `json:"working"`
	Width   int    `json:"width"`
	OK      int    `json:"ok"`
	Failed  int    `json:"failed"`
	Status  string `json:"status"`
}

// roster is the friends record, by name; empty when there is none.
func (st *Store) roster(ctx context.Context) (map[string]friendEntry, KV, error) {
	kv, err := st.rootKV()
	if err != nil {
		return nil, nil, err
	}
	out := map[string]friendEntry{}
	raw, ok, err := kv.GetKey(ctx, keyFriends)
	if err != nil || !ok {
		return out, kv, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, kv, fmt.Errorf("the friends record cannot be read (%v); run: nova-sprint friend sync", err)
	}
	return out, kv, nil
}

func putRoster(ctx context.Context, kv KV, r map[string]friendEntry) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, keyFriends, string(b))
}

// noFriend is the refusal of a name the roster lacks.
func noFriend(r map[string]friendEntry, friend string) error {
	names := slices.Sorted(maps.Keys(r))
	if len(names) == 0 {
		names = []string{"none"}
	}
	return fmt.Errorf("no friend %s on the friends table (friends: %s): its row is nova-config's friend row; run: nova-sprint friend sync", friend, strings.Join(names, ","))
}

// jobsJSON is a friend's job cards as the record holds them: in id order, so
// the same jobs read twice are the same bytes and a sync after a sync writes
// nothing; "" for none.
func jobsJSON(jobs []FriendJob) string {
	if len(jobs) == 0 {
		return ""
	}
	sorted := slices.Clone(jobs)
	slices.SortFunc(sorted, func(a, b FriendJob) int { return strings.Compare(a.ID, b.ID) })
	b, _ := json.Marshal(sorted) // a slice of three plain fields marshals
	return string(b)
}

// SyncFriends makes the roster the friends given (nova-config's friend rows,
// each with her width and the jobs of her working directory): a friend it
// lacks is added, released, at her width with her jobs; a friend it has that
// the specs lack is taken off with her beat and her jobs; a friend that stays
// keeps her hold, and her width and jobs are set from the spec. The job
// records are set from the specs, never added to. It writes nothing when
// there is nothing to change, and says who was added, who taken off and who
// stayed with a width or jobs that changed, each in name order.
func (st *Store) SyncFriends(ctx context.Context, specs []FriendSpec) (added, removed, updated []string, err error) {
	r, kv, err := st.roster(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	want := map[string]FriendSpec{}
	rosterChanged := false
	for _, s := range specs {
		want[s.Name] = s
		e, had := r[s.Name]
		switch {
		case !had:
			added = append(added, s.Name)
			rosterChanged = true
		case e.Width != s.Width:
			updated = append(updated, s.Name)
			rosterChanged = true
		}
		e.Width = s.Width
		r[s.Name] = e
	}
	for n := range r {
		if _, ok := want[n]; !ok {
			removed = append(removed, n)
			delete(r, n)
			rosterChanged = true
		}
	}
	// the jobs that differ: one read of every friend's record
	names := slices.Sorted(maps.Keys(want))
	keys := make([]string, len(names))
	for i, n := range names {
		keys[i] = friendJobsKey(n)
	}
	vals, oks, err := getKeys(ctx, kv, keys)
	if err != nil {
		return nil, nil, nil, err
	}
	type write struct{ key, val string }
	var writes []write
	for i, n := range names {
		have := ""
		if i < len(oks) && oks[i] {
			have = vals[i]
		}
		if j := jobsJSON(want[n].Jobs); j != have {
			writes = append(writes, write{friendJobsKey(n), j})
			if !slices.Contains(added, n) && !slices.Contains(updated, n) {
				updated = append(updated, n)
			}
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(updated)
	if !rosterChanged && len(writes) == 0 {
		return nil, nil, nil, nil
	}
	if rosterChanged {
		if err := putRoster(ctx, kv, r); err != nil {
			return nil, nil, nil, err
		}
	}
	for _, w := range writes {
		if w.val == "" {
			if _, err := st.B.DeleteKeys(ctx, []string{st.Names.Key(w.key)}); err != nil {
				return added, removed, updated, err
			}
			continue
		}
		if err := kv.SetKey(ctx, w.key, w.val); err != nil {
			return added, removed, updated, err
		}
	}
	if len(removed) > 0 {
		keys := make([]string, 0, 2*len(removed))
		for _, n := range removed {
			keys = append(keys, st.Names.Key(friendBeatKey(n)), st.Names.Key(friendJobsKey(n)))
		}
		if _, err := st.B.DeleteKeys(ctx, keys); err != nil {
			return added, removed, updated, err
		}
	}
	return added, removed, updated, nil
}

// FriendBeat writes one beat of the friend at the store's clock, to the
// second; a friend the roster lacks is refused and nothing is written.
func (st *Store) FriendBeat(ctx context.Context, friend string) (sprint.Beat, error) {
	r, kv, err := st.roster(ctx)
	if err != nil {
		return sprint.Beat{}, err
	}
	if _, ok := r[friend]; !ok {
		return sprint.Beat{}, noFriend(r, friend)
	}
	b := sprint.Beat{At: st.now().UTC().Truncate(time.Second)}
	out, err := json.Marshal(b)
	if err != nil {
		return b, err
	}
	return b, kv.SetKey(ctx, friendBeatKey(friend), string(out))
}

// SetFriendHeld holds the friend (friend down) or releases the hold (friend
// up), by the coordinator who; a friend the roster lacks is refused.
func (st *Store) SetFriendHeld(ctx context.Context, friend string, held bool, who string) error {
	r, kv, err := st.roster(ctx)
	if err != nil {
		return err
	}
	e, ok := r[friend]
	if !ok {
		return noFriend(r, friend)
	}
	e.Held, e.At, e.By = false, time.Time{}, ""
	if held {
		e.Held, e.At, e.By = true, st.now().UTC().Truncate(time.Second), who
	}
	r[friend] = e
	return putRoster(ctx, kv, r)
}

// FriendRows is the friends table at now: every friend of the roster with the
// counts of her job cards (working 0 while she is down), her width and her
// status (sprint.FriendStatus), in the fleet table's order (FleetOrder: up,
// then held, then down, each by name). Two reads: the roster, then every friend's beat and jobs in one
// exchange. A store that keeps no records has no friends.
func (st *Store) FriendRows(ctx context.Context, now time.Time) ([]FriendRow, error) {
	r, kv, err := st.roster(ctx)
	if kv == nil {
		return nil, nil // a store that keeps no records: no friend
	}
	if err != nil || len(r) == 0 {
		return nil, err
	}
	names := slices.Sorted(maps.Keys(r))
	keys := make([]string, 0, 2*len(names))
	for _, n := range names {
		keys = append(keys, friendBeatKey(n), friendJobsKey(n))
	}
	vals, oks, err := getKeys(ctx, kv, keys)
	if err != nil {
		return nil, err
	}
	rows := map[string]FriendRow{}
	status := map[string]string{}
	for i, n := range names {
		var b sprint.Beat
		var jobs []FriendJob
		if k := 2 * i; k < len(oks) && oks[k] {
			// ignored: an unreadable record is no beat, which the next beat replaces
			_ = json.Unmarshal([]byte(vals[k]), &b)
		}
		if k := 2*i + 1; k < len(oks) && oks[k] {
			// ignored: an unreadable record is no jobs, which the next friend sync replaces
			_ = json.Unmarshal([]byte(vals[k]), &jobs)
		}
		row := FriendRow{Name: n, Width: r[n].Width, Status: sprint.FriendStatus(r[n].Held, b, now)}
		for _, j := range jobs {
			switch {
			case j.State == JobReady:
				row.Ready++
			case j.State == JobWorking:
				row.Working++
			case j.State == JobDone && j.OK:
				row.OK++
			case j.State == JobDone:
				row.Failed++
			}
		}
		if row.Status == sprint.Down {
			// down, she works nothing: her jobs stay in her outbox and count
			// again when she beats (the owner, 2026-10-02 9:48 PM ET: "[a
			// friend] being down, she automatically is 0/8 working OK?")
			row.Working = 0
		}
		rows[n] = row
		status[n] = row.Status
	}
	out := make([]FriendRow, 0, len(names))
	for _, n := range FleetOrder(names, status) {
		out = append(out, rows[n])
	}
	return out, nil
}

// friendNames is every friend of the roster, for teardown; none when the
// store keeps no records or the record cannot be read.
func (st *Store) friendNames(ctx context.Context) []string {
	r, _, err := st.roster(ctx)
	if err != nil {
		return nil
	}
	return slices.Sorted(maps.Keys(r))
}

// friendSeats is every friend of the roster as the tick's deal gives her a friend's card
// (sprint.FriendDeal): her name, width and status at now, read only when the snapshot
// holds a friend's card ready; nil, and no read, when it holds none.
func (st *Store) friendSeats(ctx context.Context, s *sprint.Snapshot, now time.Time) ([]sprint.FriendSeat, error) {
	ready := false
	for _, c := range s.Work.Column(sprint.Ready) {
		if _, ok := sprint.FriendCard(c); ok {
			ready = true
			break
		}
	}
	if !ready {
		return nil, nil
	}
	rows, err := st.FriendRows(ctx, now)
	if err != nil {
		return nil, err
	}
	seats := make([]sprint.FriendSeat, len(rows))
	for i, r := range rows {
		seats[i] = sprint.FriendSeat{Name: r.Name, Width: r.Width, Status: r.Status}
	}
	return seats, nil
}

// FriendNames is every friend of the roster in name order (the friends table's rows), for
// add's hold of a WHO line's name; none when the store keeps no records.
func (st *Store) FriendNames(ctx context.Context) ([]string, error) {
	r, kv, err := st.roster(ctx)
	if kv == nil || err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(r)), nil
}
