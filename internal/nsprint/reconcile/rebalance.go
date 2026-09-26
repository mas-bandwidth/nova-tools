package reconcile

// A friend status change rebalances that friend's cards in the same tick
// (nova-tools #4145). Glenn 2026-09-25 4:32 PM ET: "A change in friend
// status should automatically trigger a rebalance of ready tasks." That day
// emma went down and her 15 ready tasks sat on friend:emma:cards:ready until
// a hand move.
//
// A friend is down when friend:<f>:down exists or its row's beat (friend:<f>
// at) is older than FriendLive, the window the deal duty uses; else up. The
// targets are the friends the deal duty would deal: no marker, a live beat. The
// deal duty keeps each friend's last seen status in SeenKey and, at the top of
// every pass, calls ns_friend_rebalance (deal_friend.lua) for every friend
// whose status changed since the last pass, and for any down friend with
// cards still on its ready or working set: its ready cards and lapsed leases move to the up friends with
// open slots, else back to the stream's ready set, where the same pass deals
// them. `nova-sprint friend down|up` calls RebalanceFriend right after it
// writes friend:<f>:down, so the verb and the duty print the same line:
//
//	REBALANCE friend=<f> from=<old> to=<new> moved=<n> <id>:<dest>...
//	    [refused=<n> <id>:<why>...] [REFUSED <reason>]
//
// with at most RebalanceShown pairs of each kind, then +N more. A friend
// seen for the first time with nothing on it is recorded without a line.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// SeenKey is the hash of each friend's last seen status, up or down.
const SeenKey = "friend:status:seen"

// Friend statuses as SeenKey keeps them.
const (
	StatusUp   = "up"
	StatusDown = "down"
)

// RebalanceShown caps the pairs one REBALANCE line names; the rest are
// counted as +N more.
const RebalanceShown = 12

// Rebalanced is one friend's rebalance.
type Rebalanced struct {
	Friend, From, To string
	Moves            [][2]string // (id, dest) in move order; dest "ready" is the stream's ready set
	Refused          [][2]string // (id, why)
	Reason           string      // set when the Lua refused: ready not empty, nothing moved
	Line             string
}

// friendStatus is what one pipelined read says of every friend.
type friendStatus struct {
	names             []string
	down, stale, live map[string]bool
	ready, working    map[string]int64
	seen              map[string]string
}

func (s friendStatus) status(f string) string {
	if s.down[f] {
		return StatusDown
	}
	return StatusUp
}

// up is every live friend (no marker, a beat within FriendLive, as the deal
// duty deals), by name: the rebalance's targets.
func (s friendStatus) up() []string {
	var out []string
	for _, f := range s.names {
		if s.live[f] {
			out = append(out, f)
		}
	}
	return out
}

// readStatus reads the clock, the friends, SeenKey and every friend's beat,
// down marker and card counts in two round trips.
func readStatus(ctx context.Context, c *redis.Client) (friendStatus, error) {
	st := friendStatus{down: map[string]bool{}, stale: map[string]bool{}, live: map[string]bool{}, ready: map[string]int64{},
		working: map[string]int64{}}
	p1 := c.Pipeline()
	clock := p1.Time(ctx)
	friends := p1.SMembers(ctx, "friends")
	seen := p1.HGetAll(ctx, SeenKey)
	if _, err := p1.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return st, fmt.Errorf("rebalance: read: %w", err)
	}
	now := clock.Val()
	st.names = friends.Val()
	sort.Strings(st.names)
	st.seen = seen.Val()
	type cmds struct {
		at             *redis.StringCmd
		down           *redis.IntCmd
		ready, working *redis.IntCmd
	}
	cs := make([]cmds, len(st.names))
	p2 := c.Pipeline()
	for i, f := range st.names {
		cs[i] = cmds{
			at:      p2.HGet(ctx, "friend:"+f, "at"),
			down:    p2.Exists(ctx, "friend:"+f+":down"),
			ready:   p2.ZCard(ctx, "friend:"+f+":cards:ready"),
			working: p2.ZCard(ctx, "friend:"+f+":cards:working"),
		}
	}
	if _, err := p2.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return st, fmt.Errorf("rebalance: read friends: %w", err)
	}
	for i, f := range st.names {
		at, marked := cs[i].at.Val(), cs[i].down.Val() > 0
		// A beat that is there and older than the window is down; a friend
		// with no row at all has only its marker to go by (it was never on
		// the row beat, so it has no row to go stale), and is no target.
		st.stale[f] = at != "" && !beatLive(at, now)
		st.down[f] = marked || st.stale[f]
		st.live[f] = !marked && beatLive(at, now)
		st.ready[f] = cs[i].ready.Val()
		st.working[f] = cs[i].working.Val()
	}
	return st, nil
}

