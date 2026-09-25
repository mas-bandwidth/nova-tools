package reconcile

// The deal duty (nova-tools #3873, half two of #3872; #4059): ready ->
// working is automatic, the same tick. Rowan dealt ready cards to friends by
// hand three times on the morning of 2026-09-25; Glenn (09:50 AM ET): "the
// distribution of unblocked cards to friends (or swarms) must be automatic."
// Glenn 1:40 PM ET (#4059): once in ready, a card is distributed at once to a
// friend queue or the swarm and moves to working; ready is never a resting
// state, and no duty ever moves a card ready -> waiting.
//
// Every pass, under the reconciler lease and after the waiting-resolve duty
// (both register in cmd/nova-sprint/waiting_resolve.go, in that order), the
// duty reads the consumers (consumer.go: every live friend's open seats, the
// readers, cfg:deal:kind) and every ws:<stream>:ready in ws:order rank order
// (unranked streams after, by name), oldest first within a stream, and asks
// each card the one question the resolve asked: who takes it. A friend's
// cards go in ONE ns_deal_friend call per friend (ready -> working, owner the
// friend, into friend:<f>:cards:working, up to its open seats); the cards
// whose route names the swarm and that no friend seat took go in one
// ns_deal_swarm call (ready -> working, owner swarm). A card already owned is
// its owner's queue's and is not dealt here, and a card-model card (no
// task:<id> record) is the swarm dealer's (internal/nsprint/deal). A card
// with no consumer is never moved back: the resolve does not put one in
// ready, and one that predates the rule is named on the idle receipt; a card
// whose admitted friends are full waits in ready for the next seat.
// Receipts: one `DEAL <friend> took=<k> open=<n> from=<streams>` line per
// friend dealt, `DEAL swarm took=<k> from=<streams>` when the swarm took any,
// and `DEAL ready idle=<n> ids=<ids>` when ready holds cards no one can take.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/redis/go-redis/v9"
)

// FriendDeal is the reconciler's deal duty. Its Run is a Duty.
type FriendDeal struct {
	Client *redis.Client
	// Actor is written as `by` on every ws:log entry; DefaultActor when empty.
	Actor string
	// Out receives the DEAL receipt lines; nil discards them.
	Out io.Writer

	lastIdle string
}

// FriendDealResult is what one pass moved.
type FriendDealResult struct {
	Took  map[string][]string // friend (or Swarm) -> ids dealt, in deal order
	Idle  []string            // ready ids with no consumer at all (left where they are)
	Lines []string            // the receipt lines, in order
}

// Dealt is the number of cards dealt.
func (r FriendDealResult) Dealt() int {
	n := 0
	for _, ids := range r.Took {
		n += len(ids)
	}
	return n
}

// Run is one deal pass with the lease's token.
func (d *FriendDeal) Run(ctx context.Context, l *Lease) (Counts, error) {
	if d.Client == nil || l == nil {
		return Counts{}, fmt.Errorf("deal: client and lease are required")
	}
	res, err := d.Pass(ctx, l.Token())
	if d.Out != nil {
		for _, line := range res.Lines {
			fmt.Fprintln(d.Out, line)
		}
	}
	return Counts{Dealt: res.Dealt()}, err
}

