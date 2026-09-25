package card

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// ONE PLACE (nova-tools#3692; rowan-new specs/ws-index.md, "The card model").
// Glenn 2026-09-24 11:35 PM ET: "cards are not allowed to disappear."
// 2026-09-25 12:28 AM: "a card can only ever be in no set, or one of these
// sets that drive the tables. it can only be one place. this is invariant."
//
// The card is its record s:<S>:card:<label> (the id), never deleted, listed
// forever in sprint:<S>:cards. Its where field names its one place (empty:
// null, in no table set); the views of that place are ZSETs of card ids,
// bench:<b>:cards:<where> (plus :ok and :fail while done), ws:<stream>:<where>
// and friend:<owner>:cards:<where>, every score the card's created_at. The
// Lua primitive NS.card (internal/nsprint/fn/lua/02_card_move.lua) is the
// only writer; this file reads and checks it.

// Places are the where values, in table order.
var Places = []string{"waiting", "ready", "working", "done", "parked"}

// RosterKey is sprint:<S>:cards, every card of the sprint ever.
func RosterKey(sprint string) string { return "sprint:" + sprint + ":cards" }

// BenchCardsKey is the bench view of one place (or ok/fail), bench:<b>:cards:<where>.
func BenchCardsKey(bench, where string) string { return "bench:" + bench + ":cards:" + where }

// FsckReport is one ns_card_fsck or ns_card_repair reply.
type FsckReport struct {
	Sprint                                          string
	Cards, Null                                     int64
	Waiting, Ready, Working, Done, Parked, OK, Fail int64
	Drift, Fixed                                    int64
	Lines                                           []string // the first 50 drift lines
}

// Line is the receipt: FSCK <S> cards=... drift=... fixed=....
func (r FsckReport) Line(verb string) string {
	return fmt.Sprintf("%s sprint=%s cards=%d null=%d waiting=%d ready=%d working=%d done=%d parked=%d ok=%d fail=%d drift=%d fixed=%d",
		verb, r.Sprint, r.Cards, r.Null, r.Waiting, r.Ready, r.Working, r.Done, r.Parked, r.OK, r.Fail, r.Drift, r.Fixed)
}

// Clean is true when the walk found no drift, or repair fixed all it found.
func (r FsckReport) Clean() bool { return r.Drift == 0 || r.Fixed >= r.Drift }

// Fsck walks the sprint's cards in both directions in one read-only function
// call (ns_card_fsck); repair (ns_card_repair) rewrites what it finds and
// adopts every record the sprint's indexes name that predates the model (the
// one-time reindex). No SCAN: the walk reads sprint:<S>:cards, the state
// indexes, the waiting set and the pool.
func Fsck(ctx context.Context, client redis.UniversalClient, sprint string, repair bool) (FsckReport, error) {
	if !sprintRE.MatchString(sprint) {
		return FsckReport{}, fmt.Errorf("sprint name must match [a-z0-9-]{1,40}")
	}
	fname := "ns_card_fsck"
	call := client.FCallRO
	if repair {
		fname, call = "ns_card_repair", client.FCall
	}
	reply, err := call(ctx, fname, nil, sprint).StringSlice()
	if err != nil {
		return FsckReport{}, fmt.Errorf("%s: %w", fname, err)
	}
	if len(reply) < 13 || reply[0] != "FSCK" {
		return FsckReport{}, fmt.Errorf("%s reply %q", fname, reply)
	}
	n := make([]int64, 11)
	for i := range n {
		v, err := strconv.ParseInt(reply[2+i], 10, 64)
		if err != nil {
			return FsckReport{}, fmt.Errorf("%s reply field %d %q", fname, 2+i, reply[2+i])
		}
		n[i] = v
	}
	return FsckReport{Sprint: reply[1], Cards: n[0], Null: n[1], Waiting: n[2], Ready: n[3], Working: n[4],
		Done: n[5], Parked: n[6], OK: n[7], Fail: n[8], Drift: n[9], Fixed: n[10], Lines: reply[13:]}, nil
}

// Unplaced lists the sprint's null cards (where empty: created, in no table
// set), oldest first: the roster and one pipelined HGET per card.
func Unplaced(ctx context.Context, client redis.UniversalClient, sprint string) ([]string, error) {
	ids, err := client.ZRange(ctx, RosterKey(sprint), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGet(ctx, id, "where")
	}
	if len(ids) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, err
		}
	}
	var out []string
	for i, id := range ids {
		if w, err := cmds[i].Result(); err == nil && w == "" {
			out = append(out, id)
		}
	}
	return out, nil
}

// MembersReport is one ns_card_members or ns_card_members_repair reply: the
// walk of every set that drives a table (nova-tools#4054).
type MembersReport struct {
	Sets, Members, Bad, Removed int64
	Lines                       []string // MEMBER-NOT-A-CARD <set> <member> (or WRONGTYPE <set>), the first 50
}

// Clean is true when every member is a record, or repair removed each one
// that was not.
func (r MembersReport) Clean() bool { return r.Bad == 0 || r.Removed >= r.Bad }

// Members walks every table set (ws:<stream>:<where> for each stream of
// ws:order and ws:names, bench:<b>:cards:* for each registered bench and
// _pool, friend:<f>:cards:* for each member of friends) in one function
// call and names each member that is not the id of an existing card or task
// record: MEMBER-NOT-A-CARD <set> <member>. repair removes each one with a
// receipt on ws:log whose by is by. A member that is no sprint's card is in
// no sprint's Fsck, so this is the other half of card fsck.
func Members(ctx context.Context, client redis.UniversalClient, repair bool, by string) (MembersReport, error) {
	fname := "ns_card_members"
	var cmd *redis.Cmd
	if repair {
		fname = "ns_card_members_repair"
		cmd = client.FCall(ctx, fname, nil, by)
	} else {
		cmd = client.FCallRO(ctx, fname, nil)
	}
	reply, err := cmd.StringSlice()
	if err != nil {
		return MembersReport{}, fmt.Errorf("%s: %w", fname, err)
	}
	return ParseMembers(fname, reply)
}

// ParseMembers reads a MEMBERS sets members bad removed line... reply.
func ParseMembers(fname string, reply []string) (MembersReport, error) {
	if len(reply) < 5 || reply[0] != "MEMBERS" {
		return MembersReport{}, fmt.Errorf("%s reply %q", fname, reply)
	}
	n := make([]int64, 4)
	for i := range n {
		v, err := strconv.ParseInt(reply[1+i], 10, 64)
		if err != nil {
			return MembersReport{}, fmt.Errorf("%s reply field %d %q", fname, 1+i, reply[1+i])
		}
		n[i] = v
	}
	return MembersReport{Sets: n[0], Members: n[1], Bad: n[2], Removed: n[3], Lines: reply[5:]}, nil
}
