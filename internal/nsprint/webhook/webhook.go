// Package webhook is the GitHub leg of CI results in Redis (nova-tools
// #3597): the check_run and workflow_run deliveries the signed receiver
// (`nova-post hook`, internal/post/hook, #2657) appends to ev:github become
// ci:<repo>:<sha>:gh, and every reader of a GitHub check state reads that
// hash. Nothing here, and nothing that reads the hash, calls the check-runs
// or workflow-runs REST endpoints or asks GitHub to rerun anything: a rerun is
// our own CI's `ci request --again`, a bench-side run recorded in Redis.
//
// One writer: the consumer group ci-github on ev:github, through the
// nova_sprint function ns_ci_github (internal/nsprint/fn/lua/ci_github.lua),
// which writes the field, refolds the summary word and XACKs the entry in one
// atomic call, so the ack never runs ahead of the write. Entries of any other
// kind are acked as no-ops in the same pipeline. The group is fleet-wide (one
// per stream, not per sprint): a sha's result is the same whichever sprint
// reads it, so several consumers share the group and never write twice.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/redis/go-redis/v9"
)

// Group is the consumer group on ev:github that writes the GitHub leg.
const Group = "ci-github"

// Function is the nova_sprint function that writes one entry and acks it.
const Function = "ns_ci_github"

// Suffix is the record suffix of the GitHub leg: ci:<repo>:<sha>:gh.
const Suffix = "gh"

// Words on the hash, the same three our own CI uses.
const (
	Green   = "green"
	Red     = "red"
	Pending = "pending"
)

// DefaultMinIdle is how long an entry another consumer read stays pending
// before this one reclaims it.
const DefaultMinIdle = 30 * time.Second

var shaRx = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ShortRepo is the repository name without its owner: ev:github carries
// owner/name, our own CI keys by name (ci:nova-tools:<sha>).
func ShortRepo(repo string) string {
	repo = strings.TrimSpace(repo)
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// Key is the GitHub leg of one head: ci:<repo>:<sha>:gh.
func Key(repo, sha string) string { return "ci:" + ShortRepo(repo) + ":" + sha + ":" + Suffix }

// Run is one named check or workflow on the hash.
type Run struct {
	Kind string // check or wf
	Name string
	Word string
	ID   string
	At   string
}

// Record is ci:<repo>:<sha>:gh as a reader sees it.
type Record struct {
	Found bool
	Word  string // gh: green, red or pending
	Fail  string // gh_fail: the first red run as kind:name
	At    string // gh_at
	PR    string
	Runs  []Run // in field order (kind, then name)
}

// Parse reads a Record from the hash's fields (an HGETALL answer).
func Parse(m map[string]string) Record {
	r := Record{Found: len(m) > 0, Word: m["gh"], Fail: m["gh_fail"], At: m["gh_at"], PR: m["pr"]}
	for f, v := range m {
		kind, name, ok := strings.Cut(f, ":")
		if !ok || (kind != "check" && kind != "wf") {
			continue
		}
		parts := strings.SplitN(v, " ", 3)
		for len(parts) < 3 {
			parts = append(parts, "")
		}
		r.Runs = append(r.Runs, Run{Kind: kind, Name: name, Word: parts[0], ID: parts[1], At: parts[2]})
	}
	sort.Slice(r.Runs, func(i, j int) bool {
		if r.Runs[i].Kind != r.Runs[j].Kind {
			return r.Runs[i].Kind < r.Runs[j].Kind
		}
		return r.Runs[i].Name < r.Runs[j].Name
	})
	return r
}

// Read is one HGETALL of the GitHub leg of a head.
func Read(ctx context.Context, rdb *redis.Client, repo, sha string) (Record, error) {
	m, err := rdb.HGetAll(ctx, Key(repo, sha)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Record{}, fmt.Errorf("HGETALL %s: %w", Key(repo, sha), err)
	}
	return Parse(m), nil
}

// Counts is one pass: entries written (APPLIED), older than what the hash
// holds (KEPT), not a CI result (SKIPPED), and taken over from an idle
// consumer (RECLAIMED, also counted in the other three).
type Counts struct {
	Applied, Kept, Skipped, Reclaimed int
}

// Line is the pass's receipt.
func (c Counts) Line() string {
	return fmt.Sprintf("CIGH applied=%d kept=%d skipped=%d reclaimed=%d", c.Applied, c.Kept, c.Skipped, c.Reclaimed)
}

func (c *Counts) add(o Counts) {
	c.Applied += o.Applied
	c.Kept += o.Kept
	c.Skipped += o.Skipped
	c.Reclaimed += o.Reclaimed
}

// Consumer is one member of the ci-github group.
type Consumer struct {
	Client *redis.Client
	Name   string        // the consumer name (the seat)
	Count  int64         // entries per read; 0 means 100
	Block  time.Duration // the first new-entry read's block; 0 means 1 s, negative never blocks
	// MinIdle is how long another consumer's pending entry waits before this
	// one reclaims it; 0 means DefaultMinIdle.
	MinIdle time.Duration
}

func (c *Consumer) check() error {
	if c == nil || c.Client == nil || strings.TrimSpace(c.Name) == "" {
		return errors.New("ci github: redis client and consumer name are required")
	}
	return nil
}

// Start creates the group from the stream's start (BUSYGROUP is fine), so a
// result published before the first consumer ran is still written.
func (c *Consumer) Start(ctx context.Context) error {
	if err := c.check(); err != nil {
		return err
	}
	err := c.Client.XGroupCreateMkStream(ctx, ghevent.Stream, Group, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("ci github: group: %w", err)
	}
	return nil
}

// Pass reclaims entries idle past MinIdle, handles this consumer's own
// pending entries, then new entries until a read returns nothing. Only the
// first new-entry read blocks, and only when nothing was pending.
func (c *Consumer) Pass(ctx context.Context) (Counts, error) {
	var total Counts
	if err := c.check(); err != nil {
		return total, err
	}
	count := c.Count
	if count <= 0 {
		count = 100
	}
	idle := c.MinIdle
	if idle <= 0 {
		idle = DefaultMinIdle
	}
	start := "0-0"
	for {
		msgs, next, err := c.Client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: ghevent.Stream, Group: Group, Consumer: c.Name, MinIdle: idle, Start: start, Count: count,
		}).Result()
		if err != nil {
			return total, fmt.Errorf("ci github: reclaim: %w", err)
		}
		n, err := c.apply(ctx, msgs)
		n.Reclaimed = len(msgs)
		total.add(n)
		if err != nil {
			return total, err
		}
		if next == "0-0" || next == "" {
			break
		}
		start = next
	}
	pending, err := c.read(ctx, "0", count, -1)
	if err != nil {
		return total, err
	}
	n, err := c.apply(ctx, pending)
	total.add(n)
	if err != nil {
		return total, err
	}
	wait := c.Block
	if wait == 0 {
		wait = time.Second
	}
	if len(pending) > 0 || total.Reclaimed > 0 {
		wait = -1
	}
	for {
		msgs, err := c.read(ctx, ">", count, wait)
		if err != nil {
			return total, err
		}
		if len(msgs) == 0 {
			return total, nil
		}
		n, err := c.apply(ctx, msgs)
		total.add(n)
		if err != nil {
			return total, err
		}
		wait = -1
	}
}

