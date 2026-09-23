package consume

// classify.go is the 3.3 handler of the ok-to-friend consumer (#2756 3.3,
// control 16; nova-tools #3038): every card that ends other than DONE is
// classified deterministically by its reason code into one action, under the
// dedup key `<label>:<reason>:<base_sha>` and the interim retry budgets of
// s:<S>:policy. The table is Go (ClassifyOutcome); the budget check and every
// write are one Redis Function call (ns_classify_end in
// internal/nsprint/fn/lua/classify.lua) that also records the event under
// its idempotency key and XACKs it, so a redelivered event changes nothing
// (spec 2.1 rule 2, 5.4).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// GroupClassify is the classification consumer group on s:<S>:log and the
// name of its proc line. It is a group of its own so the DONE path
// (ok-to-friend) and this one each see every ended event; `nova-sprint
// route` (#3036) runs both in one process.
const GroupClassify = "ok-to-friend-classify"

// Function names registered by internal/nsprint/fn/lua/classify.lua.
const (
	FunctionClassifyEnd  = "ns_classify_end"
	FunctionClassifyPass = "ns_classify_pass"
)

// The 3.3 actions.
const (
	ActionNone       = ""            // not this handler's: DONE, or a ci card (10.5)
	ActionRequeue    = "requeue"     // front of the pool, the failing bench avoided
	ActionRequeueEnv = "requeue-env" // as requeue, plus the bench why line and bench item
	ActionRecut      = "recut"       // a front card at the tip; the old card superseded
	ActionWaiting    = "waiting"     // back to waiting with the named dependency added
	ActionFix        = "fix"         // a front fix card at the tip, no dependency; the old superseded
	ActionUnresolved = "unresolved"  // an item for the cutter, no retry
)

// Rule is one row of the 3.3 table.
type Rule struct {
	Action string
	// Class names the card's budget counter field `retry_<Class>`; one class
	// is one budget (crash, timeout and idle-killed share one).
	Class string
	// Policy is the s:<S>:policy field that holds the budget; "" for a row
	// with no retry.
	Policy string
	// Default is the interim budget when the policy does not set one.
	Default int
}

// Budget is the row's retry budget: the policy field when it holds a
// non-negative integer, else the interim default.
func (r Rule) Budget(policy map[string]string) int {
	if r.Policy == "" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(policy[r.Policy])); err == nil && n >= 0 {
		return n
	}
	return r.Default
}

// ClassifyOutcome is the 3.3 table. An unknown reason is `other`.
func ClassifyOutcome(outcome, reason string) Rule {
	switch outcome {
	case "DONE":
		return Rule{Action: ActionNone}
	case "FAILED":
		switch reason {
		case "crash", "timeout", "idle-killed":
			return Rule{Action: ActionRequeue, Class: "crash", Policy: "retry_crash", Default: 2}
		case "tests-red":
			return Rule{Action: ActionFix, Class: "tests_red", Policy: "retry_tests_red", Default: 1}
		}
	case "BLOCKED":
		switch reason {
		case "env":
			return Rule{Action: ActionRequeueEnv, Class: "env", Policy: "retry_env", Default: 1}
		case "base-moved":
			return Rule{Action: ActionRecut, Class: "base_moved", Policy: "retry_base_moved", Default: 1}
		case "deps":
			return Rule{Action: ActionWaiting}
		}
	}
	return Rule{Action: ActionUnresolved}
}

// Classifier is the 3.3 handler for one sprint.
type Classifier struct {
	Store    *store.Store
	Sprint   string
	Consumer string // this process instance's consumer name
	Actor    string
	Count    int64         // events per read; 0 means 100
	Block    time.Duration // block of the first new-event read; 0 means 1 s, < 0 does not block
	// Tip returns the current tip sha of base in repo, for a fix or recut
	// card. nil cuts the card with an empty base_sha and cut_at_deal=1, so
	// the deal pass cuts it at the tip it deals from.
	Tip func(ctx context.Context, repo, base string) (string, error)
}

func (c *Classifier) logKey() string { return "s:" + c.Sprint + ":log" }

func (c *Classifier) check() error {
	if c == nil || c.Store == nil || c.Sprint == "" || c.Consumer == "" {
		return fmt.Errorf("classify: store, sprint and consumer are required")
	}
	return nil
}

// Start creates the consumer group if the sprint has none yet and claims
// every entry still pending under a previous instance (spec 5.4).
func (c *Classifier) Start(ctx context.Context) error {
	if err := c.check(); err != nil {
		return err
	}
	client := c.Store.Client()
	err := client.XGroupCreateMkStream(ctx, c.logKey(), GroupClassify, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("classify: group: %w", err)
	}
	start := "0-0"
	for {
		_, next, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: c.logKey(), Group: GroupClassify, Consumer: c.Consumer,
			MinIdle: 0, Start: start, Count: 1000,
		}).Result()
		if err != nil {
			return fmt.Errorf("classify: reclaim pending: %w", err)
		}
		if next == "0-0" || next == "" {
			return nil
		}
		start = next
	}
}

// Run starts the handler and passes until ctx ends.
func (c *Classifier) Run(ctx context.Context) error {
	if err := c.Start(ctx); err != nil {
		return err
	}
	for ctx.Err() == nil {
		if _, err := c.Pass(ctx); err != nil && ctx.Err() == nil {
			return err
		}
	}
	return nil
}

// Pass handles this instance's pending entries, then drains new events, and
// writes proc:ok-to-friend-classify. It starts the group on first use.
func (c *Classifier) Pass(ctx context.Context) (int, error) {
	if err := c.Start(ctx); err != nil {
		return 0, err
	}
	began := time.Now()
	n, err := c.pass(ctx)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if perr := c.Store.Client().FCall(ctx, FunctionClassifyPass, nil, GroupClassify,
		strconv.FormatInt(time.Since(began).Milliseconds(), 10), strconv.Itoa(n), msg).Err(); perr != nil && err == nil {
		err = fmt.Errorf("classify: proc: %w", perr)
	}
	return n, err
}

