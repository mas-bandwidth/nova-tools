package consume

// route.go is `nova-sprint route` (#2756 section 11 rows 2 and 5, 4.5;
// nova-tools #3036): ok-to-friend, the 3.3 classification, report-to-read,
// pr-to-read and hold-to-fix are the rules of one router, not separate
// polling loops. One process per sprint holds `lease:route:<S>` (instance,
// token, host, at; renewed every 2 s, TTL 6 s, the lease:reconciler shape of
// 2.2 and 5.1); a second instance is refused and runs nothing. Each rule
// keeps its own consumer group, so every rule sees every event, and each
// handler writes its transition and XACKs in one function call (4.5).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Function names registered by internal/nsprint/fn/lua/route_lease.lua.
const (
	FunctionRouteLeaseTake    = "ns_route_lease_take"
	FunctionRouteLeaseRenew   = "ns_route_lease_renew"
	FunctionRouteLeaseRelease = "ns_route_lease_release"
)

// Rule names, in the order the router starts them.
const (
	RuleOkFriend  = GroupOkFriend
	RuleClassify  = "classify"
	RuleReport    = GroupReport
	RulePRToRead  = "pr-to-read"
	RuleHoldToFix = "hold-to-fix"
)

// Handler is one routing rule: Start prepares its source (creates its
// consumer group and claims a previous instance's pending entries); Pass
// handles what is pending and what is new, blocking on its source for at
// most its own block. OkFriend, Report and the 3.3 Classifier (#3076)
// satisfy it as they are.
type Handler interface {
	Start(ctx context.Context) error
	Pass(ctx context.Context) (int, error)
}

// PRToRead is the pr-to-read rule (#2756 4.5, 3.2 rows review-ready to
// land-ready and landed; nova-tools #2941, prread.go).
type PRToRead interface{ Handler }

// HoldToFix is the hold-to-fix rule (#2756 4.5; nova-tools #3092):
// a typed HOLD at head becomes one fix task per dedup key. Not built yet;
// the controls run a fake.
type HoldToFix interface{ Handler }

// RouteRule is one named rule of the router.
type RouteRule struct {
	Name    string
	Handler Handler
}

// Rules lists the router's rules in their fixed order, leaving out the ones
// not wired (nil). classify is the 3.3 handler of #3076 as a Handler.
func Rules(ok *OkFriend, report *Report, classify Handler, pr PRToRead, hold HoldToFix) []RouteRule {
	var out []RouteRule
	if ok != nil {
		out = append(out, RouteRule{RuleOkFriend, ok})
	}
	if classify != nil {
		out = append(out, RouteRule{RuleClassify, classify})
	}
	if report != nil {
		out = append(out, RouteRule{RuleReport, report})
	}
	if pr != nil {
		out = append(out, RouteRule{RulePRToRead, pr})
	}
	if hold != nil {
		out = append(out, RouteRule{RuleHoldToFix, hold})
	}
	return out
}

// ErrLeaseHeld refuses a second router for a sprint whose lease is live.
var ErrLeaseHeld = errors.New("route: lease held by another instance")

// ErrLeaseLost stops a router whose lease was taken over or expired: its
// instance or token no longer matches.
var ErrLeaseLost = errors.New("route: lease lost")

// LeaseHeldError names the live holder a second instance was refused by.
type LeaseHeldError struct {
	Sprint string
	Holder string
	At     string // the holder's last take or renew, Redis ms
}

func (e *LeaseHeldError) Error() string {
	return fmt.Sprintf("route: %s held by %s at %s", LeaseKey(e.Sprint), e.Holder, e.At)
}

func (e *LeaseHeldError) Is(target error) bool { return target == ErrLeaseHeld }

// LeaseKey is the router's lease for one sprint.
func LeaseKey(sprint string) string { return "lease:route:" + sprint }

