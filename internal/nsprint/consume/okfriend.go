// Package consume implements the nova-sprint consumers of #2756 4.5. Each
// consumer reads the sprint log `s:<S>:log` in its own consumer group, and
// each handler is one Redis Function call that writes its transition and
// XACKs the event, idempotent by event id (spec 2.1 rule 2, 5.4).
package consume

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/redis/go-redis/v9"
)

// GroupOkFriend is the ok-to-friend consumer group on s:<S>:log and the name
// of its proc line.
const GroupOkFriend = "ok-to-friend"

// Function names registered by internal/nsprint/fn/lua/route.lua.
const (
	FunctionOkFriendHarvest = "ns_okfriend_harvest"
	FunctionOkFriendReview  = "ns_okfriend_review"
	FunctionOkFriendSkip    = "ns_okfriend_skip"
	FunctionOkFriendPass    = "ns_okfriend_pass"
)

// ReadTitleSuffix is the DONE-WHEN every review task carries.
const ReadTitleSuffix = "DONE-WHEN: typed DISPOSITION at head with score; 8+ lands"

// CICut is the `ci cut` request for one verified head (#2756 4.8, 10.2). The
// cut is idempotent per head and base, so a redelivered harvested event may
// call it again.
type CICut struct {
	Sprint  string
	Label   string
	Repo    string
	PR      int
	Head    string
	Base    string
	BaseRef string
}

// OkFriend is the ok-to-friend consumer for one sprint (#2756 4.5): a card
// that ends DONE gets its harvest task; a harvested card gets its ci cut and
// one review task per required reader at exactly the verified head, and moves
// to review-ready. A card that did not end DONE, a ci card and an orphan-effect
// card get nothing from this consumer (the 3.3 classification is its own
// handler).
type OkFriend struct {
	Store    *store.Store
	Sprint   string
	Consumer string // this process instance's consumer name
	Actor    string
	Count    int64 // events per read; 0 means 100
	// Block is the block of the first new-event read; 0 means 1 s and a
	// negative Block never blocks (a reconcile duty passes inside its 1 s
	// tick, #3323).
	Block time.Duration
	// RetryWait is Run's pause after a pass that left an event pending on a
	// retryable error (ErrNoReaders, ErrReviewBlocked); it doubles on each
	// consecutive retryable pass up to RetryMax and resets on a clean pass.
	// 0 means 1 s; RetryMax 0 means 10 s.
	RetryWait time.Duration
	RetryMax  time.Duration
	// CICut is called for every harvested card before its review transition.
	// nil skips the cut (the ci verb, #2842, is not wired in yet).
	CICut func(context.Context, CICut) error

	// deliverHook runs on every delivered event before it is handled; a test
	// returns an error to kill the instance between delivery and ack.
	deliverHook func(redis.XMessage) error
	// passHook runs after every pass Run makes; a test counts passes.
	passHook func(error)
	// pause waits d or until ctx ends; nil is a timer. A test injects it.
	pause func(ctx context.Context, d time.Duration)
}

// ErrNoReaders keeps a harvested event pending: fewer UP eligible friends
// than the policy requires. The next pass retries it; nothing is written.
var ErrNoReaders = errors.New("ok-to-friend: fewer eligible readers than required")

// ErrReviewBlocked keeps a harvested event pending: a required read id is
// taken by a different payload (CONFLICT) or was cancelled, so the read
// cannot exist at the exact head. The function wrote only the blocking
// unresolved item naming the id; the card stays harvested and the next pass
// retries it once the id is cleared.
var ErrReviewBlocked = errors.New("ok-to-friend: a required read cannot be created")

// retryable: the event stays pending and is retried next pass.
func retryable(err error) bool {
	return errors.Is(err, ErrNoReaders) || errors.Is(err, ErrReviewBlocked)
}

func (o *OkFriend) logKey() string { return "s:" + o.Sprint + ":log" }

func (o *OkFriend) check() error {
	if o == nil || o.Store == nil || o.Sprint == "" || o.Consumer == "" {
		return fmt.Errorf("ok-to-friend: store, sprint and consumer are required")
	}
	return nil
}