// rebalanceOne calls ns_friend_rebalance for f and writes its line.
func rebalanceOne(ctx context.Context, c *redis.Client, token, actor string, st friendStatus, f string) (Rebalanced, error) {
	from := st.seen[f]
	if from == "" {
		from = "-"
	}
	r := Rebalanced{Friend: f, From: from, To: st.status(f)}
	if r.To == StatusDown {
		stale := "0"
		if st.stale[f] {
			stale = "1"
		}
		args := []any{token, f, actor, stale}
		for _, g := range st.up() {
			if g != f {
				args = append(args, g)
			}
		}
		reply, err := c.FCall(ctx, "ns_friend_rebalance", nil, args...).Slice()
		if err != nil {
			return r, fmt.Errorf("ns_friend_rebalance %s: %w", f, err)
		}
		head := ""
		if len(reply) > 0 {
			head = fmt.Sprint(reply[0])
		}
		switch head {
		case "FENCED":
			return r, fmt.Errorf("rebalance: %w", ErrFenced)
		case "UP":
		case "REFUSED":
			if len(reply) > 1 {
				r.Reason = fmt.Sprint(reply[1])
			}
			r.Refused = pairsOf(reply, 2)
		case "REBALANCED":
			moved, _ := strconv.Atoi(fmt.Sprint(reply[1]))
			all := pairsOf(reply, 3)
			if moved > len(all) {
				moved = len(all)
			}
			r.Moves, r.Refused = all[:moved], all[moved:]
		default:
			return r, fmt.Errorf("ns_friend_rebalance %s: %v", f, reply)
		}
	}
	r.Line = fmt.Sprintf("REBALANCE friend=%s from=%s to=%s moved=%d", f, r.From, r.To, len(r.Moves)) + shownPairs(r.Moves)
	if len(r.Refused) > 0 {
		r.Line += fmt.Sprintf(" refused=%d", len(r.Refused)) + shownPairs(r.Refused)
	}
	if r.Reason != "" {
		r.Line += " REFUSED " + r.Reason
	}
	return r, nil
}

// RebalanceFriend is the verb's rebalance (`nova-sprint friend down|up`),
// run right after friend:<f>:down is written: it reads f's status as the
// duty does, moves a down f's cards, records the status in SeenKey and
// returns the one REBALANCE line. token is empty: the verb holds no lease.
func RebalanceFriend(ctx context.Context, c *redis.Client, f, actor string) (Rebalanced, error) {
	st, err := readStatus(ctx, c)
	if err != nil {
		return Rebalanced{}, err
	}
	r, err := rebalanceOne(ctx, c, "", actor, st, f)
	if err != nil {
		return r, err
	}
	if err := c.HSet(ctx, SeenKey, f, r.To).Err(); err != nil {
		return r, fmt.Errorf("rebalance: seen: %w", err)
	}
	return r, nil
}

// pairsOf reads (a, b) pairs from reply[from:].
func pairsOf(reply []any, from int) [][2]string {
	var out [][2]string
	for i := from; i+1 < len(reply); i += 2 {
		out = append(out, [2]string{fmt.Sprint(reply[i]), fmt.Sprint(reply[i+1])})
	}
	return out
}

// shownPairs is a receipt tail: each pair as a:b, space separated, at most
// RebalanceShown and then +N more.
func shownPairs(pairs [][2]string) string {
	var b strings.Builder
	for i, p := range pairs {
		if i == RebalanceShown {
			fmt.Fprintf(&b, " +%d more", len(pairs)-RebalanceShown)
			break
		}
		b.WriteString(" " + p[0] + ":" + p[1])
	}
	return b.String()
}

// FriendLive is how recent a friend's beat must be for the friend to count as
// up (the presence TTL, internal/presence DefaultTTL).
const FriendLive = 90 * time.Second

// NoConsumer is the why a card returned to its stream's ready set carries on
// its record and in the receipt (deal_friend.lua writes it).
const NoConsumer = "no-consumer"

// beatLive is true when the friend row's at is within FriendLive of now. at
// is RFC3339 (the beat's stamp) or epoch seconds or milliseconds.
func beatLive(at string, now time.Time) bool {
	at = strings.TrimSpace(at)
	if at == "" {
		return false
	}
	var t time.Time
	if n, err := strconv.ParseInt(at, 10, 64); err == nil {
		if n > 1e12 {
			t = time.UnixMilli(n)
		} else {
			t = time.Unix(n, 0)
		}
	} else if p, err := time.Parse(time.RFC3339, at); err == nil {
		t = p
	} else {
		return false
	}
	age := now.Sub(t)
	return age < FriendLive && age > -FriendLive
}
