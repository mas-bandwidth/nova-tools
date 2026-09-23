package deal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// fnStore is the deal pass's Reserver and Row over the real nova_sprint
// functions of #3063 (internal/nsprint/fn/lua/deal.lua): one ns_card_deal
// call per bench, one ns_card_undeal call per returned batch, one
// ns_bench_ssh call per bench row. It is the adapter the reconciler wires in
// (#2726, #2743); it lives here until that wiring lands so the controls run
// on the functions and not on fakeStore.
type fnStore struct {
	c     *redis.Client
	actor string

	mu    sync.Mutex
	calls map[string]int // bench -> ns_card_deal calls
}

func newFnStore(c *redis.Client) *fnStore {
	return &fnStore{c: c, actor: "reconciler", calls: map[string]int{}}
}

func tokenSHA(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

// Reserve reads each card's attempt in one pipelined round, mints
// <attempt+1>.<128 random bits> per card (spec 2.1 rule 8: the random half is
// the Go RNG's) and moves the batch queued -> dealt in ONE function call. A
// card whose attempt moved between the read and the call is skipped by the
// function, as a card no longer queued is.
func (s *fnStore) Reserve(ctx context.Context, fence, bench string, cards []Card) ([]Reservation, error) {
	s.mu.Lock()
	s.calls[bench]++
	s.mu.Unlock()
	pipe := s.c.Pipeline()
	attempts := make([]*redis.StringCmd, len(cards))
	for i, c := range cards {
		attempts[i] = pipe.HGet(ctx, "s:"+c.Sprint+":card:"+c.Label, "attempt")
	}
	if len(cards) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
	}
	args := []any{bench, fence, s.actor, ""}
	byKey := map[string]Card{}
	for i, c := range cards {
		a, _ := strconv.Atoi(attempts[i].Val())
		token := fmt.Sprintf("%d.%s", a+1, randHex())
		args = append(args, c.Sprint, c.Label, strconv.Itoa(a+1), token, tokenSHA(token))
		byKey[key(c)] = c
	}
	reply, err := s.c.FCall(ctx, "ns_card_deal", nil, args...).StringSlice()
	if err != nil {
		return nil, err
	}
	if len(reply) == 0 {
		return nil, fmt.Errorf("ns_card_deal: empty reply")
	}
	switch reply[0] {
	case "FENCED":
		return nil, ErrFenced
	case "DEALT", "NONE":
	default:
		return nil, fmt.Errorf("ns_card_deal: %v", reply)
	}
	var out []Reservation
	if reply[0] == "NONE" {
		return out, nil
	}
	for i := 1; i+3 < len(reply); i += 4 {
		a, _ := strconv.Atoi(reply[i+2])
		c := byKey[reply[i]+"/"+reply[i+1]]
		out = append(out, Reservation{Card: c, Bench: bench, Attempt: a, Token: reply[i+3]})
	}
	return out, nil
}

// Unreserve returns the batch dealt -> queued in one call.
func (s *fnStore) Unreserve(ctx context.Context, fence, bench string, res []Reservation, reason string) error {
	args := []any{bench, fence, reason, s.actor, ""}
	for _, r := range res {
		args = append(args, r.Card.Sprint, r.Card.Label, strconv.Itoa(r.Attempt))
	}
	reply, err := s.c.FCall(ctx, "ns_card_undeal", nil, args...).StringSlice()
	if err != nil {
		return err
	}
	if len(reply) > 0 && reply[0] == "FENCED" {
		return ErrFenced
	}
	if len(reply) == 0 || reply[0] != "UNDEALT" {
		return fmt.Errorf("ns_card_undeal: %v", reply)
	}
	return nil
}

// SSH writes the bench row's ssh cell, bench:<b>:ssh.
func (s *fnStore) SSH(ctx context.Context, fence, bench, state, why string) error {
	reply, err := s.c.FCall(ctx, "ns_bench_ssh", nil, bench, fence, state, why).StringSlice()
	if err != nil {
		return err
	}
	if len(reply) > 0 && reply[0] == "FENCED" {
		return ErrFenced
	}
	if len(reply) == 0 || reply[0] != "OK" {
		return fmt.Errorf("ns_bench_ssh: %v", reply)
	}
	return nil
}

