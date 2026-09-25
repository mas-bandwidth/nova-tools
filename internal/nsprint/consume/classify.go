package consume

// The rev-6 classifier for #3038 consumes every card-ended receipt in its own
// group. Go gathers only hints and the git tip needed for follow-up cards;
// ns_classify re-reads all authoritative Redis evidence and performs the one
// atomic decision, charge, transition, receipt, idempotency write and XACK.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

const (
	GroupClassify    = "classify"
	FunctionClassify = "ns_classify"
)

const (
	ActionNone       = ""
	ActionRequeue    = "requeue"
	ActionRequeueEnv = "requeue-env"
	ActionRecut      = "recut"
	ActionWaiting    = "waiting"
	ActionFix        = "fix"
	ActionUnresolved = "unresolved"
)

type Rule struct {
	Action, Class, Policy string
	Default               int
}

func (r Rule) Budget(policy map[string]string) int {
	if r.Policy == "" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(policy[r.Policy])); err == nil && n >= 0 {
		return n
	}
	return r.Default
}

func ClassifyOutcome(outcome, reason string) Rule {
	switch outcome {
	case "DONE":
		return Rule{Action: ActionNone}
	case "FAILED":
		switch reason {
		case "crash", "timeout", "idle-killed":
			return Rule{Action: ActionRequeue, Class: "fail", Policy: "retry_fail", Default: 2}
		case "tests-red":
			return Rule{Action: ActionFix, Class: "fix", Policy: "retry_fix", Default: 1}
		}
	case "BLOCKED":
		switch reason {
		case "env":
			return Rule{Action: ActionRequeueEnv, Class: "env", Policy: "retry_env", Default: 1}
		case "base-moved":
			return Rule{Action: ActionRecut, Class: "recut", Policy: "retry_recut", Default: 1}
		case "deps":
			return Rule{Action: ActionWaiting, Class: "deps", Policy: "retry_deps", Default: 1}
		}
	}
	return Rule{Action: ActionUnresolved}
}

type Classification struct {
	EventID string
	Label   string
	Outcome string
	Reason  string
	Result  string
}

type Classifier struct {
	Store    *store.Store
	Sprint   string
	Consumer string
	Actor    string
	Count    int64
	Block    time.Duration
	Tip      func(context.Context, string, string) (string, error)
}

func (c *Classifier) logKey() string { return "s:" + c.Sprint + ":log" }
func (c *Classifier) check() error {
	if c == nil || c.Store == nil || c.Sprint == "" || c.Consumer == "" {
		return errors.New("classify: store, sprint and consumer are required")
	}
	return nil
}

func (c *Classifier) Start(ctx context.Context) error {
	if err := c.check(); err != nil {
		return err
	}
	client := c.Store.Client()
	if err := client.XGroupCreateMkStream(ctx, c.logKey(), GroupClassify, "0").Err(); err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("classify: group: %w", err)
	}
	start := "0-0"
	for {
		_, next, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: c.logKey(), Group: GroupClassify,
			Consumer: c.Consumer, MinIdle: 0, Start: start, Count: 1000}).Result()
		if err != nil {
			return fmt.Errorf("classify: reclaim pending: %w", err)
		}
		if next == "" || next == "0-0" {
			return nil
		}
		start = next
	}
}

func (c *Classifier) Run(ctx context.Context) error {
	if err := c.Start(ctx); err != nil {
		return err
	}
	for ctx.Err() == nil {
		if _, err := c.Pass(ctx); err != nil && ctx.Err() == nil {
			return err
		}
	}
	return ctx.Err()
}

func (c *Classifier) Pass(ctx context.Context) (int, error) {
	rows, err := c.PassResults(ctx)
	return len(rows), err
}

func (c *Classifier) PassResults(ctx context.Context) ([]Classification, error) {
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	began := time.Now()
	rows, err := c.pass(ctx)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if perr := c.Store.Client().FCall(ctx, FunctionClassify, nil, "pass",
		strconv.FormatInt(time.Since(began).Milliseconds(), 10), strconv.Itoa(len(rows)), msg).Err(); perr != nil && err == nil {
		err = fmt.Errorf("classify: proc: %w", perr)
	}
	return rows, err
}

func (c *Classifier) pass(ctx context.Context) ([]Classification, error) {
	count := c.Count
	if count <= 0 {
		count = 100
	}
	block := c.Block
	if block == 0 {
		block = time.Second
	}
	var out []Classification
	pending, err := c.read(ctx, "0", count, -1)
	if err != nil {
		return nil, err
	}
	rows, err := c.handleBatch(ctx, pending)
	out = append(out, rows...)
	if err != nil {
		return out, err
	}
	wait := block
	if len(pending) > 0 {
		wait = -1
	}
	for {
		msgs, err := c.read(ctx, ">", count, wait)
		if err != nil {
			return out, err
		}
		if len(msgs) == 0 {
			return out, nil
		}
		rows, err = c.handleBatch(ctx, msgs)
		out = append(out, rows...)
		if err != nil {
			return out, err
		}
		wait = -1
	}
}