func (c *Classifier) pass(ctx context.Context) (int, error) {
	count := c.Count
	if count <= 0 {
		count = 100
	}
	block := c.Block
	if block == 0 {
		block = time.Second
	}
	handled := 0
	pending, err := c.read(ctx, "0", count, -1)
	if err != nil {
		return 0, err
	}
	n, err := c.handleBatch(ctx, pending)
	handled += n
	if err != nil {
		return handled, err
	}
	wait := block
	if len(pending) > 0 {
		wait = -1
	}
	for {
		msgs, err := c.read(ctx, ">", count, wait)
		if err != nil {
			return handled, err
		}
		if len(msgs) == 0 {
			return handled, nil
		}
		n, err := c.handleBatch(ctx, msgs)
		handled += n
		if err != nil {
			return handled, err
		}
		wait = -1
	}
}

func (c *Classifier) read(ctx context.Context, id string, count int64, wait time.Duration) ([]redis.XMessage, error) {
	streams, err := c.Store.Client().XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: GroupClassify, Consumer: c.Consumer, Streams: []string{c.logKey(), id},
		Count: count, Block: wait,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("classify: read %s: %w", id, err)
	}
	var out []redis.XMessage
	for _, s := range streams {
		out = append(out, s.Messages...)
	}
	return out, nil
}

// handleBatch acks every event that is not a card `ended` receipt in one
// XACK, reads the ended cards and the policy in one pipeline, and handles
// each ended event with one function call.
func (c *Classifier) handleBatch(ctx context.Context, msgs []redis.XMessage) (int, error) {
	client := c.Store.Client()
	var ignore []string
	type ended struct {
		id, label, attempt string
		card               *redis.MapStringStringCmd
	}
	var events []*ended
	for _, m := range msgs {
		kind, _ := m.Values["kind"].(string)
		to, _ := m.Values["to"].(string)
		label, _ := m.Values["id"].(string)
		attempt, _ := m.Values["attempt"].(string)
		if kind != "card" || to != "ended" || label == "" {
			ignore = append(ignore, m.ID)
			continue
		}
		events = append(events, &ended{id: m.ID, label: label, attempt: attempt})
	}
	if len(ignore) > 0 {
		if err := client.XAck(ctx, c.logKey(), GroupClassify, ignore...).Err(); err != nil {
			return 0, fmt.Errorf("classify: ack: %w", err)
		}
	}
	if len(events) == 0 {
		return 0, nil
	}
	pipe := client.Pipeline()
	policyCmd := pipe.HGetAll(ctx, "s:"+c.Sprint+":policy")
	for _, e := range events {
		e.card = pipe.HGetAll(ctx, "s:"+c.Sprint+":card:"+e.label)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("classify: read cards: %w", err)
	}
	policy := policyCmd.Val()
	handled := 0
	for _, e := range events {
		if err := c.handle(ctx, e.id, e.label, e.attempt, e.card.Val(), policy); err != nil {
			return handled, err
		}
		handled++
	}
	return handled, nil
}

// handle classifies one ended event. The Go side picks the row and, for a
// fix or recut, the new card's label, base and payload; the function guards
// the card state and the budget and writes the action atomically.
func (c *Classifier) handle(ctx context.Context, eventID, label, attempt string, card, policy map[string]string) error {
	rule := ClassifyOutcome(card["outcome"], card["reason"])
	if len(card) == 0 || isCIClassified(label, card) {
		rule = Rule{Action: ActionNone}
	}
	reason := card["reason"]
	if reason == "" {
		reason = "other"
	}
	newLabel, newBase, payload, title := "", "", "", ""
	if rule.Action == ActionFix || rule.Action == ActionRecut {
		prefix := "fix-"
		if rule.Action == ActionRecut {
			prefix = "recut-"
		}
		newLabel = prefix + label
		if c.Tip != nil && rule.Budget(policy) > atoiZero(card["retry_"+rule.Class]) {
			tip, err := c.Tip(ctx, card["repo"], card["base"])
			if err != nil {
				return fmt.Errorf("classify: tip of %s %s for %s: %w", card["repo"], card["base"], newLabel, err)
			}
			newBase = tip
		}
		title = fmt.Sprintf("%s %s: %s %s at attempt %s on %s", strings.TrimSuffix(prefix, "-"), label,
			card["outcome"], reason, attempt, card["bench"])
		payload = classifyPayloadSHA(newLabel, label, reason, card["repo"], card["base"], newBase, card["paths"])
	}
	err := c.Store.Client().FCall(ctx, FunctionClassifyEnd, nil,
		c.Sprint, GroupClassify, eventID, label, attempt, rule.Action, rule.Class,
		strconv.Itoa(rule.Budget(policy)), reason, newLabel, newBase, payload, title, c.Actor).Err()
	if err != nil {
		return fmt.Errorf("classify: %s attempt %s: %w", label, attempt, err)
	}
	return nil
}

// isCIClassified: a ci card (10.2) carries ci_for; its FAIL verdict follows
// the rerun and flaky policy of 10.5, never the tests-red row.
func isCIClassified(label string, card map[string]string) bool {
	return card["ci_for"] != "" || (card["kind"] == "script" && strings.HasPrefix(label, "ci-"))
}

func atoiZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// classifyPayloadSHA identifies a fix or recut card's content, so the same
// cut from a redelivered event compares equal and a different one conflicts.
func classifyPayloadSHA(fields ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(fields, "\x00")))
	return hex.EncodeToString(sum[:])
}
