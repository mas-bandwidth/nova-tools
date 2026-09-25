package reconcile

// The consumer of a card (nova-tools #4059): who takes a card that is ready.
//
// THE HURT (2026-09-25 1:35 PM ET). One card, build-3041, moved waiting ->
// ready by the waiting-resolve duty ("depends-on met") and ready -> waiting by
// the deal duty ("no-consumer") every three to five seconds for an hour: two
// duties with opposite rules on one card, each undoing the other, a ws:log
// entry every tick.
//
// THE RULINGS (Glenn, 1:40 PM ET). (1) waiting -> ready is ONE WAY: no duty
// ever moves a card ready -> waiting. (2) Once in ready, a card is
// distributed at once to a friend queue or the swarm (from WHO and route on
// the card) and moves to working; ready is never a resting state.
//
// So both duties ask ONE question, here, of one read of the consumers: does
// this card have a consumer this tick, and which? The resolve moves a card
// whose dependencies are met to ready only when the answer is yes, and the
// deal, the next duty in the same pass, moves it to working with the same
// answer. A card with no consumer stays in waiting with why=no-consumer,
// written once (ns_ws_note).
//
// The answer, for a card in plan order (ws:order rank, then oldest first):
//
//   - owner set: the owner's queue holds it (the deal leaves it to its owner);
//   - WHO swarm, or a kind cfg:deal:kind sends to the swarm: the swarm only;
//   - else the admitted live friend (WHO any | friend | only a,b | except a,b;
//     a read card never to its author, and only to a member of `readers` when
//     that set is non-empty) with the most open seats (slots minus
//     friend:<f>:cards:working; the name breaks ties) takes a seat;
//   - else, when the card's route names the swarm (any route but "" or "-"),
//     the swarm (friend queue up to width, else swarm by route);
//   - else no consumer: no-seat when an admitted live friend exists but every
//     seat is taken this tick (the card waits for a seat, and nothing is
//     written), no-consumer when none does.

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

// FriendLive is how recent a friend's beat must be for the friend to be dealt
// (the presence TTL, internal/presence DefaultTTL).
const FriendLive = 90 * time.Second

// NoConsumer is the why a waiting card whose dependencies are met but that
// has no consumer carries on its record.
const NoConsumer = "no-consumer"

// NoSeat is the answer for a card whose admitted friends are live but full
// this tick; it stays waiting and nothing is written.
const NoSeat = "no-seat"

// Swarm is the consumer name of the swarm, and the owner a card dealt to the
// swarm carries.
const Swarm = "swarm"

// consumers is one tick's read of who can take a card: the live friends and
// their open seats, the readers, and the kinds dealt to the swarm. place
// takes the seats it hands out, so a plan never overfills a friend.
type consumers struct {
	friends []string // live friends, by name
	open    map[string]int
	readers map[string]bool
	kinds   map[string]string // cfg:deal:kind
}

// planCard is one card as the plan sees it.
type planCard struct {
	id, stream               string
	score                    float64
	who, kind, owner, author string
	route, why               string
	waiting                  bool // in waiting (a resolve candidate), not ready
}

// planFields are the task fields place reads, in planCard order after the id.
var planFields = []string{"state", "who", "kind", "owner", "author", "route", "why"}

// setFields fills k from an HMGET of planFields; ok is false when the task
// has no record (a card-model card, s:<S>:card:<label>, which the swarm's
// dealer moves, never these duties).
func (k *planCard) setFields(v []any) bool {
	if field(v, 0) == "" {
		return false
	}
	k.who, k.kind, k.owner, k.author = field(v, 1), field(v, 2), field(v, 3), field(v, 4)
	k.route, k.why = field(v, 5), field(v, 6)
	return true
}

// readConsumers reads the consumers in two pipelined rounds.
func readConsumers(ctx context.Context, c *redis.Client) (*consumers, error) {
	p1 := c.Pipeline()
	clock := p1.Time(ctx)
	friends := p1.SMembers(ctx, "friends")
	readers := p1.SMembers(ctx, "readers")
	kinds := p1.HGetAll(ctx, "cfg:deal:kind")
	if _, err := p1.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("consumers: %w", err)
	}
	now := clock.Val()
	fs := friends.Val()
	sort.Strings(fs)
	cs := &consumers{open: map[string]int{}, readers: map[string]bool{}, kinds: kinds.Val()}
	for _, r := range readers.Val() {
		cs.readers[r] = true
	}
	if len(fs) == 0 {
		return cs, nil
	}
	type friendCmds struct {
		at, slots, desired *redis.StringCmd
		down, working      *redis.IntCmd
	}
	p2 := c.Pipeline()
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
	if _, err := p2.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("consumers: friends: %w", err)
	}
	for i, f := range fs {
		if f == Swarm || fc[i].down.Val() > 0 || !beatLive(fc[i].at.Val(), now) {
			continue
		}
		cs.friends = append(cs.friends, f)
		slots, err := strconv.Atoi(fc[i].slots.Val())
		if err != nil {
			if slots, err = strconv.Atoi(fc[i].desired.Val()); err != nil {
				slots = 0
			}
		}
		cs.open[f] = max(slots-int(fc[i].working.Val()), 0)
	}
	return cs, nil
}

// swarmRoute is whether a card's route names the swarm.
func swarmRoute(route string) bool {
	r := strings.TrimSpace(route)
	return r != "" && r != "-"
}

// place answers who takes k and takes the seat it hands out. It returns the
// consumer (a friend, Swarm, or k's owner) and "", or "" and NoSeat or
// NoConsumer.
func (cs *consumers) place(k planCard) (string, string) {
	if k.owner != "" {
		return k.owner, ""
	}
	swarmOnly := cs.kinds[k.kind] == Swarm || strings.EqualFold(strings.TrimSpace(k.who), Swarm)
	admitted := false
	if !swarmOnly {
		best := ""
		for _, f := range cs.friends {
			if !WhoAdmits(k.who, f) {
				continue
			}
			if k.kind == "read" && (f == k.author || (len(cs.readers) > 0 && !cs.readers[f])) {
				continue
			}
			admitted = true
			if cs.open[f] > 0 && (best == "" || cs.open[f] > cs.open[best]) {
				best = f
			}
		}
		if best != "" {
			cs.open[best]--
			return best, ""
		}
	}
	if swarmOnly || swarmRoute(k.route) {
		return Swarm, ""
	}
	if admitted {
		return "", NoSeat
	}
	return "", NoConsumer
}

// WhoAdmits reads a card's WHO (#3409 form: any | only a,b | except a,b;
// empty is any) and says whether consumer may take it. WHO friend (or
// friends) is any friend, and WHO swarm is the swarm alone; any, empty, only
// and except name friends, never the swarm, which takes a card by its route.
// Names compare case insensitively; an unreadable WHO admits no one.
func WhoAdmits(who, consumer string) bool {
	w := strings.ToLower(strings.TrimSpace(who))
	c := strings.ToLower(strings.TrimSpace(consumer))
	if w == Swarm || c == Swarm {
		return w == c
	}
	if w == "" || w == "any" || w == "friend" || w == "friends" {
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

// planOrder sorts one stream's cards the way ZRANGE reads a set: by score
// (the card's created_at), then by id.
func planOrder(cards []planCard) {
	sort.SliceStable(cards, func(i, j int) bool {
		if cards[i].score != cards[j].score {
			return cards[i].score < cards[j].score
		}
		return cards[i].id < cards[j].id
	})
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
