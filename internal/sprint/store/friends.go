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
// the coordinator's hold of each (friend down; friend up releases it);
// friend-beat:<f> is the friend's last beat (friend beat), written by the
// friend's own machinery. A friend's status is derived when it is shown, never
// stored, by the fleet's rule (sprint.PresenceStatus): held, else up while its
// beat is alive, else down.

const keyFriends = "friends"

func friendBeatKey(friend string) string { return "friend-beat:" + friend }

// friendHold is a friend's entry in the roster: the coordinator's hold, empty
// while released.
type friendHold struct {
	Held bool      `json:"held,omitempty"`
	At   time.Time `json:"at,omitempty"`
	By   string    `json:"by,omitempty"`
}

// FriendRow is one row of the friends table as where draws it.
type FriendRow struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// roster is the friends record, by name; empty when there is none.
func (st *Store) roster(ctx context.Context) (map[string]friendHold, KV, error) {
	kv, err := st.rootKV()
	if err != nil {
		return nil, nil, err
	}
	out := map[string]friendHold{}
	raw, ok, err := kv.GetKey(ctx, keyFriends)
	if err != nil || !ok {
		return out, kv, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, kv, fmt.Errorf("the friends record cannot be read (%v); run: nova-sprint friend sync", err)
	}
	return out, kv, nil
}

func putRoster(ctx context.Context, kv KV, r map[string]friendHold) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, keyFriends, string(b))
}

// noFriend is the refusal of a name the roster lacks.
func noFriend(r map[string]friendHold, friend string) error {
	names := slices.Sorted(maps.Keys(r))
	if len(names) == 0 {
		names = []string{"none"}
	}
	return fmt.Errorf("no friend %s on the friends table (friends: %s): its row is nova-config's friend row; run: nova-sprint friend sync", friend, strings.Join(names, ","))
}

// SyncFriends makes the roster the names (nova-config's friend rows): a name
// it lacks is added, released; a friend it has that the names lack is taken
// off and its beat forgotten; a friend that stays keeps its hold. It writes
// nothing when there is nothing to change, and says who was added and who
// taken off, each in name order.
func (st *Store) SyncFriends(ctx context.Context, names []string) (added, removed []string, err error) {
	r, kv, err := st.roster(ctx)
	if err != nil {
		return nil, nil, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
		if _, ok := r[n]; !ok {
			added = append(added, n)
			r[n] = friendHold{}
		}
	}
	for n := range r {
		if !want[n] {
			removed = append(removed, n)
			delete(r, n)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	if len(added) == 0 && len(removed) == 0 {
		return nil, nil, nil
	}
	if err := putRoster(ctx, kv, r); err != nil {
		return nil, nil, err
	}
	if len(removed) > 0 {
		keys := make([]string, len(removed))
		for i, n := range removed {
			keys[i] = st.Names.Key(friendBeatKey(n))
		}
		if _, err := st.B.DeleteKeys(ctx, keys); err != nil {
			return added, removed, err
		}
	}
	return added, removed, nil
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
	if _, ok := r[friend]; !ok {
		return noFriend(r, friend)
	}
	h := friendHold{}
	if held {
		h = friendHold{Held: true, At: st.now().UTC().Truncate(time.Second), By: who}
	}
	r[friend] = h
	return putRoster(ctx, kv, r)
}

// FriendRows is the friends table at now: every friend of the roster with its
// status (sprint.PresenceStatus), in the fleet table's order (FleetOrder: up,
// then held, then down, each by name). Two reads: the roster, then every
// friend's beat in one exchange. A store that keeps no records has no friends.
func (st *Store) FriendRows(ctx context.Context, now time.Time) ([]FriendRow, error) {
	r, kv, err := st.roster(ctx)
	if kv == nil {
		return nil, nil // a store that keeps no records: no friend
	}
	if err != nil || len(r) == 0 {
		return nil, err
	}
	names := slices.Sorted(maps.Keys(r))
	keys := make([]string, len(names))
	for i, n := range names {
		keys[i] = friendBeatKey(n)
	}
	vals, oks, err := getKeys(ctx, kv, keys)
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	for i, n := range names {
		var b sprint.Beat
		if i < len(oks) && oks[i] {
			// ignored: an unreadable record is no beat, which the next beat replaces
			_ = json.Unmarshal([]byte(vals[i]), &b)
		}
		status[n] = sprint.PresenceStatus(r[n].Held, b, now)
	}
	out := make([]FriendRow, 0, len(names))
	for _, n := range FleetOrder(names, status) {
		out = append(out, FriendRow{Name: n, Status: status[n]})
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