// Start creates the consumer group if the sprint has none yet and claims
// every entry still pending under a previous instance, so a restart handles
// what a killed instance received and never acked (spec 5.4).
func (o *OkFriend) Start(ctx context.Context) error {
	if err := o.check(); err != nil {
		return err
	}
	client := o.Store.Client()
	err := client.XGroupCreateMkStream(ctx, o.logKey(), GroupOkFriend, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("ok-to-friend: group: %w", err)
	}
	start := "0-0"
	for {
		_, next, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: o.logKey(), Group: GroupOkFriend, Consumer: o.Consumer,
			MinIdle: 0, Start: start, Count: 1000,
		}).Result()
		if err != nil {
			return fmt.Errorf("ok-to-friend: reclaim pending: %w", err)
		}
		if next == "0-0" || next == "" {
			return nil
		}
		start = next
	}
}

// Run starts the consumer and passes until ctx ends. A pass that leaves an
// event pending on a retryable error is followed by a bounded backoff, so a
// persistent BLOCKED or no-readers event is retried without a hot loop (a
// pending event makes the next pass read without blocking).
func (o *OkFriend) Run(ctx context.Context) error {
	if err := o.Start(ctx); err != nil {
		return err
	}
	var backoff time.Duration
	for ctx.Err() == nil {
		_, err := o.Pass(ctx)
		if o.passHook != nil {
			o.passHook(err)
		}
		if err == nil || ctx.Err() != nil {
			backoff = 0
			continue
		}
		if !retryable(err) {
			return err
		}
		backoff = o.nextBackoff(backoff)
		if o.pause != nil {
			o.pause(ctx, backoff)
			continue
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}

// nextBackoff doubles the previous retry pause, starting at RetryWait and
// bounded by RetryMax.
func (o *OkFriend) nextBackoff(prev time.Duration) time.Duration {
	first, max := o.RetryWait, o.RetryMax
	if first <= 0 {
		first = time.Second
	}
	if max <= 0 {
		max = 10 * time.Second
	}
	if max < first {
		max = first
	}
	next := first
	if prev > 0 {
		next = 2 * prev
	}
	if next > max {
		next = max
	}
	return next
}

// Pass handles this instance's pending entries from id 0, then drains new
// events (blocking only on the first read), and writes proc:ok-to-friend.
func (o *OkFriend) Pass(ctx context.Context) (int, error) {
	if err := o.check(); err != nil {
		return 0, err
	}
	began := time.Now()
	n, err := o.pass(ctx)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if perr := o.Store.Client().FCall(ctx, FunctionOkFriendPass, nil, GroupOkFriend,
		strconv.FormatInt(time.Since(began).Milliseconds(), 10), strconv.Itoa(n), msg).Err(); perr != nil && err == nil {
		err = fmt.Errorf("ok-to-friend: proc: %w", perr)
	}
	return n, err
}

func (o *OkFriend) pass(ctx context.Context) (int, error) {
	count := o.Count
	if count <= 0 {
		count = 100
	}
	block := o.Block
	if block == 0 {
		block = time.Second
	}
	handled := 0
	var deferred error
	// Pending first, once: an event left pending by ErrNoReaders is retried
	// once per pass, never spun on.
	pending, err := o.read(ctx, "0", count, -1)
	if err != nil {
		return 0, err
	}
	n, err := o.handleBatch(ctx, pending)
	handled += n
	if retryable(err) {
		deferred = err
	} else if err != nil {
		return handled, err
	}
	wait := block
	if len(pending) > 0 {
		wait = -1
	}
	for {
		msgs, err := o.read(ctx, ">", count, wait)
		if err != nil {
			return handled, err
		}
		if len(msgs) == 0 {
			return handled, deferred
		}
		n, err := o.handleBatch(ctx, msgs)
		handled += n
		if retryable(err) {
			deferred = err
		} else if err != nil {
			return handled, err
		}
		wait = -1
	}
}

// read issues one XREADGROUP; wait < 0 does not block.
func (o *OkFriend) read(ctx context.Context, id string, count int64, wait time.Duration) ([]redis.XMessage, error) {
	streams, err := o.Store.Client().XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: GroupOkFriend, Consumer: o.Consumer, Streams: []string{o.logKey(), id},
		Count: count, Block: wait, NoAck: false,
	}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ok-to-friend: read %s: %w", id, err)
	}
	var out []redis.XMessage
	for _, s := range streams {
		out = append(out, s.Messages...)
	}
	return out, nil
}

