// Package ghingest is the ingest consumer of ev:github (nova-tools #2657):
// the one inbound path from GitHub into our records besides import. The
// signed receiver (`nova-post hook`, internal/post/hook) appends each
// delivery to ev:github; the consumer group ingest reads it and, through the
// nova_sprint functions in internal/nsprint/fn/lua/github.lua, updates our
// records from the entry alone:
//
//   - pull_request (opened, reopened, synchronize, closed, any other action):
//     the PR record pr:<name>:<n> (internal/nsprint/prkey) gets the head and
//     the state (open, closed, merged; the lander's parked and landed stand),
//     with gh_state, merge_sha and gh_at. A PR with no record is not ours:
//     acked, counted unknown.
//   - issues closed: every card of the issue (issue:<name>:<n>:cards, which
//     card create writes from the card's origin) that is not done/fail moves
//     to done/ok with state landed through the one card move, only when the
//     closer is one of our landers and the close is completed. Anything else
//     is a finding on gh:findings and no card moves.
//
// Every other entry is acked as a no-op in the same pipeline. Each handled
// entry is one FCALL that writes, counts and XACKs, so an ack never runs
// ahead of its write and a crash leaves the entry pending for a reclaim.
// Nothing here calls GitHub: the entry is the fact, never a poll.
package ghingest

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/redis/go-redis/v9"
)

// Group is the consumer group on ev:github that ingests into our records.
const Group = "ingest"

// Findings is the stream of issue closes that landed nothing.
const Findings = "gh:findings"

// Counters is the hash of the consumer's running counts.
const Counters = "gh:ingest"

// DefaultMinIdle is how long an entry another consumer read stays pending
// before this one reclaims it.
const DefaultMinIdle = 30 * time.Second

var (
	shaRx  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	numRx  = regexp.MustCompile(`^[1-9][0-9]*$`)
	nameRx = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// IssueKey is the ZSET of the cards that came from issue n of repo.
func IssueKey(repo, n string) string { return "issue:" + prkey.Name(repo) + ":" + n + ":cards" }

// Counts is one pass: PR records written (Applied), entries that changed
// nothing (Kept: older than the record, or the same values), PRs with no
// record (Unknown), cards landed (Landed), issue closes that are findings
// (Findings), entries of no kind this group handles (Skipped), and entries
// taken over from an idle consumer (Reclaimed, also counted in the others).
type Counts struct {
	Applied, Kept, Unknown, Landed, Findings, Skipped, Reclaimed int
}

// Line is the pass's receipt.
func (c Counts) Line() string {
	return fmt.Sprintf("INGEST applied=%d kept=%d unknown=%d landed=%d findings=%d skipped=%d reclaimed=%d",
		c.Applied, c.Kept, c.Unknown, c.Landed, c.Findings, c.Skipped, c.Reclaimed)
}

func (c *Counts) add(o Counts) {
	c.Applied += o.Applied
	c.Kept += o.Kept
	c.Unknown += o.Unknown
	c.Landed += o.Landed
	c.Findings += o.Findings
	c.Skipped += o.Skipped
	c.Reclaimed += o.Reclaimed
}

// Consumer is one member of the ingest group.
type Consumer struct {
	Client *redis.Client
	Name   string // the consumer name (the seat)
	// Landers are the GitHub logins our lander closes issues as; an issue
	// closed by anyone else lands nothing.
	Landers []string
	Count   int64         // entries per read; 0 means 100
	Block   time.Duration // the first new-entry read's block; 0 means 1 s, negative never blocks
	MinIdle time.Duration // 0 means DefaultMinIdle
}

func (c *Consumer) check() error {
	if c == nil || c.Client == nil || strings.TrimSpace(c.Name) == "" {
		return errors.New("github ingest: redis client and consumer name are required")
	}
	if len(c.Landers) == 0 {
		return errors.New("github ingest: at least one lander login is required")
	}
	for _, l := range c.Landers {
		if !nameRx.MatchString(l) {
			return fmt.Errorf("github ingest: lander login %q is not a GitHub login", l)
		}
	}
	return nil
}

// Start creates the group from the stream's start (BUSYGROUP is fine) and,
// when sprint is not empty, backfills the issue index for that sprint's
// cards created before card create wrote it (ns_gh_issue_index, one walk of
// the roster). It returns the number of ids the backfill added.
func (c *Consumer) Start(ctx context.Context, sprint string) (int64, error) {
	if err := c.check(); err != nil {
		return 0, err
	}
	err := c.Client.XGroupCreateMkStream(ctx, ghevent.Stream, Group, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return 0, fmt.Errorf("github ingest: group: %w", err)
	}
	if sprint == "" {
		return 0, nil
	}
	n, err := c.Client.FCall(ctx, "ns_gh_issue_index", nil, sprint).Int64()
	if err != nil {
		return 0, fmt.Errorf("github ingest: issue index: %w", err)
	}
	return n, nil
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
			return total, fmt.Errorf("github ingest: reclaim: %w", err)
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
		return nil, fmt.Errorf("github ingest: read %s: %w", id, err)
	}
	var msgs []redis.XMessage
	for _, s := range streams {
		msgs = append(msgs, s.Messages...)
	}
	return msgs, nil
}