func (c *Classifier) read(ctx context.Context, id string, count int64, wait time.Duration) ([]redis.XMessage, error) {
	streams, err := c.Store.Client().XReadGroup(ctx, &redis.XReadGroupArgs{Group: GroupClassify,
		Consumer: c.Consumer, Streams: []string{c.logKey(), id}, Count: count, Block: wait}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("classify: read %s: %w", id, err)
	}
	var out []redis.XMessage
	for _, stream := range streams {
		out = append(out, stream.Messages...)
	}
	return out, nil
}

func (c *Classifier) handleBatch(ctx context.Context, msgs []redis.XMessage) ([]Classification, error) {
	client := c.Store.Client()
	var ignore []string
	var events []Classification
	var attempts []string
	for _, m := range msgs {
		kind, _ := m.Values["kind"].(string)
		to, _ := m.Values["to"].(string)
		label, _ := m.Values["id"].(string)
		attempt, _ := m.Values["attempt"].(string)
		if kind != "card" || to != "ended" || label == "" {
			ignore = append(ignore, m.ID)
			continue
		}
		events = append(events, Classification{EventID: m.ID, Label: label})
		attempts = append(attempts, attempt)
	}
	if len(ignore) > 0 {
		if err := client.XAck(ctx, c.logKey(), GroupClassify, ignore...).Err(); err != nil {
			return nil, fmt.Errorf("classify: ack ignored: %w", err)
		}
	}
	var out []Classification
	for i := range events {
		row, err := c.handle(ctx, events[i], attempts[i])
		if err != nil {
			return out, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (c *Classifier) handle(ctx context.Context, row Classification, attempt string) (Classification, error) {
	for tries := 0; tries < 3; tries++ {
		client := c.Store.Client()
		pipe := client.Pipeline()
		cardCmd := pipe.HGetAll(ctx, "s:"+c.Sprint+":card:"+row.Label)
		policyCmd := pipe.HGetAll(ctx, "s:"+c.Sprint+":policy")
		if _, err := pipe.Exec(ctx); err != nil {
			return row, fmt.Errorf("classify: read %s: %w", row.Label, err)
		}
		card, policy := cardCmd.Val(), policyCmd.Val()
		row.Outcome, row.Reason = card["outcome"], card["reason"]
		if row.Reason == "" {
			row.Reason = "other"
		}
		root := card["root"]
		if root == "" {
			root = row.Label
		}
		rule := ClassifyOutcome(row.Outcome, card["reason"])
		classState, err := client.HGetAll(ctx, "s:"+c.Sprint+":classify:"+root).Result()
		if err != nil {
			return row, fmt.Errorf("classify: read counter %s: %w", root, err)
		}
		used, _ := strconv.Atoi(classState[rule.Class])
		expectN := used + 1
		newLabel, newBase, title, payload := "", "", "", ""
		if (rule.Action == ActionFix || rule.Action == ActionRecut) && used < rule.Budget(policy) && card["kind"] != "ci" {
			suffix := ".fix"
			if rule.Action == ActionRecut {
				suffix = ".recut"
			}
			newLabel = root + suffix + strconv.Itoa(expectN)
			if c.Tip != nil {
				newBase, err = c.Tip(ctx, card["repo"], card["base"])
				if err != nil {
					return row, fmt.Errorf("classify: tip of %s %s: %w", card["repo"], card["base"], err)
				}
			}
			title = fmt.Sprintf("%s %s: %s %s at attempt %s on %s", strings.TrimPrefix(suffix, "."), row.Label,
				row.Outcome, row.Reason, attempt, card["bench"])
			payload = followupPayload(newLabel, rule.Action, card, newBase, title)
		}
		depsRaw := card["depends_on"]
		deps := deal.DepEntries(depsRaw)
		args := []any{"event", c.Sprint, row.EventID, row.Label, attempt, c.Actor, depsRaw, strconv.Itoa(len(deps))}
		for _, dep := range deps {
			if dep.Label != "" {
				args = append(args, "c", dep.Label, "")
			} else {
				args = append(args, "r", dep.Repo, strconv.Itoa(dep.N))
			}
		}
		args = append(args, strconv.Itoa(expectN), newLabel, newBase, payload, title)
		reply, err := client.FCall(ctx, FunctionClassify, nil, args...).StringSlice()
		if err != nil {
			return row, fmt.Errorf("classify: %s attempt %s: %w", row.Label, attempt, err)
		}
		if len(reply) < 2 {
			return row, fmt.Errorf("classify: %s malformed reply %v", row.Label, reply)
		}
		if reply[0] == "RETRY" {
			continue
		}
		if reply[0] != "OK" && reply[0] != "DUP" {
			return row, fmt.Errorf("classify: %s reply %v", row.Label, reply)
		}
		row.Result = reply[1]
		if reply[0] == "DUP" {
			row.Result = "DUP"
		}
		return row, nil
	}
	return row, fmt.Errorf("classify: %s changed during three attempts", row.Label)
}

func followupPayload(label, action string, card map[string]string, baseSHA, title string) string {
	kind := card["kind"]
	if action == ActionFix {
		kind = "fix"
	}
	body := fmt.Sprintf("RESULT: %s\nKIND: %s\nREPO: %s\nBASE: %s\nbase-sha: %s\nPATHS: %s\nDEPENDS-ON: none\nDONE-WHEN: %s\n",
		label, kind, card["repo"], card["base"], baseSHA, card["paths"], title)
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}