// Pass reads, plans and moves: the consumers, the ready sets, the cards'
// fields, then one call per friend dealt and at most one swarm call.
func (d *FriendDeal) Pass(ctx context.Context, token string) (FriendDealResult, error) {
	res := FriendDealResult{Took: map[string][]string{}}
	c := d.Client

	cs, err := readConsumers(ctx, c)
	if err != nil {
		return res, fmt.Errorf("deal: %w", err)
	}
	p1 := c.Pipeline()
	order := p1.ZRange(ctx, "ws:order", 0, -1)
	names := p1.SMembers(ctx, "ws:names")
	if _, err := p1.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return res, fmt.Errorf("deal: read: %w", err)
	}
	streams := streamOrder(order.Val(), names.Val())
	p2 := c.Pipeline()
	ready := make([]*redis.StringSliceCmd, len(streams))
	for i, s := range streams {
		ready[i] = p2.ZRange(ctx, "ws:"+s+":ready", 0, -1)
	}
	if len(streams) > 0 {
		if _, err := p2.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return res, fmt.Errorf("deal: read sets: %w", err)
		}
	}
	var cards []planCard
	for i, s := range streams {
		for _, id := range ready[i].Val() {
			cards = append(cards, planCard{id: id, stream: s})
		}
	}
	if len(cards) == 0 {
		d.idle(&res, nil)
		return res, nil
	}
	p3 := c.Pipeline()
	fields := make([]*redis.SliceCmd, len(cards))
	for i, k := range cards {
		fields[i] = p3.HMGet(ctx, "task:"+k.id, planFields...)
	}
	if _, err := p3.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return res, fmt.Errorf("deal: read cards: %w", err)
	}

	// The plan: cards in rank order, oldest first, each placed once.
	plan := map[string][]string{}
	from := map[string][]string{}
	var idle []string
	for i := range cards {
		k := &cards[i]
		if !k.setFields(fields[i].Val()) {
			continue
		}
		to, why := cs.place(*k)
		switch {
		case to == "" && why == NoConsumer:
			idle = append(idle, k.id)
			continue
		case to == "" || to == k.owner:
			continue // no seat this tick, or its owner's queue holds it
		}
		plan[to] = append(plan[to], k.id)
		if n := from[to]; len(n) == 0 || n[len(n)-1] != k.stream {
			from[to] = append(n, k.stream)
		}
	}

	// The moves: one call per friend, then the swarm's.
	var errs []string
	for _, f := range sortedKeys(plan) {
		if f == Swarm {
			continue
		}
		args := []any{token, f, d.actor(), "deal"}
		for _, id := range plan[f] {
			args = append(args, id)
		}
		reply, err := c.FCall(ctx, "ns_deal_friend", nil, args...).Slice()
		if err != nil {
			errs = append(errs, fmt.Sprintf("ns_deal_friend %s: %v", f, err))
			continue
		}
		if len(reply) > 0 && fmt.Sprint(reply[0]) == "FENCED" {
			return res, fmt.Errorf("deal: %w", ErrFenced)
		}
		if len(reply) < 4 || fmt.Sprint(reply[0]) != "DEALT" {
			errs = append(errs, fmt.Sprintf("ns_deal_friend %s: %v", f, reply))
			continue
		}
		refused := refusedIDs(reply, 4)
		for _, id := range plan[f] {
			if _, no := refused[id]; !no {
				res.Took[f] = append(res.Took[f], id)
			}
		}
		line := fmt.Sprintf("DEAL %s took=%v open=%v from=%s", f, reply[1], reply[2], strings.Join(from[f], ","))
		if len(refused) > 0 {
			line += fmt.Sprintf(" refused=%d", len(refused))
		}
		res.Lines = append(res.Lines, line)
	}
	if ids := plan[Swarm]; len(ids) > 0 {
		args := []any{token, d.actor(), "deal swarm"}
		for _, id := range ids {
			args = append(args, id)
		}
		reply, err := c.FCall(ctx, "ns_deal_swarm", nil, args...).Slice()
		switch {
		case err != nil:
			errs = append(errs, fmt.Sprintf("ns_deal_swarm: %v", err))
		case len(reply) > 0 && fmt.Sprint(reply[0]) == "FENCED":
			return res, fmt.Errorf("deal: %w", ErrFenced)
		case len(reply) < 3 || fmt.Sprint(reply[0]) != "DEALT":
			errs = append(errs, fmt.Sprintf("ns_deal_swarm: %v", reply))
		default:
			refused := refusedIDs(reply, 3)
			for _, id := range ids {
				if _, no := refused[id]; !no {
					res.Took[Swarm] = append(res.Took[Swarm], id)
				}
			}
			line := fmt.Sprintf("DEAL %s took=%v from=%s", Swarm, reply[1], strings.Join(from[Swarm], ","))
			if len(refused) > 0 {
				line += fmt.Sprintf(" refused=%d", len(refused))
			}
			res.Lines = append(res.Lines, line)
		}
	}
	d.idle(&res, idle)
	return res, joinErrs(errs)
}

// idle records the ready cards no consumer can take and prints their line
// when the set changed since the last pass (an unchanged idle set prints
// nothing, so a quiet tick stays quiet).
func (d *FriendDeal) idle(res *FriendDealResult, ids []string) {
	res.Idle = ids
	key := strings.Join(ids, ",")
	if key == d.lastIdle {
		return
	}
	d.lastIdle = key
	if len(ids) > 0 {
		res.Lines = append(res.Lines, fmt.Sprintf("DEAL ready idle=%d ids=%s", len(ids), key))
	}
}

// refusedIDs reads the (id, why) pairs of a DEALT reply from index first.
func refusedIDs(reply []any, first int) map[string]string {
	out := map[string]string{}
	for i := first; i+1 < len(reply); i += 2 {
		out[fmt.Sprint(reply[i])] = fmt.Sprint(reply[i+1])
	}
	return out
}

func (d *FriendDeal) actor() string {
	if d.Actor != "" {
		return d.Actor
	}
	return DefaultActor
}