// call is one entry as its function takes it; fn is empty for an entry this
// group does not handle.
type call struct {
	fn   string
	keys []string
	args []any
}

func (c *Consumer) callOf(m redis.XMessage) call {
	v := func(k string) string { s, _ := m.Values[k].(string); return strings.TrimSpace(s) }
	repo, n := prkey.Name(v("repo")), v("number")
	if !nameRx.MatchString(repo) || !numRx.MatchString(n) {
		return call{}
	}
	// at is one token: the record's gh_at compares it as text.
	at := strings.Join(strings.Fields(v("at")), "")
	switch v("kind") {
	case "pull_request":
		head := v("head")
		if head != "" && !shaRx.MatchString(head) {
			return call{}
		}
		return call{fn: "ns_gh_pr", keys: []string{prkey.KeyText(repo, n), ghevent.Stream},
			args: []any{Group, m.ID, v("action"), head, at, v("state"), v("merged"), v("merge_sha")}}
	case "issues":
		if v("action") != "closed" {
			return call{}
		}
		return call{fn: "ns_gh_issue_closed", keys: []string{IssueKey(repo, n), ghevent.Stream},
			args: []any{Group, m.ID, v("repo"), n, v("sender"), v("state_reason"), at, strings.Join(c.Landers, " ")}}
	}
	return call{}
}

// apply handles one batch in one pipeline: an FCALL per handled entry (write,
// count and ack in one call) and one XACK of every other entry. A failed
// pipeline leaves the unacked entries pending for the next pass.
func (c *Consumer) apply(ctx context.Context, msgs []redis.XMessage) (Counts, error) {
	var n Counts
	if len(msgs) == 0 {
		return n, nil
	}
	pipe := c.Client.Pipeline()
	var skip []string
	var cmds []*redis.Cmd
	for _, m := range msgs {
		k := c.callOf(m)
		if k.fn == "" {
			skip = append(skip, m.ID)
			continue
		}
		cmds = append(cmds, pipe.FCall(ctx, k.fn, k.keys, k.args...))
	}
	if len(skip) > 0 {
		pipe.XAck(ctx, ghevent.Stream, Group, skip...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return n, fmt.Errorf("github ingest: apply: %w", err)
	}
	n.Skipped = len(skip)
	for _, cmd := range cmds {
		reply, _ := cmd.StringSlice()
		if len(reply) == 0 {
			continue
		}
		switch reply[0] {
		case "APPLIED":
			n.Applied++
		case "KEPT":
			n.Kept++
		case "UNKNOWN":
			n.Unknown++
		case "FINDING":
			n.Findings++
		case "LANDED":
			var landed, refused int
			if len(reply) > 3 {
				_, _ = fmt.Sscan(reply[1], &landed)
				_, _ = fmt.Sscan(reply[3], &refused)
			}
			n.Landed += landed
			n.Findings += refused
			if landed > 0 {
				n.Applied++
			} else {
				n.Kept++
			}
		}
	}
	return n, nil
}