// NewInstance returns a random router instance id; a pid is never evidence
// of liveness (5.1).
func NewInstance() (string, error) {
	return randomHex(8)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("route: random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Router runs every rule of one sprint inside one process under one lease.
type Router struct {
	Store    *store.Store
	Sprint   string
	Instance string // the lease instance and every rule's consumer name
	Host     string
	Rules    []RouteRule
	TTL      time.Duration // lease TTL; 0 means 6 s
	Renew    time.Duration // lease renew period; 0 means 2 s
	Backoff  time.Duration // wait before retrying a rule short of readers; 0 means 1 s
}

func (r *Router) check() error {
	if r == nil || r.Store == nil || r.Sprint == "" || r.Instance == "" {
		return fmt.Errorf("route: store, sprint and instance are required")
	}
	if len(r.Rules) == 0 {
		return fmt.Errorf("route: no rules")
	}
	seen := map[string]bool{}
	for _, rule := range r.Rules {
		if rule.Name == "" || rule.Handler == nil {
			return fmt.Errorf("route: a rule needs a name and a handler")
		}
		if seen[rule.Name] {
			return fmt.Errorf("route: rule %s twice", rule.Name)
		}
		seen[rule.Name] = true
	}
	return nil
}

func durationOr(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Names lists the rules this router runs.
func (r *Router) Names() []string {
	out := make([]string, len(r.Rules))
	for i, rule := range r.Rules {
		out[i] = rule.Name
	}
	return out
}

// Run takes the sprint's lease, starts every rule, and passes each rule in
// its own goroutine until ctx ends (nil), a rule fails, or the lease is lost
// (ErrLeaseLost). A live lease under another instance refuses before any
// rule starts (ErrLeaseHeld, as a *LeaseHeldError). A clean stop releases
// the lease so the next instance starts at once.
func (r *Router) Run(ctx context.Context) error {
	if err := r.check(); err != nil {
		return err
	}
	ttl := durationOr(r.TTL, 6*time.Second)
	renew := durationOr(r.Renew, 2*time.Second)
	backoff := durationOr(r.Backoff, time.Second)
	token, err := randomHex(16)
	if err != nil {
		return err
	}
	client := r.Store.Client()
	reply, err := client.FCall(ctx, FunctionRouteLeaseTake, nil, r.Sprint, r.Instance, token, r.Host,
		strconv.FormatInt(ttl.Milliseconds(), 10)).Slice()
	if err != nil {
		return fmt.Errorf("route: take %s: %w", LeaseKey(r.Sprint), err)
	}
	if len(reply) > 0 && reply[0] == "HELD" {
		held := &LeaseHeldError{Sprint: r.Sprint}
		if len(reply) > 1 {
			held.Holder = fmt.Sprint(reply[1])
		}
		if len(reply) > 2 {
			held.At = fmt.Sprint(reply[2])
		}
		return held
	}
	lost := false
	defer func() {
		if lost {
			return
		}
		// The release runs after ctx ended, so it gets a context of its own.
		_ = client.FCall(context.WithoutCancel(ctx), FunctionRouteLeaseRelease, nil, r.Sprint, r.Instance, token).Err()
	}()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, rule := range r.Rules {
		if err := rule.Handler.Start(runCtx); err != nil {
			return fmt.Errorf("route: start %s: %w", rule.Name, err)
		}
	}

	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	fail := func(err error) {
		once.Do(func() { first = err })
		cancel()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(renew)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			}
			reply, err := client.FCall(runCtx, FunctionRouteLeaseRenew, nil, r.Sprint, r.Instance, token,
				strconv.FormatInt(ttl.Milliseconds(), 10)).Slice()
			if runCtx.Err() != nil {
				return
			}
			if err != nil {
				fail(fmt.Errorf("route: renew %s: %w", LeaseKey(r.Sprint), err))
				return
			}
			if len(reply) > 0 && reply[0] == "LOST" {
				holder := ""
				if len(reply) > 1 {
					holder = fmt.Sprint(reply[1])
				}
				lost = true
				fail(fmt.Errorf("%w: %s now held by %q", ErrLeaseLost, LeaseKey(r.Sprint), holder))
				return
			}
		}
	}()
	for _, rule := range r.Rules {
		wg.Add(1)
		go func(rule RouteRule) {
			defer wg.Done()
			for runCtx.Err() == nil {
				_, err := rule.Handler.Pass(runCtx)
				if runCtx.Err() != nil {
					return
				}
				if errors.Is(err, ErrNoReaders) {
					select {
					case <-runCtx.Done():
						return
					case <-time.After(backoff):
					}
					continue
				}
				if err != nil {
					fail(fmt.Errorf("route: %s: %w", rule.Name, err))
					return
				}
			}
		}(rule)
	}
	wg.Wait()
	return first
}

// groupLoop is the consumer-group reading every rule on s:<S>:log shares:
// start (create the group, claim a previous instance's pending entries),
// then per pass the pending entries from id 0 once and the new events until
// none are left (spec 5.4).
type groupLoop struct {
	store    *store.Store
	sprint   string
	group    string
	consumer string
	count    int64
	block    time.Duration
}

func (g groupLoop) logKey() string { return "s:" + g.sprint + ":log" }

func (g groupLoop) start(ctx context.Context) error {
	client := g.store.Client()
	err := client.XGroupCreateMkStream(ctx, g.logKey(), g.group, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("%s: group: %w", g.group, err)
	}
	start := "0-0"
	for {
		_, next, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: g.logKey(), Group: g.group, Consumer: g.consumer,
			MinIdle: 0, Start: start, Count: 1000,
		}).Result()
		if err != nil {
			return fmt.Errorf("%s: reclaim pending: %w", g.group, err)
		}
		if next == "0-0" || next == "" {
			return nil
		}
		start = next
	}
}