func (c *Consumer) read(ctx context.Context, id string, count int64, wait time.Duration) ([]redis.XMessage, error) {
	streams, err := c.Client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: Group, Consumer: c.Name, Streams: []string{ghevent.Stream, id}, Count: count, Block: wait,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ci github: read %s: %w", id, err)
	}
	var msgs []redis.XMessage
	for _, s := range streams {
		msgs = append(msgs, s.Messages...)
	}
	return msgs, nil
}

// result is one entry as ns_ci_github takes it; ok is false for an entry
// that is not a CI result.
type result struct {
	key, kind, name, id, status, conclusion, at, pr string
}

func resultOf(m redis.XMessage) (result, bool) {
	v := func(k string) string { s, _ := m.Values[k].(string); return strings.TrimSpace(s) }
	r := result{status: v("status"), conclusion: v("conclusion"), at: v("at"), pr: v("number")}
	switch r.kind = v("kind"); r.kind {
	case "check_run":
		r.name, r.id = v("check"), v("check_run_id")
	case "workflow_run":
		r.name, r.id = v("workflow"), v("run_id")
	default:
		return r, false
	}
	repo, head := ShortRepo(v("repo")), v("head")
	if repo == "" || strings.ContainsAny(repo, ": ") || !shaRx.MatchString(head) || r.name == "" || r.id == "" {
		return r, false
	}
	for _, ch := range r.id {
		if ch < '0' || ch > '9' {
			return r, false
		}
	}
	// A name is one field token: spaces (a workflow called "CI tests") are
	// folded to '-', so the stored value stays '<word> <id> <at>'.
	r.name = strings.Join(strings.Fields(r.name), "-")
	r.at = strings.Join(strings.Fields(r.at), "")
	r.key = Key(repo, head)
	return r, true
}

// apply handles one batch in one pipeline: an FCALL per CI result (write and
// ack in one call) and one XACK of every other entry. A failed pipeline
// leaves the unacked entries pending for the next pass; the function is a
// no-op on a redelivered entry.
func (c *Consumer) apply(ctx context.Context, msgs []redis.XMessage) (Counts, error) {
	var n Counts
	if len(msgs) == 0 {
		return n, nil
	}
	pipe := c.Client.Pipeline()
	var skip []string
	var cmds []*redis.Cmd
	for _, m := range msgs {
		r, ok := resultOf(m)
		if !ok {
			skip = append(skip, m.ID)
			continue
		}
		cmds = append(cmds, pipe.FCall(ctx, Function, []string{r.key, ghevent.Stream},
			Group, m.ID, r.kind, r.name, r.id, r.status, r.conclusion, r.at, r.pr))
	}
	if len(skip) > 0 {
		pipe.XAck(ctx, ghevent.Stream, Group, skip...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return n, fmt.Errorf("ci github: apply: %w", err)
	}
	n.Skipped = len(skip)
	for _, cmd := range cmds {
		reply, _ := cmd.Slice()
		if len(reply) > 0 && reply[0] == "APPLIED" {
			n.Applied++
		} else {
			n.Kept++
		}
	}
	return n, nil
}