// Gate writes one sprint's DEPENDS-ON moves in one ns_card_gate call (#3066).
func (s *fnStore) Gate(ctx context.Context, fence, sprint string, moves []GateMove) error {
	args := []any{fence, sprint, s.actor, ""}
	for _, m := range moves {
		args = append(args, m.Label, m.Verb, m.Why)
	}
	reply, err := s.c.FCall(ctx, "ns_card_gate", nil, args...).StringSlice()
	if err != nil {
		return err
	}
	if len(reply) > 0 && reply[0] == "FENCED" {
		return ErrFenced
	}
	if len(reply) == 0 || (reply[0] != "GATED" && reply[0] != "NONE") {
		return fmt.Errorf("ns_card_gate: %v", reply)
	}
	return nil
}

// dealRedis is a throwaway redis-server with the nova_sprint library loaded.
func dealRedis(t *testing.T) *redis.Client {
	t.Helper()
	c := throwawayRedis(t)
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

// seedLease writes lease:reconciler in the shape ns_reconciler_acquire
// (#3057) writes it; the deal functions read only its token field.
func seedLease(t *testing.T, c *redis.Client, token string) {
	t.Helper()
	ctx := context.Background()
	if err := c.HSet(ctx, "lease:reconciler", "instance", "ctl-reconciler", "token", token, "host", "ctl-host", "pid", "1", "at", "1", "acquired_at", "1").Err(); err != nil {
		t.Fatal(err)
	}
}

// seedFleet registers UP benches with the given slots, one open sprint and n
// queued pool cards card-00..card-<n-1> at priority i.
func seedFleet(t *testing.T, c *redis.Client, sprint string, n int, benches map[string]int) {
	t.Helper()
	ctx := context.Background()
	if err := c.FlushAll(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	pipe := c.Pipeline()
	for b, slots := range benches {
		pipe.SAdd(ctx, "benches", b)
		pipe.HSet(ctx, "bench:"+b+":desired", "slots", strconv.Itoa(slots))
		pipe.HSet(ctx, "bench:"+b+":beat", "host", b, "at", "1")
	}
	pipe.SAdd(ctx, "sprints", sprint)
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	pipe.HSet(ctx, "s:"+sprint, "status", "open")
	for i := 0; i < n; i++ {
		label := fmt.Sprintf("card-%02d", i)
		pipe.HSet(ctx, "s:"+sprint+":card:"+label, "state", "queued", "priority", strconv.Itoa(i),
			"attempt", "0", "retries", "0", "base_sha", "0123456789abcdef", "bench", "", "leg", "", "tier", "")
		pipe.ZAdd(ctx, "s:"+sprint+":pool", redis.Z{Score: float64(i), Member: label})
		pipe.SAdd(ctx, "s:"+sprint+":idx:card:queued", label)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func poolCards(sprint string, n int) []Card {
	cards := make([]Card, n)
	for i := range cards {
		cards[i] = Card{Sprint: sprint, Label: fmt.Sprintf("card-%02d", i), Priority: float64(i)}
	}
	return cards
}

// logEntries returns the sprint log entries of one kind for one card ("" for any card).
func logEntries(t *testing.T, c *redis.Client, sprint, kind, label string) []redis.XMessage {
	t.Helper()
	msgs, err := c.XRange(context.Background(), "s:"+sprint+":log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var out []redis.XMessage
	for _, m := range msgs {
		if (kind == "" || m.Values["kind"] == kind) && (label == "" || m.Values["id"] == label) {
			out = append(out, m)
		}
	}
	return out
}

func card(t *testing.T, c *redis.Client, sprint, label string) map[string]string {
	t.Helper()
	h, err := c.HGetAll(context.Background(), "s:"+sprint+":card:"+label).Result()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func zcard(t *testing.T, c *redis.Client, k string) int64 {
	t.Helper()
	n, err := c.ZCard(context.Background(), k).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func scard(t *testing.T, c *redis.Client, k string) int64 {
	t.Helper()
	n, err := c.SCard(context.Background(), k).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestDealFunctionsFencedAndAtomic is the DONE-WHEN of #3063: two dealers
// with different tokens call ns_card_deal at once and only the live token's
// call moves cards; a card dealt then undealt is queued again with reason
// ssh-refused and exactly one log entry; the bench ssh cell has one fenced
// writer.
func TestDealFunctionsFencedAndAtomic(t *testing.T) {
	const sprint = "control-00003063"
	ctx := context.Background()
	c := dealRedis(t)

	t.Run("two dealers at once: only the live token moves cards", func(t *testing.T) {
		seedFleet(t, c, sprint, 50, map[string]int{"ctl-a": 64, "ctl-b": 64})
		seedLease(t, c, "live-token")
		live, stale := newFnStore(c), newFnStore(c)
		cards := poolCards(sprint, 50)
		var wg sync.WaitGroup
		start := make(chan struct{})
		var liveRes, staleRes []Reservation
		var liveErr, staleErr error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; liveRes, liveErr = live.Reserve(ctx, "live-token", "ctl-a", cards) }()
		go func() {
			defer wg.Done()
			<-start
			staleRes, staleErr = stale.Reserve(ctx, "stale-token", "ctl-b", cards)
		}()
		close(start)
		wg.Wait()
		if liveErr != nil || len(liveRes) != 50 {
			t.Fatalf("live dealer: %d reservations, err %v; want 50", len(liveRes), liveErr)
		}
		if !errors.Is(staleErr, ErrFenced) || len(staleRes) != 0 {
			t.Fatalf("stale dealer: %d reservations, err %v; want FENCED and none", len(staleRes), staleErr)
		}
		if n := zcard(t, c, "bench:ctl-b:starting"); n != 0 {
			t.Fatalf("the stale token's bench holds %d reservations", n)
		}
		if n := zcard(t, c, "bench:ctl-a:starting"); n != 50 {
			t.Fatalf("bench:ctl-a:starting = %d, want 50", n)
		}
		if n := zcard(t, c, "s:"+sprint+":pool"); n != 0 {
			t.Fatalf("pool still holds %d dealt cards", n)
		}
		if q, d := scard(t, c, "s:"+sprint+":idx:card:queued"), scard(t, c, "s:"+sprint+":idx:card:dealt"); q != 0 || d != 50 {
			t.Fatalf("idx queued=%d dealt=%d, want 0/50", q, d)
		}
		if n := zcard(t, c, "s:"+sprint+":bench:ctl-a:queue"); n != 50 {
			t.Fatalf("s:<S>:bench:ctl-a:queue = %d, want 50", n)
		}
		deals := logEntries(t, c, sprint, "card deal", "")
		if len(deals) != 50 {
			t.Fatalf("%d card deal receipts, want exactly 50 (one per card)", len(deals))
		}
		for _, r := range liveRes {
			h := card(t, c, sprint, r.Card.Label)
			if h["state"] != "dealt" || h["bench"] != "ctl-a" || h["attempt"] != "1" || h["token"] != r.Token ||
				h["token_sha"] != tokenSHA(r.Token) || h["identity"] != sprint+"/"+r.Card.Label+"/01234567/ctl-a/1" || h["dealt_at"] == "" {
				t.Fatalf("card %s after deal: %v (reservation %+v)", r.Card.Label, h, r)
			}
			if !regexpToken.MatchString(r.Token) {
				t.Fatalf("token %q is not <attempt>.<32 hex>", r.Token)
			}
		}
		// The lease moves to another instance: the old live token is now
		// stale and its next call deals nothing, even onto a free bench.
		seedLease(t, c, "next-token")
		c.HSet(ctx, "s:"+sprint+":card:extra", "state", "queued", "attempt", "0")
		c.ZAdd(ctx, "s:"+sprint+":pool", redis.Z{Score: 99, Member: "extra"})
		if res, err := live.Reserve(ctx, "live-token", "ctl-b", []Card{{Sprint: sprint, Label: "extra"}}); !errors.Is(err, ErrFenced) || len(res) != 0 {
			t.Fatalf("superseded token: %d reservations, err %v; want FENCED", len(res), err)
		}
		if h := card(t, c, sprint, "extra"); h["state"] != "queued" {
			t.Fatalf("a fenced call moved card extra to %s", h["state"])
		}
	})

	t.Run("two live dealers at once never deal one card twice", func(t *testing.T) {
		seedFleet(t, c, sprint, 50, map[string]int{"ctl-a": 64, "ctl-b": 64})
		seedLease(t, c, "live-token")
		st := newFnStore(c)
		cards := poolCards(sprint, 50)
		var wg sync.WaitGroup
		start := make(chan struct{})
		res := make([][]Reservation, 2)
		errs := make([]error, 2)
		for i, b := range []string{"ctl-a", "ctl-b"} {
			wg.Add(1)
			go func(i int, b string) {
				defer wg.Done()
				<-start
				res[i], errs[i] = st.Reserve(ctx, "live-token", b, cards)
			}(i, b)
		}
		close(start)
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatal(errs)
		}
		seen := map[string]string{}
		for i, rs := range res {
			for _, r := range rs {
				if prev, ok := seen[r.Card.Label]; ok {
					t.Fatalf("card %s dealt to %s and %s", r.Card.Label, prev, []string{"ctl-a", "ctl-b"}[i])
				}
				seen[r.Card.Label] = r.Bench
			}
		}
		if len(seen) != 50 {
			t.Fatalf("%d cards dealt, want 50", len(seen))
		}
		if a, b := zcard(t, c, "bench:ctl-a:starting"), zcard(t, c, "bench:ctl-b:starting"); a+b != 50 {
			t.Fatalf("starting ctl-a=%d ctl-b=%d, want 50 in total", a, b)
		}
		if n := len(logEntries(t, c, sprint, "card deal", "")); n != 50 {
			t.Fatalf("%d card deal receipts, want 50", n)
		}
	})

	t.Run("free caps the call: desired minus starting plus living", func(t *testing.T) {
		seedFleet(t, c, sprint, 50, map[string]int{"ctl-a": 10})
		seedLease(t, c, "live-token")
		c.ZAdd(ctx, "bench:ctl-a:living", redis.Z{Score: 1, Member: "other/x/1"}, redis.Z{Score: 1, Member: "other/y/1"})
		res, err := newFnStore(c).Reserve(ctx, "live-token", "ctl-a", poolCards(sprint, 50))
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 8 {
			t.Fatalf("dealt %d, want 8 (10 slots, 2 living)", len(res))
		}
		// A full bench deals nothing and writes nothing.
		before := len(logEntries(t, c, sprint, "card deal", ""))
		if res, err := newFnStore(c).Reserve(ctx, "live-token", "ctl-a", poolCards(sprint, 50)); err != nil || len(res) != 0 {
			t.Fatalf("full bench: %d reservations, err %v", len(res), err)
		}
		if after := len(logEntries(t, c, sprint, "card deal", "")); after != before {
			t.Fatalf("a full bench wrote %d receipts", after-before)
		}
	})

	t.Run("the spec guards hold inside the call", func(t *testing.T) {
		seedFleet(t, c, sprint, 6, map[string]int{"ctl-a": 64, "ctl-b": 64, "ctl-down": 64, "ctl-paused": 64})
		seedLease(t, c, "live-token")
		c.Del(ctx, "bench:ctl-down:beat")
		c.HSet(ctx, "bench:ctl-paused:desired", "paused", "1")
		c.HSet(ctx, "bench:ctl-a:desired", "legs", "go,lua")
		c.HSet(ctx, "s:"+sprint+":card:card-00", "bench", "ctl-b")    // pinned elsewhere
		c.HSet(ctx, "s:"+sprint+":card:card-01", "leg", "haskell")    // not in ctl-a's profile
		c.HSet(ctx, "s:"+sprint+":card:card-02", "leg", "lua")        // in the profile
		c.HSet(ctx, "s:"+sprint+":card:card-03", "state", "launched") // no longer queued
		c.ZRem(ctx, "s:"+sprint+":pool", "card-04")                   // waiting, not pooled
		st := newFnStore(c)
		for _, b := range []string{"ctl-down", "ctl-paused"} {
			if res, err := st.Reserve(ctx, "live-token", b, poolCards(sprint, 6)); err != nil || len(res) != 0 {
				t.Fatalf("%s: %d reservations, err %v; want none", b, len(res), err)
			}
		}
		res, err := st.Reserve(ctx, "live-token", "ctl-a", poolCards(sprint, 6))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range res {
			got = append(got, r.Card.Label)
		}
		if strings.Join(got, ",") != "card-02,card-05" {
			t.Fatalf("ctl-a dealt %v, want card-02,card-05", got)
		}
		// Backpressure ON: only the priority tier flows.
		seedFleet(t, c, sprint, 2, map[string]int{"ctl-a": 64})
		seedLease(t, c, "live-token")
		c.HSet(ctx, "s:"+sprint+":backpressure", "state", "ON")
		c.HSet(ctx, "s:"+sprint+":card:card-01", "tier", TierPriority)
		res, err = st.Reserve(ctx, "live-token", "ctl-a", poolCards(sprint, 2))
		if err != nil || len(res) != 1 || res[0].Card.Label != "card-01" {
			t.Fatalf("under backpressure dealt %+v, err %v; want only the priority card-01", res, err)
		}
		// A missing backpressure hash applies the declared policy: closed is ON.
		seedFleet(t, c, sprint, 1, map[string]int{"ctl-a": 64})
		seedLease(t, c, "live-token")
		c.HSet(ctx, "s:"+sprint+":policy", "backpressure_missing", "closed")
		if res, err := st.Reserve(ctx, "live-token", "ctl-a", poolCards(sprint, 1)); err != nil || len(res) != 0 {
			t.Fatalf("missing backpressure under a closed policy dealt %d, err %v", len(res), err)
		}
	})

	t.Run("a malformed card_token or card_token_sha leaves the card queued", func(t *testing.T) {
		// ns_card_deal checks the shape ns_task_take checks: <attempt>.<32
		// hex> and a 12 hex sha, not just the <attempt>. prefix (stella's
		// hold 2 on #3075: "1." and "1.not-hex" used to pass and persist).
		seedFleet(t, c, sprint, 1, map[string]int{"ctl-a": 64})
		seedLease(t, c, "live-token")
		validSha := tokenSHA("1." + strings.Repeat("a", 32))
		cases := []struct {
			name  string
			token string
			sha   string
		}{
			{"token has no hex after the dot", "1.", validSha},
			{"token's tail is not hex", "1.not-hex-not-hex-not-hex-not-hex", validSha},
			{"token hex is short", "1." + strings.Repeat("a", 31), validSha},
			{"token hex is long", "1." + strings.Repeat("a", 33), validSha},
			{"token_sha is not hex", "1." + strings.Repeat("a", 32), "not-hex-1234"},
			{"token_sha is the wrong length", "1." + strings.Repeat("a", 32), "abcdef"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				reply, err := c.FCall(ctx, "ns_card_deal", nil,
					"ctl-a", "live-token", "reconciler", "",
					sprint, "card-00", "1", tc.token, tc.sha).StringSlice()
				if err != nil {
					t.Fatal(err)
				}
				if len(reply) != 1 || reply[0] != "DEALT" {
					t.Fatalf("ns_card_deal(%q, %q) = %v, want DEALT with no cards dealt", tc.token, tc.sha, reply)
				}
				if h := card(t, c, sprint, "card-00"); h["state"] != "queued" || h["token"] != "" {
					t.Fatalf("card-00 after malformed deal: state=%q token=%q, want queued and no token", h["state"], h["token"])
				}
			})
		}
		// The same card, same attempt, with a well-formed token deals normally.
		res, err := newFnStore(c).Reserve(ctx, "live-token", "ctl-a", poolCards(sprint, 1))
		if err != nil || len(res) != 1 {
			t.Fatalf("a well-formed deal after the malformed ones: %d reservations, err %v", len(res), err)
		}
	})

	t.Run("dealt then undealt: queued again, reason ssh-refused, exactly one log entry", func(t *testing.T) {
		seedFleet(t, c, sprint, 3, map[string]int{"ctl-a": 64, "ctl-b": 64})
		seedLease(t, c, "live-token")
		c.HSet(ctx, "s:"+sprint+":card:card-02", "bench", "ctl-a") // pinned: the pin survives the undeal
		st := newFnStore(c)
		res, err := st.Reserve(ctx, "live-token", "ctl-a", poolCards(sprint, 3))
		if err != nil || len(res) != 3 {
			t.Fatalf("deal: %d, err %v", len(res), err)
		}
		// A stale token undeals nothing.
		if err := st.Unreserve(ctx, "stale-token", "ctl-a", res, ReasonSSHRefused); !errors.Is(err, ErrFenced) {
			t.Fatalf("stale undeal: %v, want FENCED", err)
		}
		if n := zcard(t, c, "bench:ctl-a:starting"); n != 3 {
			t.Fatalf("a fenced undeal freed slots: starting %d", n)
		}
		capBefore, _ := c.XLen(ctx, "cap:log").Result()
		if err := st.Unreserve(ctx, "live-token", "ctl-a", res, ReasonSSHRefused); err != nil {
			t.Fatal(err)
		}
		for _, r := range res {
			h := card(t, c, sprint, r.Card.Label)
			wantBench := ""
			if r.Card.Label == "card-02" {
				wantBench = "ctl-a"
			}
			if h["state"] != "queued" || h["reason"] != ReasonSSHRefused || h["retries"] != "1" || h["token"] != "" || h["bench"] != wantBench || h["attempt"] != "1" {
				t.Fatalf("card %s after undeal: %v", r.Card.Label, h)
			}
			if score, err := c.ZScore(ctx, "s:"+sprint+":pool", r.Card.Label).Result(); err != nil || score != r.Card.Priority {
				t.Fatalf("card %s back in pool at %v (err %v), want its score %v", r.Card.Label, score, err, r.Card.Priority)
			}
			undeals := logEntries(t, c, sprint, "card undeal", r.Card.Label)
			if len(undeals) != 1 {
				t.Fatalf("card %s has %d undeal receipts, want exactly 1", r.Card.Label, len(undeals))
			}
			if v := undeals[0].Values; v["from"] != "dealt" || v["to"] != "queued" || v["reason"] != ReasonSSHRefused || v["attempt"] != "1" || v["token_sha"] != tokenSHA(r.Token) {
				t.Fatalf("undeal receipt %v", v)
			}
		}
		if n := zcard(t, c, "bench:ctl-a:starting"); n != 0 {
			t.Fatalf("starting %d after undeal, want 0", n)
		}
		if q, d := scard(t, c, "s:"+sprint+":idx:card:queued"), scard(t, c, "s:"+sprint+":idx:card:dealt"); q != 3 || d != 0 {
			t.Fatalf("idx queued=%d dealt=%d, want 3/0", q, d)
		}
		if members, _ := c.ZRange(ctx, "s:"+sprint+":bench:ctl-a:queue", 0, -1).Result(); strings.Join(members, ",") != "card-02" {
			t.Fatalf("bench queue %v, want only the pinned card-02", members)
		}
		if capAfter, _ := c.XLen(ctx, "cap:log").Result(); capAfter-capBefore != 1 {
			t.Fatalf("cap:log grew %d, want one slot-freed event for the batch", capAfter-capBefore)
		}
		// Repeating the undeal is a no-op: still exactly one entry each.
		if err := st.Unreserve(ctx, "live-token", "ctl-a", res, ReasonSSHRefused); err != nil {
			t.Fatal(err)
		}
		for _, r := range res {
			if n := len(logEntries(t, c, sprint, "card undeal", r.Card.Label)); n != 1 {
				t.Fatalf("card %s has %d undeal receipts after a repeat, want 1", r.Card.Label, n)
			}
		}
		// The old token is fenced: the next deal is a new attempt.
		again, err := st.Reserve(ctx, "live-token", "ctl-a", poolCards(sprint, 3))
		if err != nil || len(again) != 3 {
			t.Fatalf("redeal: %d, err %v", len(again), err)
		}
		for _, r := range again {
			if r.Attempt != 2 || !strings.HasPrefix(r.Token, "2.") {
				t.Fatalf("redeal of %s: attempt %d token %s, want attempt 2", r.Card.Label, r.Attempt, r.Token)
			}
		}
		// An undeal naming the old attempt does not touch the new one.
		if err := st.Unreserve(ctx, "live-token", "ctl-a", res, ReasonSSHRefused); err != nil {
			t.Fatal(err)
		}
		if h := card(t, c, sprint, "card-00"); h["state"] != "dealt" || h["attempt"] != "2" {
			t.Fatalf("an undeal of attempt 1 moved attempt 2: %v", h)
		}
	})

	t.Run("the bench ssh cell has one fenced writer", func(t *testing.T) {
		seedFleet(t, c, sprint, 0, map[string]int{"ctl-a": 64})
		seedLease(t, c, "live-token")
		st := newFnStore(c)
		if err := st.SSH(ctx, "live-token", "ctl-a", SSHRefused, "kex_exchange_identification"); err != nil {
			t.Fatal(err)
		}
		h, _ := c.HGetAll(ctx, RowKey("ctl-a")).Result()
		if h["state"] != SSHRefused || h["why"] != "kex_exchange_identification" || h["at"] == "" {
			t.Fatalf("bench:ctl-a:ssh = %v", h)
		}
		if err := st.SSH(ctx, "stale-token", "ctl-a", SSHOK, "stale"); !errors.Is(err, ErrFenced) {
			t.Fatalf("stale ssh write: %v, want FENCED", err)
		}
		if err := st.SSH(ctx, "live-token", "ctl-a", "sideways", ""); err == nil {
			t.Fatal("an unknown ssh state was written")
		}
		if err := st.SSH(ctx, "live-token", "ctl-nobody", SSHOK, ""); err == nil {
			t.Fatal("an unregistered bench got an ssh cell")
		}
		if got, _ := c.HGet(ctx, RowKey("ctl-a"), "state").Result(); got != SSHRefused {
			t.Fatalf("refused writes changed the cell to %q", got)
		}
		if beat, _ := c.HGetAll(ctx, "bench:ctl-a:beat").Result(); beat["ssh"] != "" {
			t.Fatalf("the deal pass wrote the bench's own beat: %v", beat)
		}
	})
}

// TestControl12FiftyCardsOneSessionOnRealFunctions is #3061's control 12 run
// on the real functions instead of fakeStore: RedisSource reads the fleet,
// ns_card_deal reserves, the fixture sshd takes the batch, ns_bench_ssh
// writes the row and ns_card_undeal returns a refused bench's cards.
// `go test -run TestControl12FiftyCardsOneSession` runs it beside the fixture version.
func TestControl12FiftyCardsOneSessionOnRealFunctions(t *testing.T) {
	const sprint = "control-0000c012"
	ctx := context.Background()
	c := dealRedis(t)

	t.Run("fifty cards launch over one session", func(t *testing.T) {
		seedFleet(t, c, sprint, 50, map[string]int{"ctl-a": 64})
		seedLease(t, c, "lease-1")
		f := newFixture(t)
		st := newFnStore(c)
		p := &Pass{Source: RedisSource{Client: c}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
		res, err := p.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Launched(); got != 50 {
			t.Fatalf("launched %d, want 50 (%+v)", got, res.Benches)
		}
		if st.calls["ctl-a"] != 1 {
			t.Fatalf("ns_card_deal calls on ctl-a = %d, want 1", st.calls["ctl-a"])
		}
		if s := f.lines("ctl-a", "sessions.log"); len(s) != 1 {
			t.Fatalf("ssh sessions to ctl-a = %d, want exactly 1", len(s))
		}
		launched := f.lines("ctl-a", "launched")
		if len(launched) != 50 {
			t.Fatalf("card launch --stdin got %d lines, want 50", len(launched))
		}
		for _, l := range launched {
			if !lineRE.MatchString(l) {
				t.Fatalf("stdin line %q is not `<S> <label> <attempt> <token>`", l)
			}
			fields := strings.Fields(l)
			if h := card(t, c, sprint, fields[1]); h["token"] != fields[3] || h["state"] != "dealt" {
				t.Fatalf("stdin token for %s does not match the card hash %v", fields[1], h)
			}
		}
		if got, _ := c.HGet(ctx, RowKey("ctl-a"), "state").Result(); got != SSHOK {
			t.Fatalf("row cell ssh: %q, want ok", got)
		}
		if n := zcard(t, c, "bench:ctl-a:starting"); n != 50 {
			t.Fatalf("bench:ctl-a:starting = %d, want 50", n)
		}
	})

	t.Run("a wedged sshd shows ssh refused and its cards go elsewhere", func(t *testing.T) {
		seedFleet(t, c, sprint, 50, map[string]int{"ctl-a": 64, "ctl-b": 50})
		seedLease(t, c, "lease-1")
		f := newFixture(t)
		f.wedge(t, "ctl-a")
		st := newFnStore(c)
		p := &Pass{Source: RedisSource{Client: c}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
		res, err := p.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := c.HGet(ctx, RowKey("ctl-a"), "state").Result(); got != SSHRefused {
			t.Fatalf("wedged bench row ssh: %q, want refused", got)
		}
		if n := zcard(t, c, "bench:ctl-a:starting"); n != 0 {
			t.Fatalf("%d reservations stayed on the wedged bench", n)
		}
		if n := zcard(t, c, "bench:ctl-b:starting"); n != 50 {
			t.Fatalf("%d cards dealt to ctl-b, want 50", n)
		}
		if got := len(f.lines("ctl-b", "launched")); got != 50 {
			t.Fatalf("ctl-b launched %d, want 50", got)
		}
		if res.Launched() != 50 || res.Rounds != 2 {
			t.Fatalf("launched %d in %d rounds, want 50 in 2", res.Launched(), res.Rounds)
		}
		for i := 0; i < 50; i++ {
			label := fmt.Sprintf("card-%02d", i)
			if n := len(logEntries(t, c, sprint, "card undeal", label)); n != 1 {
				t.Fatalf("%s has %d undeal receipts, want exactly 1 (ssh-refused on ctl-a)", label, n)
			}
			if h := card(t, c, sprint, label); h["bench"] != "ctl-b" || h["attempt"] != "2" || h["retries"] != "1" {
				t.Fatalf("%s: %v, want dealt to ctl-b under attempt 2", label, h)
			}
		}
		// The next pass reads the refusal from the cell and skips ctl-a.
		in, err := RedisSource{Client: c}.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range Plan(in, DefaultRefusedHold) {
			if b.Bench.Name == "ctl-a" {
				t.Fatalf("a refused bench was planned %d cards inside the hold", len(b.Cards))
			}
		}
	})

	t.Run("a stale reconciler token deals nothing", func(t *testing.T) {
		seedFleet(t, c, sprint, 50, map[string]int{"ctl-a": 64})
		seedLease(t, c, "lease-2")
		f := newFixture(t)
		st := newFnStore(c)
		p := &Pass{Source: RedisSource{Client: c}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
		if _, err := p.Run(ctx); !errors.Is(err, ErrFenced) {
			t.Fatalf("stale token: err %v, want FENCED", err)
		}
		if got := len(f.lines("ctl-a", "sessions.log")); got != 0 {
			t.Fatalf("a fenced pass opened %d sessions", got)
		}
		if n := zcard(t, c, "s:"+sprint+":pool"); n != 50 {
			t.Fatalf("a fenced pass moved %d cards out of the pool", 50-n)
		}
		if n := len(logEntries(t, c, sprint, "", "")); n != 0 {
			t.Fatalf("a fenced pass wrote %d receipts", n)
		}
	})
}

var regexpToken = regexp.MustCompile(`^\d+\.[0-9a-f]{32}$`)