type okEvent struct {
	msg     redis.XMessage
	to      string
	label   string
	attempt string
	card    map[string]string
}

// handleBatch handles one delivered batch: the events that are not a card
// ended or harvested receipt are acked in one XACK; the cards of the rest are
// read in one pipeline, the reader census is one pipeline, and each event is
// then one handler function call.
func (o *OkFriend) handleBatch(ctx context.Context, msgs []redis.XMessage) (int, error) {
	client := o.Store.Client()
	var ignore []string
	var events []*okEvent
	for _, m := range msgs {
		if o.deliverHook != nil {
			if err := o.deliverHook(m); err != nil {
				return 0, err
			}
		}
		kind, _ := m.Values["kind"].(string)
		to, _ := m.Values["to"].(string)
		label, _ := m.Values["id"].(string)
		attempt, _ := m.Values["attempt"].(string)
		if kind != "card" || (to != "ended" && to != "harvested") || label == "" {
			ignore = append(ignore, m.ID)
			continue
		}
		events = append(events, &okEvent{msg: m, to: to, label: label, attempt: attempt})
	}
	if len(ignore) > 0 {
		if err := client.XAck(ctx, o.logKey(), GroupOkFriend, ignore...).Err(); err != nil {
			return 0, fmt.Errorf("ok-to-friend: ack: %w", err)
		}
	}
	if len(events) == 0 {
		return 0, nil
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(events))
	for i, e := range events {
		cmds[i] = pipe.HGetAll(ctx, "s:"+o.Sprint+":card:"+e.label)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("ok-to-friend: read cards: %w", err)
	}
	for i, e := range events {
		e.card = cmds[i].Val()
	}
	var census *readerCensus
	handled := 0
	var deferred error
	for _, e := range events {
		var err error
		switch e.to {
		case "ended":
			err = o.onEnded(ctx, e)
		case "harvested":
			if census == nil {
				census, err = o.census(ctx)
				if err != nil {
					return handled, err
				}
			}
			err = o.onHarvested(ctx, e, census)
		}
		if retryable(err) {
			deferred = err
			continue
		}
		if err != nil {
			return handled, err
		}
		handled++
	}
	return handled, deferred
}

func (o *OkFriend) skip(ctx context.Context, e *okEvent, reason, unresolved string) error {
	return o.Store.Client().FCall(ctx, FunctionOkFriendSkip, nil,
		o.Sprint, GroupOkFriend, e.msg.ID, reason, unresolved).Err()
}

// isCICard: a ci card (10.2) carries ci_for; its verdict is already in
// ci:<repo>:<head>:<gid>, so it has nothing to harvest and is never a read.
func isCICard(label string, card map[string]string) bool {
	return card["ci_for"] != "" || (card["kind"] == "script" && strings.HasPrefix(label, "ci-"))
}

// onEnded: ended DONE creates the harvest task; a ci card, a card that did
// not end DONE, a card that committed nothing (report-to-read's), or a stale
// attempt is acked with no transition.
func (o *OkFriend) onEnded(ctx context.Context, e *okEvent) error {
	c := e.card
	switch {
	case len(c) == 0:
		return o.skip(ctx, e, "no-card", "")
	case c["attempt"] != e.attempt:
		return o.skip(ctx, e, "stale-attempt", "")
	case c["outcome"] != "DONE":
		return o.skip(ctx, e, "not-done", "")
	case isCICard(e.label, c):
		return o.skip(ctx, e, "ci-card", "")
	case NoCommit(c):
		// Nothing to harvest: the report-to-read rule (report.go) gives it
		// its one report read (#3036).
		return o.skip(ctx, e, "no-commit", "")
	}
	priority, _ := strconv.Atoi(c["priority"])
	req := task.PushRequest{
		Sprint: o.Sprint, ID: "harvest-" + e.label, Kind: task.KindHarvest,
		Title:   fmt.Sprintf("harvest %s attempt %s on %s", e.label, e.attempt, c["bench"]),
		Effects: task.EffectsExternal, Repo: c["repo"], Ref: c["identity"],
		To: "harvest:" + c["bench"], Front: true, Priority: priority,
	}
	req.PayloadSHA = task.PayloadSHA(req)
	front := "1"
	return o.Store.Client().FCall(ctx, FunctionOkFriendHarvest, nil,
		o.Sprint, GroupOkFriend, e.msg.ID, e.label, e.attempt, c["bench"],
		req.ID, req.Title, string(req.Effects), req.Repo, req.Ref, front,
		strconv.Itoa(priority), req.PayloadSHA, o.Actor).Err()
}

