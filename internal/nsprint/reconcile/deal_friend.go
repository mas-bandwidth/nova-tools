package reconcile

// The friend deal duty (nova-tools #3873, half two of #3872): ready -> dealt
// is automatic. Rowan dealt ready cards to friends by hand three times on the
// morning of 2026-09-25; Glenn (09:50 AM ET): "the distribution of unblocked
// cards to friends (or swarms) must be automatic."
//
// Every pass, under the reconciler lease, the duty reads every friend's open
// slots (friend:<f>:slots, else friend:<f>:desired slots, minus ZCARD
// friend:<f>:cards:working) for each friend whose row friend:<f> carries a
// beat `at` within FriendLive and no friend:<f>:down, and every
// ws:<stream>:ready in ws:order rank order (unranked streams after, by name),
// oldest first within a stream. It honours WHO on the card (the task's `who`
// field: any | only a,b | except a,b, the #3409 form), the task's kind
// (cfg:deal:kind <kind> = swarm leaves the kind to the swarm; a read card goes
// only to a member of `readers`, when that set is non-empty, and never to its
// `author`), and fills every open slot in ONE pass: one ns_deal_friend call
// per friend moves its k cards ready -> working and into
// friend:<f>:cards:working with owner set, batch by default. A card already
// owned is its owner's and is not dealt here. The benches are dealt by the
// refill's deal pass (#3713), not by this duty.
//
// Ready must be READY: a card whose WHO admits no live consumer (no live
// friend, no UP bench, not the swarm) goes back to waiting with why=no-consumer
// in one ns_deal_return call. Receipts: one `DEAL <friend> took=<k> open=<n>
// from=<streams>` line per friend dealt, one `DEAL waiting returned=<n>
// why=no-consumer ids=<ids>` line when cards went back.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchrole"
	"github.com/redis/go-redis/v9"
)

// FriendLive is how recent a friend's beat must be for the friend to be dealt
// (the presence TTL, internal/presence DefaultTTL).
const FriendLive = 90 * time.Second

// NoConsumer is the why a card returned to waiting carries on its record.
const NoConsumer = "no-consumer"

// FriendDeal is the reconciler's friend deal duty. Its Run is a Duty.
type FriendDeal struct {
	Client *redis.Client
	// Actor is written as `by` on every ws:log entry; DefaultActor when empty.
	Actor string
	// Out receives the DEAL receipt lines; nil discards them.
	Out io.Writer
}

// FriendDealResult is what one pass moved.
type FriendDealResult struct {
	Took     map[string][]string // friend -> ids dealt, in deal order
	Returned []string            // ids moved back to waiting, why=no-consumer
	Lines    []string            // the receipt lines, in order
}

// Dealt is the number of cards dealt to friends.
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

// dealCard is one ready card as the plan sees it.
type dealCard struct {
	id, stream               string
	who, kind, owner, author string
}

// dealFriend is one live friend's open slots.
type dealFriend struct {
	name string
	open int
}

