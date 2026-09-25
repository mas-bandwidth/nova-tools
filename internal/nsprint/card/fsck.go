package card

import (
	"context"
	"fmt"
	"sort"
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
	Nomirror                                        map[string][]string // bench -> repos with no mirror
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
	rep := FsckReport{Sprint: reply[1], Cards: n[0], Null: n[1], Waiting: n[2], Ready: n[3], Working: n[4],
		Done: n[5], Parked: n[6], OK: n[7], Fail: n[8], Drift: n[9], Fixed: n[10], Lines: reply[13:]}

	benches, err := client.SMembers(ctx, "benches").Result()
	if err == nil {
		pipe := client.Pipeline()
		cmds := make(map[string]*redis.StringSliceCmd)
		for _, b := range benches {
			cmds[b] = pipe.SMembers(ctx, "ci:nomirror:"+b)
		}
		if len(cmds) > 0 {
			if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
				// nomirror read failure is not fatal to fsck
			}
		}
		rep.Nomirror = map[string][]string{}
		for _, b := range benches {
			if nm, err := cmds[b].Result(); err == nil && len(nm) > 0 {
				sort.Strings(nm)
				rep.Nomirror[b] = nm
			}
		}
	}

	return rep, nil
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