// onHarvested: the ci cut for the verified head, then one review task per
// required reader at exactly that head and the card to review-ready, in one
// function call.
func (o *OkFriend) onHarvested(ctx context.Context, e *okEvent, census *readerCensus) error {
	c := e.card
	if len(c) == 0 {
		return o.skip(ctx, e, "no-card", "")
	}
	if c["attempt"] != e.attempt {
		return o.skip(ctx, e, "stale-attempt", "")
	}
	if c["state"] != "harvested" {
		return o.skip(ctx, e, "state "+c["state"], "")
	}
	pr, _ := strconv.Atoi(c["pr"])
	head := c["head"]
	if pr <= 0 || head == "" || head != c["pushed_sha"] {
		return o.skip(ctx, e, "unverified-head", e.label+":unverified-head:"+c["base_sha"])
	}

	resKey := "s:" + o.Sprint + ":card:" + e.label + ":result:a" + e.attempt
	if o.Store.Client().Exists(ctx, resKey).Val() == 1 {
		resFields, err := o.Store.Client().HMGet(ctx, resKey, "valid", "c_check", "field").Result()
		if err == nil && len(resFields) >= 3 {
			valid, _ := resFields[0].(string)
			cCheck, _ := resFields[1].(string)
			defectField, _ := resFields[2].(string)
			if valid != "1" {
				if defectField == "" {
					defectField = "result"
				}
				return o.skip(ctx, e, defectField, "")
			}
			if cCheck != "pass" {
				return o.skip(ctx, e, "CHECK", "")
			}
		}
	}
	want := census.required(c)
	readers := census.pick(want, c["author"])
	if len(readers) < want {
		return ErrNoReaders
	}
	if o.CICut != nil {
		if err := o.CICut(ctx, CICut{Sprint: o.Sprint, Label: e.label, Repo: c["repo"],
			PR: pr, Head: head, Base: c["base_sha"], BaseRef: c["base"]}); err != nil {
			return fmt.Errorf("ok-to-friend: ci cut %s at %s: %w", e.label, head, err)
		}
	}
	priority, _ := strconv.Atoi(c["priority"])
	args := []any{o.Sprint, GroupOkFriend, e.msg.ID, e.label, e.attempt, c["repo"],
		strconv.Itoa(pr), head, o.Actor, strconv.Itoa(len(readers))}
	for _, friend := range readers {
		req := task.PushRequest{
			Sprint: o.Sprint, ID: task.ReviewID(c["repo"], pr, head, friend), Kind: task.KindReview,
			Title:   fmt.Sprintf("read %s#%d %s at %s | %s", c["repo"], pr, e.label, head[:min(12, len(head))], ReadTitleSuffix),
			Effects: task.EffectsNone, Repo: c["repo"], PR: pr, Head: head, Ref: c["identity"],
			To: friend, Front: true, Priority: priority,
		}
		args = append(args, req.ID, friend, req.Title, strconv.Itoa(priority), task.PayloadSHA(req))
	}
	reply, err := o.Store.Client().FCall(ctx, FunctionOkFriendReview, nil, args...).Result()
	if err != nil {
		return fmt.Errorf("ok-to-friend: review %s: %w", e.label, err)
	}
	if values, ok := reply.([]any); ok && len(values) > 0 {
		switch values[0] {
		case "RETRY":
			return ErrNoReaders
		case "BLOCKED":
			return fmt.Errorf("%w: %s %v", ErrReviewBlocked, e.label, values[1:])
		}
	}
	for _, friend := range readers {
		census.load[friend]++
	}
	return nil
}

// readerCensus is one pipelined read of the friends: UP (a live beat), not
// paused, slots > 0, and load = leased plus open queue in this sprint.
type readerCensus struct {
	friends     []string
	load        map[string]int
	readers     int
	security    int
	secPrefixes []string
}