func (g groupLoop) read(ctx context.Context, id string, count int64, wait time.Duration) ([]redis.XMessage, error) {
	streams, err := g.store.Client().XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: g.group, Consumer: g.consumer, Streams: []string{g.logKey(), id},
		Count: count, Block: wait,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: read %s: %w", g.group, id, err)
	}
	var out []redis.XMessage
	for _, s := range streams {
		out = append(out, s.Messages...)
	}
	return out, nil
}

// passWithProc runs one pass and writes proc:<group> (pass_at, took_ms, n,
// err) with the ok-to-friend proc function, which takes the name.
func (g groupLoop) passWithProc(ctx context.Context, handle func(context.Context, []redis.XMessage) (int, error)) (int, error) {
	began := time.Now()
	n, err := g.pass(ctx, handle)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if perr := g.store.Client().FCall(ctx, FunctionOkFriendPass, nil, g.group,
		strconv.FormatInt(time.Since(began).Milliseconds(), 10), strconv.Itoa(n), msg).Err(); perr != nil && err == nil {
		err = fmt.Errorf("%s: proc: %w", g.group, perr)
	}
	return n, err
}

// pass handles the pending entries once (an event left pending by
// ErrNoReaders is retried once per pass, never spun on), then the new
// events, blocking only on the first read when nothing was pending.
func (g groupLoop) pass(ctx context.Context, handle func(context.Context, []redis.XMessage) (int, error)) (int, error) {
	count := g.count
	if count <= 0 {
		count = 100
	}
	block := durationOr(g.block, time.Second)
	handled := 0
	var deferred error
	pending, err := g.read(ctx, "0", count, -1)
	if err != nil {
		return 0, err
	}
	n, err := handle(ctx, pending)
	handled += n
	if errors.Is(err, ErrNoReaders) {
		deferred = err
	} else if err != nil {
		return handled, err
	}
	wait := block
	if len(pending) > 0 {
		wait = -1
	}
	for {
		msgs, err := g.read(ctx, ">", count, wait)
		if err != nil {
			return handled, err
		}
		if len(msgs) == 0 {
			return handled, deferred
		}
		n, err := handle(ctx, msgs)
		handled += n
		if errors.Is(err, ErrNoReaders) {
			deferred = err
		} else if err != nil {
			return handled, err
		}
		wait = -1
	}
}
