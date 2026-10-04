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
// table): two kinds of record under the deployment's prefix, outside the
// tables, the fence and the epochs, as a reader's beat and hold are. friends
// is the roster, every friend of nova-config's friend rows (friend sync) with
// the coordinator's hold of each (friend down; friend up releases it) and her
// width; friend-beat:<f> is the friend's last beat (friend beat), written by
// the friend's own machinery. A friend's status is derived when it is shown,
// never stored, by the friends' rule (sprint.FriendStatus): held, else up
// while her last beat is within sprint.FriendDownAfter (15 s), else down. Her
// counts are her sprint cards' (the cards dealt to her fleet row friend.<name>,
// read from the fleet table by where, never stored as a record here):
// (the owner, 2026-10-02: "give friends in the friends table the same ready,
// working, width, done, ok%, status that we have for machines, but no load").

const keyFriends = "friends"

func friendBeatKey(friend string) string { return "friend-beat:" + friend }

// friendEntry is a friend's entry in the roster: the coordinator's hold,
// empty while released, and her width, how many jobs she works at once.
type friendEntry struct {
	Held  bool      `json:"held,omitempty"`
	At    time.Time `json:"at,omitempty"`
	By    string    `json:"by,omitempty"`
	Width int       `json:"width,omitempty"`
}

// FriendSpec is what friend sync knows of one friend: her name (a friend row
// of nova-config) and her width.
type FriendSpec struct {
	Name  string
	Width int
}

// FriendRow is one row of the friends table as where draws it: the counts of
// her sprint cards (filled by where from her fleet row), her width and her
// status (filled by FriendRows from the roster and her beat).
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

// SyncFriends makes the roster the friends given (nova-config's friend rows,
// each with her width): a friend it lacks is added, released, at her width; a
// friend it has that the specs lack is taken off with her beat; a friend that
// stays keeps her hold, and her width is set from the spec. It writes nothing
// when there is nothing to change, and says who was added, who taken off and
// who stayed with a width that changed, each in name order.
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
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(updated)
	if !rosterChanged {
		return nil, nil, nil, nil
	}
	if err := putRoster(ctx, kv, r); err != nil {
		return nil, nil, nil, err
	}
	if len(removed) > 0 {
		keys := make([]string, 0, len(removed))
		for _, n := range removed {
			keys = append(keys, st.Names.Key(friendBeatKey(n)))
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

// FriendRows is the friends table at now: every friend of the roster with her
// width and her status (sprint.FriendStatus), in the fleet table's order
// (FleetOrder: up, then held, then down, each by name). The counts (ready,
// working, ok, failed) are her sprint cards', filled by where from her fleet
// row (friend.<name>), never read or counted here: the store holds no job
// record. Two reads: the roster, then every friend's beat in one exchange. A
// store that keeps no records has no friends.
func (st *Store) FriendRows(ctx context.Context, now time.Time) ([]FriendRow, error) {
	r, kv, err := st.roster(ctx)
	if kv == nil {
		return nil, nil // a store that keeps no records: no friend
	}
	if err != nil || len(r) == 0 {
		return nil, err
	}
	names := slices.Sorted(maps.Keys(r))
	keys := make([]string, 0, len(names))
	for _, n := range names {
		keys = append(keys, friendBeatKey(n))
	}
	vals, oks, err := getKeys(ctx, kv, keys)
	if err != nil {
		return nil, err
	}
	rows := map[string]FriendRow{}
	status := map[string]string{}
	for i, n := range names {
		var b sprint.Beat
		if i < len(oks) && oks[i] {
			// ignored: an unreadable record is no beat, which the next beat replaces
			_ = json.Unmarshal([]byte(vals[i]), &b)
		}
		row := FriendRow{Name: n, Width: r[n].Width, Status: sprint.FriendStatus(r[n].Held, b, now)}
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