func (o *OkFriend) census(ctx context.Context) (*readerCensus, error) {
	client := o.Store.Client()
	names, err := client.SMembers(ctx, "friends").Result()
	if err != nil {
		return nil, fmt.Errorf("ok-to-friend: friends: %w", err)
	}
	sort.Strings(names)
	policy, err := client.HGetAll(ctx, "s:"+o.Sprint+":policy").Result()
	if err != nil {
		return nil, fmt.Errorf("ok-to-friend: policy: %w", err)
	}
	c := &readerCensus{load: map[string]int{}, readers: 1, security: 2}
	if n, err := strconv.Atoi(policy["readers"]); err == nil && n > 0 {
		c.readers = n
	}
	if n, err := strconv.Atoi(policy["readers_security"]); err == nil && n > 0 {
		c.security = n
	}
	c.secPrefixes = strings.FieldsFunc(policy["security_paths"], func(r rune) bool { return r == ' ' || r == ',' })
	pipe := client.Pipeline()
	type row struct {
		beat          *redis.IntCmd
		desired       *redis.SliceCmd
		start, living *redis.IntCmd
		open          *redis.IntCmd
	}
	rows := make([]row, len(names))
	for i, f := range names {
		rows[i] = row{
			beat:    pipe.Exists(ctx, "friend:"+f+":beat"),
			desired: pipe.HMGet(ctx, "friend:"+f+":desired", "slots", "paused"),
			start:   pipe.ZCard(ctx, "friend:"+f+":starting"),
			living:  pipe.ZCard(ctx, "friend:"+f+":living"),
			open:    pipe.ZCard(ctx, "s:"+o.Sprint+":open:"+f),
		}
	}
	if len(names) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, fmt.Errorf("ok-to-friend: census: %w", err)
		}
	}
	for i, f := range names {
		r := rows[i]
		if r.beat.Val() == 0 || f == "jev" {
			continue
		}
		d := r.desired.Val()
		slots, _ := strconv.Atoi(fmt.Sprint(d[0]))
		if slots <= 0 || fmt.Sprint(d[1]) == "1" {
			continue
		}
		c.friends = append(c.friends, f)
		c.load[f] = int(r.start.Val() + r.living.Val() + r.open.Val())
	}
	return c, nil
}

// required is the policy readers, or readers_security when the card's paths
// touch a security path.
func (c *readerCensus) required(card map[string]string) int {
	return RequiredReads(map[string]string{
		"readers":          strconv.Itoa(c.readers),
		"readers_security": strconv.Itoa(c.security),
		"security_paths":   strings.Join(c.secPrefixes, ","),
	}, card)
}

// pick returns up to n least-loaded eligible friends, never the author.
func (c *readerCensus) pick(n int, author string) []string {
	var pool []string
	for _, f := range c.friends {
		if f != author {
			pool = append(pool, f)
		}
	}
	sort.SliceStable(pool, func(i, j int) bool {
		if c.load[pool[i]] != c.load[pool[j]] {
			return c.load[pool[i]] < c.load[pool[j]]
		}
		return pool[i] < pool[j]
	})
	if len(pool) > n {
		pool = pool[:n]
	}
	return pool
}

// PostResultRefusal is returned when a card result cannot be posted as read evidence.
type PostResultRefusal struct {
	Field  string
	Defect string
}

func (r *PostResultRefusal) Error() string {
	return fmt.Sprintf("field=%s defect=%s", r.Field, r.Defect)
}

// PostReads posts swarm read evidence into Redis for kind=read cards (#2506).
func PostReads(ctx context.Context, st *store.Store, sprint, label string, res typedrec.Result) error {
	if !res.Valid {
		return &PostResultRefusal{Field: res.Field, Defect: res.Defect}
	}
	head := res.Claims["HEAD"]
	if head == "" {
		return &PostResultRefusal{Field: "HEAD", Defect: "missing"}
	}
	repo := res.Claims["REPO"]
	pr := res.Claims["PR"]
	suggest := res.Claims["SUGGEST"]
	findings := res.Claims["FINDINGS"]
	floor := res.Claims["FLOOR"]
	if repo == "" || pr == "" {
		return fmt.Errorf("missing repo or pr")
	}

	reply, err := st.Client().FCall(ctx, "ns_read_evidence", nil,
		sprint, repo, pr, label, head, suggest, findings, floor).Text()
	if err != nil {
		return err
	}
	if reply != "OK" {
		return fmt.Errorf("ns_read_evidence: %s", reply)
	}
	return nil
}