// Pass reads, plans and moves: three pipelined reads, then one call per
// friend dealt and at most one return call.
func (d *FriendDeal) Pass(ctx context.Context, token string) (FriendDealResult, error) {
	res := FriendDealResult{Took: map[string][]string{}}
	c := d.Client

	// Round 1: the clock, the streams in order, the consumers, the policy.
	p1 := c.Pipeline()
	clock := p1.Time(ctx)
	order := p1.ZRange(ctx, "ws:order", 0, -1)
	names := p1.SMembers(ctx, "ws:names")
	friends := p1.SMembers(ctx, "friends")
	readers := p1.SMembers(ctx, "readers")
	benches := p1.SMembers(ctx, "benches")
	kinds := p1.HGetAll(ctx, "cfg:deal:kind")
	if _, err := p1.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return res, fmt.Errorf("deal: read: %w", err)
	}
	now := clock.Val()
	streams := streamOrder(order.Val(), names.Val())
	fs := friends.Val()
	sort.Strings(fs)
	bs := benches.Val()
	sort.Strings(bs)

	// Round 2: every ready set, every friend's beat and slots, every bench's state.
	p2 := c.Pipeline()
	ready := make([]*redis.StringSliceCmd, len(streams))
	for i, s := range streams {
		ready[i] = p2.ZRange(ctx, "ws:"+s+":ready", 0, -1)
	}
	type friendCmds struct {
		at, desired *redis.StringCmd
		slots       *redis.StringCmd
		down        *redis.IntCmd
		working     *redis.IntCmd
	}
	fc := make([]friendCmds, len(fs))
	for i, f := range fs {
		fc[i] = friendCmds{
			at:      p2.HGet(ctx, "friend:"+f, "at"),
			slots:   p2.Get(ctx, "friend:"+f+":slots"),
			desired: p2.HGet(ctx, "friend:"+f+":desired", "slots"),
			down:    p2.Exists(ctx, "friend:"+f+":down"),
			working: p2.ZCard(ctx, "friend:"+f+":cards:working"),
		}
	}
	bstate := make([]*redis.StringCmd, len(bs))
	brole := make([]*redis.StringCmd, len(bs))
	for i, b := range bs {
		bstate[i] = p2.HGet(ctx, "bench:"+b+":state", "state")
		brole[i] = p2.HGet(ctx, benchrole.Key(b), benchrole.Field)
	}
	if _, err := p2.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return res, fmt.Errorf("deal: read sets: %w", err)
	}
	live := map[string]bool{"swarm": true}
	var open []*dealFriend
	for i, f := range fs {
		if fc[i].down.Val() > 0 || !beatLive(fc[i].at.Val(), now) {
			continue
		}
		live[f] = true
		slots, err := strconv.Atoi(fc[i].slots.Val())
		if err != nil {
			if slots, err = strconv.Atoi(fc[i].desired.Val()); err != nil {
				continue
			}
		}
		if n := slots - int(fc[i].working.Val()); n > 0 {
			open = append(open, &dealFriend{name: f, open: n})
		}
	}
	for i, b := range bs {
		// #3634: a friends bench is no swarm consumer.
		if bstate[i].Val() == "UP" && brole[i].Val() != benchrole.Friends {
			live[b] = true
		}
	}

	// Round 3: the fields the plan reads on every ready card.
	var cards []dealCard
	for i, s := range streams {
		for _, id := range ready[i].Val() {
			cards = append(cards, dealCard{id: id, stream: s})
		}
	}
	if len(cards) == 0 {
		return res, nil
	}
	p3 := c.Pipeline()
	fields := make([]*redis.SliceCmd, len(cards))
	for i, k := range cards {
		fields[i] = p3.HMGet(ctx, "task:"+k.id, "who", "kind", "owner", "author")
	}
	if _, err := p3.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return res, fmt.Errorf("deal: read cards: %w", err)
	}
	for i := range cards {
		v := fields[i].Val()
		cards[i].who, cards[i].kind, cards[i].owner, cards[i].author = field(v, 0), field(v, 1), field(v, 2), field(v, 3)
	}

	// The plan: cards in rank order, oldest first; each to the admitted live
	// friend with the most open slots (name breaks ties).
	readerSet := map[string]bool{}
	for _, r := range readers.Val() {
		readerSet[r] = true
	}
	plan := map[string][]string{}
	from := map[string][]string{}
	var back []string
	for _, k := range cards {
		if k.owner != "" || kinds.Val()[k.kind] == "swarm" {
			continue
		}
		admit := func(f string) bool {
			if !WhoAdmits(k.who, f) {
				return false
			}
			if k.kind == "read" {
				return f != k.author && (len(readerSet) == 0 || readerSet[f])
			}
			return true
		}
		var best *dealFriend
		for _, f := range open {
			if f.open > 0 && admit(f.name) && (best == nil || f.open > best.open) {
				best = f
			}
		}
		if best != nil {
			best.open--
			plan[best.name] = append(plan[best.name], k.id)
			if n := from[best.name]; len(n) == 0 || n[len(n)-1] != k.stream {
				from[best.name] = append(n, k.stream)
			}
			continue
		}
		anyone := false
		for f := range live {
			if admit(f) {
				anyone = true
				break
			}
		}
		if !anyone {
			back = append(back, k.id)
		}
	}

	// The moves: one call per friend, at most one return call.
	var errs []string
	for _, f := range sortedKeys(plan) {
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
		refused := map[string]string{}
		for i := 4; i+1 < len(reply); i += 2 {
			refused[fmt.Sprint(reply[i])] = fmt.Sprint(reply[i+1])
		}
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
	if len(back) > 0 {
		args := []any{token, d.actor(), NoConsumer}
		for _, id := range back {
			args = append(args, id)
		}
		reply, err := c.FCall(ctx, "ns_deal_return", nil, args...).Slice()
		switch {
		case err != nil:
			errs = append(errs, fmt.Sprintf("ns_deal_return: %v", err))
		case len(reply) > 0 && fmt.Sprint(reply[0]) == "FENCED":
			return res, fmt.Errorf("deal: %w", ErrFenced)
		case len(reply) < 3 || fmt.Sprint(reply[0]) != "RETURNED":
			errs = append(errs, fmt.Sprintf("ns_deal_return: %v", reply))
		default:
			refused := map[string]bool{}
			for i := 3; i+1 < len(reply); i += 2 {
				refused[fmt.Sprint(reply[i])] = true
			}
			for _, id := range back {
				if !refused[id] {
					res.Returned = append(res.Returned, id)
				}
			}
			res.Lines = append(res.Lines, fmt.Sprintf("DEAL waiting returned=%d why=%s ids=%s",
				len(res.Returned), NoConsumer, strings.Join(res.Returned, ",")))
		}
	}
	return res, joinErrs(errs)
}

func (d *FriendDeal) actor() string {
	if d.Actor != "" {
		return d.Actor
	}
	return DefaultActor
}

// WhoAdmits reads a card's WHO (#3409 form: any | only a,b | except a,b;
// empty is any) and says whether consumer may take it. Names compare case
// insensitively; an unreadable WHO admits no one.
func WhoAdmits(who, consumer string) bool {
	w := strings.ToLower(strings.TrimSpace(who))
	c := strings.ToLower(strings.TrimSpace(consumer))
	if w == "" || w == "any" {
		return true
	}
	mode, list, _ := strings.Cut(w, " ")
	in := false
	for _, n := range strings.Split(list, ",") {
		if strings.TrimSpace(n) == c {
			in = true
		}
	}
	switch mode {
	case "only":
		return in
	case "except":
		return !in
	}
	return false
}

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

// streamOrder is ws:order's streams by rank, then the unranked names in
// ws:names by name (the order ns_ws_counts prints).
func streamOrder(ranked, names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, s := range ranked {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	var rest []string
	for _, s := range names {
		if !seen[s] {
			seen[s] = true
			rest = append(rest, s)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func field(v []any, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	s, _ := v[i].(string)
	return s
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
